package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"beryl-xray-web-console/internal/config"
	"beryl-xray-web-console/internal/service"
	"beryl-xray-web-console/internal/vetlib"
)

// scanState is the live record of one /api/scan/start invocation. It
// lives in memory for the duration of the scan (and a short while
// after) so /api/scan/status can poll without re-loading anything from
// disk. On completion (success, cancel, or error) the final result is
// also persisted under ScansDir as a JSON snapshot.
type scanState struct {
	ID        string
	StartedAt time.Time
	FinishedAt time.Time
	Options   vetlib.Options
	SourceIDs []string

	mu       sync.RWMutex
	stage    string         // "starting" | "fetch" | "parse" | "probe" | "deep" | "done" | "cancelled" | "error"
	total    int            // entries expected at the current stage
	done     int
	tcpOK    int
	tlsOK    int
	vlessOK  int
	errMsg   string
	result   *vetlib.Result
	cancel   context.CancelFunc

	// Optional: which sources were used + a short fetch log, surfaced
	// in the status response so the UI can show "fetched 3/4 sources".
	sourcesFetched []sourceFetchInfo

	// pausedActive remembers if we stopped the active sing-box for
	// this scan so the OFF path can bring it back when the scan ends.
	pausedActive bool
}

type sourceFetchInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Bytes int    `json:"bytes"`
	Error string `json:"error,omitempty"`
}

// scanRegistry is the panel-wide map of recent scan states. Limited
// to the last 4 (one running + a couple finished) so memory stays
// bounded. Persisted snapshots in /etc/xray-panel-cli/scans/ are the
// canonical history.
type scanRegistry struct {
	mu      sync.Mutex
	current *scanState                  // the most recent (running or just done)
	recent  map[string]*scanState       // by ID, includes current; capped
}

const maxInMemoryScans = 4

func newScanRegistry() *scanRegistry {
	return &scanRegistry{recent: map[string]*scanState{}}
}

func (r *scanRegistry) put(st *scanState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recent[st.ID] = st
	r.current = st
	if len(r.recent) > maxInMemoryScans {
		// Drop the oldest finished entry.
		var oldest *scanState
		for _, s := range r.recent {
			if s == r.current {
				continue
			}
			if oldest == nil || s.StartedAt.Before(oldest.StartedAt) {
				oldest = s
			}
		}
		if oldest != nil {
			delete(r.recent, oldest.ID)
		}
	}
}

func (r *scanRegistry) get(id string) *scanState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recent[id]
}

// ── HTTP handlers ───────────────────────────────────────────────────

