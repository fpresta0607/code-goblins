package spawn

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
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
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
		harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}},
		harness.Kimi:  nativeAdapter{kind: harness.Kimi, control: harness.Control{StopCommand: "/quit"}},
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

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Harness: harness.Kimi})

	if err == nil || !strings.Contains(err.Error(), "kimi cannot run in a native terminal") || !strings.Contains(err.Error(), "left running") {
		t.Fatalf("Switch err = %v, want a refusal naming kimi and that the goblin was left running", err)
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

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed || result.Handoff != "" {
		t.Errorf("result = %+v, want the harness resumed with no handoff", result)
	}
	launches := named(f.events(t), "env")
	if len(launches) != 2 || !strings.HasPrefix(launches[1].Text, "resume --last ") {
		t.Fatalf("launches = %+v, want the second with the harness's resume arguments first", launches)
	}
	if submitted := submittedLines(t, f, 2); !strings.Contains(delivered(t, submitted[len(submitted)-1]), "Your session was restarted") {
		t.Errorf("submitted = %q, want the resumed harness told to continue", submitted)
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
	result, resumeErr := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7"})

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

// A goblin running in Herdr moves into a native terminal in place: its
// harness stops in its Herdr pane, the same task id, worktree and branch get
// a native terminal of their own where the harness resumes its session with
// its resume arguments, the task is recorded as native with no Herdr pane, and
// its Herdr tab closes. On 2026-09-29 Herdr, restored after a reboot, had
// relaunched two goblins' sessions by itself without their environment.
func TestAHerdrGoblinMovesIntoANativeTerminalInPlace(t *testing.T) {
	f := newSwitchFixture(t)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	copyFile(t, program, filepath.Join(bin, "codex.exe"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The premise: the codex this move starts is the fake, never the real one
	// on this machine.
	if found, err := exec.LookPath("codex"); err != nil || !strings.EqualFold(found, filepath.Join(bin, "codex.exe")) {
		t.Fatalf("codex resolves to %q, %v; want the fake", found, err)
	}
	f.service.UserEnvironment = func() ([]string, error) { return os.Environ(), nil }
	record := filepath.Join(t.TempDir(), "codex.jsonl")
	t.Setenv(fakeCodexRecord, record)
	t.Setenv(fakeCodexMode, "")
	f.service.Sleep = nil
	f.service.HostCommand = []string{program, nativeSpawnHost}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/quit", ResumeArgs: []string{"resume", "--last"}}}}}
	meta := f.meta
	meta.Harness, meta.Model, meta.Effort = string(harness.Codex), "default", "default"
	if err := state.WriteTaskMeta(f.stateDir, meta); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if running, err := host.ReadRecord(f.stateDir, meta.ID); err == nil {
			if err := host.Close(f.stateDir, running, nativeCloseWait); err != nil {
				t.Errorf("close the native terminal: %v", err)
			}
		}
	})

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: meta.ID, Native: true, Session: "fleet"})

	if err != nil {
		data, _ := os.ReadFile(record)
		t.Fatalf("Switch: %v\nfake codex recorded:\n%s", err, data)
	}
	moved, err := state.ReadTaskMeta(f.stateDir, meta.ID)
	if err != nil || moved.Backend != "native" || moved.Window != "native" || moved.HerdrPaneID != "" || moved.HerdrTabID != "" || moved.Worktree != meta.Worktree || moved.SpawnGen == meta.SpawnGen {
		t.Errorf("task record = %+v, %v; want it native, out of Herdr, in the same worktree, a new generation", moved, err)
	}
	if !result.Resumed || result.Handoff != "" {
		t.Errorf("result = %+v, want the session resumed in place", result)
	}
	if terminal, err := host.ReadRecord(f.stateDir, meta.ID); err != nil || !host.Running(terminal) {
		t.Errorf("native terminal %s = %+v, %v; want it running", meta.ID, terminal, err)
	}
	fake := &nativeFixture{fixture: f.base, record: record}
	events := fake.events(t)
	if launches := named(events, "env"); len(launches) != 1 || !strings.HasPrefix(launches[0].Text, "resume --last ") {
		t.Errorf("launches = %+v, want codex started once with its resume arguments first", launches)
	}
	if submitted := named(events, "submitted"); len(submitted) != 1 || !strings.Contains(delivered(t, submitted[0].Text), "Your session was restarted") || !strings.Contains(delivered(t, submitted[0].Text), "ask it again with cfo notify --blocked") {
		t.Errorf("submitted = %+v, want the resumed session told to continue, and to ask again a question the restart cancelled, once", submitted)
	}
	closed := slices.ContainsFunc(f.runner.herdrCalls, func(call execx.Request) bool {
		return slices.Contains(call.Args, "tab") && slices.Contains(call.Args, "close") && slices.Contains(call.Args, meta.HerdrTabID)
	})
	if !closed {
		t.Errorf("herdr calls = %v, want the task's old tab %s closed", f.runner.herdrCalls, meta.HerdrTabID)
	}
}
