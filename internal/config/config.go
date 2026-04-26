// Package config loads and validates the xray-panel-cli runtime
// configuration (panel.yaml). It does NOT touch sing-box's own config.json
// — that belongs to internal/singbox.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the panel's own runtime configuration. Everything the panel
// touches on the router is parameterised through here so nothing is
// hardcoded — same shape as the flint2 sibling so the deploy/install
// script can be largely identical.
type Config struct {
	// Listen is the TCP address (host:port) the panel's HTTP server binds to.
	// LAN-only by default; WAN-binding is rejected at startup elsewhere.
	Listen string `yaml:"listen"`

	// SingBoxConfig is the path to sing-box's runtime config
	// (e.g. /etc/sing-box/config.json). The panel rewrites it when the
	// active profile changes; sing-box itself reads it on start/reload.
	SingBoxConfig string `yaml:"sing_box_config"`

	// SingBoxBin is the sing-box binary path. Used to validate a
	// rendered config (`sing-box check -c …`) before activating it.
	SingBoxBin string `yaml:"sing_box_bin"`

	// SingBoxInit is the procd init script for the sing-box service —
	// used by /api/service/* endpoints (start/stop/restart/reload) and by
	// the killswitch_/bind_switch_ extra-commands.
	SingBoxInit string `yaml:"sing_box_init"`

	// SingBoxLog is the file the panel tails for the Logs tab.
	SingBoxLog string `yaml:"sing_box_log"`

	// ProfilesStore is the JSON file holding the array of VLESS+Reality
	// profiles the user has saved. The panel maintains it; sing-box has
	// no concept of profiles, only the active config.
	ProfilesStore string `yaml:"profiles_store"`

	// ClashAPI is the host:port of sing-box's clash-API
	// (experimental.clash_api). Panel proxies live data through it
	// (traffic, connections, latency tests) without restarting sing-box.
	ClashAPI string `yaml:"clash_api"`

	// ExitIPURL is the URL the background poller hits to discover the
	// effective public IP. Optional — defaults to "https://api.ipify.org"
	// in main.go if left empty. The body must be the raw IP string.
	ExitIPURL string `yaml:"exit_ip_url,omitempty"`

	Auth AuthConfig `yaml:"auth"`
}

// AuthConfig is the basic-auth credential pair. The password is stored as
// a bcrypt hash so the yaml file can sit on the router without leaking
// plaintext.
type AuthConfig struct {
	Username       string `yaml:"username"`
	PasswordBcrypt string `yaml:"password_bcrypt"`
}

// Load reads, parses, and validates the panel config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes and validates yaml bytes. Split from Load to make tests easy.
func Parse(raw []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen is required")
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if port == "" {
		return fmt.Errorf("listen %q: empty port", c.Listen)
	}
	for _, f := range []struct {
		name, val string
	}{
		{"sing_box_config", c.SingBoxConfig},
		{"sing_box_bin", c.SingBoxBin},
		{"sing_box_init", c.SingBoxInit},
		{"sing_box_log", c.SingBoxLog},
		{"profiles_store", c.ProfilesStore},
	} {
		if f.val == "" {
			return fmt.Errorf("%s is required", f.name)
		}
		if !filepath.IsAbs(f.val) {
			return fmt.Errorf("%s must be an absolute path, got %q", f.name, f.val)
		}
	}
	if c.ClashAPI == "" {
		return fmt.Errorf("clash_api is required")
	}
	if _, _, err := net.SplitHostPort(c.ClashAPI); err != nil {
		return fmt.Errorf("clash_api %q: %w", c.ClashAPI, err)
	}
	if c.Auth.Username == "" {
		return fmt.Errorf("auth.username is required")
	}
	if c.Auth.PasswordBcrypt == "" {
		return fmt.Errorf("auth.password_bcrypt is required")
	}
	if !strings.HasPrefix(c.Auth.PasswordBcrypt, "$2") {
		return fmt.Errorf("auth.password_bcrypt does not look like a bcrypt hash")
	}
	return nil
}
