package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strconv"
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
// masked by default because it is the auth credential — full UUID is
// included only when explicitly requested (?reveal=1) for the edit
// flow.
type profileResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Server      string    `json:"server"`
	Port        int       `json:"port"`
	UUIDMask    string    `json:"uuid_mask"`
	UUID        string    `json:"uuid,omitempty"` // only when ?reveal=1
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

// toResponseFull is toResponse + the un-masked UUID. Used by the edit
// flow so the form can pre-fill every field. Callers must gate this
// on an explicit ?reveal=1 from a basic-auth'd LAN client.
func toResponseFull(p store.Profile, active bool) profileResponse {
	r := toResponse(p, active)
	r.UUID = p.UUID
	return r
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
	reveal := r.URL.Query().Get("reveal") == "1"
	activeID, _ := s.UCI.Get(r.Context(), uciActiveKey)
	out := make([]profileResponse, 0, len(list))
	for _, p := range list {
		if reveal {
			out = append(out, toResponseFull(p, p.ID == activeID))
		} else {
			out = append(out, toResponse(p, p.ID == activeID))
		}
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"profiles":  out,
		"active_id": activeID,
		"generated": time.Now().UTC().Format(time.RFC3339),
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

// profilePatchReq is the body shape of PATCH /api/profiles/{id}.
// Any field set replaces the existing one; omitted fields stay as-is.
// Pointers used so we can tell "field set to empty string" apart from
// "field not present in request".
type profilePatchReq struct {
	Name        *string `json:"name,omitempty"`
	Server      *string `json:"server,omitempty"`
	Port        *int    `json:"port,omitempty"`
	UUID        *string `json:"uuid,omitempty"`
	Flow        *string `json:"flow,omitempty"`
	SNI         *string `json:"sni,omitempty"`
	Fingerprint *string `json:"fingerprint,omitempty"`
	PublicKey   *string `json:"public_key,omitempty"`
	ShortID     *string `json:"short_id,omitempty"`
}

func (s *Server) handleProfilePatch(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("missing id"))
		return
	}

	var req profilePatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}

	existing, err := s.Profiles.Get(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, nethttp.StatusNotFound, err)
			return
		}
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	// Apply set fields onto existing.
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Server != nil {
		existing.Server = *req.Server
	}
	if req.Port != nil {
		existing.Port = *req.Port
	}
	if req.UUID != nil {
		existing.UUID = *req.UUID
	}
	if req.Flow != nil {
		existing.Flow = *req.Flow
	}
	if req.SNI != nil {
		existing.SNI = *req.SNI
	}
	if req.Fingerprint != nil {
		existing.Fingerprint = *req.Fingerprint
	}
	if req.PublicKey != nil {
		existing.PublicKey = *req.PublicKey
	}
	if req.ShortID != nil {
		existing.ShortID = *req.ShortID
	}

	// Validate the result has every required field — easy to forget
	// that PATCH means partial input but the *result* must be whole.
	if existing.Server == "" || existing.Port < 1 || existing.UUID == "" ||
		existing.SNI == "" || existing.PublicKey == "" || existing.ShortID == "" {
		writeErr(w, nethttp.StatusBadRequest,
			fmt.Errorf("after patch the profile is missing one of: server, port, uuid, sni, public_key, short_id"))
		return
	}

	if err := s.Profiles.Update(existing); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	// Re-render config so the on-disk version reflects the edited
	// profile. If sing-box is running, reload (changing server/uuid/
	// reality bits requires a real outbound rebuild — clash-API has
	// no way to mutate outbound parameters in place).
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	all, _ := s.Profiles.List()
	activeID, _ := s.UCI.Get(ctx, uciActiveKey)
	if len(all) > 0 {
		if err := s.Renderer.WriteAndCheck(ctx, all, activeID); err != nil {
			writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("render: %w", err))
			return
		}
	}

	reloaded := false
	if running, _ := s.Probe.SingBoxRunning(ctx); running {
		if err := s.Service.Do(ctx, service.ActionReload); err != nil {
			writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("reload: %w", err))
			return
		}
		_ = closeClashConnections(ctx, s.Cfg.ClashAPI)
		reloaded = true
	}

	s.nudgeExitIP()

	writeJSON(w, nethttp.StatusOK, map[string]any{
		"ok":       true,
		"id":       id,
		"reloaded": reloaded,
		"profile":  toResponse(existing, id == activeID),
	})
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

// handleProfileDelay measures latency through a specific profile by
// asking sing-box's clash-API to time an HTTP request via that
// outbound. Defaults: gstatic 204 endpoint, 5s timeout. Sing-box
// must be running and the outbound must be in its current config —
// since our render always includes every saved profile, that's
// always true after at least one activation has happened.
func (s *Server) handleProfileDelay(w nethttp.ResponseWriter, r *nethttp.Request) {
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
	if s.Clash == nil {
		writeErr(w, nethttp.StatusServiceUnavailable, fmt.Errorf("clash-API client not configured"))
		return
	}

	// Latency tests run *through* sing-box's clash-API — if sing-box
	// isn't running, return a specific error instead of a useless
	// 5-second timeout.
	if running, _ := s.Probe.SingBoxRunning(r.Context()); !running {
		writeJSON(w, nethttp.StatusOK, map[string]any{
			"id":    id,
			"ok":    false,
			"error": "sing-box is not running — start the service first",
		})
		return
	}

	testURL := r.URL.Query().Get("url")
	if testURL == "" {
		testURL = "https://www.gstatic.com/generate_204"
	}
	timeoutMs := 5000
	if v := r.URL.Query().Get("timeout"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n <= 30000 {
			timeoutMs = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(),
		time.Duration(timeoutMs+2000)*time.Millisecond)
	defer cancel()

	tag := singbox.TagOf(prof)
	delay, err := s.Clash.ProxyDelay(ctx, tag, testURL, timeoutMs)
	if err != nil {
		writeJSON(w, nethttp.StatusOK, map[string]any{
			"id":    id,
			"tag":   tag,
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"id":       id,
		"tag":      tag,
		"ok":       true,
		"delay_ms": delay,
	})
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
