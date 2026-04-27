package vless

import (
	"strings"
	"testing"
)

const fullURL = "vless://11406a7a-31f6-4454-8270-6b183c909c36@vpn.sys-lab.xyz:9443" +
	"?encryption=none&flow=xtls-rprx-vision&fp=chrome" +
	"&pbk=nx7fNkmB54WFIWMqPUtPdfqzoztrYfhLDscbGIDCQFc" +
	"&security=reality&sid=deadbeef&sni=www.cloudflare.com&type=tcp" +
	"#Ax3000-Beryl"

func TestParse_Full(t *testing.T) {
	v, err := Parse(fullURL)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := URL{
		UUID:        "11406a7a-31f6-4454-8270-6b183c909c36",
		Server:      "vpn.sys-lab.xyz",
		Port:        9443,
		Name:        "Ax3000-Beryl",
		Flow:        "xtls-rprx-vision",
		SNI:         "www.cloudflare.com",
		Fingerprint: "chrome",
		PublicKey:   "nx7fNkmB54WFIWMqPUtPdfqzoztrYfhLDscbGIDCQFc",
		ShortID:     "deadbeef",
	}
	if *v != want {
		t.Errorf("Parse mismatch:\n got %+v\nwant %+v", *v, want)
	}
}

func TestParse_NameFallsBackToHost(t *testing.T) {
	noFrag := strings.Replace(fullURL, "#Ax3000-Beryl", "", 1)
	v, err := Parse(noFrag)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v.Name != "vpn.sys-lab.xyz" {
		t.Errorf("Name: got %q, want host fallback", v.Name)
	}
}

func TestParse_DecodesURLEncodedName(t *testing.T) {
	withSpace := strings.Replace(fullURL, "#Ax3000-Beryl", "#Home%20Flint%202", 1)
	v, err := Parse(withSpace)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v.Name != "Home Flint 2" {
		t.Errorf("Name: got %q, want %q", v.Name, "Home Flint 2")
	}
}

func TestParse_TrimsWhitespace(t *testing.T) {
	v, err := Parse("  " + fullURL + "  \n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v.Server != "vpn.sys-lab.xyz" {
		t.Errorf("Parse failed to trim")
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"not vless", "https://example.com/", "not a vless"},
		{"missing UUID", "vless://@host:443?security=reality&pbk=x", "missing UUID"},
		{"missing host", "vless://UUID@:443?security=reality&pbk=x", "missing host"},
		{"missing port", "vless://UUID@host?security=reality&pbk=x", "missing port"},
		{"bad port", "vless://UUID@host:99999?security=reality&pbk=x", "invalid port"},
		{"wrong security", "vless://UUID@host:443?security=tls&pbk=x", "security=\"tls\""},
		{"wrong type", "vless://UUID@host:443?security=reality&type=ws&pbk=x", "transport type=\"ws\""},
		{"missing pbk", "vless://UUID@host:443?security=reality", "missing pbk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// sid and sni are now optional. URL must still parse cleanly; downstream
// (sing-box check) will catch any actual mismatches.
func TestParse_AcceptsMissingSidAndSni(t *testing.T) {
	v, err := Parse("vless://UUID@host:443?security=reality&pbk=PBK#name")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v.PublicKey != "PBK" {
		t.Errorf("PublicKey: got %q", v.PublicKey)
	}
	if v.SNI != "" || v.ShortID != "" {
		t.Errorf("SNI/ShortID should be empty, got SNI=%q ShortID=%q", v.SNI, v.ShortID)
	}
}
