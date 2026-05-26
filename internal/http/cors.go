package http

import (
	"net"
	nethttp "net/http"
	"strings"
)

// corsLAN wraps h with CORS headers permissive enough for the GL.iNet
// stock admin UI (served from the router's LAN IP on port 80) to call
// our panel on :9092 with credentials. Restricted to private-LAN
// origins so an external page can't pry via a browser the user has
// open to the router.
//
// We can't use a wildcard `Access-Control-Allow-Origin: *` because we
// need credentials (basic-auth) to travel on the XHR — the spec
// forbids wildcard origin when Allow-Credentials is true. So we echo
// back the request Origin only after confirming it points at a
// private IP.
//
// Preflight (OPTIONS) is answered inline; the wrapped handler is
// never invoked for it. The auth middleware sits inside this wrapper
// so preflights succeed without credentials (which browsers refuse to
// attach to OPTIONS anyway).
func corsLAN(h nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isPrivateLANOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			// Tell intermediaries (including the browser's own cache)
			// that the response depends on the Origin header so a
			// cached one from a different origin isn't reused.
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == nethttp.MethodOptions {
			w.WriteHeader(nethttp.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// isPrivateLANOrigin returns true when the Origin header points at an
// RFC1918 IPv4 address (10/8, 172.16/12, 192.168/16) or loopback. Other
// origins — public IPs, *.local mDNS, etc. — are rejected to keep the
// panel uncallable from any random tab the user has open. Port is
// ignored; scheme must be http/https.
func isPrivateLANOrigin(origin string) bool {
	// Cheap parse: scheme://host[:port]
	rest := origin
	switch {
	case strings.HasPrefix(rest, "http://"):
		rest = rest[len("http://"):]
	case strings.HasPrefix(rest, "https://"):
		rest = rest[len("https://"):]
	default:
		return false
	}
	host := rest
	if i := strings.Index(rest, ":"); i >= 0 {
		host = rest[:i]
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	// 10/8
	if v4[0] == 10 {
		return true
	}
	// 172.16/12
	if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
		return true
	}
	// 192.168/16
	if v4[0] == 192 && v4[1] == 168 {
		return true
	}
	return false
}
