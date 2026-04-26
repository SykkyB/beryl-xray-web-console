package runner

import (
	"context"
	"fmt"
	"strings"
)

// Fake is a Runner that replays canned responses keyed on the command
// line. Tests register expected calls with On(), then assert via the
// regular Runner interface that production code shells out as expected.
//
// Calls that don't match any registered prefix return an error so tests
// fail loudly when production starts running unexpected commands.
type Fake struct {
	calls    []FakeCall
	executed []string
}

// FakeCall describes one expected shell-out and what to return.
type FakeCall struct {
	// Match matches the full command line ("name arg1 arg2 ...") with
	// strings.HasPrefix. Empty matches anything.
	Match string

	Stdout string
	Stderr string
	Err    error
}

// On registers a canned response. Returns f for chaining.
func (f *Fake) On(call FakeCall) *Fake {
	f.calls = append(f.calls, call)
	return f
}

// Executed returns the full command lines that were actually run, in
// order. Useful for assertions.
func (f *Fake) Executed() []string {
	return append([]string(nil), f.executed...)
}

func (f *Fake) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	full := name
	if len(args) > 0 {
		full = name + " " + strings.Join(args, " ")
	}
	f.executed = append(f.executed, full)
	for _, c := range f.calls {
		if c.Match == "" || strings.HasPrefix(full, c.Match) {
			return []byte(c.Stdout), []byte(c.Stderr), c.Err
		}
	}
	return nil, nil, fmt.Errorf("fake runner: no match for %q", full)
}
