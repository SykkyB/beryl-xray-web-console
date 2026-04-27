// vless-vet reads a list of vless:// URLs (one per line, comments
// starting with '#' allowed), keeps only those compatible with
// xray-panel-cli (parses cleanly), then probes each one's reachability:
//
//   1) TCP connect within -tcp-timeout
//   2) TLS handshake with the URL's SNI within -tls-timeout
//
// URLs that pass both probes are written to the output file in their
// original form, GROUPED by transport+security combination so the most
// preferred bucket comes first ("tcp+reality"), then anything else
// (e.g. "ws+tls"). The output starts with a header summarising how
// many were rejected at each stage and how many alive ones each
// bucket got, so the file is self-describing.
//
// Usage:
//
//	go run ./cmd/vless-vet -in samples/raw.txt
//	go run ./cmd/vless-vet -in samples/raw.txt -out alive.txt
//	go run ./cmd/vless-vet -in samples/raw.txt -workers 128 -tcp-timeout 2s
//
// Filter the output to specific buckets with -only (comma-separated):
//
//	-only tcp+reality              # only the canonical bucket
//	-only ws+tls,tcp+tls           # WebSocket+TLS and TCP+TLS
//	-only tcp+*                    # any security on TCP
//	-only *+reality                # Reality on any transport
//
// For high-fidelity verification (slow), pass -deep — each TLS-passer
// is then re-tested by spinning up a one-shot local sing-box with the
// candidate as a SOCKS-routed VLESS outbound and making an HTTP/1.0
// GET through it. Only profiles whose VLESS session actually carries
// traffic land in the alive set:
//
//	-deep                                    # default: sing-box from PATH
//	-deep -singbox /usr/local/bin/sing-box   # custom binary path
//	-deep -deep-workers 8 -deep-timeout 12s
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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"beryl-xray-web-console/internal/vless"
)

// preferredOrder is the bucket precedence used for output grouping.
// Buckets not in this list still appear after these, sorted alphabetically,
// so the most-supported combos lead and the long tail follows.
var preferredOrder = []string{
	"tcp+reality",
	"tcp+tls",
	"ws+tls",
	"ws+reality",
}

