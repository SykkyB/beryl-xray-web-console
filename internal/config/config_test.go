package config

import (
	"strings"
	"testing"
)

const validYAML = `
listen: "192.168.200.1:9092"
sing_box_config: /etc/sing-box/config.json
sing_box_init: /etc/init.d/sing-box
sing_box_log: /var/log/sing-box.log
profiles_store: /etc/xray-panel-cli/profiles.json
clash_api: "127.0.0.1:9090"
auth:
  username: admin
  password_bcrypt: "$2a$12$abcdefghijklmnopqrstuv"
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Listen != "192.168.200.1:9092" {
		t.Errorf("listen: got %q", c.Listen)
	}
	if c.SingBoxConfig != "/etc/sing-box/config.json" {
		t.Errorf("sing_box_config: got %q", c.SingBoxConfig)
	}
	if c.Auth.Username != "admin" {
		t.Errorf("auth.username: got %q", c.Auth.Username)
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	bad := validYAML + "\nunknown_field: 123\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestRejectsRelativePaths(t *testing.T) {
	bad := strings.Replace(validYAML, "/etc/sing-box/config.json", "etc/sing-box/config.json", 1)
	_, err := Parse([]byte(bad))
	if err == nil {
		t.Fatal("expected error for relative path")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRejectsBadListen(t *testing.T) {
	// validate() catches malformed host:port. Wildcard binds and non-LAN
	// addresses are caught later by CheckLANBind at startup, not here.
	for _, listen := range []string{"", "no-port", ":"} {
		bad := strings.Replace(validYAML, `"192.168.200.1:9092"`, `"`+listen+`"`, 1)
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("expected error for listen=%q", listen)
		}
	}
}

func TestRejectsBadBcrypt(t *testing.T) {
	bad := strings.Replace(validYAML, `"$2a$12$abcdefghijklmnopqrstuv"`, `"plaintext"`, 1)
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for non-bcrypt password")
	}
}

func TestRequiresAllPaths(t *testing.T) {
	for _, missing := range []string{"sing_box_config", "sing_box_init", "sing_box_log", "profiles_store", "clash_api"} {
		// Comment out the missing line by removing it entirely from the YAML.
		bad := strings.ReplaceAll(validYAML, missing+":", "_unused:")
		_, err := Parse([]byte(bad))
		if err == nil {
			t.Errorf("expected error when %s missing", missing)
		}
	}
}
