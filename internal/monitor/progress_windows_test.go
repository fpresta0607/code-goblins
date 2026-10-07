package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeCodexProgressReadsARolloutWhileItsWriterStaysOpen(t *testing.T) {
	// Arrange
	home, stateDir := t.TempDir(), t.TempDir()
	meta := nativeMeta("g1", "codex")
	meta.Worktree = t.TempDir()
	writeTask(t, stateDir, meta)
	record := recordNativeHost(t, stateDir, meta.ID)
	record.ChildPID = os.Getpid()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", meta.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	frozen := now.Add(-3 * time.Hour)
	transcript := filepath.Join(home, ".codex", "sessions", "2026", "10", "02", "rollout-2026-10-02T17-34-25-session-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(transcript, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(struct {
		Timestamp time.Time         `json:"timestamp"`
		Type      string            `json:"type"`
		Payload   map[string]string `json:"payload"`
	}{frozen, "session_meta", map[string]string{"id": "session-1", "cwd": meta.Worktree}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(transcript, frozen, frozen); err != nil {
		t.Fatal(err)
	}
	probe := NativeProber{StateDir: stateDir, ReadScreen: screenOf("• Working (2h • esc to interrupt)", "› ", "100% context left")}
	service := testService(stateDir, probe, &now)
	service.Gate = &fakeGate{sample: GateSample{Active: false}}
	service.Progress = &HostProgress{StateDir: stateDir, Home: home}
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Act: the native sample carries no session ID; the writer never closes.
	for range 4 {
		now = now.Add(time.Hour)
		if err := encoder.Encode(map[string]string{"timestamp": now.Format(time.RFC3339Nano), "type": "event_msg"}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(transcript, frozen, frozen); err != nil {
			t.Fatal(err)
		}
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}

		// Assert
		if result.Event != nil {
			t.Fatalf("a native Codex rollout written at %s with its writer still open woke the CFO: %+v", now, result.Event)
		}
		if len(result.Observations) != 1 || result.Observations[0].EvidenceAt == nil || !result.Observations[0].EvidenceAt.Equal(now) {
			t.Fatalf("observations = %+v, want evidence from the newest rollout record at %s", result.Observations, now)
		}
	}
	now = now.Add(3 * service.BusyTurnMax)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Observations[0].Reason != BusyTurnOverAge {
		t.Fatalf("a rollout that stopped moving = %+v, want busy_turn_over_age", result.Event)
	}
}

func TestNativeCodexTranscriptProgressBelongsOnlyToItsWorktree(t *testing.T) {
	for _, test := range []struct {
		name, directory, entryType string
		shouldMatch                bool
	}{
		{"same worktree", "same", "session_meta", true},
		{"case spelling", "case", "session_meta", true},
		{"another worktree", "other", "session_meta", false},
		{"parent directory", "parent", "session_meta", false},
		{"child directory", "child", "session_meta", false},
		{"relative directory", "relative", "session_meta", false},
		{"missing directory", "missing", "session_meta", false},
		{"not session metadata", "same", "event_msg", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			directory := worktree
			switch test.directory {
			case "case":
				directory = strings.ToUpper(worktree)
			case "other":
				directory = t.TempDir()
			case "parent":
				directory = filepath.Dir(worktree)
			case "child":
				directory = filepath.Join(worktree, "child")
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			case "relative":
				directory = filepath.Base(worktree)
			case "missing":
				directory = ""
			}
			transcript := filepath.Join(home, ".codex", "sessions", "2026", "10", "02", "rollout-2026-10-02T17-34-25-session-1.jsonl")
			if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
				t.Fatal(err)
			}
			writer, err := os.OpenFile(transcript, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			metadata, err := json.Marshal(struct {
				Type    string            `json:"type"`
				Payload map[string]string `json:"payload"`
			}{test.entryType, map[string]string{"cwd": directory}})
			if err != nil {
				t.Fatal(err)
			}
			last := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
			if _, err := writer.WriteString(string(metadata) + "\n" + `{"timestamp":"` + last.Format(time.RFC3339) + `","type":"event_msg"}` + "\n" + `{"timestamp":"`); err != nil {
				t.Fatal(err)
			}
			prober := HostProgress{StateDir: t.TempDir(), Home: home}
			meta := nativeMeta("g1", "codex")
			meta.Worktree = worktree

			// Act: unreadable process evidence must preserve the transcript.
			progress, _ := prober.InspectProgress(context.Background(), meta, EndpointSample{Harness: "codex"})

			// Assert
			if test.shouldMatch && !progress.TranscriptAt.Equal(last) {
				t.Fatalf("TranscriptAt = %s, want the last complete entry %s", progress.TranscriptAt, last)
			}
			if !test.shouldMatch && !progress.TranscriptAt.IsZero() {
				t.Fatalf("TranscriptAt = %s from an unrelated rollout", progress.TranscriptAt)
			}
		})
	}
}

