package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendStatusCreatesAndAppends(t *testing.T) {
	dir := t.TempDir()
	if err := AppendStatus(dir, "g1", "spawned"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := AppendStatus(dir, "g1", "working"); err != nil {
		t.Fatalf("AppendStatus second: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "g1.status"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "spawned\nworking\n" {
		t.Errorf("log = %q, want %q", data, "spawned\nworking\n")
	}
}

func TestTailStatusBounds(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"one", "two", "three", "four"} {
		if err := AppendStatus(dir, "g1", line); err != nil {
			t.Fatal(err)
		}
	}
	got, err := TailStatus(dir, "g1", 2)
	if err != nil {
		t.Fatalf("TailStatus: %v", err)
	}
	if len(got) != 2 || got[0] != "three" || got[1] != "four" {
		t.Errorf("tail = %q, want [three four]", got)
	}
}

func TestTailStatusFewerLinesThanAsked(t *testing.T) {
	dir := t.TempDir()
	if err := AppendStatus(dir, "g1", "only"); err != nil {
		t.Fatal(err)
	}
	got, err := TailStatus(dir, "g1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "only" {
		t.Errorf("tail = %q, want [only]", got)
	}
}

func TestTailStatusMissingLogMeansNoStatusYet(t *testing.T) {
	got, err := TailStatus(t.TempDir(), "ghost", 5)
	if err != nil {
		t.Fatalf("missing log must not error, got %v", err)
	}
	if got != nil {
		t.Errorf("tail = %v, want nil", got)
	}
}
