// Package exitip runs a small background goroutine that periodically
// asks an echo service (api.ipify.org) what public IP we appear from.
// Because the panel runs ON the router, that fetch traverses sing-tun
// when the tunnel is up, so the answer is the VPN exit IP. With the
// tunnel down, the answer is the WAN-side public IP (or an error if
// the killswitch dropped the request).
package exitip

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"sync"
	"time"
)

// Poller asks URL for the public IP every Interval. Snapshot returns
// the latest result without blocking.
type Poller struct {
	URL      string        // e.g. "https://api.ipify.org"
	Interval time.Duration // default 30s
	Timeout  time.Duration // per-fetch HTTP timeout, default 8s

	HC *nethttp.Client

	mu        sync.RWMutex
	value     string
	fetchedAt time.Time
	lastErr   error
}

// Snapshot is the read-only side: thread-safe, never blocks.
type Snapshot struct {
	IP        string        // empty if no successful fetch yet
	FetchedAt time.Time     // zero if never fetched
	Age       time.Duration // 0 if never fetched
	Err       string        // empty on success
}

func (p *Poller) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := Snapshot{IP: p.value, FetchedAt: p.fetchedAt}
	if !p.fetchedAt.IsZero() {
		s.Age = time.Since(p.fetchedAt).Round(time.Second)
	}
	if p.lastErr != nil {
		s.Err = p.lastErr.Error()
	}
	return s
}

func (p *Poller) interval() time.Duration {
	if p.Interval == 0 {
		return 30 * time.Second
	}
	return p.Interval
}

func (p *Poller) timeout() time.Duration {
	if p.Timeout == 0 {
		return 8 * time.Second
	}
	return p.Timeout
}

func (p *Poller) hc() *nethttp.Client {
	if p.HC != nil {
		return p.HC
	}
	return &nethttp.Client{Timeout: p.timeout()}
}

// Start runs the poller until ctx is cancelled. Safe to call once per
// Poller. It fires immediately to populate the cache, then ticks.
func (p *Poller) Start(ctx context.Context) {
	go func() {
		p.fetchOnce(ctx)
		t := time.NewTicker(p.interval())
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.fetchOnce(ctx)
			}
		}
	}()
}

// RefreshNow triggers an immediate out-of-band fetch. Safe to call
// concurrently with the background ticker — both serialize on the
// internal mutex when writing the result.
func (p *Poller) RefreshNow(ctx context.Context) {
	p.fetchOnce(ctx)
}

func (p *Poller) fetchOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, p.timeout())
	defer cancel()

	req, err := nethttp.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if err != nil {
		p.recordErr(err)
		return
	}
	resp, err := p.hc().Do(req)
	if err != nil {
		p.recordErr(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		p.recordErr(fmt.Errorf("%s", resp.Status))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		p.recordErr(err)
		return
	}
	ip := strings.TrimSpace(string(body))
	if ip == "" {
		p.recordErr(fmt.Errorf("empty body"))
		return
	}

	p.mu.Lock()
	p.value = ip
	p.fetchedAt = time.Now()
	p.lastErr = nil
	p.mu.Unlock()
}

func (p *Poller) recordErr(err error) {
	p.mu.Lock()
	p.lastErr = err
	p.mu.Unlock()
}
