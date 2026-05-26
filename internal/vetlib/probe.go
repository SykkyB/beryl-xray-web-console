package vetlib

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Probe runs the TCP-connect and (unless skipTLS) TLS-handshake stage
// against every entry, concurrently. Entries are mutated in place —
// TCPMs/TLSMs/TCPOK/TLSOK fields filled in.
//
// ctx cancellation is honored between entries: in-flight dials/handshakes
// finish on their own deadlines but no further work is started.
//
// progress, if non-nil, gets a non-blocking send each time an entry is
// finished. Backpressure-safe: a slow consumer just sees coarser updates.
func Probe(ctx context.Context, entries []*Entry, workers int, tcpTO, tlsTO time.Duration, skipTLS bool, progress chan<- Progress) *ProbeStats {
	st := &ProbeStats{}
	total := len(entries)
	if total == 0 {
		return st
	}
	if workers < 1 {
		workers = 1
	}
	if tcpTO <= 0 {
		tcpTO = 2 * time.Second
	}
	if tlsTO <= 0 {
		tlsTO = 4 * time.Second
	}

	ch := make(chan *Entry, 256)
	var wg sync.WaitGroup

	work := func() {
		defer wg.Done()
		for e := range ch {
			if ctx.Err() != nil {
				// Don't start new work after cancellation but still
				// drain the channel so producers don't block.
				atomic.AddUint64(&st.Done, 1)
				continue
			}
			probeOne(e, tcpTO, tlsTO, skipTLS)
			if e.TCPOK {
				atomic.AddUint64(&st.TCPOK, 1)
			}
			if e.TLSOK {
				atomic.AddUint64(&st.TLSOK, 1)
			}
			d := atomic.AddUint64(&st.Done, 1)
			if progress != nil {
				select {
				case progress <- Progress{
					Stage: "probe",
					Total: total,
					Done:  int(d),
					TCPOK: int(atomic.LoadUint64(&st.TCPOK)),
					TLSOK: int(atomic.LoadUint64(&st.TLSOK)),
				}:
				default:
				}
			}
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go work()
	}

	// Feed in a separate goroutine so ctx cancellation can break out of
	// the feed loop too.
	go func() {
		defer close(ch)
		for _, e := range entries {
			select {
			case <-ctx.Done():
				return
			case ch <- e:
			}
		}
	}()

	wg.Wait()
	return st
}

// probeOne does the actual TCP + (optional) TLS work for one entry.
// Pulled out as a free function so it's easy to unit-test against a
// loopback echo server.
func probeOne(e *Entry, tcpTO, tlsTO time.Duration, skipTLS bool) {
	addr := net.JoinHostPort(e.Server, strconv.Itoa(e.Port))
	tcpStart := time.Now()
	conn, err := net.DialTimeout("tcp", addr, tcpTO)
	if err != nil {
		return
	}
	e.TCPMs = int(time.Since(tcpStart).Milliseconds())
	e.TCPOK = true

	if skipTLS {
		_ = conn.Close()
		return
	}

	// Reality fronts a real site's cert; InsecureSkipVerify is fine
	// because we're not validating identity — we're just confirming
	// the candidate completes a TLS handshake at all. A failure here
	// usually means the port is open but Reality isn't actually
	// configured (e.g. plain ssh or http listener).
	cfg := &tls.Config{InsecureSkipVerify: true}
	if e.SNI != "" {
		cfg.ServerName = e.SNI
	} else {
		cfg.ServerName = e.Server
	}
	tlsConn := tls.Client(conn, cfg)
	_ = tlsConn.SetDeadline(time.Now().Add(tlsTO))
	tlsStart := time.Now()
	if err := tlsConn.Handshake(); err == nil {
		e.TLSMs = int(time.Since(tlsStart).Milliseconds())
		e.TLSOK = true
	}
	_ = tlsConn.Close()
}
