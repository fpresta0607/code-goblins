package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/goblinname"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin already live when names shipped has none in its record. The
// supervisor's recovery pass names it, so the board never mixes names and
// ids.
func TestRecoveryCycleNamesALiveGoblinThatHasNoName(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, work: make(chan struct{}, 1)}

	// Act
	s.cycle(context.Background(), true)

	// Assert
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil || meta.GoblinName == "" || meta.GoblinTitle == "" {
		t.Fatalf("record = %+v, %v, want a name and a title", meta, err)
	}
	view, err := s.Snapshot()
	if err != nil || len(view.Tasks) == 0 || view.Tasks[0].GoblinName != meta.GoblinName {
		t.Fatalf("snapshot tasks = %+v, %v, want task-1 named %s", view.Tasks, err, meta.GoblinName)
	}
}

// The Overlord, 2026-10-08: "please make sure you name goblins that are
// tasks too". A task gets its goblin's name and title once it is queued, a
// backlog row or a brief alone, its card in Tasks shows them, and it keeps
// them until it starts, where its spawn takes the same pair.
func TestSnapshotNamesAQueuedTaskAndKeepsItsName(t *testing.T) {
	cases := []struct {
		name    string
		backlog string
	}{
		{name: "a backlog row", backlog: "## Queued\n- [ ] cg-voice-long - A long dictation of any length is heard and typed in full; Claude Code (repo: code-goblins)\n"},
		{name: "a brief alone", backlog: "## Queued\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			if err := os.MkdirAll(filepath.Join(h.Data, "cg-voice-long"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.Data, "cg-voice-long", "brief.md"), []byte("# Brief cg-voice-long\n\n## Task\n\nHear a long dictation in full.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(tc.backlog), 0o644); err != nil {
				t.Fatal(err)
			}
			s := &Service{Store: store}

			// Act
			first, firstErr := s.Snapshot()
			second, secondErr := s.Snapshot()

			// Assert
			queued := func(view Snapshot) Task {
				index := slices.IndexFunc(view.Tasks, func(task Task) bool { return task.ID == "cg-voice-long" && task.Phase == "queued" })
				if index < 0 {
					t.Fatalf("snapshot tasks = %+v, want cg-voice-long queued", view.Tasks)
				}
				return view.Tasks[index]
			}
			if firstErr != nil || secondErr != nil {
				t.Fatal(errors.Join(firstErr, secondErr))
			}
			named, again := queued(first), queued(second)
			if named.GoblinName == "" || named.GoblinTitle != "Voice Whisperer" {
				t.Fatalf("queued task = %+v, want a name and the title Voice Whisperer", named)
			}
			if again.GoblinName != named.GoblinName || again.GoblinTitle != named.GoblinTitle {
				t.Fatalf("second look named it %s - %s, want %s - %s kept", again.GoblinName, again.GoblinTitle, named.GoblinName, named.GoblinTitle)
			}
			pairs, err := goblinname.ReadQueued(h.State)
			if err != nil || pairs["cg-voice-long"] != (goblinname.Pair{Name: named.GoblinName, Title: named.GoblinTitle}) {
				t.Fatalf("queued pairs = %+v, %v, want cg-voice-long's kept for its spawn", pairs, err)
			}
		})
	}
}
