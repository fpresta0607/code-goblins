package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fakeClaude is the argument that makes this test binary a stand-in Claude
// Code in a native terminal, showing the screen the next argument names.
const fakeClaude = "cleanup-fake-claude"

// fakeClaudeScreens are Claude Code's screens as a native terminal holds
// them: waiting at its composer, and in a turn.
var fakeClaudeScreens = map[string][]string{
	"idle":    {"> ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
	"working": {"✽ Reticulating… (3s · esc to interrupt)", "", "> ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
}

// testStarted is when this test process started, as a host that recorded
// itself then would have.
var testStarted = time.Now().UTC()

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == fakeClaude {
		fmt.Print("\x1b[2J\x1b[H" + strings.Join(fakeClaudeScreens[os.Args[2]], "\r\n"))
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	os.Exit(m.Run())
}

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
		record, err := json.Marshal(map[string]any{"id": "g1", "pipe": `\\.\pipe\cfo-host-g1`, "token": "0123", "version": 1, "host_pid": hostPID, "child_pid": hostPID, "started": testStarted})
		if err != nil {
			t.Fatal(err)
		}
		writeHostRecord(t, fixture.stateDir, record)
	}
	return fixture
}

// writeHostRecord writes task g1's host record as the host would.
func writeHostRecord(t *testing.T, stateDir string, record []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "g1.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
}

// runFakeClaude runs the fixture task's native terminal, with the stand-in
// Claude showing screen, until the test closes it or the test ends.
func runFakeClaude(t *testing.T, f *cleanupFixture, screen string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(f.stateDir, host.Spec{ID: "g1", Args: []string{program, fakeClaude, screen}, Cols: 100, Rows: 30})
	}()
	t.Cleanup(func() {
		if record, err := host.ReadRecord(f.stateDir, "g1"); err == nil {
			if client, err := host.Dial(record); err == nil {
				_ = client.CloseTerminal()
				_ = client.Close()
			}
		}
		select {
		case <-ended:
		case <-time.After(15 * time.Second):
			t.Error("the native terminal's host did not end")
		}
	})
	want := fakeClaudeScreens[screen][len(fakeClaudeScreens[screen])-1]
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if record, err := host.ReadRecord(f.stateDir, "g1"); err == nil {
			if rows, err := host.ReadScreen(record); err == nil && strings.Contains(host.ScreenTail(rows, 0), want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in Claude never showed its %s screen", screen)
		}
	}
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

// reusedPID is the pid of a process started after the host record was
// written, as Windows can give an ended host's pid to a later process. It runs
// until the test ends, and is stopped by the test.
func reusedPID(t *testing.T) int {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(program, fakeClaude, "idle")
	if _, err := command.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
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
// its record left behind by a host that ended, even once a later process has
// its pid - is cleaned like a Herdr one:
// its worktree returned and its record retired, with nothing asked of Herdr.
// cfo cleanup used to refuse every native task as not a Herdr task.
func TestCleanupReturnsANativeTaskWhoseTerminalHasEnded(t *testing.T) {
	for name, hostPID := range map[string]func(*testing.T) int{
		"no host record":                          func(*testing.T) int { return 0 },
		"record of an ended host":                 endedPID,
		"record whose pid a later process reuses": reusedPID,
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

// A native goblin waiting at its composer holds no turn in progress (CFO
// decision 2339), so cleanup closes its terminal, which ends the harness, and
// retires the task; --force-archive does the same for a worktree that will not
// validate.
func TestCleanupClosesANativeTerminalIdleAtItsComposer(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force archive %v", force), func(t *testing.T) {
			fixture := nativeCleanupFixture(t, 0)
			runFakeClaude(t, fixture, "idle")
			if force {
				fixture.git.top = fixture.project
				fixture.service.ForceArchive = true
			}

			_, err := fixture.service.Cleanup(context.Background(), "g1")

			if err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			if _, err := host.ReadRecord(fixture.stateDir, "g1"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the native terminal still runs after its task was cleaned: %v", err)
			}
			if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); err == nil {
				t.Error("task metadata survives a successful cleanup")
			}
			if returned := len(fixture.git.returned); returned != map[bool]int{false: 1, true: 0}[force] {
				t.Errorf("worktree return calls = %v", fixture.git.returned)
			}
			fixture.assertNoHerdrRequests(t)
		})
	}
}

// The cleanup reap runs never ends a process, so under LeaveRunningTerminals a
// native goblin idle at its composer is refused naming cfo cleanup, force or
// not, and its terminal and task are kept.
func TestCleanupLeavingRunningTerminalsRefusesAnIdleNativeTerminal(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force archive %v", force), func(t *testing.T) {
			fixture := nativeCleanupFixture(t, 0)
			runFakeClaude(t, fixture, "idle")
			fixture.service.ForceArchive = force
			fixture.service.LeaveRunningTerminals = true

			_, err := fixture.service.Cleanup(context.Background(), "g1")

			if err == nil || !strings.Contains(err.Error(), "native task g1 still runs") || !strings.Contains(err.Error(), "cfo cleanup g1") {
				t.Fatalf("Cleanup error = %v, want the running task refused naming cfo cleanup", err)
			}
			record, err := host.ReadRecord(fixture.stateDir, "g1")
			if err != nil {
				t.Fatalf("the idle terminal was closed: %v", err)
			}
			if _, err := host.ReadScreen(record); err != nil {
				t.Errorf("the idle terminal no longer answers: %v", err)
			}
			fixture.assertMetadataPreserved(t)
			fixture.assertNoHerdrRequests(t)
		})
	}
}

// A native goblin in a turn, a terminal whose screen cannot be read, and a
// host record that cannot be read are all refused, force or not, and the task
// and its terminal are kept for a retry.
func TestCleanupRefusesANativeTaskThatMayBeWorking(t *testing.T) {
	for name, test := range map[string]struct {
		hostPID int
		setup   func(*testing.T, *cleanupFixture)
		want    string
	}{
		"a turn in progress": {0, func(t *testing.T, f *cleanupFixture) { runFakeClaude(t, f, "working") }, "does not show its harness waiting at the composer"},
		// A running host whose pipe answers no one: its screen is never read.
		"a screen that cannot be read": {os.Getpid(), func(*testing.T, *cleanupFixture) {}, "its screen cannot be read"},
		"an unreadable host record":    {0, func(t *testing.T, f *cleanupFixture) { writeHostRecord(t, f.stateDir, []byte("{")) }, "native terminal evidence is unreadable"},
	} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, force archive %v", name, force), func(t *testing.T) {
				fixture := nativeCleanupFixture(t, test.hostPID)
				test.setup(t, fixture)
				fixture.service.ForceArchive = force

				_, err := fixture.service.Cleanup(context.Background(), "g1")

				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("Cleanup error = %v, want %q", err, test.want)
				}
				fixture.assertMetadataPreserved(t)
				fixture.assertNoHerdrRequests(t)
				if name == "a turn in progress" {
					if _, err := host.ReadRecord(fixture.stateDir, "g1"); err != nil {
						t.Errorf("the working terminal was closed: %v", err)
					}
				}
			})
		}
	}
}
