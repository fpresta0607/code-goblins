package spawn

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A native goblin switches in place, where switch once refused every task
// that did not run in Herdr: its harness exits on its own command, a new one
// starts in a new terminal under the same id with the new model and a
// handoff, and the task's generation moves on.
func TestANativeGoblinSwitchesInPlace(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	after, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if after.Model != "gpt-9" || after.SpawnGen == before.SpawnGen || after.Backend != "native" {
		t.Errorf("after the switch the task is model %q generation %q backend %q; want gpt-9, a new generation and still native", after.Model, after.SpawnGen, after.Backend)
	}
	second, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || second.HostPID == first.HostPID || !host.Running(second) {
		t.Errorf("the terminal after the switch is %+v, %v; want a new running host under the same id", second, err)
	}
	if !ended(first.HostPID) {
		t.Errorf("the first terminal's host pid %d still runs beside its replacement", first.HostPID)
	}
	submitted := submittedLines(t, f, 3)
	if len(submitted) < 3 || submitted[1] != "/exit" || !strings.Contains(delivered(t, submitted[2]), result.Handoff) || result.Handoff == "" {
		t.Errorf("submitted = %q with handoff %q; want the instruction, /exit, then the new harness pointed at the handoff", submitted, result.Handoff)
	}
	if launches := len(named(f.events(t), "env")); launches != 2 {
		t.Errorf("the harness started %d times, want twice", launches)
	}
}

// containedSwitch marks the copy of this test binary that switches a native
// goblin from inside a job that forbids breaking away.
const containedSwitch = "SPAWN_TEST_CONTAINED_SWITCH"

// A switch whose new terminal could not leave the job of the process that
// ran cfo names it in the switch's output, as a spawn does, since that
// terminal ends when the job closes. The switch runs in a copy of this test
// binary, put in such a job before it starts.
func TestAContainedNativeSwitchIsReported(t *testing.T) {
	notice := containedNotice(host.Record{ID: "task-7", Contained: true})
	if os.Getenv(containedSwitch) != "" {
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			t.Fatalf("read: %v", err)
		}
		f := newNativeFixture(t, harness.Codex, "turns")
		f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
		f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}}}}
		if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		closeCurrentTerminal(t, f)
		awaitComposer(t, f)
		result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})
		if err != nil {
			t.Fatalf("Switch: %v", err)
		}
		fmt.Println(result.Output)
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(job) })
	var output bytes.Buffer
	switcher := exec.Command(os.Args[0], "-test.run=^TestAContainedNativeSwitchIsReported$", "-test.count=1")
	switcher.Env = append(os.Environ(), containedSwitch+"=1")
	switcher.Stdout, switcher.Stderr = &output, &output
	input, err := switcher.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := switcher.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		_ = switcher.Wait()
	})
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(switcher.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatal(err)
	}

	_, _ = input.Write([]byte("go\n"))
	_ = input.Close()
	err = switcher.Wait()

	if err != nil || !strings.Contains(output.String(), notice) {
		t.Errorf("the contained switch ended with %v and said:\n%s\nwant its output to carry %q", err, output.String(), notice)
	}
}

// A native goblin whose harness ignores its own exit command has its
// terminal closed, which ends it and everything it started, before the new
// harness starts: two never run side by side.
func TestANativeSwitchClosesAHarnessThatWillNotExit(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/stay"}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !ended(first.HostPID) {
		t.Errorf("the first terminal's host pid %d still runs", first.HostPID)
	}
	if second, err := host.ReadRecord(f.stateDir, "task-7"); err != nil || second.HostPID == first.HostPID {
		t.Errorf("the terminal after the switch is %+v, %v; want a new host", second, err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Contains(submitted, "/stay") {
		t.Errorf("submitted = %q, want the ignored exit command tried first", submitted)
	}
}

// A switch of a native goblin to a harness no native terminal can start is
// refused before anything is stopped or recorded: the goblin keeps its
// terminal, its metadata and its generation.
func TestANativeSwitchToAHarnessWithNoNativeScreensLeavesTheGoblinRunning(t *testing.T) {
	const unscreened = harness.Kind("unscreened")
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
		harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}},
		unscreened:    nativeAdapter{kind: unscreened, control: harness.Control{StopCommand: "/quit"}},
	}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Harness: unscreened})

	if err == nil || !strings.Contains(err.Error(), "unscreened cannot run in a native terminal") || !strings.Contains(err.Error(), "left running") {
		t.Fatalf("Switch err = %v, want a refusal naming the harness and that the goblin was left running", err)
	}
	after, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if after.Harness != before.Harness || after.Model != before.Model || after.Effort != before.Effort || after.SpawnGen != before.SpawnGen {
		t.Errorf("after the refusal the task is %s/%s/%s generation %q; want %s/%s/%s generation %q", after.Harness, after.Model, after.Effort, after.SpawnGen, before.Harness, before.Model, before.Effort, before.SpawnGen)
	}
	current, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || current.HostPID != first.HostPID || !host.Running(current) {
		t.Errorf("the terminal after the refusal is %+v, %v; want the first host %d still running", current, err, first.HostPID)
	}
	if submitted := submittedLines(t, f, 1); slices.Contains(submitted, "/exit") {
		t.Errorf("submitted = %q, want no exit command sent", submitted)
	}
}

