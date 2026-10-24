package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// killedTask is a native task in a home of the test's own whose terminal has
// ended: a project cloned from a bare origin and the task's worktree of it.
func killedTask(t *testing.T) (home.Home, state.TaskMeta) {
	t.Helper()
	root := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	origin := filepath.Join(root, "origin.git")
	git(root, "init", "-q", "--bare", "--initial-branch=main", origin)
	project := filepath.Join(root, "app")
	git(root, "clone", "-q", origin, project)
	git(project, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	git(project, "push", "-q", "origin", "HEAD:main")
	git(project, "remote", "set-head", "origin", "--auto")
	h := home.Home{Root: filepath.Join(root, "home"), State: filepath.Join(root, "home", "state"), Data: filepath.Join(root, "home", "data")}
	worktree := filepath.Join(h.Worktrees(), "app", "g1")
	for _, dir := range []string{h.State, h.Data, filepath.Dir(worktree)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(project, "worktree", "add", "-q", "--detach", worktree, "origin/main")
	meta := state.TaskMeta{ID: "g1", Window: "native", Worktree: worktree, Project: project, Harness: "claude", Backend: "native", SpawnGen: "s1", TaskTmp: filepath.Join(h.State, "tasktmp", "g1")}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return h, meta
}

// keptMessage is a board message as the supervisor keeps it in a paused
// goblin's resume note.
const keptMessage = "Message from the Overlord, typed on the board: use the staging database"

// pausedWithKeptMessage records meta's goblin paused, with keptMessage kept
// for its resume.
func pausedWithKeptMessage(t *testing.T, h home.Home, meta state.TaskMeta) {
	t.Helper()
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", ResumeNote: keptMessage, NoticeSent: true}); err != nil {
		t.Fatal(err)
	}
}

// toldCFO is what task's notify wakes tell the CFO.
func toldCFO(t *testing.T, h home.Home, task string) []string {
	t.Helper()
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var told []string
	for _, record := range pending {
		if record.Kind == "notify" && record.Key == task {
			told = append(told, record.Detail)
		}
	}
	return told
}

// A cfo cleanup that retires a paused goblin before its resume runs hands each
// board message kept for that resume to the CFO with its words, and leaves
// none in its record. Before, the messages stayed in the paused record, which
// nothing reads once the task is retired (issue #1317).
func TestACleanupOfAPausedGoblinHandsItsKeptMessagesToTheCFO(t *testing.T) {
	// Arrange
	h, meta := killedTask(t)
	pausedWithKeptMessage(t, h, meta)

	// Act
	output, err := defaultCleanup(t.Context(), h, meta.ID, false)

	// Assert
	if err != nil {
		t.Fatalf("the cleanup = %q, %v, want the task retired", output, err)
	}
	want := "message not delivered: g1 was retired by a cleanup while it was paused, so the resume the message was kept for never runs, so it is yours to relay or answer. The Overlord wrote: use the staging database"
	if told := toldCFO(t, h, meta.ID); len(told) != 1 || told[0] != want {
		t.Fatalf("the CFO was told %q, want %q once", told, want)
	}
	if record, err := state.ReadLifecycle(h.State, meta.ID); err != nil || record.ResumeNote != "" {
		t.Fatalf("the paused record after the cleanup = %+v, %v; want no message left in its note", record, err)
	}
}

// cfo kill of a paused goblin stops it before the resume its record keeps a
// board message for, so the stop hands the message to the CFO with its words
// before it writes its own record, and the cleanup inside the stop finds
// nothing more to hand on: the CFO is told once (issue #1317).
func TestAKillOfAPausedGoblinHandsItsKeptMessagesToTheCFOOnce(t *testing.T) {
	// Arrange
	h, meta := killedTask(t)
	pausedWithKeptMessage(t, h, meta)
	service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Archive: func(ctx context.Context, meta state.TaskMeta, _ *state.Lifecycle) (lifecycle.Preservation, error) {
			_, err := defaultCleanup(ctx, h, meta.ID, false)
			return lifecycle.Preservation{}, err
		},
		Notify: func(state.Lifecycle) error { return nil },
		HandOver: func(meta state.TaskMeta, prior state.Lifecycle) (bool, error) {
			return supervisor.HandOverReplacedNote(h.State, meta, prior)
		},
		HandOverEnded: func(meta state.TaskMeta, prior state.Lifecycle, action string) (bool, error) {
			return supervisor.HandOverEndedNote(h.State, meta, prior, action)
		},
	}}

	// Act
	record, err := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "kill-1", Action: "stop", Reason: "Requested by the CFO", IsWatched: true})

	// Assert
	if err != nil || record.Phase != "stopped" {
		t.Fatalf("the kill = phase %q, %v, want the task stopped\nproblems: %s", record.Phase, err, strings.Join(record.Problems, "; "))
	}
	want := "message not delivered: g1 was stopped while it was paused, so the resume the message was kept for never runs, so it is yours to relay or answer. The Overlord wrote: use the staging database"
	if told := toldCFO(t, h, meta.ID); len(told) != 1 || told[0] != want {
		t.Fatalf("the CFO was told %q, want %q once", told, want)
	}
}

// cfo kill stops its goblin and cleans it up in one process: the stop holds
// the task's lifecycle and record locks while its archive step runs the
// cleanup, which holds those same locks against every other command. The
// cleanup takes them as its caller's, and the kill archives the task.
func TestKillStillArchivesThroughItsOwnCleanup(t *testing.T) {
	// Arrange: the stop's own steps, with the production cleanup as its
	// archive step and nothing left running to stop.
	h, meta := killedTask(t)
	var heldDuringArchive []string
	service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Archive: func(ctx context.Context, meta state.TaskMeta, _ *state.Lifecycle) (lifecycle.Preservation, error) {
			for _, name := range []string{state.LifecycleLockName(meta.ID), state.MetadataLockName(meta.ID)} {
				if lock.HeldByNamed(h.State, name, os.Getpid()) {
					heldDuringArchive = append(heldDuringArchive, name)
				}
			}
			_, err := defaultCleanup(ctx, h, meta.ID, false)
			return lifecycle.Preservation{}, err
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}

	// Act
	record, err := service.Run(context.Background(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "kill-1", Action: "stop", Reason: "Requested by the CFO", IsWatched: true})

	// Assert
	if err != nil || record.Phase != "stopped" {
		t.Fatalf("the kill = phase %q, %v, want the task stopped\nproblems: %s", record.Phase, err, strings.Join(record.Problems, "; "))
	}
	if len(heldDuringArchive) != 2 {
		t.Fatalf("the stop held %v while it archived, want the task's lifecycle and record locks, which is what the cleanup must run under", heldDuringArchive)
	}
	if _, err := state.ReadTaskMeta(h.State, meta.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the task record after the kill: %v, want it retired", err)
	}
	if _, err := os.Stat(meta.Worktree); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree after the kill: %v, want it returned", err)
	}
	for _, name := range []string{state.LifecycleLockName(meta.ID), state.MetadataLockName(meta.ID), state.CleanupLockName(meta.ID)} {
		if _, err := lock.ReadNamed(h.State, name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still held after the kill: %v", name, err)
		}
	}
}
