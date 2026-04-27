// vless-vet reads a list of vless:// URLs (one per line, comments
// starting with '#' allowed), keeps only those compatible with
// xray-panel-cli (VLESS+Reality+Vision over TCP, parses cleanly),
// then probes each one's reachability:
//
//   1) TCP connect within -tcp-timeout
//   2) TLS handshake with the URL's SNI within -tls-timeout
//
// URLs that pass both probes are written to the output file in their
// original form. The output starts with a header summarising how many
// were rejected at each stage, so the file is self-describing.
//
// Usage:
//
//	go run ./cmd/vless-vet -in samples/raw.txt
//	go run ./cmd/vless-vet -in samples/raw.txt -out alive.txt
//	go run ./cmd/vless-vet -in samples/raw.txt -workers 128 -tcp-timeout 2s
//
// Defaults output path = INPUT with ".alive" inserted before the ext.
package main

import (
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"beryl-xray-web-console/internal/vless"
)

func main() {
	var (
		inPath     = flag.String("in", "", "input file with vless:// URLs (one per line)")
		outPath    = flag.String("out", "", "output file (default: <in>.alive<ext>)")
		workers    = flag.Int("workers", 64, "concurrent probes")
		tcpTimeout = flag.Duration("tcp-timeout", 3*time.Second, "TCP connect timeout")
		tlsTimeout = flag.Duration("tls-timeout", 4*time.Second, "TLS handshake timeout")
		skipTLS    = flag.Bool("skip-tls", false, "TCP-only check; don't attempt TLS handshake")
	)
	flag.Parse()

	if *inPath == "" {
		fmt.Fprintln(os.Stderr, "missing -in <file>")
		flag.Usage()
		os.Exit(2)
	}
	if *outPath == "" {
		*outPath = defaultOutputPath(*inPath)
	}

	// ── stage 1: read + parse, filter to compatible URLs ──────────────
	urls, parseStats, err := readAndParse(*inPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("parsed: %d / %d (rejected by parser: %d)\n",
		parseStats.parsed, parseStats.total, parseStats.rejected)

	// ── stage 2: probe each URL ───────────────────────────────────────
	startedAt := time.Now()
	probeStats := probe(urls, *workers, *tcpTimeout, *tlsTimeout, *skipTLS)
	elapsed := time.Since(startedAt).Round(time.Second)

	// ── stage 3: write output ─────────────────────────────────────────
	if err := writeOutput(*outPath, urls, parseStats, probeStats, *skipTLS, elapsed); err != nil {
		fmt.Fprintln(os.Stderr, "write output:", err)
		os.Exit(1)
	}

	fmt.Printf("DONE in %s — kept %d/%d (tcp_ok=%d tls_ok=%d) → %s\n",
		elapsed, probeStats.alive(*skipTLS), parseStats.parsed,
		probeStats.tcpOK, probeStats.tlsOK, *outPath)
}

func defaultOutputPath(in string) string {
	dir, base := filepath.Split(in)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, stem+".alive"+ext)
}

// ── parse stage ───────────────────────────────────────────────────────

type parseStats struct {
	total    int            // input lines that started with vless://
	parsed   int            // accepted by internal/vless
	rejected int            // total - parsed
	reasons  map[string]int // breakdown of rejection reasons
}

type entry struct {
	URL    string
	Server string
	Port   int
	SNI    string
	tcpOK  bool
	tlsOK  bool
}

func readAndParse(path string) ([]*entry, *parseStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	st := &parseStats{reasons: map[string]int{}}
	var entries []*entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "vless://") {
			continue
		}
		st.total++
		v, perr := vless.Parse(line)
		if perr != nil {
			st.rejected++
			key := bucket(perr.Error())
			st.reasons[key]++
			continue
		}
		st.parsed++
		entries = append(entries, &entry{
			URL: line, Server: v.Server, Port: v.Port, SNI: v.SNI,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}
	return entries, st, nil
}

