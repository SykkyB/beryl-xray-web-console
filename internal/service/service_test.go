package service

import (
	"context"
	"errors"
	"testing"

	"beryl-xray-web-console/internal/runner"
)

func TestIsValidAction(t *testing.T) {
	for _, a := range []string{"start", "stop", "restart", "reload"} {
		if !IsValidAction(a) {
			t.Errorf("IsValidAction(%q) = false, want true", a)
		}
	}
	for _, a := range []string{"", "kill", "rm", "../start", "RESTART"} {
		if IsValidAction(a) {
			t.Errorf("IsValidAction(%q) = true, want false", a)
		}
	}
}

func TestDo_RunsInitScript(t *testing.T) {
	f := (&runner.Fake{}).On(runner.FakeCall{Match: "/etc/init.d/sing-box restart"})
	mgr := &Manager{InitScript: "/etc/init.d/sing-box", Runner: f}
	if err := mgr.Do(context.Background(), ActionRestart); err != nil {
		t.Fatalf("Do: %v", err)
	}
	got := f.Executed()
	if len(got) != 1 || got[0] != "/etc/init.d/sing-box restart" {
		t.Errorf("Executed: %v", got)
	}
}

func TestDo_PropagatesError(t *testing.T) {
	f := (&runner.Fake{}).On(runner.FakeCall{
		Match:  "/etc/init.d/sing-box start",
		Stderr: "config invalid",
		Err:    errors.New("exit 1"),
	})
	mgr := &Manager{InitScript: "/etc/init.d/sing-box", Runner: f}
	err := mgr.Do(context.Background(), ActionStart)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSetKillswitch(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/etc/init.d/sing-box killswitch_on"})
	f.On(runner.FakeCall{Match: "/etc/init.d/sing-box killswitch_off"})
	mgr := &Manager{InitScript: "/etc/init.d/sing-box", Runner: f}

	if err := mgr.SetKillswitch(context.Background(), true); err != nil {
		t.Fatalf("on: %v", err)
	}
	if err := mgr.SetKillswitch(context.Background(), false); err != nil {
		t.Fatalf("off: %v", err)
	}

	got := f.Executed()
	want := []string{
		"/etc/init.d/sing-box killswitch_on",
		"/etc/init.d/sing-box killswitch_off",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("call %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSetBindSwitch(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/etc/init.d/sing-box bind_switch_on"})
	f.On(runner.FakeCall{Match: "/etc/init.d/sing-box bind_switch_off"})
	mgr := &Manager{InitScript: "/etc/init.d/sing-box", Runner: f}

	if err := mgr.SetBindSwitch(context.Background(), true); err != nil {
		t.Fatalf("on: %v", err)
	}
	if err := mgr.SetBindSwitch(context.Background(), false); err != nil {
		t.Fatalf("off: %v", err)
	}
}
