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
		// Filter TLS-passers, sort by fastest TLS handshake, cap to
		// MaxDeep. The stage is slow (~10s per probe on 4 workers),
		// so 500 TLS-OK candidates would be 20+ minutes uncapped.
		todo := pickDeepCandidates(entries, opts.MaxDeep)
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
// passed to DeepProbe: TLS-passers only, sorted by fastest TLS
// handshake, capped at max (0 = no cap). Entries themselves are NOT
// mutated; the returned slice is a new selection.
func pickDeepCandidates(entries []*Entry, max int) []*Entry {
	tlsPassers := make([]*Entry, 0, len(entries))
	for _, e := range entries {
		if e.TLSOK {
			tlsPassers = append(tlsPassers, e)
		}
	}
	sort.SliceStable(tlsPassers, func(i, j int) bool {
		ai, aj := tlsPassers[i].TLSMs, tlsPassers[j].TLSMs
		if ai == 0 {
			ai = 1 << 30
		}
		if aj == 0 {
			aj = 1 << 30
		}
		return ai < aj
	})
	if max > 0 && len(tlsPassers) > max {
		tlsPassers = tlsPassers[:max]
	}
	return tlsPassers
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
