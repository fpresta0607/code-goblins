package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// thisProcessCreated is this test process's creation time in Windows' units,
// as Claude Code records its own process's.
func thisProcessCreated(t *testing.T) int64 {
	t.Helper()
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(syscall.Handle(^uintptr(0)), &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	return int64(creation.HighDateTime)<<32 | int64(creation.LowDateTime)
}

// nativeClaudeGoblin sets up a native Claude goblin whose harness is this
// test process: its host record, the session record Claude Code keeps for
// its process, and its conversation, in which it started a sub-agent hours
// ago. A newer conversation in the same folder is a forked CFO session's.
// It returns the goblin and what writes its sub-agent's transcript.
func nativeClaudeGoblin(t *testing.T, home, stateDir string, started time.Time) (state.TaskMeta, func(written time.Time)) {
	t.Helper()
	meta := nativeMeta("g1", "claude")
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
	session, err := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "own-session", "procStart": strconv.FormatInt(thisProcessCreated(t), 10), "cwd": meta.Worktree})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "sessions", strconv.Itoa(os.Getpid())+".json"), session, 0o600); err != nil {
		t.Fatal(err)
	}
	folder := claudeProjectFolder(home, meta.Worktree)
	stamp := started.Format(time.RFC3339Nano)
	transcriptLines(t, filepath.Join(folder, "own-session.jsonl"), started,
		map[string]any{"type": "assistant", "timestamp": stamp, "message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": "toolu_A", "name": "Agent", "input": map[string]any{"description": "Map the monitor", "subagent_type": "Explore"}}}}},
		map[string]any{"type": "user", "timestamp": stamp, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "toolu_A", "content": "launched"}}}, "toolUseResult": map[string]any{"status": "async_launched", "agentId": "a1"}})
	agent := filepath.Join(folder, "own-session", "subagents", "agent-a1.jsonl")
	write := func(written time.Time) {
		transcriptLines(t, agent, written, map[string]any{"type": "assistant", "timestamp": written.Format(time.RFC3339Nano), "message": map[string]any{"role": "assistant", "content": claudeText("still reading")}})
	}
	return meta, write
}

// A native Claude goblin counts as working while its sub-agent works: the
// sub-agent's transcript is its progress, read from the conversation its own
// harness process records, never the newest one in its folder.
func TestANativeClaudeGoblinsSubagentIsItsProgress(t *testing.T) {
	// Arrange
	home, stateDir := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	meta, writeAgent := nativeClaudeGoblin(t, home, stateDir, now.Add(-3*time.Hour))
	writeAgent(now.Add(-time.Minute))
	forked := filepath.Join(claudeProjectFolder(home, meta.Worktree), "forked-cfo.jsonl")
	transcriptLines(t, forked, now, claudeEntry("assistant", meta.Worktree, claudeText("a forked session's work")))
	prober := &HostProgress{StateDir: stateDir, Home: home}

	// Act
	progress, err := prober.InspectProgress(context.Background(), meta, EndpointSample{Harness: "claude"})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !progress.TranscriptAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("TranscriptAt = %v, want the sub-agent's last entry %v, not the forked session's %v", progress.TranscriptAt, now.Add(-time.Minute), now)
	}
}

// Past the busy budget a native Claude goblin whose own turn shows nothing
// new stays quiet while its sub-agent keeps writing, and wakes once nothing
// under it moves.
func TestAWorkingSubagentHoldsTheStaleWake(t *testing.T) {
	// Arrange
	home, stateDir := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	_, writeAgent := nativeClaudeGoblin(t, home, stateDir, now.Add(-time.Hour))
	writeAgent(now)
	probe := NativeProber{StateDir: stateDir, ReadScreen: screenOf("✽ Churning… (2h · esc to interrupt)", "", "⏵⏵ bypass permissions on (shift+tab to cycle)")}
	service := testService(stateDir, probe, &now)
	service.Gate = &fakeGate{sample: GateSample{Active: false}}
	service.Progress = &HostProgress{StateDir: stateDir, Home: home}
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	for range 4 {
		// Act
		now = now.Add(time.Hour)
		writeAgent(now)
		result, err := service.Scan(context.Background())

		// Assert
		if err != nil {
			t.Fatal(err)
		}
		if result.Event != nil {
			t.Fatalf("a goblin whose sub-agent wrote at %s woke the CFO: %+v", now, result.Event)
		}
	}
	now = now.Add(3 * service.BusyTurnMax)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Observations[0].Reason != BusyTurnOverAge {
		t.Fatalf("once the sub-agent stopped = %+v, want busy_turn_over_age", result.Event)
	}
}

// treeGate is a goblin's gate run as the pipeline's reader gives it.
type treeGate struct {
	progress pipeline.Progress
	steps    []pipeline.StepDetail
}

func (g treeGate) Progress(context.Context, string, string) (pipeline.Progress, error) {
	return g.progress, nil
}

func (g treeGate) StepDetails(context.Context, string) ([]pipeline.StepDetail, error) {
	return g.steps, nil
}

// A goblin whose gate is working counts as working while its own turn shows
// nothing new: the gate is its work and the gate step's last activity its
// progress, so a goblin waiting on its gate is not owed an answer.
func TestAWorkingGateIsTheGoblinsProgress(t *testing.T) {
	// Arrange
	home, stateDir := t.TempDir(), t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	meta, _ := nativeClaudeGoblin(t, home, stateDir, now.Add(-3*time.Hour))
	gitDir := filepath.Join(t.TempDir(), "worktrees", "gb-g1")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta.Worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feat/g1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stepActivity := now.Add(-time.Minute)
	gate := treeGate{progress: pipeline.Progress{RunID: "run-1", Status: "running"}, steps: []pipeline.StepDetail{
		{Name: "test", Status: "running", StartedAt: now.Add(-20 * time.Minute).Unix(), LastActivityAt: stepActivity.Unix(), LastActivity: "go test ./internal/fleettree"},
	}}
	prober := &HostProgress{StateDir: stateDir, Home: home, Tree: &fleettree.Reader{Home: home, Gate: gate}}

	// Act
	progress, err := prober.InspectProgress(context.Background(), meta, EndpointSample{Harness: "claude"})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(progress.Jobs, `gate "Gate: test"`) {
		t.Errorf("Jobs = %v, want the working gate named as the goblin's work", progress.Jobs)
	}
	if !progress.TranscriptAt.Equal(stepActivity) {
		t.Errorf("TranscriptAt = %v, want the gate step's last activity %v", progress.TranscriptAt, stepActivity)
	}
}
