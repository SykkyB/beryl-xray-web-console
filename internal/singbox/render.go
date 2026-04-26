// Package singbox renders /etc/sing-box/config.json from an embedded
// template and a panel-managed Profile, validates it via `sing-box check`,
// and writes it atomically. Activating a profile means: render → check
// → write → reload service. The reload itself is the caller's job
// (internal/service.Manager.Do(ActionReload)).
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
	// ConfigPath is where the rendered JSON lands (typically
	// /etc/sing-box/config.json).
	ConfigPath string

	// SingBoxBin is the binary used for `sing-box check`. Empty disables
	// validation (useful for tests where the binary isn't present).
	SingBoxBin string

	// Runner runs sing-box check; defaults to runner.Exec{}.
	Runner runner.Runner

	// CheckTimeout caps how long `sing-box check` may take.
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

// templateFuncs adds a `json` filter so the template can produce
// JSON-safe quoted strings without the caller worrying about embedded
// quotes / newlines / unicode in profile fields.
var templateFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	},
}

// Render returns the JSON bytes that would land on disk for the given
// profile. Always parses the result through json.Unmarshal as a sanity
// check (catches a malformed template before we even try sing-box check).
func Render(p store.Profile) ([]byte, error) {
	t, err := template.New("config").Funcs(templateFuncs).Parse(configTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	data := struct {
		Server      string
		Port        int
		UUID        string
		Flow        string
		SNI         string
		Fingerprint string
		PublicKey   string
		ShortID     string
	}{
		Server:      p.Server,
		Port:        p.Port,
		UUID:        p.UUID,
		Flow:        p.Flow, // empty string is fine for plain VLESS
		SNI:         p.SNI,
		Fingerprint: p.Fingerprint,
		PublicKey:   p.PublicKey,
		ShortID:     p.ShortID,
	}
	if data.Fingerprint == "" {
		data.Fingerprint = "chrome"
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	// Defensive parse — guarantees we never write malformed JSON to disk.
	var probe any
	if err := json.Unmarshal(buf.Bytes(), &probe); err != nil {
		return nil, fmt.Errorf("template produced invalid JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteAndCheck renders the profile, runs `sing-box check` on the
// candidate config, and only on success writes it atomically to
// ConfigPath. Returns the captured stderr from `sing-box check` on
// validation failure so the caller can surface it.
func (r *Renderer) WriteAndCheck(ctx context.Context, p store.Profile) error {
	raw, err := Render(p)
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

// checkConfig pipes raw to `sing-box check -c <tmp>` because the binary
// can't read from stdin in the version we ship. We use a temp file in
// the same directory as ConfigPath to avoid cross-fs issues.
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
