// Package service wraps the sing-box procd init script and its custom
// extra-commands (killswitch_*, bind_switch_*) so HTTP handlers can
// drive sing-box without each one having to know how to shell out.
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"beryl-xray-web-console/internal/runner"
)

// Manager manages a single sing-box instance via its procd init.
type Manager struct {
	InitScript string        // e.g. /etc/init.d/sing-box
	Runner     runner.Runner // default: runner.Exec{}
	Timeout    time.Duration // default: 15s for init.d ops; 5s for status probes
}

func (m *Manager) runner() runner.Runner {
	if m.Runner == nil {
		return runner.Exec{}
	}
	return m.Runner
}

func (m *Manager) timeout() time.Duration {
	if m.Timeout == 0 {
		return 15 * time.Second
	}
	return m.Timeout
}

// Action is a constrained type for /etc/init.d/sing-box <action>.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionReload  Action = "reload"
)

// IsValidAction returns true for an action the panel is allowed to invoke.
func IsValidAction(a string) bool {
	switch Action(a) {
	case ActionStart, ActionStop, ActionRestart, ActionReload:
		return true
	}
	return false
}

// Do invokes /etc/init.d/sing-box <action>. Returns the captured stderr
// on failure for surfacing to the user.
func (m *Manager) Do(ctx context.Context, a Action) error {
	ctx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	_, stderr, err := m.runner().Run(ctx, m.InitScript, string(a))
	if err != nil {
		return fmt.Errorf("init %s: %w (%s)", a, err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// SetKillswitch toggles the killswitch via the init's extra-commands.
// The init persists state in UCI (sing-box.config.killswitch) and applies
// the rule live.
func (m *Manager) SetKillswitch(ctx context.Context, on bool) error {
	cmd := "killswitch_off"
	if on {
		cmd = "killswitch_on"
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	_, stderr, err := m.runner().Run(ctx, m.InitScript, cmd)
	if err != nil {
		return fmt.Errorf("init %s: %w (%s)", cmd, err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// SetBindSwitch toggles whether sing-box follows the GL.iNet physical
// mode switch.
func (m *Manager) SetBindSwitch(ctx context.Context, on bool) error {
	cmd := "bind_switch_off"
	if on {
		cmd = "bind_switch_on"
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	_, stderr, err := m.runner().Run(ctx, m.InitScript, cmd)
	if err != nil {
		return fmt.Errorf("init %s: %w (%s)", cmd, err, strings.TrimSpace(string(stderr)))
	}
	return nil
}