func main() {
	var (
		inPath     = flag.String("in", "", "input file with vless:// URLs (one per line)")
		outPath    = flag.String("out", "", "output file (default: <in>.alive<ext>)")
		workers    = flag.Int("workers", 64, "concurrent probes")
		tcpTimeout = flag.Duration("tcp-timeout", 3*time.Second, "TCP connect timeout")
		tlsTimeout = flag.Duration("tls-timeout", 4*time.Second, "TLS handshake timeout")
		skipTLS    = flag.Bool("skip-tls", false, "TCP-only check; don't attempt TLS handshake")
		only       = flag.String("only", "", "comma-separated bucket filter, e.g. \"tcp+reality,ws+tls\". "+
			"Wildcards allowed: \"tcp+*\", \"*+reality\". Empty = keep all buckets.")
		deep        = flag.Bool("deep", false, "after TLS, spin up local sing-box per profile and verify the VLESS session actually carries traffic (slow; needs sing-box binary)")
		singboxBin  = flag.String("singbox", "sing-box", "sing-box binary path (used when -deep is set; sing-box must be installed locally)")
		deepWorkers = flag.Int("deep-workers", 4, "concurrent deep probes (each spawns a sing-box process; keep low)")
		deepTimeout = flag.Duration("deep-timeout", 10*time.Second, "per-profile deep-probe timeout (sing-box startup + HTTP-via-SOCKS request)")
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

	filter, err := parseOnly(*only)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if *deep && *skipTLS {
		fmt.Fprintln(os.Stderr, "-deep and -skip-tls are mutually exclusive (deep-probe filters by TLS-passers)")
		os.Exit(2)
	}
	if *deep {
		if _, err := exec.LookPath(*singboxBin); err != nil {
			fmt.Fprintf(os.Stderr,
				"-deep: %q not found in PATH (use -singbox /path/to/sing-box). "+
					"On macOS: `brew install sing-box`. On the router: /usr/bin/sing-box.\n",
				*singboxBin)
			os.Exit(1)
		}
	}

	// ── stage 1: read + parse, filter to compatible URLs ──────────────
	urls, parseStats, err := readAndParse(*inPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("parsed: %d / %d (rejected by parser: %d)\n",
		parseStats.parsed, parseStats.total, parseStats.rejected)

	// ── stage 2: TCP + TLS probe ──────────────────────────────────────
	startedAt := time.Now()
	probeStats := probe(urls, *workers, *tcpTimeout, *tlsTimeout, *skipTLS)

	// ── stage 2.5: deep VLESS-session probe (opt-in) ──────────────────
	var deepStat *deepStats
	if *deep {
		deepStat = deepProbe(urls, *singboxBin, *deepWorkers, *deepTimeout)
	}
	elapsed := time.Since(startedAt).Round(time.Second)

	// ── stage 3: group + write output ─────────────────────────────────
	groups := groupByBucket(urls, *skipTLS, *deep)
	if err := writeOutput(*outPath, groups, parseStats, probeStats, deepStat, *skipTLS, *deep, filter, elapsed); err != nil {
		fmt.Fprintln(os.Stderr, "write output:", err)
		os.Exit(1)
	}

	kept := 0
	for _, g := range groups {
		if filter == nil || filter.matches(g.bucket) {
			kept += len(g.entries)
		}
	}
	header := fmt.Sprintf("kept %d/%d (tcp_ok=%d tls_ok=%d",
		kept, parseStats.parsed, probeStats.tcpOK, probeStats.tlsOK)
	if deepStat != nil {
		header += fmt.Sprintf(" vless_ok=%d", deepStat.vlessOK)
	}
	header += ")"
	fmt.Printf("DONE in %s — %s → %s\n", elapsed, header, *outPath)
	for _, g := range groups {
		mark := " "
		if filter != nil && !filter.matches(g.bucket) {
			mark = "-"
		}
		fmt.Printf("  %s %-14s %d\n", mark, g.bucket, len(g.entries))
	}
}

func defaultOutputPath(in string) string {
	dir, base := filepath.Split(in)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, stem+".alive"+ext)
}

// ── -only filter ──────────────────────────────────────────────────────

// onlyFilter is a list of bucket patterns. Empty list = match nothing
// (caller checks for nil to mean "no filter, keep everything").
type onlyFilter struct {
	patterns []bucketPattern
}

type bucketPattern struct {
	transport string // "" = wildcard
	security  string // "" = wildcard
}

func parseOnly(s string) (*onlyFilter, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	f := &onlyFilter{}
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(strings.ToLower(raw))
		if raw == "" {
			continue
		}
		parts := strings.SplitN(raw, "+", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("-only: bucket %q must look like transport+security (e.g. tcp+reality, ws+*)", raw)
		}
		t, sec := parts[0], parts[1]
		if t == "*" {
			t = ""
		}
		if sec == "*" {
			sec = ""
		}
		f.patterns = append(f.patterns, bucketPattern{transport: t, security: sec})
	}
	if len(f.patterns) == 0 {
		return nil, nil
	}
	return f, nil
}

func (f *onlyFilter) matches(bucket string) bool {
	if f == nil {
		return true
	}
	parts := strings.SplitN(bucket, "+", 2)
	if len(parts) != 2 {
		return false
	}
	t, sec := parts[0], parts[1]
	for _, p := range f.patterns {
		if (p.transport == "" || p.transport == t) &&
			(p.security == "" || p.security == sec) {
			return true
		}
	}
	return false
}

// ── parse stage ───────────────────────────────────────────────────────

type parseStats struct {
	total    int            // input lines that started with vless://
	parsed   int            // accepted by internal/vless
	rejected int            // total - parsed
	reasons  map[string]int // breakdown of rejection reasons
}