// POST /api/scan/start
// body: {source_ids?: [...], deep?: bool, max_deep?: int, hard_timeout_s?: int,
//        skip_active?: bool}
// source_ids omitted ⇒ all enabled sources.
func (s *Server) handleScanStart(w nethttp.ResponseWriter, r *nethttp.Request) {
	if s.ScanRegistry == nil {
		writeErr(w, nethttp.StatusInternalServerError, errors.New("scan registry not initialised"))
		return
	}
	var body struct {
		SourceIDs     []string `json:"source_ids"`
		Deep          bool     `json:"deep"`
		MaxDeep       int      `json:"max_deep"`
		MaxPerCountry int      `json:"max_per_country"`
		HardTimeoutS  int      `json:"hard_timeout_s"`
		SkipActive    bool     `json:"skip_active"` // pause active sing-box for the scan
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}

	// Refuse concurrent scans — sing-box spawn-storm + LAN-throughput
	// hit are bad enough single-tenant; two parallel scans would
	// fight for file descriptors and ephemeral ports.
	s.ScanRegistry.mu.Lock()
	if cur := s.ScanRegistry.current; cur != nil {
		cur.mu.RLock()
		running := cur.stage != "done" && cur.stage != "cancelled" && cur.stage != "error"
		cur.mu.RUnlock()
		if running {
			s.ScanRegistry.mu.Unlock()
			writeJSON(w, nethttp.StatusConflict, map[string]any{
				"ok":    false,
				"error": "another scan is already running",
				"scan_id": cur.ID,
			})
			return
		}
	}
	s.ScanRegistry.mu.Unlock()

	st := newSourceStore(s.Cfg.ScoutWithDefaults().SourcesFile)
	all, err := st.allSources()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	picked := pickSources(all, body.SourceIDs)
	if len(picked) == 0 {
		writeErr(w, nethttp.StatusBadRequest, errors.New("no enabled sources selected"))
		return
	}

	hard := time.Duration(body.HardTimeoutS) * time.Second
	if hard == 0 {
		hard = 20 * time.Minute
	}
	maxDeep := body.MaxDeep
	if maxDeep == 0 && body.Deep {
		maxDeep = 200
	}
	maxPerCountry := body.MaxPerCountry
	if maxPerCountry == 0 && body.Deep {
		maxPerCountry = 30 // fair-share default, ~7 countries fills 200
	}

	ctx, cancel := context.WithCancel(context.Background())
	state := &scanState{
		ID:        "scan-" + time.Now().UTC().Format("20060102-150405"),
		StartedAt: time.Now().UTC(),
		stage:     "starting",
		cancel:    cancel,
		SourceIDs: idsOf(picked),
		Options: vetlib.Options{
			Workers:       32, // conservative on 2-core Beryl
			TCPTimeout:    2 * time.Second,
			TLSTimeout:    4 * time.Second,
			Deep:          body.Deep,
			DeepWorkers:   3,
			DeepTimeout:   10 * time.Second,
			MaxDeep:       maxDeep,
			MaxPerCountry: maxPerCountry,
			HardTimeout:   hard,
			DedupByAddr:   true,
			SingBoxBin:    s.Cfg.SingBoxBin,
		},
	}
	s.ScanRegistry.put(state)

	// If user asked to pause the active tunnel for the duration of
	// the scan, do it BEFORE launching the goroutine so the response
	// reflects the post-pause state.
	if body.SkipActive {
		// Best-effort: ignore errors; user can re-start manually.
		_ = s.Service.Do(ctx, service.Action("stop"))
		state.pausedActive = true
	}

	go s.runScan(ctx, state, picked)

	writeJSON(w, nethttp.StatusAccepted, map[string]any{
		"ok":      true,
		"scan_id": state.ID,
	})
}

// GET /api/scan/status?id=...   (or no id = current/most-recent)
func (s *Server) handleScanStatus(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.URL.Query().Get("id")
	st := s.lookupScan(id)
	if st == nil {
		writeErr(w, nethttp.StatusNotFound, errors.New("scan not found"))
		return
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	resp := map[string]any{
		"id":           st.ID,
		"started_at":   st.StartedAt.UTC().Format(time.RFC3339),
		"elapsed_s":    int(time.Since(st.StartedAt).Seconds()),
		"stage":        st.stage,
		"total":        st.total,
		"done":         st.done,
		"tcp_ok":       st.tcpOK,
		"tls_ok":       st.tlsOK,
		"vless_ok":     st.vlessOK,
		"sources":      st.sourcesFetched,
		"paused_active": st.pausedActive,
	}
	if !st.FinishedAt.IsZero() {
		resp["finished_at"] = st.FinishedAt.UTC().Format(time.RFC3339)
	}
	if st.errMsg != "" {
		resp["error"] = st.errMsg
	}
	writeJSON(w, nethttp.StatusOK, resp)
}

// GET /api/scan/results?id=...
func (s *Server) handleScanResults(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.URL.Query().Get("id")
	st := s.lookupScan(id)
	if st == nil {
		// If it's not in memory, try the disk snapshots.
		if id != "" {
			snap, err := readScanSnapshot(s.Cfg.ScoutWithDefaults().ScansDir, id)
			if err == nil {
				writeJSON(w, nethttp.StatusOK, snap)
				return
			}
		}
		writeErr(w, nethttp.StatusNotFound, errors.New("scan not found"))
		return
	}
	st.mu.RLock()
	res := st.result
	stage := st.stage
	st.mu.RUnlock()
	if res == nil {
		writeJSON(w, nethttp.StatusOK, map[string]any{
			"ok":    false,
			"stage": stage,
			"note":  "scan still running; poll /api/scan/status",
		})
		return
	}
	writeJSON(w, nethttp.StatusOK, makeResultPayload(st, res))
}

// POST /api/scan/cancel?id=...
func (s *Server) handleScanCancel(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.URL.Query().Get("id")
	st := s.lookupScan(id)
	if st == nil {
		writeErr(w, nethttp.StatusNotFound, errors.New("scan not found"))
		return
	}
	st.cancel()
	writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true})
}

