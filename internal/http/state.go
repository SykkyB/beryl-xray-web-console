package http

import (
	"context"
	nethttp "net/http"
	"time"
)

// stateResponse is the shape of GET /api/state. Each block has its own
// "ok" flag and an optional "error" string so a partial failure (e.g.
// uci hung but pgrep worked) still gives the UI something to render.
type stateResponse struct {
	Service        block  `json:"service"`
	TUN            block  `json:"tun"`
	PhysicalSwitch block  `json:"physical_switch"`
	Killswitch     block  `json:"killswitch"`
	BindSwitch     block  `json:"bind_switch"`
	Enabled        block  `json:"enabled"`
	ActiveProfile  block  `json:"active_profile"`
	GeneratedAt    string `json:"generated_at"`
}

type block struct {
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleState(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	resp := stateResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if running, err := s.Probe.SingBoxRunning(ctx); err != nil {
		resp.Service = block{Error: err.Error()}
	} else {
		resp.Service = block{OK: true, Value: running}
	}

	if up, err := s.Probe.TunUp(ctx, "sing-tun"); err != nil {
		resp.TUN = block{Error: err.Error()}
	} else {
		resp.TUN = block{OK: true, Value: up}
	}

	if pos, err := s.Probe.SwitchPosition(ctx); err != nil {
		resp.PhysicalSwitch = block{Error: err.Error()}
	} else {
		resp.PhysicalSwitch = block{OK: true, Value: pos}
	}

	if v, err := s.UCI.GetBool(ctx, "sing-box.config.killswitch"); err != nil {
		resp.Killswitch = block{Error: err.Error()}
	} else {
		resp.Killswitch = block{OK: true, Value: v}
	}

	if v, err := s.UCI.GetBool(ctx, "sing-box.config.bind_switch"); err != nil {
		resp.BindSwitch = block{Error: err.Error()}
	} else {
		resp.BindSwitch = block{OK: true, Value: v}
	}

	if v, err := s.UCI.GetBool(ctx, "sing-box.config.enabled"); err != nil {
		resp.Enabled = block{Error: err.Error()}
	} else {
		resp.Enabled = block{OK: true, Value: v}
	}

	if activeID, err := s.UCI.Get(ctx, uciActiveKey); err != nil {
		resp.ActiveProfile = block{Error: err.Error()}
	} else if activeID == "" {
		resp.ActiveProfile = block{OK: true, Value: nil}
	} else if s.Profiles != nil {
		if p, err := s.Profiles.Get(activeID); err != nil {
			resp.ActiveProfile = block{OK: true, Value: map[string]any{
				"id":   activeID,
				"name": "(missing)",
			}}
		} else {
			resp.ActiveProfile = block{OK: true, Value: map[string]any{
				"id":     activeID,
				"name":   p.Name,
				"server": p.Server,
				"port":   p.Port,
			}}
		}
	}

	writeJSON(w, nethttp.StatusOK, resp)
}
