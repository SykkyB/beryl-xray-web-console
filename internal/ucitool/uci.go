// Package ucitool is a thin wrapper over the OpenWrt `uci` CLI for the
// fields the panel reads (sing-box.config.{enabled,killswitch,
// bind_switch}). We don't reimplement UCI in Go — uci is always present
// on the router, and shelling out keeps semantics in lockstep with what
// init scripts and other GL.iNet tooling see.
package ucitool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"beryl-xray-web-console/internal/runner"
)

// Tool is a configured UCI client.
type Tool struct {
	Bin     string        // default: "uci"
	Runner  runner.Runner // default: runner.Exec{}
	Timeout time.Duration // default: 5s
}

func (t *Tool) bin() string {
	if t.Bin == "" {
		return "uci"
	}
	return t.Bin
}

func (t *Tool) runner() runner.Runner {
	if t.Runner == nil {
		return runner.Exec{}
	}
	return t.Runner
}

func (t *Tool) timeout() time.Duration {
	if t.Timeout == 0 {
		return 5 * time.Second
	}
	return t.Timeout
}

// Get returns the raw value of key (e.g. "sing-box.config.killswitch").
// Returns ("", nil) if the key is unset (uci exit 1 with no stderr).
func (t *Tool) Get(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	out, _, err := t.runner().Run(ctx, t.bin(), "-q", "get", key)
	if err != nil {
		// uci -q exits 1 silently when the key doesn't exist — treat as
		// empty so callers can choose a default.
		if ee, ok := runner.AsExitErr(err); ok && ee.ExitCode == 1 && ee.Stderr == "" {
			return "", nil
		}
		return "", fmt.Errorf("uci get %s: %w", key, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// GetBool reads key as a UCI boolean. Anything in {1, true, yes, on, enabled}
// is true; everything else (including unset) is false.
func (t *Tool) GetBool(ctx context.Context, key string) (bool, error) {
	v, err := t.Get(ctx, key)
	if err != nil {
		return false, err
	}
	return ParseBool(v), nil
}

// ParseBool mirrors the boolean recognition uci config_get_bool does.
// It is exported so http handlers can apply the same rule to incoming
// JSON values from the UI.
func ParseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	}
	return false
}

// Set runs `uci set key=value`. The caller is responsible for following
// up with Commit (or several Sets followed by one Commit) — the panel
// follows the same pattern OpenWrt scripts do.
func (t *Tool) Set(ctx context.Context, key, value string) error {
	ctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	_, _, err := t.runner().Run(ctx, t.bin(), "set", key+"="+value)
	if err != nil {
		return fmt.Errorf("uci set %s=%s: %w", key, value, err)
	}
	return nil
}

// Commit persists pending changes to /etc/config/<package>.
func (t *Tool) Commit(ctx context.Context, pkg string) error {
	ctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	_, _, err := t.runner().Run(ctx, t.bin(), "commit", pkg)
	if err != nil {
		return fmt.Errorf("uci commit %s: %w", pkg, err)
	}
	return nil
}