// GET /api/scans/list — recent snapshots from disk
func (s *Server) handleScanList(w nethttp.ResponseWriter, r *nethttp.Request) {
	dir := s.Cfg.ScoutWithDefaults().ScansDir
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, nethttp.StatusOK, map[string]any{"scans": []any{}})
		return
	}
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	var out []map[string]any
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		out = append(out, map[string]any{
			"id":        id,
			"size":      info.Size(),
			"mtime":     info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	// newest first
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["mtime"]) > fmt.Sprint(out[j]["mtime"])
	})
	writeJSON(w, nethttp.StatusOK, map[string]any{"scans": out})
}

// ── orchestration ───────────────────────────────────────────────────

func (s *Server) lookupScan(id string) *scanState {
	if s.ScanRegistry == nil {
		return nil
	}
	if id == "" {
		s.ScanRegistry.mu.Lock()
		cur := s.ScanRegistry.current
		s.ScanRegistry.mu.Unlock()
		return cur
	}
	return s.ScanRegistry.get(id)
}

func (s *Server) runScan(ctx context.Context, st *scanState, picked []Source) {
	defer func() {
		if st.pausedActive {
			// Best-effort restart of the active tunnel after the scan.
			rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = s.Service.Do(rctx, service.Action("start"))
		}
		st.cancel()
		st.mu.Lock()
		st.FinishedAt = time.Now().UTC()
		st.mu.Unlock()
		if st.result != nil {
			if err := writeScanSnapshot(s.Cfg.ScoutWithDefaults(), st); err != nil {
				// snapshot persistence is best-effort; not fatal.
				st.mu.Lock()
				if st.errMsg == "" {
					st.errMsg = "snapshot: " + err.Error()
				}
				st.mu.Unlock()
			}
		}
	}()

	// Stage A: fetch. Pass the sourceStore so per-source meta
	// (status, bytes, hash, last-fetched) is persisted as part of
	// the fetch, surface-able to the UI without waiting for the
	// scan to finish.
	st.setStage("fetch")
	store := newSourceStore(s.Cfg.ScoutWithDefaults().SourcesFile)
	inputs, fetched := openSourceInputs(ctx, picked, store)
	st.mu.Lock()
	st.sourcesFetched = fetched
	st.mu.Unlock()
	if len(inputs) == 0 {
		st.markError("no sources fetched successfully")
		return
	}
	st.Options.Inputs = inputs

	// Pipeline run with a progress channel.
	prog := make(chan vetlib.Progress, 32)
	go func() {
		for p := range prog {
			st.applyProgress(p)
		}
	}()

	res, err := vetlib.Run(ctx, st.Options, prog)
	if err != nil {
		st.markError(err.Error())
		return
	}
	st.mu.Lock()
	st.result = res
	if res.Cancelled {
		st.stage = "cancelled"
	} else if res.TimedOut {
		st.stage = "timed_out"
	} else {
		st.stage = "done"
	}
	st.mu.Unlock()
}

func (st *scanState) setStage(stage string) {
	st.mu.Lock()
	st.stage = stage
	st.mu.Unlock()
}

func (st *scanState) applyProgress(p vetlib.Progress) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.stage = p.Stage
	if p.Total > 0 {
		st.total = p.Total
	}
	st.done = p.Done
	if p.TCPOK > 0 {
		st.tcpOK = p.TCPOK
	}
	if p.TLSOK > 0 {
		st.tlsOK = p.TLSOK
	}
	if p.VLESSOK > 0 {
		st.vlessOK = p.VLESSOK
	}
}

func (st *scanState) markError(msg string) {
	st.mu.Lock()
	st.stage = "error"
	st.errMsg = msg
	st.mu.Unlock()
}

// ── source fetching ──────────────────────────────────────────────

