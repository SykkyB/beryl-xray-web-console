package singbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"beryl-xray-web-console/internal/runner"
	"beryl-xray-web-console/internal/store"
)

func sampleProfile(suffix string) store.Profile {
	return store.Profile{
		ID:          "id-" + suffix,
		Name:        "Profile " + suffix,
		Server:      suffix + ".example.com",
		Port:        9443,
		UUID:        "11406a7a-31f6-4454-8270-6b183c909c36",
		Flow:        "xtls-rprx-vision",
		SNI:         "www.cloudflare.com",
		Fingerprint: "chrome",
		PublicKey:   "nx7fNkmB54WFIWMqPUtPdfqzoztrYfhLDscbGIDCQFc",
		ShortID:     "deadbeef",
	}
}

func TestRender_SingleProfile(t *testing.T) {
	p := sampleProfile("a")
	raw, err := Render([]store.Profile{p}, p.ID)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	out := string(raw)
	if !strings.Contains(out, `"a.example.com"`) {
		t.Errorf("missing server")
	}
	// Selector must always exist with tag "proxy" so route.final keeps working.
	if !strings.Contains(out, `"type": "selector"`) {
		t.Errorf("missing selector outbound")
	}
	if !strings.Contains(out, `"tag": "proxy"`) {
		t.Errorf("missing proxy tag on selector")
	}
}

func TestRender_MultipleProfilesUseSelector(t *testing.T) {
	pa := sampleProfile("a")
	pb := sampleProfile("b")
	pb.UUID = "f62f2e29-c581-4423-bafe-3771c7faefe9"

	raw, err := Render([]store.Profile{pa, pb}, pb.ID)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
		DNS       struct {
			Rules []map[string]any `json:"rules"`
		} `json:"dns"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Expect: 2 vless + 1 selector + 1 direct = 4 outbounds.
	if got := len(cfg.Outbounds); got != 4 {
		t.Fatalf("outbounds count: got %d, want 4", got)
	}

	// The selector's default must be the active profile's tag.
	var selector map[string]any
	for _, o := range cfg.Outbounds {
		if o["type"] == "selector" {
			selector = o
			break
		}
	}
	if selector == nil {
		t.Fatalf("no selector outbound")
	}
	wantTag := TagOf(pb)
	if selector["default"] != wantTag {
		t.Errorf("selector default: got %v, want %v", selector["default"], wantTag)
	}

	// DNS rule must mention both servers so they resolve via local-dns.
	if len(cfg.DNS.Rules) == 0 {
		t.Fatalf("no DNS rules")
	}
	domains, _ := cfg.DNS.Rules[0]["domain"].([]any)
	if len(domains) != 2 {
		t.Errorf("DNS rule domains: got %d, want 2", len(domains))
	}
}

func TestRender_DefaultsActiveToFirstWhenIDMissing(t *testing.T) {
	p := sampleProfile("a")
	raw, err := Render([]store.Profile{p}, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"default": "`+TagOf(p)+`"`) {
		t.Errorf("active should fall back to first profile")
	}
}

func TestRender_RejectsEmpty(t *testing.T) {
	_, err := Render(nil, "")
	if err == nil {
		t.Fatal("expected error for empty profile list")
	}
}

func TestRender_DefaultsFingerprint(t *testing.T) {
	p := sampleProfile("a")
	p.Fingerprint = ""
	raw, err := Render([]store.Profile{p}, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"fingerprint": "chrome"`) {
		t.Errorf("missing fingerprint default")
	}
}

func TestRender_EscapesQuotesInProfile(t *testing.T) {
	p := sampleProfile("a")
	p.Server = `evil"; }, "extra": {`
	raw, err := Render([]store.Profile{p}, p.ID)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("escape failure produced invalid JSON: %v\n--- output ---\n%s", err, raw)
	}
}

func TestWriteAndCheck_WritesAtomically(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{ConfigPath: filepath.Join(dir, "config.json")}
	p := sampleProfile("a")
	if err := r.WriteAndCheck(context.Background(), []store.Profile{p}, p.ID); err != nil {
		t.Fatalf("WriteAndCheck: %v", err)
	}
	data, err := os.ReadFile(r.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Errorf("written file is not valid JSON: %v", err)
	}
	if _, err := os.Stat(r.ConfigPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file leaked")
	}
}

func TestWriteAndCheck_RejectsOnCheckFailure(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(confPath, []byte(`{"prev":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	f := (&runner.Fake{}).On(runner.FakeCall{
		Match: "/usr/bin/sing-box check",
		Err:   &runner.ExitErr{ExitCode: 1, Stderr: "FATAL: bad config"},
	})
	r := &Renderer{
		ConfigPath: confPath,
		SingBoxBin: "/usr/bin/sing-box",
		Runner:     f,
	}
	p := sampleProfile("a")
	if err := r.WriteAndCheck(context.Background(), []store.Profile{p}, p.ID); err == nil {
		t.Fatal("expected error from check rejection")
	}
	got, _ := os.ReadFile(confPath)
	if string(got) != `{"prev":true}` {
		t.Errorf("config was overwritten despite check failure: %s", got)
	}
}
