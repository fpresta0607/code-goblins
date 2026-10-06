package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// comebackHome is a home after a reboot: a task record for every goblin
// named, each in the harness and backend given, the board's record that
// claude-owned's conversation is its own, and a lifecycle record for each
// phase given, of the task's generation unless it says stale.
func comebackHome(t *testing.T, goblins map[string][2]string, lifecycles map[string]string) home.Home {
	t.Helper()
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, goblin := range goblins {
		meta := state.TaskMeta{ID: id, Harness: goblin[0], Backend: goblin[1], SpawnGen: "s1"}
		if meta.Backend == "herdr" {
			meta.HerdrSession, meta.HerdrWorkspaceID, meta.HerdrTabID, meta.HerdrPaneID = "fleet", "w1", "t1", "p1"
		}
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
	}
	for id, phase := range lifecycles {
		generation := "s1"
		if stale, ok := strings.CutPrefix(phase, "stale "); ok {
			phase, generation = stale, "s0"
		}
		record := state.Lifecycle{ID: id, Generation: generation, Operation: "proof-" + id, Action: map[string]string{"paused": "pause", "stopped": "stop", "resuming": "resume"}[phase], Phase: phase, Started: time.Now().UTC()}
		if err := state.WriteLifecycle(h.State, record); err != nil {
			t.Fatal(err)
		}
	}
	database := supervisor.Database{
		TaskSessions: map[string]string{"claude-owned": "owned"},
		Sessions:     map[string]supervisor.Session{"owned": {NativeID: "owned-conversation", TaskID: "claude-owned", Generation: "s1", Harness: "claude", Role: "goblin"}},
	}
	data, err := json.Marshal(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, ".supervisor.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

// comebackRuntime answers goblins resume's questions about the goblins: the
// terminals named as running, room for every launch unless refused says
// otherwise, and each switch as switched says, recording every request.
func comebackRuntime(running []string, refused error, switched func(spawn.SwitchRequest) (spawn.SwitchResult, error), requests *[]spawn.SwitchRequest) commandRuntime {
	return commandRuntime{
		nativeTerminalRuns: func(_, id string) bool { return slices.Contains(running, id) },
		admitLaunch:        func(home.Home) error { return refused },
		switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
			*requests = append(*requests, request)
			return switched(request)
		},
	}
}

// After a reboot goblins resume brings back, in place, every goblin whose
// terminal ended: on its own conversation where the board's record proves it
// is the task's, and from a handoff where it does not. It leaves a goblin
// that still runs, or was paused or stopped on purpose, as it is, and says
// what needs a hand: a goblin that ran in Herdr, an operation the reboot cut
// short, and a switch that failed.
func TestGoblinsResumeBringsBackTheGoblinsARebootEnded(t *testing.T) {
	// Arrange
	h := comebackHome(t, map[string][2]string{
		"claude-owned": {"claude", "native"},
		"pi-handoff":   {"pi", "native"},
		"still-runs":   {"codex", "native"},
		"paused":       {"codex", "native"},
		"stopped":      {"codex", "native"},
		"old-pause":    {"codex", "native"},
		"cut-short":    {"codex", "native"},
		"in-herdr":     {"claude", "herdr"},
		"fails":        {"codex", "native"},
	}, map[string]string{"paused": "paused", "stopped": "stopped", "old-pause": "stale paused", "cut-short": "resuming"})
	var requests []spawn.SwitchRequest
	runtime := comebackRuntime([]string{"still-runs"}, nil, func(request spawn.SwitchRequest) (spawn.SwitchResult, error) {
		if request.ID == "fails" {
			return spawn.SwitchResult{}, errors.New("switch: validate harness codex: codex is not on PATH")
		}
		return spawn.SwitchResult{Resumed: request.ResumeSession != ""}, nil
	}, &requests)

	// Act
	comebacks, err := bringGoblinsBack(t.Context(), h, runtime)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	said := map[string]string{}
	for _, comeback := range comebacks {
		said[comeback.id] = comeback.said
	}
	want := map[string]string{
		"claude-owned": "back on its conversation",
		"pi-handoff":   "back from a handoff",
		"old-pause":    "back from a handoff",
		"cut-short":    "its resume was cut short",
		"in-herdr":     "ran in Herdr",
		"fails":        "codex is not on PATH",
	}
	if len(said) != len(want) {
		t.Errorf("goblins resume said %q; want something only of %q", said, slices.Sorted(func(yield func(string) bool) {
			for id := range want {
				if !yield(id) {
					return
				}
			}
		}))
	}
	for id, words := range want {
		if !strings.Contains(said[id], words) {
			t.Errorf("of %s goblins resume said %q, want %q", id, said[id], words)
		}
	}
	switched := map[string]spawn.SwitchRequest{}
	for _, request := range requests {
		switched[request.ID] = request
	}
	if len(switched) != 4 || switched["claude-owned"].ResumeSession != "owned-conversation" || switched["pi-handoff"].ResumeSession != "" {
		t.Errorf("switches %+v; want claude-owned on its own conversation, and pi-handoff, old-pause and fails without one", requests)
	}
	for _, request := range requests {
		if request.Generation != "s1" || !request.ForceDirty || request.Harness != "" || request.Model != "" || request.Effort != "" || request.IsResume {
			t.Errorf("switch %+v; want one in place, of the task's own generation, keeping its uncommitted work", request)
		}
	}
}

// After a reboot, goblins resume brings the CFO back and then the goblins,
// and says what came back; plain goblins brings back only the CFO.
func TestGoblinsResumeSaysWhichGoblinsCameBackAndGoblinsBringsNone(t *testing.T) {
	for _, c := range []struct {
		name     string
		args     []string
		switches int
		said     string
	}{
		{"goblins resume", []string{"resume"}, 1, "Goblins    1 of 1 whose terminals ended are back"},
		{"goblins", nil, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			if err := state.WriteTaskMeta(f.home.State, state.TaskMeta{ID: "g1", Harness: "codex", Backend: "native", SpawnGen: "s1"}); err != nil {
				t.Fatal(err)
			}
			var requests []spawn.SwitchRequest
			f.runtime.admitLaunch = func(home.Home) error { return nil }
			f.runtime.switchTask = func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				requests = append(requests, request)
				return spawn.SwitchResult{}, nil
			}

			// Act
			exit, stdout, stderr := f.launch(c.args...)

			// Assert
			if exit != 0 || len(requests) != c.switches {
				t.Fatalf("exit=%d switches=%+v stderr=%q, want %d goblins brought back", exit, requests, stderr, c.switches)
			}
			if c.said != "" && (!strings.Contains(stdout, c.said) || !strings.Contains(stdout, "g1: back from a handoff")) {
				t.Errorf("stdout = %q, want %q and what g1 came back from", stdout, c.said)
			}
		})
	}
}

// A goblin the machine has no room for yet is not launched: it waits, with
// the reason, and so does every goblin after it.
func TestGoblinsResumeLaunchesNoGoblinThereIsNoRoomFor(t *testing.T) {
	// Arrange
	h := comebackHome(t, map[string][2]string{"first": {"codex", "native"}, "second": {"claude", "native"}}, nil)
	var requests []spawn.SwitchRequest
	runtime := comebackRuntime(nil, errors.New("3.1 GB of memory is free, under the 4 GB a goblin starts with"), func(spawn.SwitchRequest) (spawn.SwitchResult, error) {
		return spawn.SwitchResult{}, nil
	}, &requests)

	// Act
	comebacks, err := bringGoblinsBack(t.Context(), h, runtime)

	// Assert
	if err != nil || len(requests) != 0 || len(comebacks) != 2 {
		t.Fatalf("comebacks %+v, err %v, switches %+v; want both waiting and none launched", comebacks, err, requests)
	}
	for _, comeback := range comebacks {
		if comeback.isBack || !strings.Contains(comeback.said, "waits for room: 3.1 GB of memory is free") {
			t.Errorf("of %s goblins resume said %q; want it waiting for room with the reason", comeback.id, comeback.said)
		}
	}
}
