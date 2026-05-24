package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathsFor_DateSharded(t *testing.T) {
	root := t.TempDir()
	s := New(root, "")
	ts := time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC)
	p := s.PathsFor("01H8XYZ123ABCDEFGHJKLMNPQR", ts)

	want := filepath.Join(root, "2026", "05", "24", "01H8XYZ123ABCDEFGHJKLMNPQR")
	if p.Dir != want {
		t.Errorf("Dir = %q, want %q", p.Dir, want)
	}
	if !strings.HasSuffix(p.Original, "/original") {
		t.Errorf("Original path = %q, missing /original", p.Original)
	}
}

func TestEnsureDir_CreatesPath(t *testing.T) {
	root := t.TempDir()
	s := New(root, "")
	ts := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	p := s.PathsFor("01H8ABC", ts)
	if err := s.EnsureDir(p); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}
