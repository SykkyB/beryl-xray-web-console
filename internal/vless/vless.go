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

// BuildURL reconstructs a vless:// link from the URL fields. Used as
// a fallback for legacy profiles imported before RawURL was stored on
// disk — building from parsed fields is approximate (param ordering,
// case, URL-encoded fragments may not be byte-identical to the
// original), but the result is still a valid VLESS link any client
// will accept.
//
// Skips empty values and writes parameters in a deterministic order so
// the result is stable across calls.
func BuildURL(u URL) string {
	var sb strings.Builder
	sb.WriteString("vless://")
	sb.WriteString(u.UUID)
	sb.WriteByte('@')
	sb.WriteString(u.Server)
	sb.WriteByte(':')
	sb.WriteString(itoa(u.Port))

	// Param order mirrors what most clients emit: security, encryption,
	// flow, pbk, fp, sni, sid, spx, type, host, path. (encryption is
	// always "none" for VLESS; include it for parity with stock outputs.)
	type kv struct{ k, v string }
	params := []kv{
		{"security", u.Security},
		{"encryption", "none"},
		{"flow", u.Flow},
		{"pbk", u.PublicKey},
		{"fp", u.Fingerprint},
		{"sni", u.SNI},
		{"sid", u.ShortID},
		{"type", u.Type},
		{"host", u.Host},
		{"path", u.Path},
	}
	first := true
	for _, p := range params {
		if p.v == "" {
			continue
		}
		if first {
			sb.WriteByte('?')
			first = false
		} else {
			sb.WriteByte('&')
		}
		sb.WriteString(p.k)
		sb.WriteByte('=')
		// Light URL-escape: only escape the few chars that genuinely
		// break parsers (& # space). Most VLESS clients tolerate the
		// rest verbatim, and over-escaping makes the QR longer.
		sb.WriteString(strings.NewReplacer("&", "%26", "#", "%23", " ", "%20").Replace(p.v))
	}
	if u.Name != "" {
		sb.WriteByte('#')
		sb.WriteString(strings.NewReplacer(" ", "%20", "#", "%23").Replace(u.Name))
	}
	return sb.String()
}

func itoa(n int) string {
	// Avoid strconv import just for this one place.
	if n == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

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

	// Type is the stream transport: "tcp" (default) or "ws".
	Type string
	// Security is the TLS layer: "reality" or "tls".
	Security string
	// Path / Host populated only when Type == "ws".
	Path string
	Host string
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

	// Supported combinations:
	//   transport: "tcp" (default) or "ws"
	//   security:  "reality" (default) or "tls"
	// Anything else (gRPC, XHTTP, raw, etc., or "none" / "xtls") is
	// rejected with a sentence the panel can show verbatim — the more
	// obvious it is what went wrong, the less guessing for the user.
	transport := strings.ToLower(strings.TrimSpace(q.Get("type")))
	if transport == "" {
		transport = "tcp"
	}
	switch transport {
	case "tcp", "ws":
	default:
		return nil, fmt.Errorf(
			"this URL uses transport type=%q (e.g. gRPC / XHTTP / raw / kcp). "+
				"xray-panel-cli supports only TCP and WebSocket transports. "+
				"Look for a config with type=tcp or type=ws.", transport)
	}

	security := strings.ToLower(strings.TrimSpace(q.Get("security")))
	if security == "" {
		security = "reality"
	}
	switch security {
	case "reality", "tls":
	default:
		return nil, fmt.Errorf(
			"this URL uses security=%q. xray-panel-cli supports only "+
				"security=reality (default) or security=tls.", security)
	}

	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		return nil, fmt.Errorf(
			"this URL uses encryption=%q; only encryption=none is allowed.", enc)
	}

	// pbk (Reality public key) is required only for Reality. With
	// plain TLS the cert is verified normally by SNI (no extra key).
	pbk := q.Get("pbk")
	if security == "reality" && pbk == "" {
		return nil, fmt.Errorf(
			"missing pbk= (Reality public key). " +
				"This URL is missing the Reality handshake credentials — " +
				"xray-panel-cli cannot use it.")
	}
	// sid (short id) and sni (server name) are optional for Reality.
	// For plain TLS, sni is needed for cert verification — but most
	// URLs either include it or fall back to the hostname; we accept
	// empty here and let sing-box derive it.
	sid := q.Get("sid")
	sni := q.Get("sni")

	name := u.Fragment
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	if name == "" {
		name = host
	}

	// WebSocket fields. Path defaults to "/" if absent (sing-box will
	// 404 without one). Host header (`host`) is optional — it's the
	// SNI-style domain to send in the Host: header; needed when the
	// server is fronted through a CDN.
	wsPath := q.Get("path")
	wsHost := q.Get("host")
	if transport == "ws" && wsPath == "" {
		wsPath = "/"
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
		Type:        transport,
		Security:    security,
		Path:        wsPath,
		Host:        wsHost,
	}, nil
}
