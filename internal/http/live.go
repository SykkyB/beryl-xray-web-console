package http

import (
	"context"
	nethttp "net/http"
	"sort"
	"sync"
	"time"
)

// trafficSnapshot is what the panel needs to compute up/down rates
// between two consecutive /api/live polls. We hold the most recent one
// in liveCache and diff against it.
type trafficSnapshot struct {
	at         time.Time
	upTotal    int64
	downTotal  int64
}

// liveCache memoises the most recent /api/live payload (so concurrent
// browser tabs share a single upstream clash-API hit per ~1s) and also
// stores the previous traffic counters so a fresh refresh can compute
// instantaneous rates.
type liveCache struct {
	mu       sync.Mutex
	payload  liveResponse
	at       time.Time
	prev     trafficSnapshot   // previous totals for rate computation
	pending  *liveRefreshSlot
}

type liveRefreshSlot struct {
	done    chan struct{}
	payload liveResponse
}

const liveCacheTTL = 3 * time.Second

// liveResponse is the shape the UI consumes.
type liveResponse struct {
	ExitIP      block        `json:"exit_ip"`
	Traffic     block        `json:"traffic"`
	TopFlows    block        `json:"top_flows"`
	GeneratedAt string       `json:"generated_at"`
}

// trafficBlock is the value side of resp.Traffic when ok.
type trafficBlock struct {
	UpTotal    int64 `json:"up_total"`
	DownTotal  int64 `json:"down_total"`
	UpRate     int64 `json:"up_rate"`     // bytes/sec since last successful poll
	DownRate   int64 `json:"down_rate"`
	Connections int  `json:"connections"`
	Memory     int64 `json:"memory,omitempty"`
}

// flowBlock is one entry in TopFlows.
type flowBlock struct {
	Host        string `json:"host"`
	Destination string `json:"destination"`
	Network     string `json:"network"`
	Up          int64  `json:"up"`
	Down        int64  `json:"down"`
	Start       string `json:"start"`
}

func (s *Server) handleLive(w nethttp.ResponseWriter, r *nethttp.Request) {
	c := s.liveCache

	c.mu.Lock()
	if !c.at.IsZero() && time.Since(c.at) < liveCacheTTL {
		payload := c.payload
		c.mu.Unlock()
		writeJSON(w, nethttp.StatusOK, payload)
		return
	}
	if c.pending != nil {
		slot := c.pending
		c.mu.Unlock()
		<-slot.done
		writeJSON(w, nethttp.StatusOK, slot.payload)
		return
	}
	slot := &liveRefreshSlot{done: make(chan struct{})}
	c.pending = slot
	prev := c.prev
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	payload, fresh := s.collectLive(ctx, prev)

	c.mu.Lock()
	c.payload = payload
	c.at = time.Now()
	c.prev = fresh
	c.pending = nil
	slot.payload = payload
	c.mu.Unlock()
	close(slot.done)

	writeJSON(w, nethttp.StatusOK, payload)
}

// collectLive gathers exit IP + connections snapshot, computes rates,
// and returns the response shape plus the new "previous" snapshot to
// store for the next round of rate computation.
func (s *Server) collectLive(ctx context.Context, prev trafficSnapshot) (liveResponse, trafficSnapshot) {
	resp := liveResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Exit IP — pulled from the background poller, no upstream hit.
	if s.ExitIP != nil {
		snap := s.ExitIP.Snapshot()
		if snap.Err != "" {
			resp.ExitIP = block{Error: snap.Err, Value: snap.IP}
		} else {
			resp.ExitIP = block{OK: true, Value: map[string]any{
				"ip":         snap.IP,
				"fetched_at": snap.FetchedAt.UTC().Format(time.RFC3339),
				"age_sec":    int(snap.Age.Seconds()),
			}}
		}
	} else {
		resp.ExitIP = block{Error: "exit IP poller not configured"}
	}

	if s.Clash == nil {
		resp.Traffic = block{Error: "clash-API client not configured"}
		resp.TopFlows = block{Error: "clash-API client not configured"}
		return resp, prev
	}

	// Connections snapshot — drives Traffic and TopFlows.
	conns, err := s.Clash.GetConnections(ctx)
	if err != nil {
		resp.Traffic = block{Error: err.Error()}
		resp.TopFlows = block{Error: err.Error()}
		return resp, prev
	}

	now := time.Now()
	tb := trafficBlock{
		UpTotal:     conns.UploadTotal,
		DownTotal:   conns.DownloadTotal,
		Connections: len(conns.Connections),
		Memory:      conns.Memory,
	}
	if !prev.at.IsZero() {
		dt := now.Sub(prev.at).Seconds()
		if dt > 0 {
			if d := conns.UploadTotal - prev.upTotal; d >= 0 {
				tb.UpRate = int64(float64(d) / dt)
			}
			if d := conns.DownloadTotal - prev.downTotal; d >= 0 {
				tb.DownRate = int64(float64(d) / dt)
			}
		}
	}
	resp.Traffic = block{OK: true, Value: tb}

	// Top flows — by total bytes (up+down).
	flows := make([]flowBlock, 0, len(conns.Connections))
	for _, c := range conns.Connections {
		dest := c.Metadata.DestinationIP
		if c.Metadata.DestinationPort != "" {
			dest = dest + ":" + c.Metadata.DestinationPort
		}
		flows = append(flows, flowBlock{
			Host:        c.Metadata.Host,
			Destination: dest,
			Network:     c.Metadata.Network,
			Up:          c.Upload,
			Down:        c.Download,
			Start:       c.Start,
		})
	}
	sort.Slice(flows, func(i, j int) bool {
		return (flows[i].Up + flows[i].Down) > (flows[j].Up + flows[j].Down)
	})
	if len(flows) > 10 {
		flows = flows[:10]
	}
	resp.TopFlows = block{OK: true, Value: flows}

	return resp, trafficSnapshot{at: now, upTotal: conns.UploadTotal, downTotal: conns.DownloadTotal}
}
