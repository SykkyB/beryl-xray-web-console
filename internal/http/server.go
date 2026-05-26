// Package http hosts the panel's HTTP server: routes, middleware, and
// the embedded web UI. Service control, profile CRUD, sing-box config
// rendering live in sibling internal/* packages and are wired in here.
package http

import (
	"encoding/json"
	nethttp "net/http"
	"runtime/debug"

	"beryl-xray-web-console/internal/clash"
	"beryl-xray-web-console/internal/config"
	"beryl-xray-web-console/internal/exitip"
	"beryl-xray-web-console/internal/logs"
	"beryl-xray-web-console/internal/service"
	"beryl-xray-web-console/internal/singbox"
	"beryl-xray-web-console/internal/store"
	"beryl-xray-web-console/internal/sysprobe"
	"beryl-xray-web-console/internal/ucitool"
)

// Server bundles the dependencies the HTTP handlers need. Anything that
// touches the OS goes through these injected ports so handlers stay
// trivially unit-testable with FakeRunner.
type Server struct {
	Cfg      *config.Config
	Service  *service.Manager
	UCI      *ucitool.Tool
	Probe    *sysprobe.Probe
	Profiles *store.Profiles
	Renderer *singbox.Renderer
	Clash    *clash.Client
	ExitIP   *exitip.Poller
	LogHub   *logs.Hub

	// stateCache is a single-flight cache around handleState; liveCache
	// does the same for handleLive. Both initialised by NewServer so
	// the handlers don't race on lazy creation.
	stateCache *stateCache
	liveCache  *liveCache
}

// NewServer returns *Server with internal caches initialised. Pass it
// the config-shaped fields you'd otherwise set on a literal struct;
// internal/private fields (like the state cache) are filled in here.
func NewServer(seed Server) *Server {
	s := seed
	s.stateCache = &stateCache{}
	s.liveCache = &liveCache{}
	return &s
}

// Handler returns a net/http handler with all routes registered. Most
// routes are wrapped in basic-auth (panel-wide); a small allow-list of
// "intentionally public" endpoints bypasses auth so cross-origin
// callers (e.g. the GL.iNet stock home page polling us via <img>) can
// reach them without a credential prompt.
func (s *Server) Handler() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /api/ping", s.handlePing)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/service", s.handleServiceAction)
	mux.HandleFunc("POST /api/native-vpn/stop", s.handleNativeVPNStop)
	mux.HandleFunc("POST /api/native-vpn/restore", s.handleNativeVPNRestore)
	mux.HandleFunc("POST /api/side-switch", s.handleSideSwitch)
	mux.HandleFunc("POST /api/killswitch", s.handleKillswitch)
	mux.HandleFunc("POST /api/bind_switch", s.handleBindSwitch)
	mux.HandleFunc("GET /api/profiles", s.handleProfilesList)
	mux.HandleFunc("POST /api/profiles/import-vless", s.handleProfilesImportVless)
	mux.HandleFunc("PATCH /api/profiles/{id}", s.handleProfilePatch)
	mux.HandleFunc("DELETE /api/profiles/{id}", s.handleProfileDelete)
	mux.HandleFunc("POST /api/profiles/{id}/activate", s.handleProfileActivate)
	mux.HandleFunc("GET /api/profiles/{id}/delay", s.handleProfileDelay)
	mux.HandleFunc("GET /api/live", s.handleLive)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/logs/stream", s.handleLogsStream)
	registerUIRoutes(mux)
	authed := BasicAuth(s.Cfg.Auth.Username, s.Cfg.Auth.PasswordBcrypt, mux)

	// /api/up.png — "is sing-box tunnelling right now" probe used by
	// the GL.iNet stock home-page launcher. Cross-origin <img> loads
	// can't carry basic-auth without first triggering a browser
	// password prompt, so this route bypasses auth. Leaks only
	// "tunnel up/down" — already inferable from any external request.
	//
	// /api/launcher-config — read by the same launcher at every page
	// load to learn which dashboard injections are enabled. Mode
	// string is no more sensitive than panel.yaml itself (and the
	// launcher file is world-readable at /www/ anyway).
	dispatch := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method == nethttp.MethodGet && r.URL.Path == "/api/up.png" {
			s.handleUpPing(w, r)
			return
		}
		if r.Method == nethttp.MethodGet && r.URL.Path == "/api/launcher-config" {
			s.handleLauncherConfig(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})

	// Wrap everything in CORS so cross-origin XHR from the GL.iNet
	// admin UI (http://<lan-ip>) can reach :9092 with credentials.
	// Preflight (OPTIONS) is answered inside the middleware before
	// auth runs — required by browsers, which won't send creds on
	// preflight.
	return corsLAN(dispatch)
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
