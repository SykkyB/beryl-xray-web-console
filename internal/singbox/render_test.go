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

func sampleProfile() store.Profile {
	return store.Profile{
		ID:          "id-1",
		Name:        "Home Flint2",
		Server:      "vpn.example.com",
		Port:        9443,
		UUID:        "11406a7a-31f6-4454-8270-6b183c909c36",
		Flow:        "xtls-rprx-vision",
		SNI:         "www.cloudflare.com",
		Fingerprint: "chrome",
		PublicKey:   "nx7fNkmB54WFIWMqPUtPdfqzoztrYfhLDscbGIDCQFc",
		ShortID:     "deadbeef",
	}
}

func TestRender_ProducesValidJSON(t *testing.T) {
	raw, err := Render(sampleProfile())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	// Spot-check a couple of placeholders landed where expected.
	out := string(raw)
	if !strings.Contains(out, `"vpn.example.com"`) {
		t.Errorf("server not substituted")
	}
	if !strings.Contains(out, `"server_port": 9443`) {
		t.Errorf("port not substituted")
	}
	if !strings.Contains(out, `"uuid": "11406a7a-31f6-4454-8270-6b183c909c36"`) {
		t.Errorf("uuid not substituted")
	}
}

func TestRender_DefaultsFingerprint(t *testing.T) {
	p := sampleProfile()
	p.Fingerprint = ""
	raw, err := Render(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"fingerprint": "chrome"`) {
		t.Errorf("missing fingerprint default")
	}
}

func TestRender_EscapesQuotesInProfile(t *testing.T) {
	p := sampleProfile()
	p.Server = `evil"; }, "extra": {`
	raw, err := Render(p)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Even with an evil server name, output must still be a single
	// well-formed JSON object — no injection.
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("escape failure produced invalid JSON: %v", err)
	}
}

func TestWriteAndCheck_WritesAtomically(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{
		ConfigPath: filepath.Join(dir, "config.json"),
		// SingBoxBin empty → skip validation, we test pure I/O here.
	}
	if err := r.WriteAndCheck(context.Background(), sampleProfile()); err != nil {
		t.Fatalf("WriteAndCheck: %v", err)
	}
	data, err := os.ReadFile(r.ConfigPath)
	if err != nil {
		t.Fatalf("read result: %v", err)
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

	// Pre-populate so we can verify it isn't overwritten on rejection.
	if err := os.WriteFile(confPath, []byte(`{"prev":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	f := (&runner.Fake{}).On(runner.FakeCall{
		Match:  "/usr/bin/sing-box check",
		Stderr: "FATAL: bad config",
		Err:    &runner.ExitErr{ExitCode: 1, Stderr: "FATAL: bad config"},
	})
	r := &Renderer{
		ConfigPath: confPath,
		SingBoxBin: "/usr/bin/sing-box",
		Runner:     f,
	}
	err := r.WriteAndCheck(context.Background(), sampleProfile())
	if err == nil {
		t.Fatal("expected error from check rejection")
	}
	got, _ := os.ReadFile(confPath)
	if string(got) != `{"prev":true}` {
		t.Errorf("config was overwritten despite check failure: %s", got)
	}
}