// rolloutWrittenAt is the write time every rollout fixture is left with, so
// only its entries can show progress, as when Codex holds its writer open.
var rolloutWrittenAt = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

func rolloutPath(home, day, session string) string {
	return filepath.Join(home, ".codex", "sessions", filepath.FromSlash(day), "rollout-"+strings.ReplaceAll(day, "/", "-")+"T17-34-25-"+session+".jsonl")
}

func writeRollout(t *testing.T, path, cwd string, stamps ...time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"cwd": cwd}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(metadata, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	appendRolloutEntries(t, path, stamps...)
}

func appendRolloutEntries(t *testing.T, path string, stamps ...time.Time) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, stamp := range stamps {
		if _, err := file.WriteString(`{"timestamp":"` + stamp.Format(time.RFC3339) + `","type":"event_msg"}` + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, rolloutWrittenAt, rolloutWrittenAt); err != nil {
		t.Fatal(err)
	}
}

// nativeCodexProbe reads a native Codex task's transcript progress through
// prober, the way every scan of that task does.
func nativeCodexProbe(t *testing.T, prober *HostProgress, worktree string) time.Time {
	t.Helper()
	meta := nativeMeta("g1", "codex")
	meta.Worktree = worktree
	progress, _ := prober.InspectProgress(context.Background(), meta, EndpointSample{Harness: "codex"})
	return progress.TranscriptAt
}

func TestNativeCodexProbesReuseARolloutsBindingWhileItsEntriesAdvance(t *testing.T) {
	// Arrange
	home, worktree := t.TempDir(), t.TempDir()
	rollout := rolloutPath(home, "2026/10/02", "session-1")
	start := time.Now().UTC().Truncate(time.Second)
	writeRollout(t, rollout, worktree, start)
	prober := &HostProgress{StateDir: t.TempDir(), Home: home}
	if got := nativeCodexProbe(t, prober, worktree); !got.Equal(start) {
		t.Fatalf("first probe = %s, want %s", got, start)
	}
	// Once bound, the rollout's opening metadata is never read again: spoil
	// it so any later read of it can no longer bind the rollout.
	file, err := os.OpenFile(rollout, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("#"), 0); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	for hour := 1; hour <= 3; hour++ {
		// Act
		written := start.Add(time.Duration(hour) * time.Hour)
		appendRolloutEntries(t, rollout, written)
		got := nativeCodexProbe(t, prober, worktree)

		// Assert
		if !got.Equal(written) {
			t.Fatalf("probe %d = %s, want the newest entry %s read through the kept binding", hour, got, written)
		}
	}
	if got := nativeCodexProbe(t, &HostProgress{StateDir: t.TempDir(), Home: home}, worktree); !got.IsZero() {
		t.Fatalf("a prober reading the spoiled metadata afresh = %s, want no binding", got)
	}
}

