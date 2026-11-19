package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// foldersBoard is a service whose CEO reaches task-1, a native goblin first
// spawned at 11:00 on 2026-11-18 whose terminal runs, and whose janitor record,
// a sweep at the time given, measures its worktrees and scratch folder at the
// gigabytes given.
func foldersBoard(t *testing.T) (*Service, string, func(at time.Time, worktreesGB, scratchGB int64)) {
	t.Helper()
	store, h := testStore(t)
	liveNativeGoblin(t, h.State, "task-1")
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.SpawnGen = fmt.Sprintf("s%d", time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC).UnixNano())
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	s := boardService(store)
	s.Options.CEO = &CEOConnection{State: h.State}
	s.Options.Dispatch = &Dispatch{Memory: func() (Memory, error) {
		return Memory{Available: 20 * gigabyte, CommitAvailable: 20 * gigabyte, Total: 32 * gigabyte}, nil
	}}
	measure := func(at time.Time, worktreesGB, scratchGB int64) {
		t.Helper()
		record := janitor.Record{Time: at, Buckets: janitor.Buckets{Worktrees: map[string]int64{"task-1": worktreesGB * gigabyte}, Scratch: map[string]int64{"task-1": scratchGB * gigabyte}}}
		if err := janitor.WriteRecord(h.State, record); err != nil {
			t.Fatal(err)
		}
	}
	return s, h.State, measure
}

// A live goblin whose worktrees or scratch folder passes goblin_disk_gb, 5 GB
// by default, is told once to clear what it can rebuild and keep every file
// its reports, cards and questions name; one under it is told nothing
// (#2761).
func TestALiveGoblinPastItsFolderSizeIsToldOnce(t *testing.T) {
	cases := []struct {
		name                 string
		worktreesGB, scratch int64
		want                 string
	}{
		{name: "its worktrees past the size", worktreesGB: 6, scratch: 1, want: "your worktrees hold 6.0 GB, past the 5 GB"},
		{name: "its scratch folder past the size", worktreesGB: 1, scratch: 7, want: "your scratch folder holds 7.0 GB, past the 5 GB"},
		{name: "both past the size", worktreesGB: 8, scratch: 9, want: "your worktrees hold 8.0 GB and your scratch folder holds 9.0 GB"},
		{name: "both under the size", worktreesGB: 5, scratch: 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, _, measure := foldersBoard(t)
			notices := deliverNotices(t, nil)
			now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
			measure(now, c.worktreesGB, c.scratch)

			// Act
			for reading := range 3 {
				if err := s.checkFleet(context.Background(), now.Add(time.Duration(reading)*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}

			// Assert
			if c.want == "" {
				if len(*notices) != 0 {
					t.Fatalf("notices = %q, want none under the size", *notices)
				}
				return
			}
			if len(*notices) != 1 {
				t.Fatalf("notices = %q, want one in three readings", *notices)
			}
			for _, want := range []string{c.want, "Clear now what you can rebuild", "Keep every file your reports, cards and questions name, and all uncommitted work"} {
				if !strings.Contains((*notices)[0], want) {
					t.Errorf("notice %q lacks %q", (*notices)[0], want)
				}
			}
		})
	}
}

// A goblin whose terminal does not run, as a paused task's, is told nothing
// until it runs again, and then once; a later task under the same id, first
// spawned on another generation, is told nothing from the sweep that measured
// the task before it, and once a sweep measures it.
func TestAGoblinPastItsFolderSizeIsToldOnceItRunsAndAgainAsANewTask(t *testing.T) {
	// Arrange
	s, stateDir, measure := foldersBoard(t)
	notices := deliverNotices(t, nil)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	measure(now, 6, 0)
	hostRecord := filepath.Join(stateDir, "hosts", "task-1.json")
	held, err := os.ReadFile(hostRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hostRecord); err != nil {
		t.Fatal(err)
	}
	read := func() int {
		t.Helper()
		before := len(*notices)
		now = now.Add(time.Minute)
		if err := s.checkFleet(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		return len(*notices) - before
	}

	// Act and Assert
	if told := read(); told != 0 {
		t.Fatalf("a goblin whose terminal does not run was told %d times, want none", told)
	}
	writeFile(t, hostRecord, string(held))
	if told := read() + read(); told != 1 {
		t.Fatalf("the goblin running again was told %d times, want once", told)
	}
	meta, err := state.ReadTaskMeta(stateDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.FirstGen = fmt.Sprintf("s%d", now.UnixNano())
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	if told := read(); told != 0 {
		t.Fatalf("a later task under the same id was told %d times from the sweep that measured the task before it, want none", told)
	}
	measure(now, 6, 0)
	if told := read() + read(); told != 1 {
		t.Fatalf("a later task under the same id was told %d times, want once", told)
	}
}

// A notice that reaches no terminal is sent again on the next reading, three
// sends at most, and the supervisor then says it could not tell the goblin;
// one held for the goblin's next tool call reached it.
func TestAFolderNoticeThatReachesNoTerminalIsTriedThreeTimes(t *testing.T) {
	cases := []struct {
		name      string
		delivery  error
		wantSends int
		wantError bool
	}{
		{name: "typed into no terminal", delivery: fmt.Errorf("spawn: native terminal task-1: %w", fleet.ErrNotTyped), wantSends: 3, wantError: true},
		{name: "queued for the goblin's next tool call", delivery: fmt.Errorf("spawn: native terminal task-1 has the text submitted: %w", fleet.ErrQueuedForToolCall), wantSends: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, _, measure := foldersBoard(t)
			notices := deliverNotices(t, c.delivery)
			now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
			measure(now, 6, 0)

			// Act
			var errs []error
			for reading := range 5 {
				if err := s.checkFleet(context.Background(), now.Add(time.Duration(reading)*time.Minute)); err != nil {
					errs = append(errs, err)
				}
			}

			// Assert
			if len(*notices) != c.wantSends {
				t.Fatalf("sends = %d, want %d", len(*notices), c.wantSends)
			}
			isReported := len(errs) > 0 && strings.Contains(errs[0].Error(), "task-1 could not be told its folders passed goblin_disk_gb after 3 tries")
			if isReported != c.wantError || len(errs) > 1 {
				t.Fatalf("errors = %v, want it reported once: %v", errs, c.wantError)
			}
		})
	}
}
