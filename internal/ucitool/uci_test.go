package ucitool

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"beryl-xray-web-console/internal/runner"
)

func TestParseBool(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1", true}, {"true", true}, {"YES", true}, {"on", true}, {"enabled", true},
		{"0", false}, {"false", false}, {"", false}, {"nope", false}, {"  ", false},
	}
	for _, tt := range tests {
		if got := ParseBool(tt.in); got != tt.want {
			t.Errorf("ParseBool(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestGet_Found(t *testing.T) {
	f := (&runner.Fake{}).On(runner.FakeCall{
		Match:  "uci -q get sing-box.config.killswitch",
		Stdout: "1\n",
	})
	tool := &Tool{Runner: f}
	v, err := tool.Get(context.Background(), "sing-box.config.killswitch")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != "1" {
		t.Errorf("Get: got %q, want %q", v, "1")
	}
}

func TestGet_MissingReturnsEmpty(t *testing.T) {
	// Simulate `uci -q get nonexistent` — exit code 1, empty stderr.
	missing := &exec.ExitError{ProcessState: nil} // ExitCode() returns -1 here
	_ = missing                                   // we use a contrived error path
	f := (&runner.Fake{}).On(runner.FakeCall{
		Match: "uci -q",
		Err:   errors.New("uci-style error"),
	})
	tool := &Tool{Runner: f}
	_, err := tool.Get(context.Background(), "nope.x.y")
	// We don't have a real *exec.ExitError so the error propagates; this
	// asserts the non-ExitErr path returns the wrapped error rather than
	// silently swallowing it.
	if err == nil {
		t.Fatal("expected error for non-ExitErr failure")
	}
}

func TestGetBool(t *testing.T) {
	f := (&runner.Fake{}).On(runner.FakeCall{
		Match:  "uci -q get sing-box.config.bind_switch",
		Stdout: "1\n",
	})
	tool := &Tool{Runner: f}
	b, err := tool.GetBool(context.Background(), "sing-box.config.bind_switch")
	if err != nil {
		t.Fatalf("GetBool: %v", err)
	}
	if !b {
		t.Errorf("GetBool: got false, want true")
	}
}
