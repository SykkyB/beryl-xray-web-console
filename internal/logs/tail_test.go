package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTail_LessThanRequested(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.log")
	writeFile(t, p, "a\nb\nc\n")

	got, err := Tail(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "a|b|c" {
		t.Errorf("got %v", got)
	}
}

func TestTail_ExactN(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.log")
	writeFile(t, p, "a\nb\nc\nd\ne\n")

	got, err := Tail(p, 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "c|d|e" {
		t.Errorf("got %v", got)
	}
}

func TestTail_NoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.log")
	writeFile(t, p, "a\nb\nc")

	got, err := Tail(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "b|c" {
		t.Errorf("got %v", got)
	}
}

func TestTail_LargeFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.log")
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("line ")
		sb.WriteByte(byte('a' + i%26))
		sb.WriteByte('\n')
	}
	writeFile(t, p, sb.String())

	got, err := Tail(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Fatalf("len: got %d, want 100", len(got))
	}
	if got[len(got)-1] != "line "+string(rune('a'+(4999%26))) {
		t.Errorf("last line: %q", got[len(got)-1])
	}
}

func TestTail_MissingFile(t *testing.T) {
	got, err := Tail("/no/such/file/at/all.log", 10)
	if err != nil {
		t.Fatalf("missing should be nil err, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing should be empty, got %v", got)
	}
}

func TestTail_Empty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.log")
	writeFile(t, p, "")

	got, err := Tail(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v", got)
	}
}
