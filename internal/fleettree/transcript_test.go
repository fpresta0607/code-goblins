package fleettree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// On 2026-09-29 the monitor woke the CFO about two native Codex goblins with
// no progress evidence for an hour while both were working. Codex keeps its
// rollout open and appends to it, and each rollout's write time stayed about
// ninety seconds after its creation for hours while its entries ran on, read
// alike with os.Stat and from an open handle. Every harness stamps each
// transcript entry, so the last complete entry says when the harness last
// wrote, whatever the file's write time says.
func TestWrittenAtReadsTheLastCompleteEntry(t *testing.T) {
	frozen := time.Date(2026, 9, 29, 12, 41, 27, 0, time.UTC)
	last := time.Date(2026, 9, 29, 15, 22, 49, 970_000_000, time.UTC)
	for name, entries := range map[string][]string{
		"codex": {
			`{"timestamp":"2026-09-29T15:22:31.251Z","ordinal":2499,"type":"event_msg","payload":{"type":"agent_message"}}`,
			`{"timestamp":"2026-09-29T15:22:49.970Z","ordinal":2500,"type":"event_msg","payload":{"type":"task_complete"}}`,
		},
		"claude": {
			`{"parentUuid":"a1","message":{"role":"user"},"timestamp":"2026-09-29T15:22:31.251Z"}`,
			`{"parentUuid":"a2","message":{"role":"assistant"},"timestamp":"2026-09-29T15:22:49.970Z"}`,
		},
		"pi": {
			`{"type":"message","id":"107df141","timestamp":"2026-09-29T15:22:31.251Z","message":{"role":"user"}}`,
			`{"type":"message","id":"107df142","timestamp":"2026-09-29T15:22:49.970Z","message":{"role":"assistant"}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
			writer, err := os.OpenFile(transcript, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			// The harness is part way through writing its next entry.
			if _, err := writer.WriteString(strings.Join(entries, "\n") + "\n" + `{"timestamp":"2026-09-29T15:23:`); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(transcript, frozen, frozen); err != nil {
				t.Fatal(err)
			}

			// Act
			written := WrittenAt(transcript)

			// Assert
			if !written.Equal(last) {
				t.Fatalf("WrittenAt = %v, want %v, the last complete entry's, not the frozen write time %v", written, last, frozen)
			}
		})
	}
}

func TestSessionTranscriptFindsEachHarnessTranscript(t *testing.T) {
	home := t.TempDir()
	for _, path := range [][]string{
		{".claude", "projects", "C--work-g1", "a1b2.jsonl"},
		{".codex", "sessions", "2026", "09", "26", "rollout-2026-09-26T09-00-00-c3d4.jsonl"},
		{".pi", "agent", "sessions", "--C--work-g1--", "2026-09-26T09-00-00-000Z_e5f6.jsonl"},
	} {
		writeFile(t, filepath.Join(append([]string{home}, path...)...), "{}\n", at)
	}
	for _, test := range []struct {
		harness, session string
		want             bool
	}{
		{"claude", "a1b2", true},
		{"codex", "c3d4", true},
		{"pi", "e5f6", true},
		{"kimi", "a1b2", false},
		{"claude", "missing", false},
		{"claude", `..\a1b2`, false},
		{"claude", "*", false},
	} {
		if got := SessionTranscript(home, test.harness, test.session); (got != "") != test.want {
			t.Errorf("SessionTranscript(%s, %q) = %q, want found %v", test.harness, test.session, got, test.want)
		}
	}
}

// A goblin's Claude Code conversation is read from the folder Claude Code
// keeps for its worktree, and no other folder is looked in: on 2026-10-28 a
// look in each of the 1,273 folders took 10,196 file operations, for each of
// 13 live goblins every 15 seconds (#1716). A file of the same name another
// folder holds is not taken for it, even written later, and a conversation
// its own folder does not hold is still found wherever it is.
func TestAGoblinsConversationIsReadFromItsWorktreesOwnFolder(t *testing.T) {
	// Arrange: the worktree's folder holds conversation a1b2, another folder
	// a file of the same name written an hour later, and conversation c3d4.
	home := t.TempDir()
	projects := filepath.Join(home, ".claude", "projects")
	own := filepath.Join(projects, "D--Goblins-worktrees-code-goblins-olive-task", "a1b2.jsonl")
	elsewhere := filepath.Join(projects, "C--dev-elsewhere", "c3d4.jsonl")
	writeFile(t, own, "{}\n", at)
	writeFile(t, filepath.Join(projects, "C--dev-elsewhere", "a1b2.jsonl"), "{}\n", at.Add(time.Hour))
	writeFile(t, elsewhere, "{}\n", at)
	reader := &Reader{Home: home}
	for _, test := range []struct{ session, want string }{{"a1b2", own}, {"c3d4", elsewhere}} {
		t.Run(test.session, func(t *testing.T) {
			goblin := Goblin{Meta: state.TaskMeta{ID: "olive-task", Harness: "claude", Worktree: `D:\Goblins\worktrees\code-goblins\olive-task`}, Session: test.session}

			// Act
			got := reader.Conversation(context.Background(), goblin)

			// Assert
			if got != test.want {
				t.Errorf("conversation %s read from %q, want %q", test.session, got, test.want)
			}
		})
	}
}
