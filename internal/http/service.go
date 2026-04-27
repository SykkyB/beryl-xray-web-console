package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"time"

	"beryl-xray-web-console/internal/service"
)

// nudgeExitIP triggers an out-of-band exit-IP refresh after any action
// that might change connectivity (service start/stop, profile change,
// killswitch / bind toggle). Best-effort: never blocks the response.
//
// The cached value is invalidated *before* the refresh fires so the UI
// shows "—" for the brief window between the action and the new fetch
// landing — better than displaying the previous outbound's exit IP for
// up to 30s while the next scheduled poll waits.
func (s *Server) nudgeExitIP() {
	if s.ExitIP == nil {
		return
	}
	s.ExitIP.Invalidate()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.ExitIP.RefreshNow(ctx)
	}()
}

// serviceActionReq is the body of POST /api/service.
type serviceActionReq struct {
	Action string `json:"action"`
}

func (s *Server) handleServiceAction(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req serviceActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	if !service.IsValidAction(req.Action) {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("invalid action %q (allowed: start|stop|restart|reload)", req.Action))
		return
	}

	// Guard: when bind_switch is ON and the physical switch is OFF, the
	// init script intentionally refuses to start sing-box (return 1 in
	// start_service). procd doesn't propagate that as a non-zero exit
	// from `/etc/init.d/sing-box start`, so without this pre-check the
	// UI would show a misleading "ok" while the service stays stopped.
	if req.Action == "start" || req.Action == "restart" || req.Action == "reload" {
		if bindOn, err := s.UCI.GetBool(r.Context(), "sing-box.config.bind_switch"); err == nil && bindOn {
			if sw, err := s.Probe.SwitchPosition(r.Context()); err == nil && sw == "off" {
				writeErr(w, nethttp.StatusConflict, fmt.Errorf(
					"%s blocked: bind_switch=on and physical switch is OFF — flip the switch ON, or disable bind_switch first",
					req.Action))
				return
			}
		}
	}

	if err := s.Service.Do(r.Context(), service.Action(req.Action)); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	s.nudgeExitIP()
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "action": req.Action})
}

// toggleReq is the shared body shape for /api/killswitch and /api/bind_switch.
type toggleReq struct {
	On bool `json:"on"`
}

func (s *Server) handleKillswitch(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req toggleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	if err := s.Service.SetKillswitch(r.Context(), req.On); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	s.nudgeExitIP()
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "killswitch": req.On})
}

func (s *Server) handleBindSwitch(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req toggleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	if err := s.Service.SetBindSwitch(r.Context(), req.On); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	s.nudgeExitIP()
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "bind_switch": req.On})
}
