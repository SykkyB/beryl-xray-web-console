// Package singbox renders /etc/sing-box/config.json from an embedded
// template and the panel's saved profiles, validates it via
// `sing-box check`, and writes it atomically.
//
// The rendered config always uses a sing-box `selector` outbound tagged
// "proxy" so that switching the active profile can be done online via
// the clash-API (`PUT /proxies/proxy {"name": "<vless-tag>"}`) without
// a restart. The selector's `default` is the currently-active profile —
// used both at sing-box startup and as a fallback if no clash switch
// has happened yet.
package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"beryl-xray-web-console/internal/runner"
	"beryl-xray-web-console/internal/store"
)

//go:embed config.tmpl.json
var configTemplate string

// Renderer wires the template to disk + sing-box check.
type Renderer struct {
	ConfigPath   string
	SingBoxBin   string
	Runner       runner.Runner
	CheckTimeout time.Duration
}

func (r *Renderer) runner() runner.Runner {
	if r.Runner == nil {
		return runner.Exec{}
	}
	return r.Runner
}

func (r *Renderer) checkTimeout() time.Duration {
	if r.CheckTimeout == 0 {
		return 10 * time.Second
	}
	return r.CheckTimeout
}

var templateFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	},
}

// renderProfile is what ends up in the template for each VLESS outbound.
type renderProfile struct {
	Tag         string
	Server      string
	Port        int
	UUID        string
	Flow        string
	SNI         string
	Fingerprint string
	PublicKey   string
	ShortID     string
}

type renderData struct {
	Profiles  []renderProfile
	Servers   []string
	ActiveTag string
}

// TagOf returns the stable outbound tag for a profile. Derived from the
// profile ID so it survives renames; first 12 hex chars of the UUID
// after stripping dashes — short enough to read in clash logs but
// unambiguous in practice.
func TagOf(p store.Profile) string {
	id := strings.ReplaceAll(p.ID, "-", "")
	if len(id) > 12 {
		id = id[:12]
	}
	return "vless-" + id
}

// Render returns the JSON bytes that would land on disk for the given
// list of profiles, with `activeID` selected as the selector default.
// At least one profile is required. activeID must match one of them;
// if it doesn't, the first profile is used as the default.
func Render(profiles []store.Profile, activeID string) ([]byte, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("at least one profile required to render")
	}

	data := renderData{
		Profiles: make([]renderProfile, 0, len(profiles)),
		Servers:  make([]string, 0, len(profiles)),
	}
	seenServer := make(map[string]bool)
	activeFound := false

	for _, p := range profiles {
		fp := p.Fingerprint
		if fp == "" {
			fp = "chrome"
		}
		tag := TagOf(p)
		data.Profiles = append(data.Profiles, renderProfile{
			Tag:         tag,
			Server:      p.Server,
			Port:        p.Port,
			UUID:        p.UUID,
			Flow:        p.Flow,
			SNI:         p.SNI,
			Fingerprint: fp,
			PublicKey:   p.PublicKey,
			ShortID:     p.ShortID,
		})
		if !seenServer[p.Server] {
			data.Servers = append(data.Servers, p.Server)
			seenServer[p.Server] = true
		}
		if p.ID == activeID {
			data.ActiveTag = tag
			activeFound = true
		}
	}
	if !activeFound {
		data.ActiveTag = data.Profiles[0].Tag
	}

	t, err := template.New("config").Funcs(templateFuncs).Parse(configTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	// Defensive parse — guarantees we never write malformed JSON.
	var probe any
	if err := json.Unmarshal(buf.Bytes(), &probe); err != nil {
		return nil, fmt.Errorf("template produced invalid JSON: %w\n--- output ---\n%s", err, buf.String())
	}
	return buf.Bytes(), nil
}

// WriteAndCheck renders the profiles, runs `sing-box check` on the
// candidate config, and only on success writes it atomically.
func (r *Renderer) WriteAndCheck(ctx context.Context, profiles []store.Profile, activeID string) error {
	raw, err := Render(profiles, activeID)
	if err != nil {
		return err
	}

	if r.SingBoxBin != "" {
		if err := r.checkConfig(ctx, raw); err != nil {
			return fmt.Errorf("sing-box check rejected the config: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(r.ConfigPath), 0o700); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	tmp := r.ConfigPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, r.ConfigPath); err != nil {
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}

func (r *Renderer) checkConfig(ctx context.Context, raw []byte) error {
	ctx, cancel := context.WithTimeout(ctx, r.checkTimeout())
	defer cancel()

	dir := filepath.Dir(r.ConfigPath)
	tmp, err := os.CreateTemp(dir, "config.check.*.json")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	_, stderr, err := r.runner().Run(ctx, r.SingBoxBin, "check", "-c", tmpName)
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
