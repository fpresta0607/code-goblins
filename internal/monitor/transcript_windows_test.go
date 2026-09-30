package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// writeTranscript writes a transcript file under home whose last entry is
// stamped entryAt and whose file was last written at writtenAt, creating it
// with first as its first line.
func writeTranscript(t *testing.T, home, first string, entryAt, writtenAt time.Time, parts ...string) {
	t.Helper()
	path := filepath.Join(append([]string{home}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	last := fmt.Sprintf(`{"timestamp":%q,"type":"message"}`, entryAt.UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(first+"\n"+last+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, writtenAt, writtenAt); err != nil {
		t.Fatal(err)
	}
}

// codexMeta is a Codex rollout's first line, which names the directory the
// session runs in.
func codexMeta(t *testing.T, cwd string, at time.Time) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"timestamp": at.UTC().Format(time.RFC3339Nano), "type": "session_meta", "payload": map[string]any{"id": "01a0f418-48d2-7a60-b094-5ed1a06bd54c", "cwd": cwd, "base_instructions": map[string]string{"text": strings.Repeat("instructions ", 400)}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A native terminal names no session, so a native goblin's transcript is
// found where its harness files a session by the directory it runs in: Claude
// Code's project folder named for the worktree, pi's session folder named for
// it, and a Codex rollout whose first line names it. Only transcripts written
// since the harness started count, and a Codex rollout, whose write time
// stays near its creation while entries run on, is read by its last entry.
func TestAWorktreeTranscriptIsFoundForEachHarnessWithoutASessionID(t *testing.T) {
	home := t.TempDir()
	worktree := `C:\work\.worktrees\gb-g1`
	started := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	now := started.Add(2 * time.Hour)
	day := filepath.Join(".codex", "sessions", started.Format("2006"), started.Format("01"), started.Format("02"))

	writeTranscript(t, home, `{"type":"summary"}`, started.Add(-time.Hour), started.Add(-time.Hour), ".claude", "projects", "C--work--worktrees-gb-g1", "0ld-session.jsonl")
	writeTranscript(t, home, `{"type":"summary"}`, started.Add(40*time.Minute), started.Add(40*time.Minute), ".claude", "projects", "C--work--worktrees-gb-g1", "a1b2c3.jsonl")
	writeTranscript(t, home, `{"type":"summary"}`, started.Add(50*time.Minute), started.Add(50*time.Minute), ".claude", "projects", "C--work--worktrees-gb-g1", "a1b2c3", "subagents", "agent-1.jsonl")
	writeTranscript(t, home, `{"type":"summary"}`, now, now, ".claude", "projects", "C--work--worktrees-gb-g2", "other.jsonl")
	writeTranscript(t, home, `{"type":"session"}`, started.Add(30*time.Minute), started.Add(30*time.Minute), ".pi", "agent", "sessions", "--C--work-.worktrees-gb-g1--", "2026-09-30T12-00-01-000Z_e5f6.jsonl")
	writeTranscript(t, home, codexMeta(t, worktree, started), started.Add(70*time.Minute), started.Add(time.Second), day, "rollout-2026-09-30T12-00-01-c3d4.jsonl")
	writeTranscript(t, home, codexMeta(t, `C:\work\.worktrees\gb-g2`, started), now, now, day, "rollout-2026-09-30T12-00-02-d4e5.jsonl")

	for harness, want := range map[string]time.Time{
		"claude": started.Add(50 * time.Minute),
		"pi":     started.Add(30 * time.Minute),
		"codex":  started.Add(70 * time.Minute),
		"kimi":   {},
	} {
		if got := worktreeTranscriptAt(home, harness, worktree, started, now); !got.Equal(want) {
			t.Errorf("%s: worktreeTranscriptAt = %s, want %s", harness, got, want)
		}
	}
	if got := worktreeTranscriptAt(home, "claude", worktree, started.Add(55*time.Minute), now); !got.IsZero() {
		t.Errorf("a transcript written before the harness started was read: %s", got)
	}
}

// 3566 and 3568 on 2026-09-30: native goblins plainly mid-turn woke the CFO
// with "no progress evidence (transcript write ...)" while their transcripts
// were being written, because a native sample names no session. A native
// goblin whose transcript was written a minute ago is progressing.
func TestANativeGoblinWritingItsTranscriptRaisesNoBusyTurnOverAge(t *testing.T) {
	stateDir, userHome := t.TempDir(), t.TempDir()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	meta := nativeMeta("g1", "claude")
	meta.Worktree = `C:\work\.worktrees\gb-g1`
	writeTask(t, stateDir, meta)
	launched := now.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), launched, launched); err != nil {
		t.Fatal(err)
	}
	// Its program's pid names no process, so no processor use can stand in
	// for the transcript as progress.
	data, err := json.Marshal(host.Record{ID: "g1", Pipe: `\\.\pipe\code-goblins-host-transcript-proof`, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: 1 << 30, Started: launched.UTC()})
	if err != nil {
		t.Fatal(err)
	}
	writeProofRecord(t, stateDir, data)
	working := []string{"✽ Reticulating… (12m · ↓ 3k tokens · esc to interrupt)", "", "⏵⏵ bypass permissions on (shift+tab to cycle)"}
	service := testService(stateDir, BackendProber{Herdr: &fakeProber{}, Native: NativeProber{StateDir: stateDir, ReadScreen: screenOf(working...)}}, &now)
	service.Gate = &fakeGate{sample: GateSample{Active: false}}
	service.Progress = HostProgress{StateDir: stateDir, Home: userHome}

	for minute := 1; minute <= 25; minute++ {
		now = now.Add(time.Minute)
		writeTranscript(t, userHome, `{"type":"summary"}`, now.Add(-time.Minute), now.Add(-time.Minute), ".claude", "projects", "C--work--worktrees-gb-g1", "a1b2c3.jsonl")
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Event != nil {
			t.Fatalf("a native goblin whose transcript was written a minute ago woke the CFO at minute %d: %+v", minute, result.Event)
		}
	}
}

func writeProofRecord(t *testing.T, stateDir string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "g1.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
