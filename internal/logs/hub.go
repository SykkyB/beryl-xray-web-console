package logs

import (
	"context"
	"sync"
)

// Hub multiplexes a single Streamer over many SSE subscribers. It runs
// at most one Follow() goroutine — started lazily when the first
// subscriber arrives, stopped when the last leaves — so N browser tabs
// don't translate into N independent file tails.
//
// Fan-out is non-blocking: if a subscriber's buffered channel is full
// (slow consumer) the line is dropped for that subscriber only,
// never stalling the streamer or other subscribers.
type Hub struct {
	Path     string
	BufLines int // per-subscriber channel buffer; default 128

	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	cancel context.CancelFunc // non-nil while the streamer goroutine is alive
}

type subscriber struct {
	ch chan string
}

// NewHub returns a Hub that tails path. Nothing runs until the first
// Subscribe.
func NewHub(path string) *Hub {
	return &Hub{
		Path: path,
		subs: make(map[*subscriber]struct{}),
	}
}

// Subscribe returns a receive-only channel of new log lines plus an
// unsubscribe func the caller MUST call when done (typically via
// defer). The channel is buffered so brief stalls in the consumer
// don't drop lines instantly.
func (h *Hub) Subscribe() (<-chan string, func()) {
	bufN := h.BufLines
	if bufN <= 0 {
		bufN = 128
	}
	sub := &subscriber{ch: make(chan string, bufN)}

	h.mu.Lock()
	h.subs[sub] = struct{}{}
	first := len(h.subs) == 1
	if first {
		h.startLocked()
	}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		if _, ok := h.subs[sub]; ok {
			delete(h.subs, sub)
			close(sub.ch)
		}
		// Stop the streamer when nobody's listening to save the CPU.
		if len(h.subs) == 0 && h.cancel != nil {
			h.cancel()
			h.cancel = nil
		}
		h.mu.Unlock()
	}

	return sub.ch, unsub
}

// startLocked spawns the Streamer goroutine. Caller MUST hold h.mu.
func (h *Hub) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	in := make(chan string, 64)
	go func() {
		s := &Streamer{Path: h.Path}
		_ = s.Follow(ctx, in)
		close(in)
	}()

	go h.fanOut(ctx, in)
}

func (h *Hub) fanOut(ctx context.Context, in <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-in:
			if !ok {
				return
			}
			h.mu.Lock()
			for sub := range h.subs {
				select {
				case sub.ch <- line:
				default:
					// Slow consumer — drop this line for them rather
					// than stall every other subscriber. SSE has no
					// gap-detection, so the user won't notice unless
					// they're really backed up.
				}
			}
			h.mu.Unlock()
		}
	}
}

// SubscriberCount is exposed for diagnostics / metrics.
func (h *Hub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
