package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"os/exec"
	"time"
)

// handleSideSwitch flips the physical side-switch binding to / away
// from XRAY *and* swaps the running VPN tunnel transactionally. Used
// by the launcher's "Side switch" tag on the dashboard card.
//
// Body: {"on": true|false}
//
// on=true (claim the switch for XRAY):
//   1. Read switch-button.@main[0].func and stash it in
//      sing-box.config.prev_sw_func (so we can restore later).
//   2. Set switch-button.@main[0].func='xray'. Our hotplug recognises
//      this; stock /etc/rc.button/switch tries to exec
//      /etc/gl-switch.d/xray.sh which doesn't exist → exit 0, no
//      native handler runs, no "Turning VPN ON" notification. We
//      don't touch sub_func, so the user's previously-selected
//      native tunnel stays as their default when binding is released.
//   3. Set sing-box.config.bind_switch=1 — legacy signal still
//      honoured by init.d/sing-box's start-time gate.
//   4. Transactional mutex: if a native WG/OVPN tunnel is currently
//      up, stop it (route_policy enabled=0 + vpn-client restart) and
//      memo the disabled rules to sing-box.config.native_vpn_disabled.
//      OFF will read this memo to know whether to restore.
//   5. Sync to current physical position via init's bind_switch_on
//      custom command — starts sing-box if physical=ON.
//
// on=false (give the switch back to native):
//   1. If we previously stopped native (memo non-empty), stop
//      sing-box first (so our ip rule prio 5000 vacates before
//      native interfaces come back), then restore native rules and
//      restart vpn-client. Result: physical=ON → native VPN UP;
//      physical=OFF → nothing.
//      If memo is empty (we never touched native), leave sing-box
//      alone — the user may have started XRAY independently and OFF
//      should just unbind the button.
//   2. Restore switch-button.@main[0].func from prev_sw_func.
//   3. Legacy cleanup: if a prior build saved prev_sub_func, restore
//      sub_func from it and clear the key.
//   4. Clear sing-box.config.bind_switch.
//
// The two UCI packages are committed at the end so a panic mid-flight
// can't leave half-applied state on disk.
func (s *Server) handleSideSwitch(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	const (
		swFuncKey     = "switch-button.@main[0].func"
		swSubFuncKey  = "switch-button.@main[0].sub_func"
		prevSwFunc    = "sing-box.config.prev_sw_func"
		prevSubFunc   = "sing-box.config.prev_sub_func"
		bindSwitchKey = "sing-box.config.bind_switch"
		nativeDisabledKey = "sing-box.config.native_vpn_disabled"
	)

	if body.On {
		// Idempotent ON: only snapshot when we haven't already, so a
		// double-click doesn't overwrite the original with "xray".
		cur, _ := s.UCI.Get(ctx, swFuncKey)
		saved, _ := s.UCI.Get(ctx, prevSwFunc)
		if saved == "" && cur != "xray" {
			if err := s.UCI.Set(ctx, prevSwFunc, cur); err != nil {
				writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("save prev sw_func: %w", err))
				return
			}
		}
		if err := s.UCI.Set(ctx, swFuncKey, "xray"); err != nil {
			writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("set sw_func=xray: %w", err))
			return
		}
		if err := s.UCI.Set(ctx, bindSwitchKey, "1"); err != nil {
			writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("set bind_switch: %w", err))
			return
		}
		if err := s.UCI.Commit(ctx, "switch-button"); err != nil {
			writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("commit switch-button: %w", err))
			return
		}
		if err := s.UCI.Commit(ctx, "sing-box"); err != nil {
			writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("commit sing-box: %w", err))
			return
		}

		// Transactional mutex: stop native VPN if active, BEFORE
		// starting sing-box. NativeVPNActive probes for wgclient*/
		// ovpnclient* interfaces.
		var nativeStop map[string]any
		if active, _ := s.Probe.NativeVPNActive(ctx); active {
			disabled, restartOut, restartErr, err := s.stopNativeVPN(ctx)
			nativeStop = map[string]any{"disabled": disabled}
			if err != nil {
				nativeStop["error"] = err.Error()
			}
			if restartErr != nil {
				nativeStop["restart_error"] = restartErr.Error()
				nativeStop["restart_output"] = restartOut
			}
		}

		// Sync to physical position: bind_switch_on fires the hotplug
		// with ACTION=pressed/released based on get_switch_button_status.
		out, _ := exec.CommandContext(ctx, s.Cfg.SingBoxInit, "bind_switch_on").CombinedOutput()
		writeJSON(w, nethttp.StatusOK, map[string]any{
			"ok":           true,
			"on":           true,
			"prev_sw_func": cur,
			"native_stop":  nativeStop,
			"init_output":  string(out),
		})
		return
	}

	// OFF: symmetric reverse of ON.
	//
	// 1. If we stopped native earlier (memo non-empty), stop sing-box
	//    and restore native. The memo lives in
	//    sing-box.config.native_vpn_disabled — populated by stopNativeVPN.
	memo, _ := s.UCI.Get(ctx, nativeDisabledKey)
	var stopOut string
	var nativeRestore map[string]any
	if memo != "" {
		// Stop sing-box first so its ip rule prio 5000 (LAN→sing-tun)
		// vacates before vpn-client restart brings wgclient1 back up.
		// We use the init script's stop so procd handles the daemon.
		out, _ := exec.CommandContext(ctx, s.Cfg.SingBoxInit, "stop").CombinedOutput()
		stopOut = string(out)

		restored, restartOut, restartErr, err := s.restoreNativeVPN(ctx)
		nativeRestore = map[string]any{"restored": restored}
		if err != nil {
			nativeRestore["error"] = err.Error()
		}
		if restartErr != nil {
			nativeRestore["restart_error"] = restartErr.Error()
			nativeRestore["restart_output"] = restartOut
		}
	}

	// 2. Restore func from snapshot, legacy-clean sub_func, clear flags.
	savedFunc, _ := s.UCI.Get(ctx, prevSwFunc)
	if savedFunc != "" {
		if err := s.UCI.Set(ctx, swFuncKey, savedFunc); err != nil {
			writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("restore sw_func: %w", err))
			return
		}
	}
	if err := s.UCI.Set(ctx, prevSwFunc, ""); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("clear prev_sw_func: %w", err))
		return
	}
	// Legacy compat: older builds saved sub_func to prev_sub_func.
	if legacySub, _ := s.UCI.Get(ctx, prevSubFunc); legacySub != "" {
		_ = s.UCI.Set(ctx, swSubFuncKey, legacySub)
		_ = s.UCI.Set(ctx, prevSubFunc, "")
	}
	if err := s.UCI.Set(ctx, bindSwitchKey, "0"); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("clear bind_switch: %w", err))
		return
	}
	if err := s.UCI.Commit(ctx, "switch-button"); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("commit switch-button: %w", err))
		return
	}
	if err := s.UCI.Commit(ctx, "sing-box"); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("commit sing-box: %w", err))
		return
	}
	out, _ := exec.CommandContext(ctx, s.Cfg.SingBoxInit, "bind_switch_off").CombinedOutput()
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"ok":               true,
		"on":               false,
		"restored_sw_func": savedFunc,
		"singbox_stop":     stopOut,
		"native_restore":   nativeRestore,
		"init_output":      string(out),
	})
}