// bucket trims a long error message to its first sentence so the
// summary header stays readable.
func bucket(s string) string {
	if i := strings.Index(s, "."); i >= 0 && i < 80 {
		return s[:i+1]
	}
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// ── probe stage ───────────────────────────────────────────────────────

type probeStats struct {
	tcpOK uint64
	tlsOK uint64
}

func (p *probeStats) alive(skipTLS bool) uint64 {
	if skipTLS {
		return p.tcpOK
	}
	return p.tlsOK
}

func probe(entries []*entry, workers int, tcpTO, tlsTO time.Duration, skipTLS bool) *probeStats {
	st := &probeStats{}
	if len(entries) == 0 {
		return st
	}

	ch := make(chan *entry, 256)
	var wg sync.WaitGroup
	var done uint64
	total := uint64(len(entries))

	work := func() {
		defer wg.Done()
		for e := range ch {
			// JoinHostPort handles IPv6 brackets correctly when hosts
			// happen to be literal v6 addresses.
			addr := net.JoinHostPort(e.Server, strconv.Itoa(e.Port))
			conn, err := net.DialTimeout("tcp", addr, tcpTO)
			if err != nil {
				atomic.AddUint64(&done, 1)
				continue
			}
			atomic.AddUint64(&st.tcpOK, 1)
			e.tcpOK = true

			if !skipTLS {
				cfg := &tls.Config{InsecureSkipVerify: true}
				if e.SNI != "" {
					cfg.ServerName = e.SNI
				} else {
					cfg.ServerName = e.Server
				}
				tlsConn := tls.Client(conn, cfg)
				_ = tlsConn.SetDeadline(time.Now().Add(tlsTO))
				if err := tlsConn.Handshake(); err == nil {
					atomic.AddUint64(&st.tlsOK, 1)
					e.tlsOK = true
				}
				_ = tlsConn.Close()
			} else {
				_ = conn.Close()
			}
			atomic.AddUint64(&done, 1)
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go work()
	}

	// Progress reporter — prints every 5s.
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
				fmt.Printf("  %d/%d  tcp_ok=%d tls_ok=%d\n",
					d, total,
					atomic.LoadUint64(&st.tcpOK),
					atomic.LoadUint64(&st.tlsOK))
			}
		}
	}()

	for _, e := range entries {
		ch <- e
	}
	close(ch)
	wg.Wait()
	close(stopProgress)
	return st
}

// ── write stage ───────────────────────────────────────────────────────

func writeOutput(path string, entries []*entry, ps *parseStats, qs *probeStats, skipTLS bool, took time.Duration) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	fmt.Fprintf(w, "# vless-vet report — %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "# Probe took %s. Two-stage check: parser then network.\n", took)
	fmt.Fprintf(w, "#\n")
	fmt.Fprintf(w, "# Parser stage\n")
	fmt.Fprintf(w, "#   input lines starting with vless:// : %d\n", ps.total)
	fmt.Fprintf(w, "#   parsed (TCP+Reality+pbk; sid/sni optional) : %d\n", ps.parsed)
	fmt.Fprintf(w, "#   rejected : %d\n", ps.rejected)
	if len(ps.reasons) > 0 {
		fmt.Fprintf(w, "#     reasons:\n")
		for r, n := range ps.reasons {
			fmt.Fprintf(w, "#       %5d  %s\n", n, r)
		}
	}
	fmt.Fprintf(w, "#\n")
	fmt.Fprintf(w, "# Probe stage\n")
	fmt.Fprintf(w, "#   TCP connect succeeded : %d / %d\n", qs.tcpOK, ps.parsed)
	if !skipTLS {
		fmt.Fprintf(w, "#   TLS handshake succeeded : %d / %d  (kept in this file)\n",
			qs.tlsOK, ps.parsed)
		fmt.Fprintf(w, "#\n")
		fmt.Fprintf(w, "# A passing TLS handshake is a strong signal that the host is\n")
		fmt.Fprintf(w, "# alive AND properly configured for Reality (Reality forwards\n")
		fmt.Fprintf(w, "# the real handshake to its `dest` site). It does NOT prove\n")
		fmt.Fprintf(w, "# the post-handshake VLESS+Reality session will succeed —\n")
		fmt.Fprintf(w, "# import + Test in the panel for that.\n")
	} else {
		fmt.Fprintf(w, "# (TLS check was skipped via -skip-tls)\n")
	}
	fmt.Fprintln(w, "#")

	for _, e := range entries {
		var keep bool
		if skipTLS {
			keep = e.tcpOK
		} else {
			keep = e.tlsOK
		}
		if keep {
			fmt.Fprintln(w, e.URL)
		}
	}
	return nil
}
