package cleanup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// nativeCleanupFixture is the cleanup fixture's task recorded as a native one:
// no Herdr identity, its terminal found by the host record hostPID names, or
// by none when hostPID is zero.
func nativeCleanupFixture(t *testing.T, hostPID int) *cleanupFixture {
	t.Helper()
	fixture := newCleanupFixture(t)
	fixture.meta = state.TaskMeta{ID: "g1", Window: "native", Worktree: fixture.worktree, Project: fixture.project, Harness: "claude", Backend: "native"}
	if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
		t.Fatal(err)
	}
	if hostPID != 0 {
		record, err := json.Marshal(map[string]any{"id": "g1", "pipe": `\\.\pipe\cfo-host-g1`, "token": "0123", "version": 1, "host_pid": hostPID, "child_pid": hostPID})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(fixture.stateDir, "hosts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.stateDir, "hosts", "g1.json"), record, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

// endedPID is the pid of a process that has run and ended.
func endedPID(t *testing.T) int {
	t.Helper()
	command := exec.Command("cmd", "/c", "exit 0")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	return command.Process.Pid
}

// assertNoHerdrRequests proves a native cleanup never asks Herdr anything: the
// task has no pane to read or tab to close.
func (f *cleanupFixture) assertNoHerdrRequests(t *testing.T) {
	t.Helper()
	for _, request := range f.runner.requests {
		if request.Name != "git" {
			t.Errorf("a native cleanup ran %s %q", request.Name, request.Args)
		}
	}
}

// A native task whose terminal has ended - its host gone with its record, or
// its record left behind by a host that ended - is cleaned like a Herdr one:
// its worktree returned and its record retired, with nothing asked of Herdr.
// cfo cleanup used to refuse every native task as not a Herdr task.
func TestCleanupReturnsANativeTaskWhoseTerminalHasEnded(t *testing.T) {
	for name, hostPID := range map[string]func(*testing.T) int{
		"no host record":          func(*testing.T) int { return 0 },
		"record of an ended host": endedPID,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := nativeCleanupFixture(t, hostPID(t))

			result, err := fixture.service.Cleanup(context.Background(), "g1")

			if err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			if result.Output != "cleaned g1 worktree="+fixture.worktree {
				t.Errorf("Output = %q", result.Output)
			}
			if len(fixture.git.returned) != 1 || fixture.git.returned[0] != [2]string{fixture.project, fixture.worktree} {
				t.Errorf("worktree return calls = %v, want one return of the task's worktree", fixture.git.returned)
			}
			if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); err == nil {
				t.Error("task metadata survives a successful cleanup")
			}
			fixture.assertNoHerdrRequests(t)
		})
	}
}

// A native terminal whose host may still run holds a live harness, and a
// record that cannot be read proves nothing: both are refused, force or not,
// and the task is kept for a retry.
func TestCleanupRefusesANativeTaskThatMayStillRun(t *testing.T) {
	for name, test := range map[string]struct {
		setup func(*testing.T, *cleanupFixture)
		want  string
	}{
		"host still running": {func(*testing.T, *cleanupFixture) {}, "still runs its harness in host pid"},
		"unreadable host record": {func(t *testing.T, f *cleanupFixture) {
			if err := os.WriteFile(filepath.Join(f.stateDir, "hosts", "g1.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "native terminal evidence is unreadable"},
	} {
		for _, force := range []bool{false, true} {
			t.Run(name, func(t *testing.T) {
				fixture := nativeCleanupFixture(t, os.Getpid())
				test.setup(t, fixture)
				fixture.service.ForceArchive = force

				_, err := fixture.service.Cleanup(context.Background(), "g1")

				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("Cleanup (force %v) error = %v, want %q", force, err, test.want)
				}
				fixture.assertMetadataPreserved(t)
				fixture.assertNoHerdrRequests(t)
			})
		}
	}
}

// --force-archive retires a native task whose worktree will not validate the
// way it retires a Herdr one, with no tab to close.
func TestForceArchiveRetiresANativeTaskWhoseTerminalHasEnded(t *testing.T) {
	fixture := nativeCleanupFixture(t, endedPID(t))
	fixture.git.top = fixture.project
	fixture.service.ForceArchive = true

	result, err := fixture.service.Cleanup(context.Background(), "g1")

	if err != nil {
		t.Fatalf("force archive: %v", err)
	}
	if !strings.Contains(result.Output, "force-archived g1") || strings.Contains(result.Output, "tab close") {
		t.Errorf("output = %q", result.Output)
	}
	if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); err == nil {
		t.Error("task metadata still present after force archive")
	}
	if len(fixture.git.returned) != 0 {
		t.Errorf("force archive returned a worktree: %v", fixture.git.returned)
	}
	fixture.assertNoHerdrRequests(t)
}
