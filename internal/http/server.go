// Package http hosts the panel's HTTP server: routes, middleware, and
// the embedded web UI. Service control, profile CRUD, sing-box config
// rendering live in sibling internal/* packages and are wired in here.
package http

import (
	"encoding/json"
	nethttp "net/http"
	"runtime/debug"

	"beryl-xray-web-console/internal/config"
)

// Server bundles the dependencies the HTTP handlers need. Phase 2A is
// minimal — just the config and embedded UI; service / clash / profiles
// will land in later sub-phases.
type Server struct {
	Cfg *config.Config
}

// Handler returns a net/http handler with all routes registered, wrapped
// in basic-auth (panel-wide).
func (s *Server) Handler() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /api/ping", s.handlePing)
	registerUIRoutes(mux)
	return BasicAuth(s.Cfg.Auth.Username, s.Cfg.Auth.PasswordBcrypt, mux)
}

type pingResponse struct {
	OK      bool   `json:"ok"`
	Service string `json:"service"`
	Version string `json:"version"`
}

func (s *Server) handlePing(w nethttp.ResponseWriter, _ *nethttp.Request) {
	v := "dev"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		v = info.Main.Version
	}
	writeJSON(w, nethttp.StatusOK, pingResponse{
		OK:      true,
		Service: "xray-panel-cli",
		Version: v,
	})
}

func writeJSON(w nethttp.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w nethttp.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
