package http

import (
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strconv"
	"strings"

	"beryl-xray-web-console/internal/qr"
	"beryl-xray-web-console/internal/vless"
)

// handleQRPost renders a QR PNG for an arbitrary payload. Used by the
// VPN Scout tab — results carry the full vless:// URL inline, so the
// frontend just POSTs the candidate's URL here and shoves the response
// into an <img>. POST (vs GET with query param) avoids URL-length
// limits with the long Reality public keys and short IDs.
//
// Body: {url, size?, level?}  level ∈ {low,medium,high,highest}
//
// We do NOT validate the URL beyond a length check — the frontend
// passes back exactly the vless:// it just received in /api/scan/results
// and we trust that pipeline. An attacker who could POST here can also
// trivially mint their own QR with a phone camera, so there's no
// confused-deputy risk to guard against.
func (s *Server) handleQRPost(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body struct {
		URL   string `json:"url"`
		Size  int    `json:"size"`
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	if body.URL == "" {
		writeErr(w, nethttp.StatusBadRequest, errors.New("url required"))
		return
	}
	// Sanity bound — a real vless:// payload is <2 KB. 16 KB lets the
	// occasional bloated kort0881 URL through without enabling abuse.
	if len(body.URL) > 16*1024 {
		writeErr(w, nethttp.StatusRequestEntityTooLarge, errors.New("url too long"))
		return
	}

	size := body.Size
	if size <= 0 {
		size = 384
	}
	if size > 1024 {
		size = 1024
	}
	level := parseQRLevel(body.Level)

	png, err := qr.PNG(body.URL, size, level)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	// Content-Length helps the browser show a progress indicator on the
	// (rare) slow render; standard practice for in-memory images.
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write(png)
}

// handleProfileQR renders a QR for a stored profile by ID. Mirrors
// flint2's /api/clients/{id}/qr.png so the two consoles feel the same.
// 404 if the profile doesn't exist; never returns a partial PNG.
func (s *Server) handleProfileQR(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, nethttp.StatusBadRequest, errors.New("id required"))
		return
	}
	p, err := s.Profiles.Get(id)
	if err != nil {
		writeErr(w, nethttp.StatusNotFound, err)
		return
	}
	// Prefer the original URL stored at import time; fall back to a
	// rebuilt one for legacy profiles imported before raw_url was a
	// thing. Rebuilt URLs are valid VLESS links but may differ from
	// the original byte-for-byte (param order, fragment encoding).
	payload := p.RawURL
	if payload == "" {
		payload = vless.BuildURL(vless.URL{
			UUID:        p.UUID,
			Server:      p.Server,
			Port:        p.Port,
			Name:        p.Name,
			Flow:        p.Flow,
			SNI:         p.SNI,
			Fingerprint: p.Fingerprint,
			PublicKey:   p.PublicKey,
			ShortID:     p.ShortID,
			Type:        p.EffectiveType(),
			Security:    p.Security,
			Path:        p.Path,
			Host:        p.Host,
		})
	}

	size := 384
	if v := r.URL.Query().Get("size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
			if size > 1024 {
				size = 1024
			}
		}
	}
	level := parseQRLevel(r.URL.Query().Get("level"))

	png, err := qr.PNG(payload, size, level)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write(png)
}

// handleProfileLink returns the vless:// URL for a stored profile as
// JSON. Used by the frontend's "Copy link" button — fetch + read text
// keeps the secret out of the URL bar (a GET-with-fragment leak vector
// for vless URLs that include the UUID).
func (s *Server) handleProfileLink(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, nethttp.StatusBadRequest, errors.New("id required"))
		return
	}
	p, err := s.Profiles.Get(id)
	if err != nil {
		writeErr(w, nethttp.StatusNotFound, err)
		return
	}
	payload := p.RawURL
	if payload == "" {
		payload = vless.BuildURL(vless.URL{
			UUID:        p.UUID,
			Server:      p.Server,
			Port:        p.Port,
			Name:        p.Name,
			Flow:        p.Flow,
			SNI:         p.SNI,
			Fingerprint: p.Fingerprint,
			PublicKey:   p.PublicKey,
			ShortID:     p.ShortID,
			Type:        p.EffectiveType(),
			Security:    p.Security,
			Path:        p.Path,
			Host:        p.Host,
		})
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"url": payload, "name": p.Name})
}

func parseQRLevel(s string) qr.Level {
	switch strings.ToLower(s) {
	case "low", "l":
		return qr.Low
	case "high", "h":
		return qr.High
	case "highest", "x":
		return qr.Highest
	default:
		return qr.Medium
	}
}
