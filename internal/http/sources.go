package http

import (
	"context"
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
	URL     string `json:"url"`            // empty when Path is set
	Path    string `json:"path,omitempty"` // local file path on the router
	Kind    string `json:"kind"`           // "preset" | "user"
	Enabled bool   `json:"enabled"`

	// Meta is the last-fetch outcome. Populated by allSources from the
	// per-ID Meta map in sourcesFile when listing. Not persisted as
	// part of the source itself — admin-editing sources.json shouldn't
	// require touching transient meta fields.
	Meta SourceMeta `json:"meta,omitempty"`
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

// SourceMeta carries last-fetch outcome for a source. Surfaced to the
// UI alongside the source itself so the user can see whether the
// remote is reachable, when we last pulled it, and whether the body
// changed since the previous fetch. Updated by both the scan path
// (fetched as part of /api/scan/start) and /api/sources/refresh.
type SourceMeta struct {
	LastFetchedAt   string `json:"last_fetched_at,omitempty"`   // RFC3339 UTC
	LastStatus      string `json:"last_status,omitempty"`       // "ok" | "unchanged" | "error"
	LastError       string `json:"last_error,omitempty"`
	LastBytes       int    `json:"last_bytes,omitempty"`
	LastLines       int    `json:"last_lines,omitempty"`        // count of vless:// lines
	ContentHash     string `json:"content_hash,omitempty"`      // first 16 hex of sha256
	PrevContentHash string `json:"prev_content_hash,omitempty"` // for change detection across runs
	HTTPLastMod     string `json:"http_last_modified,omitempty"`
	HTTPETag        string `json:"http_etag,omitempty"`
}

// sourcesFile is the on-disk persistence shape. We split user sources
// from preset overrides so adding a new preset in a new release just
// shows up; user list and per-preset enable flags persist. Meta lives
// here too, keyed by source ID, so admins editing the file by hand
// don't have to worry about a parallel state file.
type sourcesFile struct {
	UserSources   []Source              `json:"user_sources"`
	PresetEnabled map[string]bool       `json:"preset_enabled"` // ID → enabled
	Meta          map[string]SourceMeta `json:"meta,omitempty"` // ID → meta
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

// allSources merges presets + user, applying PresetEnabled overrides
// and attaching per-source Meta from the same file.
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
		if m, ok := f.Meta[p.ID]; ok {
			p.Meta = m
		}
		out = append(out, p)
	}
	for _, u := range f.UserSources {
		if m, ok := f.Meta[u.ID]; ok {
			u.Meta = m
		}
		out = append(out, u)
	}
	return out, nil
}

// updateMeta atomically writes a single source's meta back to disk.
// Used by scan fetch and by /api/sources/refresh. Existing meta for
// the same ID is replaced wholesale.
func (s *sourceStore) updateMeta(id string, m SourceMeta) error {
	f, err := s.load()
	if err != nil {
		return err
	}
	if f.Meta == nil {
		f.Meta = map[string]SourceMeta{}
	}
	f.Meta[id] = m
	return s.save(f)
}

// previousMeta returns the meta we last persisted for id, or zero
// value if there isn't one. Cheap read — no error path needed since
// load() already handles missing file.
func (s *sourceStore) previousMeta(id string) SourceMeta {
	f, err := s.load()
	if err != nil || f == nil {
		return SourceMeta{}
	}
	return f.Meta[id]
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

// POST /api/sources/refresh   body: {ids?: [...]}  (omit = ALL sources)
// Fetches each picked source, updates per-source meta on disk, returns
// the updated list. Does NOT run a probe — strictly a "did the fetch
// succeed and is the content fresh?" check, takes ~1-3s for a
// handful of public lists.
//
// Refresh deliberately includes DISABLED sources too when ids is
// omitted: users want to see the state of a source before deciding
// whether to enable it. The scan path, by contrast, only picks
// enabled sources (see pickSources).
func (s *Server) handleSourcesRefresh(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	// Body is optional — empty {} or no body at all = refresh everything.
	_ = json.NewDecoder(r.Body).Decode(&body)

	store := newSourceStore(s.Cfg.ScoutWithDefaults().SourcesFile)
	all, err := store.allSources()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	var picked []Source
	if len(body.IDs) == 0 {
		// "Refresh all" — include disabled too. Otherwise a disabled
		// source can never get its meta probed, which defeats the
		// whole point of the metadata view.
		picked = all
	} else {
		picked = pickSources(all, body.IDs)
	}
	if len(picked) == 0 {
		writeErr(w, nethttp.StatusBadRequest, errors.New("no sources to refresh"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	results := make([]map[string]any, 0, len(picked))
	for _, src := range picked {
		_, meta, ferr := fetchSource(ctx, src)
		prev := store.previousMeta(src.ID)
		if prev.ContentHash != "" {
			meta.PrevContentHash = prev.ContentHash
			if ferr == nil && prev.ContentHash == meta.ContentHash {
				meta.LastStatus = "unchanged"
			}
		}
		_ = store.updateMeta(src.ID, meta)
		entry := map[string]any{
			"id":     src.ID,
			"name":   src.Name,
			"meta":   meta,
		}
		if ferr != nil {
			entry["error"] = ferr.Error()
		}
		results = append(results, entry)
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"ok":      true,
		"results": results,
	})
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
