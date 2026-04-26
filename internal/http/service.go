package http

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"

	"beryl-xray-web-console/internal/service"
)

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
	if err := s.Service.Do(r.Context(), service.Action(req.Action)); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
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
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "bind_switch": req.On})
}
