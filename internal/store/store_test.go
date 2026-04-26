package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Profiles {
	t.Helper()
	dir := t.TempDir()
	return &Profiles{Path: filepath.Join(dir, "profiles.json")}
}

func sample(id string) Profile {
	return Profile{
		ID:        id,
		Name:      "Test " + id,
		Server:    "vpn.example.com",
		Port:      9443,
		UUID:      "11406a7a-31f6-4454-8270-6b183c909c36",
		SNI:       "www.cloudflare.com",
		PublicKey: "pbk",
		ShortID:   "deadbeef",
	}
}

func TestList_EmptyOnMissingFile(t *testing.T) {
	s := newStore(t)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("got %d profiles, want 0", len(list))
	}
}

func TestAdd_RoundTrip(t *testing.T) {
	s := newStore(t)
	if err := s.Add(sample("a")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := s.Get("a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Test a" {
		t.Errorf("Name: got %q", got.Name)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt zero — should be set automatically")
	}
}

func TestAdd_RejectsDuplicateID(t *testing.T) {
	s := newStore(t)
	if err := s.Add(sample("a")); err != nil {
		t.Fatal(err)
	}
	err := s.Add(sample("a"))
	if !errors.Is(err, ErrIDExists) {
		t.Errorf("got %v, want ErrIDExists", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	s := newStore(t)
	err := s.Update(sample("nope"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestUpdate_PreservesCreatedAt_BumpsUpdatedAt(t *testing.T) {
	s := newStore(t)
	if err := s.Add(sample("a")); err != nil {
		t.Fatal(err)
	}
	original, _ := s.Get("a")

	mod := sample("a")
	mod.Name = "renamed"
	if err := s.Update(mod); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("a")
	if got.Name != "renamed" {
		t.Errorf("Name: got %q", got.Name)
	}
	if !got.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt drifted: was %v, now %v", original.CreatedAt, got.CreatedAt)
	}
	if !got.UpdatedAt.After(original.UpdatedAt) && !got.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("UpdatedAt should be >= old (got %v < %v)", got.UpdatedAt, original.UpdatedAt)
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	if err := s.Add(sample("a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(sample("b")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].ID != "b" {
		t.Errorf("after delete: %v", list)
	}
	err := s.Delete("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: got %v", err)
	}
}

func TestSave_AtomicWriteCleansUpTmp(t *testing.T) {
	s := newStore(t)
	if err := s.Add(sample("a")); err != nil {
		t.Fatal(err)
	}
	tmp := s.Path + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp file leaked: %v", err)
	}
}
