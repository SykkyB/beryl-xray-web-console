// Package sysprobe answers runtime-shaped questions about the box that
// don't fit cleanly under any one of uci / service / clash:
//
//   - is sing-box actually running right now? (pgrep)
//   - does the sing-tun virtual interface exist? (ip link show)
//   - what position is the GL.iNet physical mode switch in? (gl_util.sh)
//
// Every probe is best-effort: if the underlying tool is missing or fails,
// it returns a zero/neutral value plus the error so the caller can show
// "unknown" without taking the whole status endpoint down.
package sysprobe

import (
	"context"
	"strings"
	"time"

	"beryl-xray-web-console/internal/runner"
)

// Probe is a configured runtime probe.
type Probe struct {
	Runner  runner.Runner // default: runner.Exec{}
	Timeout time.Duration // default: 5s
}

func (p *Probe) runner() runner.Runner {
	if p.Runner == nil {
		return runner.Exec{}
	}
	return p.Runner
}

func (p *Probe) timeout() time.Duration {
	if p.Timeout == 0 {
		return 5 * time.Second
	}
	return p.Timeout
}

// SingBoxRunning returns true if a sing-box process is currently running.
// Uses busybox pgrep with no flags — the default match is against
// /proc/PID/comm, which is exactly "sing-box" for our binary. We do NOT
// use `-x` because busybox's `-x` is broken on this build (always
// reports not-found even when the process exists), and we do NOT use
// `-f` because that matches anything with "sing-box" in its full
// cmdline (e.g. a shell whose argv carries the word).
func (p *Probe) SingBoxRunning(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	_, _, err := p.runner().Run(ctx, "pgrep", "sing-box")
	if err == nil {
		return true, nil
	}
	if ee, ok := runner.AsExitErr(err); ok && ee.ExitCode == 1 {
		// pgrep exit 1 = no process matched — that's a true "not running",
		// not an error.
		return false, nil
	}
	return false, err
}

// TunUp returns true if the sing-box TUN interface (sing-tun) is present
// and the kernel says it's up.
func (p *Probe) TunUp(ctx context.Context, ifname string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	out, _, err := p.runner().Run(ctx, "ip", "-o", "link", "show", "dev", ifname)
	if err != nil {
		// Most common failure: device doesn't exist (exit 1). Surface
		// "not up" without an error.
		if ee, ok := runner.AsExitErr(err); ok && (ee.ExitCode == 1 || ee.ExitCode == 255) {
			return false, nil
		}
		return false, err
	}
	// "ip link show" output contains state markers like "<...,UP,LOWER_UP,...>".
	// We treat anything with "LOWER_UP" or just "UP," as up.
	s := string(out)
	return strings.Contains(s, ",UP,") || strings.Contains(s, "<UP,") || strings.Contains(s, ",LOWER_UP"), nil
}

// NativeVPNActive returns true if any GL.iNet stock VPN tunnel
// (WireGuard wgclient*, OpenVPN ovpnclient*) currently has a live
// network interface. Used by the dashboard launcher to detect that
// the user has enabled a native client out-of-band, so it can stop
// sing-box to enforce mutual exclusion the other direction (XRAY
// already stops native via /api/native-vpn/stop on its own ON).
//
// We probe `ip -o link show` once and scan for known iface name
// prefixes. Cheap; runs alongside the other probes inside collectState.
func (p *Probe) NativeVPNActive(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	out, _, err := p.runner().Run(ctx, "ip", "-o", "link", "show")
	if err != nil {
		return false, err
	}
	s := string(out)
	// Stock GL.iNet names: wgclient1, wgclient2, …, ovpnclient1, …
	// Tor's iface is "tun0" but Tor isn't a stock-client we collide
	// with (it's a separate UI). Skip it.
	for _, prefix := range []string{"wgclient", "ovpnclient"} {
		// Lines look like: "18: wgclient1: <POINTOPOINT,…,UP,…> ..."
		// We need the iface name in the second column.
		for _, line := range strings.Split(s, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			name := strings.TrimSuffix(fields[1], ":")
			if strings.HasPrefix(name, prefix) {
				return true, nil
			}
		}
	}
	return false, nil
}

// SwitchPosition reads the GL.iNet physical mode switch via gl_util.sh.
// Returns "on", "off", or "unknown" (e.g. when the helper is absent).
func (p *Probe) SwitchPosition(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	out, _, err := p.runner().Run(ctx, "sh", "-c",
		". /lib/functions/gl_util.sh 2>/dev/null && get_switch_button_status 2>/dev/null")
	if err != nil {
		return "unknown", err
	}
	v := strings.TrimSpace(string(out))
	switch v {
	case "on", "off":
		return v, nil
	}
	return "unknown", nil
}
