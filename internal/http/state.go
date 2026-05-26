package http

import (
	"context"
	nethttp "net/http"
	"sync"
	"time"
)

// stateResponse is the shape of GET /api/state. Each block has its own
// "ok" flag and an optional "error" string so a partial failure (e.g.
// uci hung but pgrep worked) still gives the UI something to render.
type stateResponse struct {
	Service         block  `json:"service"`
	TUN             block  `json:"tun"`
	PhysicalSwitch  block  `json:"physical_switch"`
	Killswitch      block  `json:"killswitch"`
	BindSwitch      block  `json:"bind_switch"`
	Enabled         block  `json:"enabled"`
	ActiveProfile   block  `json:"active_profile"`
	NativeVPNActive block  `json:"native_vpn_active"`
	SwFunc          block  `json:"sw_func"`
	GeneratedAt     string `json:"generated_at"`
}

type block struct {
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// stateCacheTTL bounds how long a /api/state response may be reused
// across concurrent pollers. The probe cost is dominated by sourcing
// gl_util.sh in a busybox subshell (~50ms) plus a handful of pgrep/
// uci forks. With multiple browser tabs each polling every 10s we
// want at most ~1 actual probe set every 3s.
const stateCacheTTL = 3 * time.Second

// stateCache + stateRefresh form a tiny single-flight cache. Multiple
// concurrent /api/state requests:
//   - return the cached payload if it's still fresh
//   - otherwise, exactly ONE goroutine refreshes; the rest wait on the
//     refresh's done channel, then return the same fresh payload.
// This collapses the 7 shell-outs/probe per request into 7 per ~1.5s
// no matter how many browser tabs are polling.
type stateCacheEntry struct {
	payload stateResponse
	at      time.Time
}

type stateRefreshSlot struct {
	done    chan struct{}
	payload stateResponse
}

type stateCache struct {
	mu      sync.Mutex
	entry   *stateCacheEntry
	pending *stateRefreshSlot
}

func (s *Server) handleState(w nethttp.ResponseWriter, r *nethttp.Request) {
	c := s.stateCache

	c.mu.Lock()
	if c.entry != nil && time.Since(c.entry.at) < stateCacheTTL {
		payload := c.entry.payload
		c.mu.Unlock()
		writeJSON(w, nethttp.StatusOK, payload)
		return
	}
	if c.pending != nil {
		// Someone is already refreshing — wait for it.
		slot := c.pending
		c.mu.Unlock()
		<-slot.done
		writeJSON(w, nethttp.StatusOK, slot.payload)
		return
	}
	// We're the refresher.
	slot := &stateRefreshSlot{done: make(chan struct{})}
	c.pending = slot
	c.mu.Unlock()

	// Use a context tied to this request, but with our own cap so the
	// whole probe set has a shared deadline.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	payload := s.collectState(ctx)

	c.mu.Lock()
	c.entry = &stateCacheEntry{payload: payload, at: time.Now()}
	c.pending = nil
	slot.payload = payload
	c.mu.Unlock()
	close(slot.done)

	writeJSON(w, nethttp.StatusOK, payload)
}

// collectState runs every state probe in parallel — wall time becomes
// max(individual probe times) instead of sum, and CPU pressure becomes
// the same total work spread across both cores.
func (s *Server) collectState(ctx context.Context) stateResponse {
	resp := stateResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	var wg sync.WaitGroup
	wg.Add(9)

	go func() {
		defer wg.Done()
		if running, err := s.Probe.SingBoxRunning(ctx); err != nil {
			resp.Service = block{Error: err.Error()}
		} else {
			resp.Service = block{OK: true, Value: running}
		}
	}()

	go func() {
		defer wg.Done()
		if up, err := s.Probe.TunUp(ctx, "sing-tun"); err != nil {
			resp.TUN = block{Error: err.Error()}
		} else {
			resp.TUN = block{OK: true, Value: up}
		}
	}()

	go func() {
		defer wg.Done()
		if pos, err := s.Probe.SwitchPosition(ctx); err != nil {
			resp.PhysicalSwitch = block{Error: err.Error()}
		} else {
			resp.PhysicalSwitch = block{OK: true, Value: pos}
		}
	}()

	go func() {
		defer wg.Done()
		if v, err := s.UCI.GetBool(ctx, "sing-box.config.killswitch"); err != nil {
			resp.Killswitch = block{Error: err.Error()}
		} else {
			resp.Killswitch = block{OK: true, Value: v}
		}
	}()

	go func() {
		defer wg.Done()
		if v, err := s.UCI.GetBool(ctx, "sing-box.config.bind_switch"); err != nil {
			resp.BindSwitch = block{Error: err.Error()}
		} else {
			resp.BindSwitch = block{OK: true, Value: v}
		}
	}()

	go func() {
		defer wg.Done()
		if v, err := s.UCI.GetBool(ctx, "sing-box.config.enabled"); err != nil {
			resp.Enabled = block{Error: err.Error()}
		} else {
			resp.Enabled = block{OK: true, Value: v}
		}
	}()

	go func() {
		defer wg.Done()
		// switch-button.@main[0].func — the GL.iNet UCI key that
		// names the physical-toggle binding. "" / missing = no
		// binding. "xray" = our marker (set via /api/sw-func).
		// Native values: vpn, wireguard, openvpn, tor, adguardhome,
		// wifi, repeater, cellular, led.
		v, err := s.UCI.Get(ctx, "switch-button.@main[0].func")
		if err != nil {
			resp.SwFunc = block{Error: err.Error()}
		} else {
			resp.SwFunc = block{OK: true, Value: v}
		}
	}()

	go func() {
		defer wg.Done()
		active, err := s.Probe.NativeVPNActive(ctx)
		if err != nil {
			resp.NativeVPNActive = block{Error: err.Error()}
		} else {
			resp.NativeVPNActive = block{OK: true, Value: active}
		}
	}()

	go func() {
		defer wg.Done()
		activeID, err := s.UCI.Get(ctx, uciActiveKey)
		if err != nil {
			resp.ActiveProfile = block{Error: err.Error()}
			return
		}
		if activeID == "" {
			resp.ActiveProfile = block{OK: true, Value: nil}
			return
		}
		if s.Profiles == nil {
			resp.ActiveProfile = block{OK: true, Value: map[string]any{"id": activeID}}
			return
		}
		if p, err := s.Profiles.Get(activeID); err != nil {
			resp.ActiveProfile = block{OK: true, Value: map[string]any{
				"id":   activeID,
				"name": "(missing)",
			}}
		} else {
			val := map[string]any{
				"id":     activeID,
				"name":   p.Name,
				"server": p.Server,
				"port":   p.Port,
			}
			// If sing-box is running and clash-API answers, surface the
			// CURRENTLY-LIVE selector pick. Diverges from UCI only in
			// the brief window between an activate-via-UI press and
			// the next clash refresh; useful for spotting drift.
			if s.Clash != nil {
				if px, err := s.Clash.GetProxy(ctx, "proxy"); err == nil && px.Now != "" {
					val["selector_now"] = px.Now
				}
			}
			resp.ActiveProfile = block{OK: true, Value: val}
		}
	}()

	wg.Wait()
	return resp
}
