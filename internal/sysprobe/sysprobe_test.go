package sysprobe

import (
	"context"
	"errors"
	"testing"

	"beryl-xray-web-console/internal/runner"
)

func TestSingBoxRunning_True(t *testing.T) {
	f := (&runner.Fake{}).On(runner.FakeCall{Match: "pgrep", Stdout: "1234\n"})
	p := &Probe{Runner: f}
	on, err := p.SingBoxRunning(context.Background())
	if err != nil || !on {
		t.Fatalf("got (%v, %v)", on, err)
	}
}

func TestTunUp_RecognizesUpState(t *testing.T) {
	out := "13: sing-tun: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1400 ...\n"
	f := (&runner.Fake{}).On(runner.FakeCall{Match: "ip", Stdout: out})
	p := &Probe{Runner: f}
	up, err := p.TunUp(context.Background(), "sing-tun")
	if err != nil || !up {
		t.Fatalf("got (%v, %v)", up, err)
	}
}

func TestTunUp_FailsClosedOnError(t *testing.T) {
	f := (&runner.Fake{}).On(runner.FakeCall{Match: "ip", Err: errors.New("boom")})
	p := &Probe{Runner: f}
	up, err := p.TunUp(context.Background(), "sing-tun")
	if up {
		t.Errorf("up = true, want false")
	}
	if err == nil {
		t.Errorf("err = nil, want non-nil for non-ExitErr")
	}
}

func TestSwitchPosition(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{"on", "on\n", "on"},
		{"off", "off\n", "off"},
		{"unsupported", "no support\n", "unknown"},
		{"empty", "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := (&runner.Fake{}).On(runner.FakeCall{Match: "sh", Stdout: tt.stdout})
			p := &Probe{Runner: f}
			got, err := p.SwitchPosition(context.Background())
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
