// Package store persists the panel's VLESS profile list to disk as a
// JSON array. One file (default /etc/xray-panel-cli/profiles.json),
// atomic writes via temp + rename, RW mutex around all operations.
//
// Profiles are addressed by an opaque ID the caller assigns (typically a
// UUIDv4). The store does not generate IDs — that lives in the http
// handler so we can keep this package side-effect-free for tests.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Profile is one saved VLESS endpoint. Defaults: Type="tcp",
// Security="reality" — keeps profiles imported before the WS / TLS
// transport split working without migration.
type Profile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Server      string    `json:"server"`
	Port        int       `json:"port"`
	UUID        string    `json:"uuid"`
	Flow        string    `json:"flow,omitempty"`
	SNI         string    `json:"sni"`
	Fingerprint string    `json:"fingerprint,omitempty"`

	// Reality fields — required when Security == "reality".
	PublicKey string `json:"public_key,omitempty"`
	ShortID   string `json:"short_id,omitempty"`

	// Transport: "tcp" (default) or "ws".
	// For "ws" the additional fields below come into play.
	Type string `json:"type,omitempty"`

	// Security: "reality" (default) or "tls".
	Security string `json:"security,omitempty"`

	// Path is the WebSocket path. "/" if absent. Ignored when Type == "tcp".
	Path string `json:"path,omitempty"`

	// Host is the WebSocket Host header (a.k.a. ws-host). May differ
	// from Server when fronting through a CDN. Ignored when Type == "tcp".
	Host string `json:"host,omitempty"`

	// RawURL is the original vless:// link as imported. Stored
	// verbatim so QR generation + clipboard share are byte-identical
	// to what the user pasted (URL-encoded paths, exact fragment
	// name, etc.). Legacy profiles imported before this field existed
	// have RawURL == ""; the QR handler falls back to BuildURL for
	// those.
	RawURL string `json:"raw_url,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EffectiveType returns Type with the historical default applied.
func (p Profile) EffectiveType() string {
	if p.Type == "" {
		return "tcp"
	}
	return p.Type
}

// EffectiveSecurity returns Security with the historical default applied.
func (p Profile) EffectiveSecurity() string {
	if p.Security == "" {
		return "reality"
	}
	return p.Security
}

// Profiles is the on-disk profile store.
type Profiles struct {
	Path string

	mu sync.RWMutex
}

// ErrNotFound means an id was looked up but not present in the file.
var ErrNotFound = errors.New("profile not found")

// ErrIDExists is returned when Add receives a profile whose ID already exists.
var ErrIDExists = errors.New("profile id already exists")

// load reads the file from disk. A missing file is treated as an empty list.
func (p *Profiles) load() ([]Profile, error) {
	data, err := os.ReadFile(p.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.Path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var list []Profile
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.Path, err)
	}
	return list, nil
}

// save serialises list and writes it atomically.
func (p *Profiles) save(list []Profile) error {
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := p.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, p.Path); err != nil {
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}

// List returns all profiles in insertion order. Empty slice if file missing.
func (p *Profiles) List() ([]Profile, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.load()
}

// Get returns a single profile by ID, or ErrNotFound.
func (p *Profiles) Get(id string) (Profile, error) {
	list, err := p.List()
	if err != nil {
		return Profile{}, err
	}
	for _, x := range list {
		if x.ID == id {
			return x, nil
		}
	}
	return Profile{}, ErrNotFound
}

// Add appends a profile. Returns ErrIDExists if the ID is already in use.
func (p *Profiles) Add(prof Profile) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	list, err := p.load()
	if err != nil {
		return err
	}
	for _, x := range list {
		if x.ID == prof.ID {
			return ErrIDExists
		}
	}
	if prof.CreatedAt.IsZero() {
		prof.CreatedAt = time.Now().UTC()
	}
	if prof.UpdatedAt.IsZero() {
		prof.UpdatedAt = prof.CreatedAt
	}
	list = append(list, prof)
	return p.save(list)
}

// Update replaces the profile with the same ID. Returns ErrNotFound if missing.
func (p *Profiles) Update(prof Profile) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	list, err := p.load()
	if err != nil {
		return err
	}
	for i, x := range list {
		if x.ID == prof.ID {
			if prof.CreatedAt.IsZero() {
				prof.CreatedAt = x.CreatedAt
			}
			prof.UpdatedAt = time.Now().UTC()
			list[i] = prof
			return p.save(list)
		}
	}
	return ErrNotFound
}

// Delete removes the profile by ID. Returns ErrNotFound if missing.
func (p *Profiles) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	list, err := p.load()
	if err != nil {
		return err
	}
	for i, x := range list {
		if x.ID == id {
			list = append(list[:i], list[i+1:]...)
			return p.save(list)
		}
	}
	return ErrNotFound
}
