package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"strconv"
	"time"

	"beryl-xray-web-console/internal/logs"
)

type logsResponse struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
	Count int      `json:"count"`
}

func (s *Server) handleLogs(w nethttp.ResponseWriter, r *nethttp.Request) {
	n := 100
	if v := r.URL.Query().Get("lines"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 1000 {
			n = parsed
		}
	}
	lines, err := logs.Tail(s.Cfg.SingBoxLog, n)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, logsResponse{
		Path:  s.Cfg.SingBoxLog,
		Lines: lines,
		Count: len(lines),
	})
}

// handleLogsStream is a Server-Sent Events endpoint that pushes one
// `data: <line>\n\n` event per appended log line. Browser side uses
// EventSource which auto-reconnects on transient drops.
//
// Initial backfill: the last `?backfill=N` lines are streamed first
// so the UI has context immediately. Heartbeat comments every 15s
// keep middleboxes from killing the idle connection.
func (s *Server) handleLogsStream(w nethttp.ResponseWriter, r *nethttp.Request) {
	flusher, ok := w.(nethttp.Flusher)
	if !ok {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // belt-and-suspenders for any reverse proxy
	w.WriteHeader(nethttp.StatusOK)

	// Backfill — default 200, max 1000.
	backfill := 200
	if v := r.URL.Query().Get("backfill"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 && parsed <= 1000 {
			backfill = parsed
		}
	}
	if backfill > 0 {
		if seed, err := logs.Tail(s.Cfg.SingBoxLog, backfill); err == nil {
			for _, line := range seed {
				writeSSE(w, line)
			}
			flusher.Flush()
		}
	}

	// Live stream from now on. Subscribe to the shared hub instead of
	// spawning our own Streamer — N concurrent SSE clients share a
	// single file tailer, the hub fans lines out non-blocking.
	if s.LogHub == nil {
		// Fallback for tests / setups without a hub: per-request streamer.
		out := make(chan string, 64)
		streamCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			defer close(out)
			st := &logs.Streamer{Path: s.Cfg.SingBoxLog}
			_ = st.Follow(streamCtx, out)
		}()
		streamSSEFromChan(r.Context(), w, flusher, out)
		return
	}

	lines, unsub := s.LogHub.Subscribe()
	defer unsub()
	streamSSEFromChan(r.Context(), w, flusher, lines)
}

// streamSSEFromChan pumps lines from src to w as SSE events with a
// 15s heartbeat. Returns when ctx is cancelled or src closes.
func streamSSEFromChan(ctx context.Context, w nethttp.ResponseWriter, flusher nethttp.Flusher, src <-chan string) {

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-src:
			if !ok {
				return
			}
			writeSSE(w, line)
			flusher.Flush()
		case <-heartbeat.C:
			// SSE comment line — clients ignore it, but proxies and
			// browsers count it as activity.
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// writeSSE encodes a single payload as one SSE `data:` event. The SSE
// spec splits on bare \n inside `data:` so we emit one `data:` line
// per logical line (here we already get one line at a time, but be
// defensive and replace embedded \n just in case).
func writeSSE(w nethttp.ResponseWriter, payload string) {
	for i := 0; i < len(payload); i++ {
		if payload[i] == '\r' {
			payload = payload[:i] + payload[i+1:]
			i--
		}
	}
	// Each event terminator is a blank line.
	fmt.Fprintf(w, "data: %s\n\n", payload)
}