// A reboot ends every native terminal. Switching a native goblin whose
// terminal has ended, to what it already ran, starts its harness again under
// the same id with the harness's own resume and the resume instruction.
func TestANativeGoblinWhoseTerminalEndedResumesInPlace(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(f.stateDir, first, nativeCloseWait); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", ResumeSession: "owned-task-7-session"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed || result.Handoff != "" {
		t.Errorf("result = %+v, want the harness resumed with no handoff", result)
	}
	launches := named(f.events(t), "env")
	if len(launches) != 2 || !strings.HasPrefix(launches[1].Text, "resume owned-task-7-session ") {
		t.Fatalf("launches = %+v, want the second with the harness's resume arguments first", launches)
	}
	if submitted := submittedLines(t, f, 2); !strings.Contains(delivered(t, submitted[len(submitted)-1]), "Your session was restarted") {
		t.Errorf("submitted = %q, want the resumed harness told to continue", submitted)
	}
}

func TestResumeCompletesWhenTheOwnedNativeSessionIsAlreadyWorking(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "resumed-working")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	closeCurrentTerminal(t, f)
	if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
	first, err := host.ReadRecord(f.stateDir, f.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(f.stateDir, first, nativeCloseWait); err != nil {
		t.Fatal(err)
	}
	before, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	prior := state.Lifecycle{ID: before.ID, Generation: before.SpawnGen, Operation: "pause-proof", Action: "pause", Phase: "paused", Session: "owned-session", Started: time.Now().UTC()}
	if err := state.WriteLifecycle(f.stateDir, prior); err != nil {
		t.Fatal(err)
	}
	previous := nativeStartup
	nativeStartup = 10 * time.Second
	t.Cleanup(func() { nativeStartup = previous })
	controller := lifecycle.Service{StateDir: f.stateDir, Operations: lifecycle.Operations{
		Memory: func() (uint64, uint64, error) { return 5 << 30, 5 << 30, nil },
		Resume: func(ctx context.Context, meta state.TaskMeta, paused state.Lifecycle) error {
			_, err := f.service.Switch(ctx, SwitchRequest{ID: meta.ID, Generation: meta.SpawnGen, IsResume: true, ResumeSession: paused.Session})
			return err
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}

	// Act
	result, err := controller.Run(t.Context(), lifecycle.Request{ID: before.ID, Generation: before.SpawnGen, Operation: "resume-proof", Action: "resume"})

	// Assert
	if err != nil || result.Phase != "running" || len(result.Problems) != 0 {
		t.Fatalf("resume=%+v, %v; want the already-working owned session running", result, err)
	}
	after, err := state.ReadTaskMeta(f.stateDir, before.ID)
	if err != nil || after.SpawnGen == before.SpawnGen || after.SpawnGen != result.Generation || after.ResumeOperation != result.Operation {
		t.Fatalf("replacement generation does not match successful resume: %+v, %v", after, err)
	}
	launches := named(f.events(t), "env")
	if len(launches) != 2 || !strings.HasPrefix(launches[1].Text, "resume owned-session ") {
		t.Fatalf("launches=%+v, want the exact owned conversation", launches)
	}
	if input := named(f.events(t), "typed into resumed turn"); len(input) != 0 {
		t.Fatalf("startup typed into a turn already in progress: %+v", input)
	}
	status, err := os.ReadFile(filepath.Join(f.stateDir, before.ID+".status"))
	if err != nil || strings.Contains(string(status), "failed:") {
		t.Fatalf("resume published a failure: %s, %v", status, err)
	}
	for _, name := range []string{".spawn.lock", ".lifecycle-" + before.ID + ".lock", state.MetadataLockName(before.ID), switchLockName(before.ID)} {
		if _, err := lock.AcquireExclusiveNamed(f.stateDir, name); err != nil {
			t.Fatalf("resume retained %s: %v", name, err)
		}
		if err := lock.ReleaseExclusiveNamed(f.stateDir, name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTwoEndedNativeCodexTasksDoNotResumeTheForeignLatestSession(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		session string
	}{
		{name: "exact owned session", session: "owned-task-7-session"},
		{name: "no proven owned session"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			owned := newQuickFixture(t)
			owned.service.Commands = cleanWorktree{owned.service.Worktrees.Commands}
			owned.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{ResumeArgs: []string{"resume", "--last"}}}}}
			if _, err := owned.service.Spawn(t.Context(), owned.request); err != nil {
				t.Fatal(err)
			}
			closeCurrentTerminal(t, owned)
			first, err := host.ReadRecord(owned.stateDir, owned.request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := host.Close(owned.stateDir, first, nativeCloseWait); err != nil {
				t.Fatal(err)
			}
			before, err := state.ReadTaskMeta(owned.stateDir, owned.request.ID)
			if err != nil {
				t.Fatal(err)
			}
			foreign := newQuickFixture(t)
			foreign.stateDir, foreign.service.StateDir = owned.stateDir, owned.stateDir
			foreign.request.ID = "task-8"
			closeTerminalAtEnd(t, foreign.stateDir, foreign.request.ID)
			if _, err := foreign.service.Spawn(t.Context(), foreign.request); err != nil {
				t.Fatal(err)
			}
			latest, err := host.ReadRecord(foreign.stateDir, foreign.request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := host.Close(foreign.stateDir, latest, nativeCloseWait); err != nil {
				t.Fatal(err)
			}
			otherBefore, err := state.ReadTaskMeta(foreign.stateDir, foreign.request.ID)
			if err != nil || host.Running(first) || host.Running(latest) {
				t.Fatalf("both native tasks must have ended: %v", err)
			}

			result, err := owned.service.Switch(t.Context(), SwitchRequest{ID: before.ID, Effort: "xhigh", Generation: before.SpawnGen, ResumeSession: testCase.session})

			if err != nil {
				t.Fatal(err)
			}
			launches := named(owned.events(t), "env")
			if len(launches) != 2 || strings.Contains(launches[1].Text, "--last") || strings.Contains(launches[1].Text, "foreign") {
				t.Fatalf("task-8 is the latest ended native task; task-7 launched %+v, want no latest-session selection", launches)
			}
			if testCase.session != "" {
				if !result.Resumed || result.Handoff != "" || !strings.HasPrefix(launches[1].Text, "resume "+testCase.session+" ") {
					t.Fatalf("result=%+v launch=%s, want the exact owned session", result, launches[1].Text)
				}
			} else {
				if result.Resumed || result.Handoff == "" || strings.HasPrefix(launches[1].Text, "resume ") {
					t.Fatalf("result=%+v launch=%s, want a fresh handoff", result, launches[1].Text)
				}
				submitted := submittedLines(t, owned, 2)
				if !strings.Contains(delivered(t, submitted[len(submitted)-1]), result.Handoff) {
					t.Fatalf("replacement did not receive its handoff: %q", submitted)
				}
			}
			otherAfter, err := state.ReadTaskMeta(foreign.stateDir, foreign.request.ID)
			if err != nil || !reflect.DeepEqual(otherAfter, otherBefore) || result.Meta.SpawnGen == before.SpawnGen {
				t.Fatalf("foreign task changed or replacement did not advance its generation: %v", err)
			}
		})
	}
}

// A native goblin whose terminal has ended resumes in place over its own
// uncommitted work with no --force-dirty, while a switch that changes its
// model on the same dirty worktree is still refused.
func TestAnEndedNativeGoblinResumesOverItsUncommittedWork(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = dirtyWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(f.stateDir, first, nativeCloseWait); err != nil {
		t.Fatal(err)
	}

	_, changeErr := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})
	result, resumeErr := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", ResumeSession: "owned-task-7-session"})

	if changeErr == nil || !strings.Contains(changeErr.Error(), "--force-dirty") {
		t.Errorf("model change err = %v, want a refusal naming --force-dirty", changeErr)
	}
	if resumeErr != nil {
		t.Fatalf("resume Switch: %v", resumeErr)
	}
	if !result.Resumed {
		t.Errorf("result = %+v, want the harness resumed", result)
	}
	if launches := named(f.events(t), "env"); len(launches) != 2 {
		t.Errorf("the harness started %d times, want twice: the spawn and the resume only", len(launches))
	}
}

