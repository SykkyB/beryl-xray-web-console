package http

import "testing"

func TestCheckLANBind(t *testing.T) {
	tests := []struct {
		name    string
		listen  string
		wantErr bool
	}{
		{"lan ip", "192.168.200.1:9092", false},
		{"loopback", "127.0.0.1:9092", false},
		{"link-local v4", "169.254.10.5:9092", false},
		{"ula v6", "[fd9f:ccc4:6741::1]:9092", false},
		{"wildcard v4", "0.0.0.0:9092", false}, // accepted with stderr warning
		{"wildcard v6", "[::]:9092", false},    // accepted with stderr warning
		{"empty host", ":9092", true},
		{"public ipv4", "8.8.8.8:9092", true},
		{"missing port", "192.168.1.1", true},
		{"bad host", "this is not a host:9092", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckLANBind(tt.listen)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckLANBind(%q) err=%v wantErr=%v", tt.listen, err, tt.wantErr)
			}
		})
	}
}