// fetchSource downloads (URL) or reads (Path) one source, computing
// hash/line-count/header metadata as it goes. Pure: no side effects
// — callers persist meta themselves via sourceStore.updateMeta.
//
// On error, body is nil and meta still carries status/last_error/
// last_fetched_at so the UI can surface the failure.
func fetchSource(ctx context.Context, src Source) (body []byte, meta SourceMeta, err error) {
	meta.LastFetchedAt = time.Now().UTC().Format(time.RFC3339)

	if src.Path != "" {
		data, ferr := os.ReadFile(src.Path)
		if ferr != nil {
			meta.LastStatus = "error"
			meta.LastError = ferr.Error()
			return nil, meta, ferr
		}
		fillContentMeta(&meta, data)
		return data, meta, nil
	}

	client := &nethttp.Client{Timeout: 30 * time.Second}
	req, rerr := nethttp.NewRequestWithContext(ctx, "GET", src.URL, nil)
	if rerr != nil {
		meta.LastStatus = "error"
		meta.LastError = rerr.Error()
		return nil, meta, rerr
	}
	req.Header.Set("User-Agent", "xray-panel-cli/scout")
	resp, rerr := client.Do(req)
	if rerr != nil {
		meta.LastStatus = "error"
		meta.LastError = rerr.Error()
		return nil, meta, rerr
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		meta.LastStatus = "error"
		meta.LastError = fmt.Sprintf("http %d %s", resp.StatusCode, resp.Status)
		return nil, meta, errors.New(meta.LastError)
	}
	const maxBytes = 32 << 20 // 32 MB hard cap on a single source
	data, ierr := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if ierr != nil {
		meta.LastStatus = "error"
		meta.LastError = ierr.Error()
		return nil, meta, ierr
	}
	fillContentMeta(&meta, data)
	meta.HTTPLastMod = resp.Header.Get("Last-Modified")
	meta.HTTPETag = resp.Header.Get("ETag")
	return data, meta, nil
}

// fillContentMeta populates LastStatus, LastBytes, LastLines and
// ContentHash from a freshly-fetched body. Default status is "ok";
// the caller may overwrite to "unchanged" after a hash compare.
func fillContentMeta(meta *SourceMeta, data []byte) {
	meta.LastStatus = "ok"
	meta.LastBytes = len(data)
	meta.LastLines = countVlessLines(data)
	h := sha256.Sum256(data)
	meta.ContentHash = hex.EncodeToString(h[:])[:16]
}

// countVlessLines is a cheap "how many candidates does this list
// claim to have" counter — anything starting with "vless://" wins.
func countVlessLines(data []byte) int {
	n := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("vless://")) {
			n++
		}
	}
	return n
}

// openSourceInputs fetches each picked source, persists per-source
// meta (status, bytes, hash, …), and returns vetlib readers for the
// ones that succeeded. Compares against previous meta to flag
// "unchanged" runs so the UI can hint "list unchanged since 6h ago".
func openSourceInputs(ctx context.Context, picked []Source, store *sourceStore) ([]vetlib.NamedReader, []sourceFetchInfo) {
	out := make([]vetlib.NamedReader, 0, len(picked))
	info := make([]sourceFetchInfo, 0, len(picked))
	for _, src := range picked {
		body, meta, err := fetchSource(ctx, src)

		// Compare against previous meta to detect unchanged content.
		// Carry the prior hash forward as PrevContentHash so the UI
		// can show "hash a1b2c3d4 → e5f6a7b8 (changed)" if needed.
		prev := store.previousMeta(src.ID)
		if prev.ContentHash != "" {
			meta.PrevContentHash = prev.ContentHash
			if err == nil && prev.ContentHash == meta.ContentHash {
				meta.LastStatus = "unchanged"
			}
		}
		// Persist meta even on failure — surfacing a "tried, failed"
		// record is the whole point of the feature.
		_ = store.updateMeta(src.ID, meta)

		fi := sourceFetchInfo{
			ID:    src.ID,
			Name:  src.Name,
			OK:    err == nil,
			Bytes: meta.LastBytes,
		}
		if err != nil {
			fi.Error = err.Error()
		}
		info = append(info, fi)
		if err == nil {
			out = append(out, vetlib.NamedReader{
				Name:   src.Name,
				Reader: strings.NewReader(string(body)),
			})
		}
	}
	return out, info
}

