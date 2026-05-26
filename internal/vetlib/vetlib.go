// Package vetlib is the reusable probe library extracted from
// cmd/vless-vet. The CLI tool is now a thin wrapper around these
// functions, and the xray-panel-cli HTTP handler in internal/http/scan.go
// uses the same pipeline to power the "VPN Scout" dashboard tab.
//
// The pipeline is a 3-stage funnel that mirrors the strictness of the
// underlying network operations:
//
//	parse     vless:// URL syntax check; cheap, deterministic
//	  ↓
//	tcp/tls   network reachability and Reality-front cert check (~1-2s)
//	  ↓
//	deep      spawn a one-shot sing-box, send HTTP/204 via SOCKS (~5-15s)
//
// Each stage filters down; only TLS-passers go to deep. A typical
// 11k-line public list with ~3k unique IPs ends up around 50-200 deep-OK
// entries.
package vetlib

import "time"

// Entry is one parsed vless:// URL plus probe results filled in over
// the lifetime of a Run. Fields that didn't pass a stage are zeroed.
type Entry struct {
	URL       string // original vless:// line
	Name      string // friendly name from the URL fragment (decoded)
	Server    string
	Port      int
	SNI       string
	Transport string // "tcp" | "ws"
	Security  string // "reality" | "tls" | "none"

	// Country (ISO-3166-1 alpha-2) parsed from flag emoji in Name; "" if
	// no flag is present.
	Country string

	// Latencies in milliseconds. 0 = not measured or probe failed.
	// TCPMs   = raw TCP connect time
	// TLSMs   = TLS handshake alone (server hello + finished)
	// DeepMs  = HTTP/204 wall-clock through the candidate's VLESS tunnel
	//           — the user-meaningful number.
	TCPMs  int
	TLSMs  int
	DeepMs int

	TCPOK  bool
	TLSOK  bool
	DeepOK bool
}

// Bucket returns the standard "transport+security" tag used for
// grouping in the report ("tcp+reality", "ws+tls", …).
func (e *Entry) Bucket() string {
	return e.Transport + "+" + e.Security
}

// ParseStats summarises stage 1 — how many input lines reached the
// parser, how many were accepted, and why the rest were rejected.
type ParseStats struct {
	Total    int            // input lines that started with vless://
	Parsed   int            // accepted by internal/vless
	Rejected int            // Total - Parsed
	Reasons  map[string]int // breakdown of rejection reasons
	Skipped  int            // Parsed - len(out) due to dedup
}

// ProbeStats summarises stage 2 — how many entries passed each network
// check. Updated atomically during Probe so a polling caller can read
// it mid-run.
type ProbeStats struct {
	TCPOK uint64
	TLSOK uint64
	Done  uint64 // entries processed so far (success or fail)
}

// DeepStats summarises stage 3.
type DeepStats struct {
	Tested  uint64
	VLESSOK uint64
	Done    uint64
}

// Options drives Run. Zero values pick sensible defaults; see
// resolveDefaults for what those are.
type Options struct {
	// Inputs is a sequence of io.Readers whose contents will be
	// concatenated and scanned for vless:// lines.
	Inputs []NamedReader

	// TCP/TLS stage
	Workers    int           // concurrent TCP/TLS probes (default 64)
	TCPTimeout time.Duration // per-probe TCP dial timeout (default 2s)
	TLSTimeout time.Duration // per-probe TLS handshake timeout (default 4s)
	SkipTLS    bool          // run TCP-only — much faster, less informative

	// Deep stage
	Deep         bool          // run the deep probe at all
	SingBoxBin   string        // sing-box binary path (used iff Deep)
	DeepWorkers  int           // concurrent deep probes (default 4)
	DeepTimeout  time.Duration // per-probe timeout (default 10s)
	MaxDeep      int           // global cap on deep candidates (0 = no cap)
	// MaxPerCountry caps how many TLS-passers each country contributes
	// to the deep stage. Round-robin selection across countries then
	// fills up to MaxDeep, so a single popular region can't dominate
	// the budget. 0 = no per-country cap. Default in the panel: 30.
	MaxPerCountry int
	HardTimeout   time.Duration // total wall-clock budget; 0 = no cap

	// DedupByAddr collapses entries with the same Server:Port to one
	// representative (the first encountered). Public lists are full of
	// duplicates; on by default in the panel.
	DedupByAddr bool
}

// NamedReader wraps an io.Reader with a human-readable name (file path
// or URL) so progress messages can attribute lines back to a source.
type NamedReader struct {
	Name   string
	Reader interface{} // io.Reader; typed loosely so callers don't need to import io
}

// Progress is the optional channel sent through Run. The caller drains
// it as fast as it likes; Run drops sends if the channel is full so a
// slow consumer can't stall the pipeline.
type Progress struct {
	Stage   string // "parse" | "probe" | "deep" | "done"
	Total   int    // total items expected at this stage
	Done    int    // items completed so far
	TCPOK   int    // (probe stage only)
	TLSOK   int    // (probe stage only)
	VLESSOK int    // (deep stage only)
}

// Result is what Run returns when it completes.
type Result struct {
	Entries     []*Entry
	ParseStats  ParseStats
	ProbeStats  ProbeStats
	DeepStats   DeepStats
	Elapsed     time.Duration
	Cancelled   bool // ctx cancelled mid-run
	TimedOut    bool // HardTimeout exceeded
}
