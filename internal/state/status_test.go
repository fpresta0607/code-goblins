package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if got := events(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")); len(got) != 2 || got[0] != "spawned" || got[1] != "working" {
		t.Errorf("log = %q, want the events [spawned working] after their stamps", data)
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
	if got := events(got); len(got) != 2 || got[0] != "three" || got[1] != "four" {
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
	if got := events(got); len(got) != 1 || got[0] != "only" {
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

func TestAppendStatusStampsEachEvent(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().UTC().Truncate(time.Second)
	if err := AppendStatus(dir, "g1", "blocked: which option"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	lines, err := TailStatus(dir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("TailStatus = %v, %v", lines, err)
	}
	stamp, rest, found := strings.Cut(lines[0], " ")
	if !found {
		t.Fatalf("line %q carries no stamp", lines[0])
	}
	if rest != "blocked: which option" {
		t.Errorf("event = %q, want the line unchanged after the stamp", rest)
	}
	recorded, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatalf("stamp %q is not RFC3339: %v", stamp, err)
	}
	if recorded.Before(before) || recorded.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("stamp %s is outside the window the write happened in", stamp)
	}
}

// events drops the stamp AppendStatus writes so a test can assert on the
// event text it passed in.
func events(lines []string) []string {
	stripped := make([]string, len(lines))
	for i, line := range lines {
		_, stripped[i] = SplitStatus(line)
	}
	return stripped
}
