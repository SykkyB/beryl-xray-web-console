// Deep-probe stage. After TLS handshake passes we still don't know if
// the VLESS session inside actually carries traffic — Reality fronts a
// real site's cert, so a "passing" TLS handshake just means the proxy
// is up to its first packet. The classic failure mode is correct cert
// + stale UUID/pbk/sid → handshake "succeeds" but no bytes flow.
//
// To prove a profile actually works we spin up a one-shot local
// sing-box per candidate with a minimal config:
//
//	socks-in (127.0.0.1:<ephemeral>)  →  vless-out (the candidate)
//
// then make a tiny HTTP/1.0 GET to www.gstatic.com/generate_204
// through that SOCKS. A 2xx response = the tunnel is real.
//
// This is *much* slower than TCP+TLS probe (each test = sing-box
// startup + handshake + request, ~2–10s per candidate), so it is
// gated behind -deep and only runs on TLS-passers.
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"beryl-xray-web-console/internal/vless"
)

type deepStats struct {
	tested  uint64
	vlessOK uint64
}

func deepProbe(entries []*entry, singboxBin string, workers int, perTimeout time.Duration) *deepStats {
	st := &deepStats{}

	var todo []*entry
	for _, e := range entries {
		if e.tlsOK {
			todo = append(todo, e)
		}
	}
	if len(todo) == 0 {
		fmt.Println("deep-probe: nothing to test (no TLS-passers)")
		return st
	}
	st.tested = uint64(len(todo))

	fmt.Printf("deep-probe: %d TLS-passers via sing-box (workers=%d, timeout=%s)\n",
		len(todo), workers, perTimeout)

	ch := make(chan *entry, 64)
	var wg sync.WaitGroup
	var done uint64
	total := uint64(len(todo))

	work := func() {
		defer wg.Done()
		for e := range ch {
			if lat, err := probeOneDeep(e, singboxBin, perTimeout); err == nil {
				atomic.AddUint64(&st.vlessOK, 1)
				e.deepOK = true
				e.deepMs = int(lat.Milliseconds())
			}
			atomic.AddUint64(&done, 1)
		}
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go work()
	}

	stopProgress := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopProgress:
				return
			case <-t.C:
				d := atomic.LoadUint64(&done)
				if d >= total {
					return
				}
				fmt.Printf("  deep %d/%d  vless_ok=%d\n",
					d, total, atomic.LoadUint64(&st.vlessOK))
			}
		}
	}()

	for _, e := range todo {
		ch <- e
	}
	close(ch)
	wg.Wait()
	close(stopProgress)
	fmt.Printf("deep-probe done: vless_ok=%d / %d\n", st.vlessOK, total)
	return st
}

// probeOneDeep returns the wall-clock time of the actual HTTP-via-SOCKS
// round trip on success — not counting sing-box startup, since startup
// time is sing-box overhead, not a property of the upstream link.
func probeOneDeep(e *entry, singboxBin string, timeout time.Duration) (time.Duration, error) {
	u, err := vless.Parse(e.URL)
	if err != nil {
		return 0, fmt.Errorf("re-parse: %w", err)
	}

	socksPort, err := pickEphemeralPort()
	if err != nil {
		return 0, fmt.Errorf("pick port: %w", err)
	}

	cfg, err := renderDeepConfig(u, socksPort)
	if err != nil {
		return 0, fmt.Errorf("render: %w", err)
	}

	f, err := os.CreateTemp("", "vless-vet-deep-*.json")
	if err != nil {
		return 0, fmt.Errorf("temp: %w", err)
	}
	cfgPath := f.Name()
	defer os.Remove(cfgPath)
	if _, err := f.Write(cfg); err != nil {
		f.Close()
		return 0, fmt.Errorf("write cfg: %w", err)
	}
	f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, singboxBin, "run", "-c", cfgPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start sing-box: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	socksAddr := "127.0.0.1:" + strconv.Itoa(socksPort)
	if err := waitForPort(ctx, socksAddr, 3*time.Second); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return 0, fmt.Errorf("sing-box not ready: %v / %s", err, firstLine(msg))
		}
		return 0, fmt.Errorf("sing-box not ready: %w", err)
	}

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	httpStart := time.Now()
	code, err := socks5HTTPGet(socksAddr, "www.gstatic.com", 80, "/generate_204", deadline)
	if err != nil {
		return 0, err
	}
	latency := time.Since(httpStart)
	if code != 204 && (code < 200 || code >= 300) {
		return 0, fmt.Errorf("unexpected http status %d", code)
	}
	return latency, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// renderDeepConfig builds a one-outbound sing-box config for the deep
