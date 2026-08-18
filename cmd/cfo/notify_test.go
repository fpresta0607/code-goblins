package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestNotifyBlockedWritesStatusAndWakesTheCFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	stateDir := filepath.Join(dir, "state")
	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 || lines[0] != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one blocked line", lines, err)
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "notify" || records[0].Key != "g1" || records[0].Detail != "blocked: Should I merge this?" {
		t.Fatalf("wake records = %+v, want one notify carrying the question", records)
	}

	ep, err := wake.ReadEpisode(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 1 {
		t.Errorf("episode = %+v, want pending:1 so the CFO is actually woken", ep)
	}
}

func TestNotifyDoneRequiresPR(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--done"}, &stdout, &stderr); exit != 2 || !strings.Contains(stderr.String(), "--pr") {
		t.Fatalf("exit=%d stderr=%q, want --pr refusal", exit, stderr.String())
	}
}

func TestNotifyRequiresExactlyOneOutcome(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1"}, &stdout, &stderr); exit != 2 || !strings.Contains(stderr.String(), "exactly one") {
		t.Fatalf("exit=%d stderr=%q, want exactly-one refusal", exit, stderr.String())
	}
}

func TestNotifyTargetsStateOverrideWithoutCFOHome(t *testing.T) {
	worktree := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	t.Chdir(worktree)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 || lines[0] != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one blocked line in the override dir", lines, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("notify polluted the worktree with a state dir: %v", err)
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "notify" {
		t.Fatalf("wake records = %+v, want one notify in the override dir", records)
	}
}