// pickSources filters allSources by requested IDs, defaulting to all
// enabled if ids is empty.
func pickSources(all []Source, ids []string) []Source {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := make([]Source, 0, len(all))
	for _, s := range all {
		if len(wanted) > 0 {
			if wanted[s.ID] {
				out = append(out, s)
			}
			continue
		}
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

func idsOf(srcs []Source) []string {
	out := make([]string, len(srcs))
	for i, s := range srcs {
		out[i] = s.ID
	}
	return out
}

// ── result rendering + snapshot persistence ─────────────────────────

// makeResultPayload turns a vetlib.Result into the JSON shape the
// frontend consumes — flat entry list + a grouping-by-country block
// so the UI can do either treatment without a second pass.
func makeResultPayload(st *scanState, res *vetlib.Result) map[string]any {
	type entryDTO struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		Server    string `json:"server"`
		Port      int    `json:"port"`
		SNI       string `json:"sni"`
		Transport string `json:"transport"`
		Security  string `json:"security"`
		Country   string `json:"country"`
		Flag      string `json:"flag"`
		TCPMs     int    `json:"tcp_ms,omitempty"`
		TLSMs     int    `json:"tls_ms,omitempty"`
		DeepMs    int    `json:"deep_ms,omitempty"`
		TCPOK     bool   `json:"tcp_ok"`
		TLSOK     bool   `json:"tls_ok"`
		DeepOK    bool   `json:"deep_ok"`
	}

	all := make([]entryDTO, 0, len(res.Entries))
	for _, e := range res.Entries {
		all = append(all, entryDTO{
			Name: e.Name, URL: e.URL, Server: e.Server, Port: e.Port,
			SNI: e.SNI, Transport: e.Transport, Security: e.Security,
			Country: e.Country, Flag: vetlib.FlagEmoji(e.Country),
			TCPMs: e.TCPMs, TLSMs: e.TLSMs, DeepMs: e.DeepMs,
			TCPOK: e.TCPOK, TLSOK: e.TLSOK, DeepOK: e.DeepOK,
		})
	}

	type group struct {
		Country string     `json:"country"`
		Flag    string     `json:"flag"`
		Count   int        `json:"count"`
		Entries []entryDTO `json:"entries"`
	}

	// Group by country, keeping only entries that passed the strictest
	// stage that ran. (No reason to surface dead candidates in the
	// "results by country" view — they go in a separate stats block.)
	deep := st.Options.Deep
	skipTLS := st.Options.SkipTLS
	keep := func(e entryDTO) bool {
		if deep {
			return e.DeepOK
		}
		if !skipTLS {
			return e.TLSOK
		}
		return e.TCPOK
	}

	byCC := map[string][]entryDTO{}
	for _, e := range all {
		if !keep(e) {
			continue
		}
		byCC[e.Country] = append(byCC[e.Country], e)
	}
	groups := make([]group, 0, len(byCC))
	for cc, es := range byCC {
		groups = append(groups, group{
			Country: cc, Flag: vetlib.FlagEmoji(cc), Count: len(es), Entries: es,
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		// Bigger groups first; "" (unknown) goes last regardless of size.
		if groups[i].Country == "" {
			return false
		}
		if groups[j].Country == "" {
			return true
		}
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Country < groups[j].Country
	})

	return map[string]any{
		"id":         st.ID,
		"started_at": st.StartedAt.UTC().Format(time.RFC3339),
		"elapsed_s":  int(res.Elapsed.Seconds()),
		"parsed":     res.ParseStats.Parsed,
		"deduped":    res.ParseStats.Skipped,
		"tcp_ok":     int(res.ProbeStats.TCPOK),
		"tls_ok":     int(res.ProbeStats.TLSOK),
		"deep_ok":    int(res.DeepStats.VLESSOK),
		"deep":       deep,
		"skip_tls":   skipTLS,
		"sources":    st.sourcesFetched,
		"entries":    all,
		"groups":     groups,
	}
}

// writeScanSnapshot writes the result payload to ScansDir as JSON,
// rotating oldest beyond ScansKeep. Caller holds st.result, which is
// embedded into the payload via makeResultPayload.
func writeScanSnapshot(c config.ScoutConfig, st *scanState) error {
	if st.result == nil {
		return errors.New("no result to snapshot")
	}
	if c.ScansDir == "" {
		return errors.New("scans_dir not set")
	}
	if err := os.MkdirAll(c.ScansDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	payload := makeResultPayload(st, st.result)
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	out := filepath.Join(c.ScansDir, st.ID+".json")
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		return err
	}
	if c.ScansKeep > 0 {
		rotateSnapshots(c.ScansDir, c.ScansKeep)
	}
	return nil
}

func readScanSnapshot(dir, id string) (map[string]any, error) {
	p := filepath.Join(dir, id+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func rotateSnapshots(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fi struct {
		name string
		mt   time.Time
	}
	var files []fi
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		inf, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fi{e.Name(), inf.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mt.After(files[j].mt) })
	for _, f := range files[keep:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}