type entry struct {
	URL       string
	Name      string // friendly name from URL fragment (decoded)
	Server    string
	Port      int
	SNI       string
	Transport string // tcp / ws
	Security  string // reality / tls

	// Latencies are in milliseconds; 0 = not measured / probe failed.
	// tcpMs is the raw TCP connect time; tlsMs is the TLS handshake
	// alone (server hello + finished); deepMs is the user-meaningful
	// number — wall-clock time of an HTTP/204 fetch through the
	// candidate's VLESS-tunneled SOCKS proxy.
	tcpMs  int
	tlsMs  int
	deepMs int

	tcpOK  bool
	tlsOK  bool
	deepOK bool // VLESS session actually carried HTTP traffic (only set when -deep)
}

func (e *entry) bucket() string {
	return e.Transport + "+" + e.Security
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
			URL: line, Name: v.Name,
			Server: v.Server, Port: v.Port, SNI: v.SNI,
			Transport: v.Type, Security: v.Security,
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
			tcpStart := time.Now()
			conn, err := net.DialTimeout("tcp", addr, tcpTO)
			if err != nil {
				atomic.AddUint64(&done, 1)
				continue
			}
			e.tcpMs = int(time.Since(tcpStart).Milliseconds())
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
				tlsStart := time.Now()
				if err := tlsConn.Handshake(); err == nil {
					e.tlsMs = int(time.Since(tlsStart).Milliseconds())
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

// ── grouping ──────────────────────────────────────────────────────────

type group struct {
	bucket  string // "tcp+reality", "ws+tls", …
	entries []*entry
}

// groupByBucket keeps only alive entries and sorts the resulting groups
// so preferredOrder leads, then alphabetical. The "alive" criterion
// depends on the strictest stage that was run: -deep > default(TLS) >
// -skip-tls.
func groupByBucket(entries []*entry, skipTLS, deep bool) []group {
	by := map[string][]*entry{}
	for _, e := range entries {
		var alive bool
		switch {
		case deep:
			alive = e.deepOK
		case skipTLS:
			alive = e.tcpOK
		default:
			alive = e.tlsOK
		}
		if !alive {
			continue
		}
		b := e.bucket()
		by[b] = append(by[b], e)
	}

	rank := map[string]int{}
	for i, b := range preferredOrder {
		rank[b] = i
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, oki := rank[keys[i]]
		rj, okj := rank[keys[j]]
		switch {
		case oki && okj:
			return ri < rj
		case oki:
			return true
		case okj:
			return false
		default:
			return keys[i] < keys[j]
		}
	})

	out := make([]group, 0, len(keys))
	for _, k := range keys {
		es := by[k]
		// Sort entries by the strictest latency we have: deep > tls >
		// tcp. Within the same metric, ascending — fastest first, so
		// the user can copy from the top of the file and get the most
		// responsive profiles without further fiddling.
		sort.SliceStable(es, func(i, j int) bool {
			return latencyForSort(es[i], skipTLS, deep) < latencyForSort(es[j], skipTLS, deep)
		})
		out = append(out, group{bucket: k, entries: es})
	}
	return out
}

func latencyForSort(e *entry, skipTLS, deep bool) int {
	switch {
	case deep:
		return e.deepMs
	case skipTLS:
		return e.tcpMs
	default:
		return e.tlsMs
	}
}

// ── write stage ───────────────────────────────────────────────────────

func writeOutput(path string, groups []group, ps *parseStats, qs *probeStats, ds *deepStats, skipTLS, deep bool, filter *onlyFilter, took time.Duration) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	stages := "parser → TCP → TLS"
	if skipTLS {
		stages = "parser → TCP"
	}
	if deep {
		stages = "parser → TCP → TLS → VLESS-session (sing-box SOCKS+HTTP)"
	}
	fmt.Fprintf(w, "# vless-vet report — %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "# Pipeline: %s. Took %s.\n", stages, took)
	if filter != nil {
		fmt.Fprintf(w, "# Output filtered via -only (other buckets dropped from this file).\n")
	}
	fmt.Fprintf(w, "#\n")
	fmt.Fprintf(w, "# Parser stage\n")
	fmt.Fprintf(w, "#   input lines starting with vless:// : %d\n", ps.total)
	fmt.Fprintf(w, "#   parsed (TCP/WS + Reality/TLS) : %d\n", ps.parsed)
	fmt.Fprintf(w, "#   rejected : %d\n", ps.rejected)
	if len(ps.reasons) > 0 {
		fmt.Fprintf(w, "#     reasons:\n")
		for r, n := range ps.reasons {
			fmt.Fprintf(w, "#       %5d  %s\n", n, r)
		}
	}
	fmt.Fprintf(w, "#\n")
	fmt.Fprintf(w, "# Probe stage\n")
	keptStage := "TLS handshake"
	if skipTLS {
		keptStage = "TCP connect"
	}
	if deep {
		keptStage = "VLESS session (deep)"
	}
	fmt.Fprintf(w, "#   TCP connect succeeded : %d / %d\n", qs.tcpOK, ps.parsed)
	if !skipTLS {
		fmt.Fprintf(w, "#   TLS handshake succeeded : %d / %d\n", qs.tlsOK, ps.parsed)
	}
	if deep && ds != nil {
		fmt.Fprintf(w, "#   VLESS session OK : %d / %d  (HTTP/204 fetched through sing-box+SOCKS)\n",
			ds.vlessOK, ds.tested)
	}
	fmt.Fprintf(w, "#   kept in this file : entries that passed %s\n", keptStage)
	if deep {
		fmt.Fprintf(w, "#\n")
		fmt.Fprintf(w, "# Deep-probe verifies post-handshake VLESS plumbing — a profile\n")
		fmt.Fprintf(w, "# is kept ONLY if a real HTTP request to www.gstatic.com/generate_204\n")
		fmt.Fprintf(w, "# completed through it. This catches stale UUID / pbk / sid combos\n")
		fmt.Fprintf(w, "# that pass the fronted TLS handshake but fail the VLESS session.\n")
	} else if !skipTLS {
		fmt.Fprintf(w, "#\n")
		fmt.Fprintf(w, "# A passing TLS handshake is a strong signal that the host is\n")
		fmt.Fprintf(w, "# alive AND configured for the advertised security layer. It\n")
		fmt.Fprintf(w, "# does NOT prove the post-handshake VLESS session will succeed —\n")
		fmt.Fprintf(w, "# re-run with -deep, or import + Test in the panel for that.\n")
	}
	fmt.Fprintf(w, "#\n")

	fmt.Fprintf(w, "# Alive buckets (transport+security):\n")
	if len(groups) == 0 {
		fmt.Fprintf(w, "#   (none)\n")
	}
	for _, g := range groups {
		mark := " "
		if filter != nil && !filter.matches(g.bucket) {
			mark = "-"
		}
		fmt.Fprintf(w, "#  %s %-14s %d\n", mark, g.bucket, len(g.entries))
	}
	fmt.Fprintln(w, "#")

	metric := "tls"
	if skipTLS {
		metric = "tcp"
	}
	if deep {
		metric = "deep"
	}
	for _, g := range groups {
		if filter != nil && !filter.matches(g.bucket) {
			continue
		}
		fmt.Fprintf(w, "\n# ── %s (%d, sorted by %s latency asc) ──\n", g.bucket, len(g.entries), metric)
		for _, e := range g.entries {
			lat := latencyForSort(e, skipTLS, deep)
			name := e.Name
			if name == "" {
				name = e.Server
			}
			fmt.Fprintf(w, "#  %5dms  %s\n", lat, name)
			fmt.Fprintln(w, e.URL)
		}
	}
	return nil
}
