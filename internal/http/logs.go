package http

import (
	nethttp "net/http"
	"strconv"

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
