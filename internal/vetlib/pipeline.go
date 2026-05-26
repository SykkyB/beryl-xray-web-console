package vetlib

import (
	"context"
	"errors"
	"io"
	"sort"
	"time"
)

// Run executes the full parse → probe → deep pipeline against the
// inputs in opts. It honors ctx cancellation and opts.HardTimeout.
//
// progress is optional. When non-nil, the channel receives Progress
// messages over the run. Drop semantics are non-blocking so a slow
// consumer can never stall the pipeline. Run closes progress on return
// so a `for p := range progress` loop terminates cleanly.
//
// The returned Result.Entries is the parsed (and deduped, if requested)
// slice with probe results filled in. Sorted by the strictest passing
// latency descending: DeepMs (asc) when Deep, else TLSMs, else TCPMs.
func Run(ctx context.Context, opts Options, progress chan<- Progress) (*Result, error) {
	if progress != nil {
		defer close(progress)
	}

	if len(opts.Inputs) == 0 {
		return nil, errors.New("no inputs")
	}
	if opts.Workers <= 0 {
		opts.Workers = 64
	}
	if opts.TCPTimeout <= 0 {
		opts.TCPTimeout = 2 * time.Second
	}
	if opts.TLSTimeout <= 0 {
		opts.TLSTimeout = 4 * time.Second
	}
	if opts.DeepWorkers <= 0 {
		opts.DeepWorkers = 4
	}
	if opts.DeepTimeout <= 0 {
		opts.DeepTimeout = 10 * time.Second
	}

	runCtx := ctx
	if opts.HardTimeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.HardTimeout)
		defer cancel()
	}

	start := time.Now()

	// ── stage 1: parse ────────────────────────────────────────────
	if progress != nil {
		select {
		case progress <- Progress{Stage: "parse", Total: 0, Done: 0}:
		default:
		}
	}

	r, err := chainReaders(opts.Inputs)
	if err != nil {
		return nil, err
	}
	entries, pst, err := ReadAndParse(r)
	if err != nil {
		return nil, err
	}
	if opts.DedupByAddr {
		var dropped int
		entries, dropped = DedupByAddr(entries)
		pst.Skipped = dropped
	}
	if progress != nil {
		select {
		case progress <- Progress{Stage: "parse", Total: pst.Total, Done: pst.Total}:
		default:
		}
	}

	// Early outs: cancellation between stages
	if runCtx.Err() != nil {
		return &Result{
			Entries:    entries,
			ParseStats: *pst,
			Elapsed:    time.Since(start),
			Cancelled:  errors.Is(runCtx.Err(), context.Canceled),
			TimedOut:   errors.Is(runCtx.Err(), context.DeadlineExceeded),
		}, nil
	}

	// ── stage 2: TCP / TLS ────────────────────────────────────────
	probeSt := Probe(runCtx, entries, opts.Workers, opts.TCPTimeout, opts.TLSTimeout, opts.SkipTLS, progress)

	if runCtx.Err() != nil {
		return &Result{
			Entries:    entries,
			ParseStats: *pst,
			ProbeStats: *probeSt,
			Elapsed:    time.Since(start),
			Cancelled:  errors.Is(runCtx.Err(), context.Canceled),
			TimedOut:   errors.Is(runCtx.Err(), context.DeadlineExceeded),
		}, nil
	}

	// ── stage 3: deep ─────────────────────────────────────────────
	deepSt := &DeepStats{}
	if opts.Deep {
		// Pick TLS-passers fairly across countries: per-country cap
		// first, then round-robin until global MaxDeep is reached.
		// Prevents one popular region from sweeping the budget while
		// still finishing in bounded time.
		todo := pickDeepCandidates(entries, opts.MaxDeep, opts.MaxPerCountry)
		deepSt = DeepProbe(runCtx, todo, opts.SingBoxBin, opts.DeepWorkers, opts.DeepTimeout, progress)
	}

	sortByBestLatency(entries, opts.Deep, opts.SkipTLS)

	res := &Result{
		Entries:    entries,
		ParseStats: *pst,
		ProbeStats: *probeSt,
		DeepStats:  *deepSt,
		Elapsed:    time.Since(start),
		Cancelled:  errors.Is(runCtx.Err(), context.Canceled),
		TimedOut:   errors.Is(runCtx.Err(), context.DeadlineExceeded),
	}
	if progress != nil {
		select {
		case progress <- Progress{Stage: "done", Total: len(entries), Done: len(entries)}:
		default:
		}
	}
	return res, nil
}

