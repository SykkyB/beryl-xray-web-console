package http

import (
	"context"
	nethttp "net/http"
	"time"
)

// pixelPNG is a 1×1 fully-transparent PNG, pre-encoded. The bytes
// don't matter to the consumer — the consumer is the GL.iNet stock
// home page polling us via <img>; it cares only about onload vs
// onerror to decide whether to flip the VPN service icon to teal.
var pixelPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// handleUpPing is the public, no-auth, no-CORS-fuss probe used by the
// GL.iNet home-page launcher to flip the stock VPN service icon green
// when our sing-box tunnel is up.
//
// 200 + 1×1 transparent PNG when sing-box is running AND sing-tun is
// up; 404 otherwise. Two states the browser easily distinguishes via
// <img>'s onload / onerror handlers — no CORS preflight, no
// credentials, just the lightest possible cross-origin signal.
//
// Deliberately bypasses BasicAuth: the only information leaked is
// "tunnel up / down", which the LAN already knows by checking exit IP
// or simply whether traffic flows. Avoiding auth here also avoids the
// browser dialog that would otherwise pop on the GL.iNet UI.
func (s *Server) handleUpPing(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	running, _ := s.Probe.SingBoxRunning(ctx)
	tunUp := false
	if running {
		tunUp, _ = s.Probe.TunUp(ctx, "sing-tun")
	}

	// Same-origin headers regardless of state — keeps tooling happy
	// and lets future fetch() callers (e.g. uptime monitor) parse
	// Content-Type without a Vary surprise.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")

	if !running || !tunUp {
		nethttp.Error(w, "down", nethttp.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pixelPNG)
}
