// Package vless parses vless:// connection URLs into a structured
// shape the panel can store and feed to the sing-box config renderer.
//
// Supported shape (the only one we currently care about):
//
//	vless://UUID@host:port?security=reality&type=tcp&encryption=none
//	   &flow=xtls-rprx-vision&fp=chrome&pbk=PUBLIC_KEY&sid=SHORT_ID
//	   &sni=www.cloudflare.com#FriendlyName
//
// Anything we can't represent (e.g. type=ws, security=tls, gRPC) is
// rejected at parse time so a bad import never lands in profiles.json.
package vless

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// URL is the result of parsing a vless:// link.
type URL struct {
	UUID        string
	Server      string
	Port        int
	Name        string
	Flow        string
	SNI         string
	Fingerprint string
	PublicKey   string
	ShortID     string
}

// Parse decodes a vless:// URL. Returns an error with a human-readable
// reason on the first thing the panel can't safely round-trip.
func Parse(s string) (*URL, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "vless://") {
		return nil, fmt.Errorf("not a vless:// URL")
	}

	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("missing UUID before '@' (vless://UUID@host:port?...)")
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing host")
	}
	portStr := u.Port()
	if portStr == "" {
		return nil, fmt.Errorf("missing port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port %q", portStr)
	}

	q := u.Query()

	// We only support Reality + plain TCP + no extra encryption today.
	// Error messages are written long-form because they show up verbatim
	// in the panel UI when a paste fails — the more obvious it is what
	// went wrong, the less guessing the user has to do.
	if sec := q.Get("security"); sec != "" && sec != "reality" {
		return nil, fmt.Errorf(
			"this URL uses security=%q, but xray-panel-cli only supports VLESS+Reality. "+
				"Look for a config with security=reality in the URL.", sec)
	}
	if t := q.Get("type"); t != "" && t != "tcp" {
		return nil, fmt.Errorf(
			"this URL uses transport type=%q (e.g. WebSocket / gRPC / XHTTP). "+
				"xray-panel-cli only supports VLESS+Reality+Vision over plain TCP. "+
				"Look for a config with type=tcp.", t)
	}
	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		return nil, fmt.Errorf(
			"this URL uses encryption=%q; only encryption=none works with VLESS+Reality.", enc)
	}

	// pbk (Reality public key) is the one truly required Reality field —
	// without it the handshake can't complete.
	pbk := q.Get("pbk")
	if pbk == "" {
		return nil, fmt.Errorf(
			"missing pbk= (Reality public key). " +
				"This URL is missing the Reality handshake credentials — " +
				"xray-panel-cli cannot use it.")
	}
	// sid (short id) and sni (server name) are technically optional in
	// the Reality spec — a server can be configured with an empty
	// shortIds list and most accept SNI from the URL or from the server
	// hostname. We let them through and let sing-box decide.
	sid := q.Get("sid")
	sni := q.Get("sni")

	name := u.Fragment
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	if name == "" {
		name = host
	}

	return &URL{
		UUID:        u.User.Username(),
		Server:      host,
		Port:        port,
		Name:        name,
		Flow:        q.Get("flow"),
		SNI:         sni,
		Fingerprint: q.Get("fp"),
		PublicKey:   pbk,
		ShortID:     sid,
	}, nil
}