// pickDeepCandidates returns the subset of entries that should be
// passed to DeepProbe: TLS-passers picked fairly across countries.
//
// Strategy:
//  1. Group by country (Entry.Country, "" = unknown).
//  2. Within each country, sort ascending by TLSMs (fastest first).
//  3. Apply perCountryCap (0 = no cap).
//  4. Round-robin across countries (1 from each in turn) until the
//     global max is reached or all buckets are drained.
//
// This way, a country with 500 fast TLS-passers can't sweep all 200
// global slots — every represented country gets at least one shot at
// deep verification.
//
// Country bucket order is deterministic (alphabetic by ISO code with
// "" sorted last) so the same input always produces the same picks —
// makes the report reproducible run-to-run.
func pickDeepCandidates(entries []*Entry, maxGlobal, perCountryCap int) []*Entry {
	// Group + sort within each country.
	byCC := map[string][]*Entry{}
	for _, e := range entries {
		if e.TLSOK {
			byCC[e.Country] = append(byCC[e.Country], e)
		}
	}
	for cc := range byCC {
		es := byCC[cc]
		sort.SliceStable(es, func(i, j int) bool {
			ai, aj := es[i].TLSMs, es[j].TLSMs
			if ai == 0 {
				ai = 1 << 30
			}
			if aj == 0 {
				aj = 1 << 30
			}
			return ai < aj
		})
		if perCountryCap > 0 && len(es) > perCountryCap {
			byCC[cc] = es[:perCountryCap]
		}
	}

	// Stable iteration order: alphabetical by country code, with the
	// unknown-country bucket ("") last so it doesn't displace named
	// countries in the early round-robin rounds.
	ccs := make([]string, 0, len(byCC))
	for cc := range byCC {
		ccs = append(ccs, cc)
	}
	sort.Slice(ccs, func(i, j int) bool {
		if ccs[i] == "" {
			return false
		}
		if ccs[j] == "" {
			return true
		}
		return ccs[i] < ccs[j]
	})

	cursors := make(map[string]int, len(ccs))
	out := make([]*Entry, 0, len(entries))
	for {
		progressed := false
		for _, cc := range ccs {
			bucket := byCC[cc]
			i := cursors[cc]
			if i >= len(bucket) {
				continue
			}
			out = append(out, bucket[i])
			cursors[cc] = i + 1
			progressed = true
			if maxGlobal > 0 && len(out) >= maxGlobal {
				return out
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// sortByBestLatency orders entries so the most-trusted, fastest ones
// come first: deep-OK then TLS-OK then TCP-OK then dead, each tier
// sorted ascending by its respective latency.
func sortByBestLatency(entries []*Entry, deep, skipTLS bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		// Tier rank: lower is better.
		ar, br := tierRank(a, deep, skipTLS), tierRank(b, deep, skipTLS)
		if ar != br {
			return ar < br
		}
		// Same tier — compare latency at that tier.
		al, bl := tierLatency(a, ar), tierLatency(b, br)
		if al == 0 {
			al = 1 << 30
		}
		if bl == 0 {
			bl = 1 << 30
		}
		return al < bl
	})
}

func tierRank(e *Entry, deep, skipTLS bool) int {
	if deep && e.DeepOK {
		return 0
	}
	if e.TLSOK && !skipTLS {
		return 1
	}
	if e.TCPOK {
		return 2
	}
	return 3
}

func tierLatency(e *Entry, rank int) int {
	switch rank {
	case 0:
		return e.DeepMs
	case 1:
		return e.TLSMs
	case 2:
		return e.TCPMs
	}
	return 0
}

// chainReaders builds a single io.Reader that reads each named input in
// sequence (a concatenation). io.MultiReader inserts an empty separator,
// which is fine: scanner.Scan ignores blank lines.
func chainReaders(inputs []NamedReader) (io.Reader, error) {
	readers := make([]io.Reader, 0, len(inputs))
	for _, in := range inputs {
		r, ok := in.Reader.(io.Reader)
		if !ok {
			return nil, errors.New("vetlib: NamedReader.Reader is not an io.Reader for " + in.Name)
		}
		readers = append(readers, r)
	}
	return io.MultiReader(readers...), nil
}
