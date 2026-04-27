package logs

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestHub_FanOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub := NewHub(path)

	sub1, unsub1 := hub.Subscribe()
	defer unsub1()
	sub2, unsub2 := hub.Subscribe()
	defer unsub2()

	if got := hub.SubscriberCount(); got != 2 {
		t.Errorf("count: got %d, want 2", got)
	}

	// Give the streamer goroutine its initial stat (it seeks to EOF
	// on first run so subscribers don't get flooded with history).
	// Without this, our append below races the initial stat and the
	// streamer thinks "hello" is part of the seed.
	time.Sleep(80 * time.Millisecond)

	// Append a line and confirm both subscribers see it.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	deadline := time.After(3 * time.Second)
	var wg sync.WaitGroup
	wg.Add(2)

	check := func(name string, ch <-chan string) {
		defer wg.Done()
		select {
		case line := <-ch:
			if line != "hello" {
				t.Errorf("%s: got %q", name, line)
			}
		case <-deadline:
			t.Errorf("%s: timeout", name)
		}
	}
	go check("sub1", sub1)
	go check("sub2", sub2)
	wg.Wait()
}

func TestHub_StopsStreamerWhenIdle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hub := NewHub(path)

	_, unsub := hub.Subscribe()
	hub.mu.Lock()
	hadCancel := hub.cancel != nil
	hub.mu.Unlock()
	if !hadCancel {
		t.Error("streamer should be running after first Subscribe")
	}

	unsub()

	hub.mu.Lock()
	stillRunning := hub.cancel != nil
	hub.mu.Unlock()
	if stillRunning {
		t.Error("streamer should be stopped after last unsubscribe")
	}
}
