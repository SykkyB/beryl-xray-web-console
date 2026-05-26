package http

import nethttp "net/http"

// handleLauncherConfig tells the injected /www/xray-panel-launcher.js
// script which DOM mutations are currently enabled (mode = legacy /
// dashboard / full). The launcher fetches this once on each page load
// and gates its modules accordingly — so flipping `injection.mode` in
// panel.yaml + restarting :9092 swaps behaviour without redeploying
// the launcher file.
//
// Intentionally public (bypasses BasicAuth, see server.go) so the
// cross-origin pre-XHR fetch from the GL.iNet admin UI doesn't trip a
// credential prompt before the user has interacted with the panel.
// The only data leaked is the mode itself; that's the same string
// anyone can read from /etc/xray-panel-cli/panel.yaml on the router.
func (s *Server) handleLauncherConfig(w nethttp.ResponseWriter, _ *nethttp.Request) {
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"mode": s.Cfg.InjectionMode(),
	})
}
