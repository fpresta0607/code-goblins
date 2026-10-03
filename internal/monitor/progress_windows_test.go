package monitor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
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
	service.Progress = HostProgress{StateDir: stateDir, Home: home}
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

// The fakes above stand for a process table; this reads the real one, with
// this test process as the harness and a busy stand-in it started as its job.
func TestHarnessJobsReadsTheLiveProcessTable(t *testing.T) {
	fixture := newPollFixture(t)
	command := exec.Command(fixture.standIn("bash.exe"))
	command.Env = append(os.Environ(), "CFO_POLL_STANDIN=busy")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	time.Sleep(300 * time.Millisecond)

	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	jobs, used := harnessJobs(os.Getpid(), processes, 0, proc.StartTime, proc.CPUTime)
	want := "bash.exe (pid " + strconv.Itoa(command.Process.Pid) + ")"
	if !slices.Contains(jobs, want) {
		t.Fatalf("jobs = %v, want %s", jobs, want)
	}
	if used < 100*time.Millisecond {
		t.Errorf("processor time = %s, want the busy child's", used)
	}
}