func TestNativeCodexFindsARolloutCreatedAfterAnEarlierProbe(t *testing.T) {
	// Arrange
	home, worktree := t.TempDir(), t.TempDir()
	written := time.Now().UTC().Truncate(time.Second)
	writeRollout(t, rolloutPath(home, "2026/10/02", "foreign"), t.TempDir(), written)
	prober := &HostProgress{StateDir: t.TempDir(), Home: home}
	if got := nativeCodexProbe(t, prober, worktree); !got.IsZero() {
		t.Fatalf("probe before the task's rollout exists = %s, want zero", got)
	}

	// Act
	writeRollout(t, rolloutPath(home, "2026/10/02", "session-1"), worktree, written.Add(time.Minute))
	got := nativeCodexProbe(t, prober, worktree)

	// Assert
	if !got.Equal(written.Add(time.Minute)) {
		t.Fatalf("probe after the rollout appeared = %s, want %s", got, written.Add(time.Minute))
	}
}

func TestNativeCodexCountsFreshEntriesInAResumedOlderRollout(t *testing.T) {
	// Arrange
	home, worktree := t.TempDir(), t.TempDir()
	rollout := rolloutPath(home, "2025/01/15", "session-1")
	started := time.Date(2025, 1, 15, 17, 34, 25, 0, time.UTC)
	writeRollout(t, rollout, worktree, started)
	prober := &HostProgress{StateDir: t.TempDir(), Home: home}
	if got := nativeCodexProbe(t, prober, worktree); !got.Equal(started) {
		t.Fatalf("probe before resuming = %s, want %s", got, started)
	}

	// Act
	resumed := time.Now().UTC().Truncate(time.Second)
	appendRolloutEntries(t, rollout, resumed)
	got := nativeCodexProbe(t, prober, worktree)

	// Assert
	if !got.Equal(resumed) {
		t.Fatalf("probe after resuming = %s, want the fresh entry %s", got, resumed)
	}
}

func TestNativeCodexReadsIncompleteRolloutMetadataAgain(t *testing.T) {
	// Arrange
	home, worktree := t.TempDir(), t.TempDir()
	rollout := rolloutPath(home, "2026/10/02", "session-1")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"cwd":`), 0o600); err != nil {
		t.Fatal(err)
	}
	prober := &HostProgress{StateDir: t.TempDir(), Home: home}
	if got := nativeCodexProbe(t, prober, worktree); !got.IsZero() {
		t.Fatalf("probe of half-written metadata = %s, want zero", got)
	}

	// Act
	written := time.Now().UTC().Truncate(time.Second)
	writeRollout(t, rollout, worktree, written)
	got := nativeCodexProbe(t, prober, worktree)

	// Assert
	if !got.Equal(written) {
		t.Fatalf("probe once the metadata is whole = %s, want %s", got, written)
	}
}

func TestNativeCodexForgetsARolloutOnceItDisappears(t *testing.T) {
	// Arrange
	home, worktree := t.TempDir(), t.TempDir()
	rollout := rolloutPath(home, "2026/10/02", "session-1")
	written := time.Now().UTC().Truncate(time.Second)
	writeRollout(t, rollout, worktree, written)
	prober := &HostProgress{StateDir: t.TempDir(), Home: home}
	if got := nativeCodexProbe(t, prober, worktree); !got.Equal(written) {
		t.Fatalf("probe while the rollout exists = %s, want %s", got, written)
	}
	if err := os.Remove(rollout); err != nil {
		t.Fatal(err)
	}
	if got := nativeCodexProbe(t, prober, worktree); !got.IsZero() {
		t.Fatalf("probe after the rollout was removed = %s, want zero", got)
	}

	// Act: a file later at the same path belongs to another worktree.
	writeRollout(t, rollout, t.TempDir(), written.Add(time.Hour))
	got := nativeCodexProbe(t, prober, worktree)

	// Assert
	if !got.IsZero() {
		t.Fatalf("probe = %s, want the removed rollout's binding forgotten", got)
	}
}
