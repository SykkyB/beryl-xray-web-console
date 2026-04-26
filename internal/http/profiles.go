package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"time"

	"beryl-xray-web-console/internal/service"
	"beryl-xray-web-console/internal/singbox"
	"beryl-xray-web-console/internal/store"
	"beryl-xray-web-console/internal/vless"

	"github.com/google/uuid"
)

// closeClashConnections asks the clash-API to close every active
// connection so they re-establish through whatever outbound the live
// config now points at. Best-effort: failure is logged but not surfaced
// as an activation error — the new config is already in place.
func closeClashConnections(ctx context.Context, addr string) error {
	if addr == "" {
		return nil
	}
	req, err := nethttp.NewRequestWithContext(ctx, "DELETE", "http://"+addr+"/connections", nil)
	if err != nil {
		return err
	}
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("clash DELETE /connections: %s", resp.Status)
	}
	return nil
}

// uciActiveKey is the UCI path holding the id of the currently active
// VLESS profile. Matches what xray-panel-cli writes from this package
// and what /api/state surfaces on read.
const uciActiveKey = "sing-box.config.active_profile"
const uciPackage = "sing-box"

// profileResponse is the per-profile shape the API returns. UUID is
// masked because it is the auth credential — full UUID is only needed
// internally when rendering the sing-box config.
type profileResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Server      string    `json:"server"`
	Port        int       `json:"port"`
	UUIDMask    string    `json:"uuid_mask"`
	Flow        string    `json:"flow,omitempty"`
	SNI         string    `json:"sni"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	PublicKey   string    `json:"public_key"`
	ShortID     string    `json:"short_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Active      bool      `json:"active"`
}

func toResponse(p store.Profile, active bool) profileResponse {
	return profileResponse{
		ID:          p.ID,
		Name:        p.Name,
		Server:      p.Server,
		Port:        p.Port,
		UUIDMask:    maskUUID(p.UUID),
		Flow:        p.Flow,
		SNI:         p.SNI,
		Fingerprint: p.Fingerprint,
		PublicKey:   p.PublicKey,
		ShortID:     p.ShortID,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
		Active:      active,
	}
}

// maskUUID returns "11406a7a-…-c909c36" so the UI can show "this is the
// right profile" without the full secret. Anything shorter than ~16
// chars is returned as-is to avoid accidentally leaking.
func maskUUID(s string) string {
	if len(s) < 16 {
		return s
	}
	return s[:8] + "…" + s[len(s)-7:]
}

func (s *Server) handleProfilesList(w nethttp.ResponseWriter, r *nethttp.Request) {
	list, err := s.Profiles.List()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	activeID, _ := s.UCI.Get(r.Context(), uciActiveKey)
	out := make([]profileResponse, 0, len(list))
	for _, p := range list {
		out = append(out, toResponse(p, p.ID == activeID))
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"profiles":   out,
		"active_id":  activeID,
		"generated":  time.Now().UTC().Format(time.RFC3339),
	})
}

type importRequest struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

func (s *Server) handleProfilesImportVless(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req importRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	if req.URL == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("missing url"))
		return
	}

	parsed, err := vless.Parse(req.URL)
	if err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse vless: %w", err))
		return
	}

	name := req.Name
	if name == "" {
		name = parsed.Name
	}

	prof := store.Profile{
		ID:          uuid.New().String(),
		Name:        name,
		Server:      parsed.Server,
		Port:        parsed.Port,
		UUID:        parsed.UUID,
		Flow:        parsed.Flow,
		SNI:         parsed.SNI,
		Fingerprint: parsed.Fingerprint,
		PublicKey:   parsed.PublicKey,
		ShortID:     parsed.ShortID,
	}
	if err := s.Profiles.Add(prof); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("save: %w", err))
		return
	}

	writeJSON(w, nethttp.StatusCreated, toResponse(prof, false))
}

func (s *Server) handleProfileDelete(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("missing id"))
		return
	}
	activeID, _ := s.UCI.Get(r.Context(), uciActiveKey)
	if activeID == id {
		writeErr(w, nethttp.StatusConflict, fmt.Errorf("cannot delete the active profile — activate another first"))
		return
	}
	if err := s.Profiles.Delete(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, nethttp.StatusNotFound, err)
			return
		}
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleProfileActivate(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("missing id"))
		return
	}

	prof, err := s.Profiles.Get(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, nethttp.StatusNotFound, err)
			return
		}
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	all, err := s.Profiles.List()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Render the full selector-style config with every saved profile
	// and the new active one as the selector default. Always write to
	// disk so a future restart picks the right default; the live switch
	// itself is done via clash-API below (no reload).
	if err := s.Renderer.WriteAndCheck(ctx, all, id); err != nil {
		writeErr(w, nethttp.StatusBadRequest, err)
		return
	}

	if err := s.UCI.Set(ctx, uciActiveKey, id); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	if err := s.UCI.Commit(ctx, uciPackage); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	running, _ := s.Probe.SingBoxRunning(ctx)
	switched := false
	reloaded := false

	if running {
		// Try the cheap path first — clash-API selector switch. Works
		// when sing-box is already running with a selector-style
		// config (i.e. has been (re)loaded at least once on the new
		// template). Instant, no tunnel interruption for unrelated
		// LAN flows.
		newTag := singbox.TagOf(prof)
		if s.Clash != nil {
			if err := s.Clash.SelectProxy(ctx, "proxy", newTag); err == nil {
				switched = true
				// Drop existing proxy flows so the new outbound's
				// VLESS UUID gets used for everything new.
				_ = closeClashConnections(ctx, s.Cfg.ClashAPI)
			}
		}

		// Fallback: full reload. Happens on first activation after
		// migrating to selector-style config (old config didn't have
		// a selector, so the clash PUT 404'd) or on any other clash
		// API issue.
		if !switched {
			if err := s.Service.Do(ctx, service.ActionReload); err != nil {
				writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("reload: %w", err))
				return
			}
			_ = closeClashConnections(ctx, s.Cfg.ClashAPI)
			reloaded = true
		}
	}

	s.nudgeExitIP()

	writeJSON(w, nethttp.StatusOK, map[string]any{
		"ok":           true,
		"active_id":    id,
		"profile_name": prof.Name,
		"switched":     switched, // true = instant clash-API selector flip
		"reloaded":     reloaded, // true = full sing-box reload (slower)
	})
}
