package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"os/exec"
	"strings"
	"time"
)

// stopNativeVPN disables every active stock VPN client tunnel
// (WireGuard / OpenVPN) and restarts vpn-client to tear interfaces
// down. Returns the list of route_policy section keys we disabled
// (also persisted to sing-box.config.native_vpn_disabled for restore),
// plus restart stdout/err. A nil-error return with empty list means
// nothing was active.
//
// We don't (and can't) just `/etc/init.d/vpn-client stop` — that init
// script only has a `start()` (no stop), so default rc.common stop
// is a no-op. The actual stop pattern, lifted from GL.iNet's own
// /etc/gl-switch.d/vpn.sh (the physical-switch OFF handler):
//
//   for each enabled WG/OVPN rule in route_policy:
//       uci set route_policy.<rule>.enabled=0
//   uci commit route_policy
//   /etc/init.d/vpn-client restart        # re-runs rtp2.sh → tears down ifaces
//
// Side effect: the user's chosen tunnel persists as `enabled=0` in
// UCI. Re-enabling means flipping the stock UI toggle back ON; same
// as if they'd manually clicked OFF themselves.
func (s *Server) stopNativeVPN(ctx context.Context) (disabled []string, restartOutput string, restartErr error, err error) {
	rules, err := listEnabledVPNRules(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("list rules: %w", err)
	}
	if len(rules) == 0 {
		return []string{}, "", nil, nil
	}

	disabled = make([]string, 0, len(rules))
	for _, ruleKey := range rules {
		// `uci set` returns 0 on success regardless of whether the
		// value changed. Keep going so a single bad rule doesn't
		// strand the others enabled.
		if err := uciSet(ctx, ruleKey+".enabled", "0"); err != nil {
			continue
		}
		disabled = append(disabled, ruleKey)
	}
	if err := uciCommit(ctx, "route_policy"); err != nil {
		return disabled, "", nil, fmt.Errorf("uci commit route_policy: %w", err)
	}

	// Persist what we disabled so restoreNativeVPN can put it back.
	if len(disabled) > 0 {
		_ = uciSet(ctx, "sing-box.config.native_vpn_disabled", strings.Join(disabled, ","))
		_ = uciCommit(ctx, "sing-box")
	}

	// Best-effort restart — if it fails the rules are still disabled.
	out, rerr := exec.CommandContext(ctx, "/etc/init.d/vpn-client", "restart").CombinedOutput()
	return disabled, string(out), rerr, nil
}

func (s *Server) handleNativeVPNStop(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	disabled, restartOut, restartErr, err := s.stopNativeVPN(ctx)
	if err != nil {
		writeJSON(w, nethttp.StatusInternalServerError, map[string]any{
			"ok":       false,
			"error":    err.Error(),
			"disabled": disabled,
		})
		return
	}
	resp := map[string]any{"ok": true, "disabled": disabled}
	if len(disabled) == 0 {
		resp["note"] = "no active native VPN tunnels"
	}
	if restartErr != nil {
		resp["restart_error"] = restartErr.Error()
		resp["restart_output"] = restartOut
	}
	writeJSON(w, nethttp.StatusOK, resp)
}

// listEnabledVPNRules returns the UCI section keys (e.g.
// "route_policy.@rule[0]") of all WireGuard/OpenVPN rules currently
// flagged enabled=1. We scan `uci show route_policy` once and pair
// the .enabled=1 lines with matching .via_type lines on the same
// section. Cheap; route_policy has at most a few dozen rules in
// practice.
func listEnabledVPNRules(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "uci", "-q", "show", "route_policy")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Two-pass: first collect enabled flags per section, then keep
	// only those with via_type ∈ {wireguard, openvpn}.
	enabled := map[string]bool{}
	viaType := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Lines look like: route_policy.@rule[0].enabled='1'
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		fullKey := line[:eq]
		val := strings.Trim(line[eq+1:], "'\"")
		dot := strings.LastIndex(fullKey, ".")
		if dot < 0 {
			continue
		}
		section := fullKey[:dot]
		field := fullKey[dot+1:]
		switch field {
		case "enabled":
			if val == "1" {
				enabled[section] = true
			}
		case "via_type":
			viaType[section] = val
		}
	}

	out2 := make([]string, 0)
	for section := range enabled {
		t := viaType[section]
		if t == "wireguard" || t == "openvpn" {
			out2 = append(out2, section)
		}
	}
	return out2, nil
}

// restoreNativeVPN re-enables route_policy rules previously disabled
// by stopNativeVPN (tracked in sing-box.config.native_vpn_disabled),
// clears the memo, and restarts vpn-client so interfaces come back
// up. Returns the list actually restored, plus restart stdout/err.
// A nil-error return with empty list means there was nothing to restore.
func (s *Server) restoreNativeVPN(ctx context.Context) (restored []string, restartOutput string, restartErr error, err error) {
	encoded, _ := s.UCI.Get(ctx, "sing-box.config.native_vpn_disabled")
	if encoded == "" {
		return []string{}, "", nil, nil
	}

	rules := strings.Split(encoded, ",")
	restored = make([]string, 0, len(rules))
	for _, ruleKey := range rules {
		ruleKey = strings.TrimSpace(ruleKey)
		if ruleKey == "" {
			continue
		}
		if err := uciSet(ctx, ruleKey+".enabled", "1"); err != nil {
			continue
		}
		restored = append(restored, ruleKey)
	}
	if err := uciCommit(ctx, "route_policy"); err != nil {
		return restored, "", nil, fmt.Errorf("uci commit route_policy: %w", err)
	}

	// Clear memo so subsequent OFF cycles don't keep restoring stale rules.
	_ = uciSet(ctx, "sing-box.config.native_vpn_disabled", "")
	_ = uciCommit(ctx, "sing-box")

	out, rerr := exec.CommandContext(ctx, "/etc/init.d/vpn-client", "restart").CombinedOutput()
	return restored, string(out), rerr, nil
}

func (s *Server) handleNativeVPNRestore(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	restored, restartOut, restartErr, err := s.restoreNativeVPN(ctx)
	if err != nil {
		writeJSON(w, nethttp.StatusInternalServerError, map[string]any{
			"ok":       false,
			"error":    err.Error(),
			"restored": restored,
		})
		return
	}
	resp := map[string]any{"ok": true, "restored": restored}
	if len(restored) == 0 {
		resp["note"] = "nothing to restore"
	}
	if restartErr != nil {
		resp["restart_error"] = restartErr.Error()
		resp["restart_output"] = restartOut
	}
	writeJSON(w, nethttp.StatusOK, resp)
}

func uciSet(ctx context.Context, key, val string) error {
	return exec.CommandContext(ctx, "uci", "set", key+"="+val).Run()
}

func uciCommit(ctx context.Context, pkg string) error {
	return exec.CommandContext(ctx, "uci", "commit", pkg).Run()
}