// probe. We deliberately omit DNS / route-rules: VLESS outbound takes
// the destination domain as-is, the remote server resolves it. Direct
// outbound is included so sing-box has somewhere to route any internal
// chatter (none expected at this scale, but cheap insurance).
func renderDeepConfig(u *vless.URL, socksPort int) ([]byte, error) {
	type m = map[string]any

	fp := u.Fingerprint
	if fp == "" {
		fp = "chrome"
	}
	sni := u.SNI
	if sni == "" {
		if u.Host != "" {
			sni = u.Host
		} else {
			sni = u.Server
		}
	}

	tls := m{
		"enabled":     true,
		"server_name": sni,
		"utls":        m{"enabled": true, "fingerprint": fp},
	}
	if u.Security == "reality" {
		tls["reality"] = m{
			"enabled":    true,
			"public_key": u.PublicKey,
			"short_id":   u.ShortID,
		}
	}

	out := m{
		"type":        "vless",
		"tag":         "proxy",
		"server":      u.Server,
		"server_port": u.Port,
		"uuid":        u.UUID,
		"flow":        u.Flow,
		"tls":         tls,
	}
	if u.Type == "ws" {
		path := u.Path
		if path == "" {
			path = "/"
		}
		tr := m{"type": "ws", "path": path}
		if u.Host != "" {
			tr["headers"] = m{"Host": u.Host}
		}
		out["transport"] = tr
	}

	cfg := m{
		"log": m{"level": "error"},
		"inbounds": []m{
			{
				"type":        "socks",
				"tag":         "socks-in",
				"listen":      "127.0.0.1",
				"listen_port": socksPort,
			},
		},
		"outbounds": []m{
			out,
			{"type": "direct", "tag": "direct"},
		},
		"route": m{"final": "proxy"},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func pickEphemeralPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func waitForPort(ctx context.Context, addr string, max time.Duration) error {
	deadline := time.Now().Add(max)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("not listening in time")
}

// socks5HTTPGet does a no-auth SOCKS5 CONNECT to host:port and then a
// minimal HTTP/1.0 GET, returning the response status code. Hand-rolled
// to avoid pulling in golang.org/x/net just for proxy.SOCKS5.
func socks5HTTPGet(socksAddr, host string, port int, path string, deadline time.Time) (int, error) {
	to := time.Until(deadline)
	if to <= 0 {
		return 0, errors.New("deadline passed before HTTP request")
	}
	conn, err := net.DialTimeout("tcp", socksAddr, to)
	if err != nil {
		return 0, fmt.Errorf("socks dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	// Method negotiation: VER=5, NMETHODS=1, METHOD=0 (no auth).
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return 0, fmt.Errorf("socks greet: %w", err)
	}
	var rep [2]byte
	if _, err := io.ReadFull(conn, rep[:]); err != nil {
		return 0, fmt.Errorf("socks greet reply: %w", err)
	}
	if rep[0] != 0x05 || rep[1] != 0x00 {
		return 0, fmt.Errorf("socks no-auth not accepted: %02x %02x", rep[0], rep[1])
	}

	// CONNECT request: VER=5 CMD=1 RSV=0 ATYP=3 (domain) LEN host port.
	if len(host) > 255 {
		return 0, fmt.Errorf("host too long: %d", len(host))
	}
	req := make([]byte, 0, 7+len(host))
	req = append(req, 0x05, 0x01, 0x00, 0x03, byte(len(host)))
	req = append(req, []byte(host)...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return 0, fmt.Errorf("socks connect req: %w", err)
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return 0, fmt.Errorf("socks connect reply: %w", err)
	}
	if head[1] != 0x00 {
		return 0, fmt.Errorf("socks connect refused: rep=0x%02x", head[1])
	}
	var addrLen int
	switch head[3] {
	case 0x01:
		addrLen = 4
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return 0, fmt.Errorf("socks reply domain len: %w", err)
		}
		addrLen = int(l[0])
	case 0x04:
		addrLen = 16
	default:
		return 0, fmt.Errorf("socks reply unknown atyp 0x%02x", head[3])
	}
	skip := make([]byte, addrLen+2)
	if _, err := io.ReadFull(conn, skip); err != nil {
		return 0, fmt.Errorf("socks reply tail: %w", err)
	}

	// Tunnel ready. Send a tiny HTTP/1.0 GET — generate_204 returns 204
	// with no body, so we only need the status line.
	httpReq := fmt.Sprintf("GET %s HTTP/1.0\r\nHost: %s\r\nUser-Agent: vless-vet/1\r\nConnection: close\r\n\r\n",
		path, host)
	if _, err := conn.Write([]byte(httpReq)); err != nil {
		return 0, fmt.Errorf("http write: %w", err)
	}

	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("http read: %w", err)
	}
	parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("bad http status: %q", line)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("bad http status code: %q", parts[1])
	}
	return code, nil
}
