// Package clash is a tiny HTTP client for sing-box's experimental
// clash-API (experimental.clash_api in config.json). Only the read
// endpoints the panel surfaces — /version, /connections — are wrapped.
// Streaming endpoints (/traffic, /logs) are not used yet; periodic REST
// polling is enough for the first cut of the live-data UI.
package clash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"time"
)

// Client is a configured clash-API client.
type Client struct {
	BaseURL string        // e.g. "http://127.0.0.1:9090"
	Timeout time.Duration // default 5s
	HC      *nethttp.Client
}

func (c *Client) hc() *nethttp.Client {
	if c.HC != nil {
		return c.HC
	}
	to := c.Timeout
	if to == 0 {
		to = 5 * time.Second
	}
	return &nethttp.Client{Timeout: to}
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	req, err := nethttp.NewRequestWithContext(ctx, "GET", c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("clash %s: %s: %s", path, resp.Status, body)
	}
	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("clash %s: parse: %w", path, err)
		}
	}
	return nil
}

// Version returns the sing-box version string reported by clash-API,
// useful as a sanity check that the API endpoint is alive.
type versionResp struct {
	Version string `json:"version"`
	Premium bool   `json:"premium"`
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var v versionResp
	if err := c.get(ctx, "/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Connections is the snapshot returned by GET /connections. Counters are
// cumulative since sing-box started. Connections is the live list.
type Connections struct {
	DownloadTotal int64        `json:"downloadTotal"`
	UploadTotal   int64        `json:"uploadTotal"`
	Connections   []Connection `json:"connections"`
	Memory        int64        `json:"memory,omitempty"`
}

// Connection is one active proxied flow.
type Connection struct {
	ID       string             `json:"id"`
	Metadata ConnectionMetadata `json:"metadata"`
	Upload   int64              `json:"upload"`
	Download int64              `json:"download"`
	Start    string             `json:"start"`
	Chains   []string           `json:"chains"`
	Rule     string             `json:"rule"`
}

// ConnectionMetadata is what sing-box knows about each connection.
type ConnectionMetadata struct {
	Network         string `json:"network"`
	Type            string `json:"type"`
	SourceIP        string `json:"sourceIP"`
	SourcePort      string `json:"sourcePort"`
	DestinationIP   string `json:"destinationIP"`
	DestinationPort string `json:"destinationPort"`
	Host            string `json:"host"`
	ProcessPath     string `json:"processPath,omitempty"`
}

// GetConnections fetches the current connection snapshot.
func (c *Client) GetConnections(ctx context.Context) (*Connections, error) {
	var out Connections
	if err := c.get(ctx, "/connections", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Proxy is the response shape of GET /proxies/<name>. For a selector
// outbound, Now is the currently-selected sub-outbound and All lists
// the available choices.
type Proxy struct {
	Type string   `json:"type"`
	Name string   `json:"name"`
	Now  string   `json:"now,omitempty"`
	All  []string `json:"all,omitempty"`
	UDP  bool     `json:"udp,omitempty"`
}

// GetProxy returns one proxy outbound by tag.
func (c *Client) GetProxy(ctx context.Context, name string) (*Proxy, error) {
	var p Proxy
	if err := c.get(ctx, "/proxies/"+name, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// SelectProxy switches the named selector outbound to the given target.
// Returns an error if the selector or target doesn't exist, or if the
// outbound at `selector` isn't actually a selector type. Used after
// rendering a new config so the active profile change takes effect
// without a full sing-box reload.
func (c *Client) SelectProxy(ctx context.Context, selector, target string) error {
	body, err := json.Marshal(map[string]string{"name": target})
	if err != nil {
		return err
	}
	req, err := nethttp.NewRequestWithContext(ctx, "PUT",
		c.BaseURL+"/proxies/"+selector, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("clash PUT /proxies/%s: %s: %s", selector, resp.Status, raw)
	}
	return nil
}
