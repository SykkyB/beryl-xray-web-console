package http

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Source is one VLESS-list provider that VPN Scout will fetch from.
// Kind distinguishes user-added sources (persisted to disk) from
// hard-coded presets (in-binary; can only be enabled/disabled).
type Source struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`     // empty when Path is set
	Path    string `json:"path,omitempty"` // local file path on the router
	Kind    string `json:"kind"`    // "preset" | "user"
	Enabled bool   `json:"enabled"`
}

// presetSources are baked-in source definitions. Identical names to the
// ones in cmd/vless-vet/main.go's sourcePresets so users moving between
// the CLI and the panel see consistent labels.
var presetSources = []Source{
	{
		ID:      "preset:kort0881",
		Name:    "kort0881 (clean)",
		URL:     "https://raw.githubusercontent.com/kort0881/vpn-vless-configs-russia/main/githubmirror/clean/vless.txt",
		Kind:    "preset",
		Enabled: true,
	},
	{
		ID:      "preset:kort0881-ru",
		Name:    "kort0881 (RU-SNI)",
		URL:     "https://raw.githubusercontent.com/kort0881/vpn-vless-configs-russia/main/githubmirror/ru-sni/vless.txt",
		Kind:    "preset",
		Enabled: false,
	},
}

// sourcesFile is the on-disk persistence shape. We split user sources
// from preset overrides so adding a new preset in a new release just
// shows up; user list and per-preset enable flags persist.
type sourcesFile struct {
	UserSources    []Source        `json:"user_sources"`
	PresetEnabled  map[string]bool `json:"preset_enabled"` // ID → enabled
}

// sourceStore is a thin file-backed store with a mutex so concurrent
// /api/sources requests can't trample each other.
type sourceStore struct {
	mu   sync.Mutex
	path string
}

func newSourceStore(path string) *sourceStore { return &sourceStore{path: path} }

// load reads the file. Missing file = empty store (not an error).
func (s *sourceStore) load() (*sourcesFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &sourcesFile{PresetEnabled: map[string]bool{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var f sourcesFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if f.PresetEnabled == nil {
		f.PresetEnabled = map[string]bool{}
	}
	return &f, nil
}

func (s *sourceStore) save(f *sourcesFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// allSources merges presets + user, applying PresetEnabled overrides.
func (s *sourceStore) allSources() ([]Source, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(presetSources)+len(f.UserSources))
	for _, p := range presetSources {
		if v, ok := f.PresetEnabled[p.ID]; ok {
			p.Enabled = v
		}
		out = append(out, p)
	}
	out = append(out, f.UserSources...)
	return out, nil
}

// ── HTTP handlers ──────────────────────────────────────────────────

// GET /api/sources
func (s *Server) handleSourcesList(w nethttp.ResponseWriter, r *nethttp.Request) {
	st := newSourceStore(s.Cfg.ScoutWithDefaults().SourcesFile)
	src, err := st.allSources()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"sources": src})
}

// POST /api/sources  body: {name, url?, path?, enabled?}
func (s *Server) handleSourcesAdd(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Path    string `json:"path"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.URL = strings.TrimSpace(body.URL)
	body.Path = strings.TrimSpace(body.Path)
	if body.Name == "" {
		writeErr(w, nethttp.StatusBadRequest, errors.New("name required"))
		return
	}
	if body.URL == "" && body.Path == "" {
		writeErr(w, nethttp.StatusBadRequest, errors.New("url or path required"))
		return
	}
	if body.URL != "" && body.Path != "" {
		writeErr(w, nethttp.StatusBadRequest, errors.New("url and path are mutually exclusive"))
		return
	}
	if body.URL != "" && !strings.HasPrefix(body.URL, "http://") && !strings.HasPrefix(body.URL, "https://") {
		writeErr(w, nethttp.StatusBadRequest, errors.New("url must be http(s)://"))
		return
	}
	if body.Path != "" && !filepath.IsAbs(body.Path) {
		writeErr(w, nethttp.StatusBadRequest, errors.New("path must be absolute"))
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	st := newSourceStore(s.Cfg.ScoutWithDefaults().SourcesFile)
	f, err := st.load()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	src := Source{
		ID:      "user:" + newRandHex(8),
		Name:    body.Name,
		URL:     body.URL,
		Path:    body.Path,
		Kind:    "user",
		Enabled: enabled,
	}
	f.UserSources = append(f.UserSources, src)
	if err := st.save(f); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "source": src})
}

// PATCH /api/sources/{id}  body: {name?, enabled?}
func (s *Server) handleSourcesUpdate(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	var body struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}

	st := newSourceStore(s.Cfg.ScoutWithDefaults().SourcesFile)
	f, err := st.load()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	if strings.HasPrefix(id, "preset:") {
		// Only enabled flag can be flipped on presets.
		if body.Enabled == nil {
			writeErr(w, nethttp.StatusBadRequest, errors.New("preset sources only accept {enabled}"))
			return
		}
		// Validate the preset exists.
		known := false
		for _, p := range presetSources {
			if p.ID == id {
				known = true
				break
			}
		}
		if !known {
			writeErr(w, nethttp.StatusNotFound, errors.New("unknown preset id"))
			return
		}
		if f.PresetEnabled == nil {
			f.PresetEnabled = map[string]bool{}
		}
		f.PresetEnabled[id] = *body.Enabled
	} else {
		// Find and update user source.
		found := false
		for i := range f.UserSources {
			if f.UserSources[i].ID == id {
				if body.Name != nil {
					f.UserSources[i].Name = strings.TrimSpace(*body.Name)
				}
				if body.Enabled != nil {
					f.UserSources[i].Enabled = *body.Enabled
				}
				found = true
				break
			}
		}
		if !found {
			writeErr(w, nethttp.StatusNotFound, errors.New("source not found"))
			return
		}
	}
	if err := st.save(f); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/sources/{id}
func (s *Server) handleSourcesDelete(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	if strings.HasPrefix(id, "preset:") {
		writeErr(w, nethttp.StatusBadRequest, errors.New("preset sources can only be disabled, not deleted"))
		return
	}
	st := newSourceStore(s.Cfg.ScoutWithDefaults().SourcesFile)
	f, err := st.load()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	before := len(f.UserSources)
	out := f.UserSources[:0]
	for _, sc := range f.UserSources {
		if sc.ID != id {
			out = append(out, sc)
		}
	}
	if len(out) == before {
		writeErr(w, nethttp.StatusNotFound, errors.New("source not found"))
		return
	}
	f.UserSources = out
	if err := st.save(f); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true})
}

func newRandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a time-based ID if /dev/urandom is unavailable —
		// uniqueness within a session is enough for source IDs.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