// dirtyWorktree answers a switch's git status as a worktree with an
// uncommitted edit, its other git reads as empty, and hands every other
// command to the fixture's runner.
type dirtyWorktree struct {
	next execx.Runner
}

func (d dirtyWorktree) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name != "git" {
		return d.next.Run(ctx, req)
	}
	if len(req.Args) > 0 && req.Args[0] == "status" {
		return execx.Result{Stdout: []byte(" M main.go\n")}, nil
	}
	return execx.Result{}, nil
}

// cleanWorktree answers a switch's git reads as a worktree with nothing
// uncommitted and no history to hand off, and hands every other command to
// the fixture's runner.
type cleanWorktree struct {
	next execx.Runner
}

func (c cleanWorktree) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "git" {
		return execx.Result{}, nil
	}
	return c.next.Run(ctx, req)
}

// closeCurrentTerminal ends whichever terminal task-7 has when the test ends,
// the one a switch started included, which the fixture does not know about.
func closeCurrentTerminal(t *testing.T, f *nativeFixture) {
	t.Helper()
	t.Cleanup(func() {
		if record, err := host.ReadRecord(f.stateDir, "task-7"); err == nil {
			if err := host.Close(f.stateDir, record, nativeCloseWait); err != nil {
				t.Errorf("close the switched terminal: %v", err)
			}
		}
	})
}
