package spawn

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const completedSpawnBrief = "## Task\nDo the work.\n## Acceptance criteria\nThe requested behavior works.\n## Constraints\nChange only the requested behavior.\n## Authentication\ncredentials: none\nmcp: none\n## Delivery\nkind: ship\nmode: no-mistakes\nready: true\n"

func TestSpawnRejectsInvalidIDBeforeFilesystemMutation(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state-does-not-exist")
	service := Service{StateDir: stateDir}

	_, err := service.Spawn(context.Background(), Request{ID: "../escape"})
	if err == nil || !strings.Contains(err.Error(), "task ID") {
		t.Fatalf("Spawn invalid ID error = %v, want task ID refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid ID created state directory: stat error = %v", statErr)
	}
}

func TestSpawnRejectsTrailingDotIDBeforeFilesystemMutation(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state-does-not-exist")
	service := Service{StateDir: stateDir}

	_, err := service.Spawn(context.Background(), Request{ID: "task."})
	if err == nil || !strings.Contains(err.Error(), "must not end with '.'") {
		t.Fatalf("Spawn trailing-dot ID error = %v, want task ID refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("trailing-dot ID created state directory: stat error = %v", statErr)
	}
}

func TestSpawnRefusesMissingBriefAndDeliveryMismatchBeforeLock(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := makeDir(t, filepath.Join(root, "project"))
	brief := filepath.Join(root, "brief.md")
	service := Service{StateDir: stateDir, Project: project}

	_, err := service.Spawn(context.Background(), Request{
		ID:        "missing-brief",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Claude,
	})
	if err == nil || !strings.Contains(err.Error(), "brief") {
		t.Fatalf("Spawn missing brief error = %v, want brief refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing brief created state directory: stat error = %v", statErr)
	}

	writeFile(t, brief, completedSpawnBrief+"Delivery contract: mode=direct-PR\n")
	_, err = service.Spawn(context.Background(), Request{
		ID:        "mode-mismatch",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Claude,
	})
	if err == nil || !strings.Contains(err.Error(), "delivery mismatch") {
		t.Fatalf("Spawn delivery mismatch error = %v, want delivery mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, ".spawn-mode-mismatch.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("delivery mismatch acquired a spawn lock: stat error = %v", statErr)
	}
}

func TestSpawnRefusesUnsupportedHarnessBeforeFilesystemMutation(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := makeDir(t, filepath.Join(root, "project"))
	brief := filepath.Join(root, "brief.md")
	writeFile(t, brief, completedSpawnBrief+"Delivery contract: mode=no-mistakes\n")
	service := Service{StateDir: stateDir, Project: project}

	_, err := service.Spawn(context.Background(), Request{
		ID:        "unsupported",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Kind("grok"),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported harness") {
		t.Fatalf("Spawn unsupported harness error = %v, want harness refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsupported harness created state directory: stat error = %v", statErr)
	}
}

// A harness whose screens no native terminal knows yet is refused before
// anything is created, since a spawn never types into a screen it cannot
// read.
func TestSpawnRefusesAHarnessWithNoNativeScreensBeforeFilesystemMutation(t *testing.T) {
	f := newFixture(t)
	const unscreened = harness.Kind("unscreened")
	f.service.Harness.Adapters[unscreened] = fixtureAdapter{events: &f.events}
	f.request.Harness = unscreened
	stateDir := filepath.Join(t.TempDir(), "state")
	f.service.StateDir = stateDir

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "unscreened cannot run in a native terminal yet") {
		t.Fatalf("Spawn error = %v, want the harness refused", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) || len(f.events) != 0 {
		t.Fatalf("the refusal created state (%v) or ran %v", statErr, f.events)
	}
}

func TestSpawnRefusesEmptyDeliveryModeLine(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := makeDir(t, filepath.Join(root, "project"))
	brief := filepath.Join(root, "brief.md")
	writeFile(t, brief, completedSpawnBrief+"Delivery contract: mode=\n")
	service := Service{StateDir: stateDir, Project: project}

	_, err := service.Spawn(context.Background(), Request{
		ID:        "empty-contract",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Claude,
	})
	if err == nil || !strings.Contains(err.Error(), "delivery contract") {
		t.Fatalf("Spawn empty delivery contract error = %v, want malformed contract refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("empty delivery contract created state directory: stat error = %v", statErr)
	}
}

func TestSpawnRefusesUnvalidatableWorktreeWithoutLaunching(t *testing.T) {
	f := newFixture(t)
	f.git.topErr = errors.New("worktree: not a git worktree")
	primaryMarker := filepath.Join(f.project, "primary-marker.txt")
	writeFile(t, primaryMarker, "unchanged")

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "not a git worktree") {
		t.Fatalf("Spawn unvalidatable worktree error = %v, want a validation refusal", err)
	}
	if slices.Contains(f.events, "build-harness") || hasTerminal(f.stateDir, f.request.ID) {
		t.Fatalf("unvalidatable worktree launched a harness: events=%v", f.events)
	}
	if got, readErr := os.ReadFile(primaryMarker); readErr != nil || string(got) != "unchanged" {
		t.Fatalf("primary project changed after the refusal: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(f.worktree); statErr != nil {
		t.Fatalf("refusal removed acquired worktree: %v", statErr)
	}
	if f.git.returned != 1 {
		t.Fatalf("refusal returned the lease %d times, want 1", f.git.returned)
	}
}

func TestSpawnNormalizesLaunchFailureStatusToOneFailedEvent(t *testing.T) {
	f := newFixture(t)
	f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, buildErr: errors.New("preview unavailable\r\ndone: forged")}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "preview unavailable") {
		t.Fatalf("Spawn launch error = %v, want the build refusal propagated", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(f.stateDir, f.request.ID+".status"))
	if readErr != nil {
		t.Fatalf("ReadFile status: %v", readErr)
	}
	status := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"), "\n")
	if len(status) != 1 {
		t.Fatalf("raw status = %q; status events = %q, want exactly one", string(raw), status)
	}
	_, event := state.SplitStatus(status[0])
	verb, _, found := strings.Cut(event, ":")
	if !found || verb != "failed" {
		t.Fatalf("status event = %q, want one failed event", status[0])
	}
	if strings.ContainsAny(event, "\r\n") || strings.HasPrefix(event, "done:") {
		t.Fatalf("status event permits forged status line: %q", status[0])
	}
}

func TestSpawnRejectsMetadataBearingControlsBeforeAnyMutation(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Request)
	}{
		{"project", func(req *Request) { req.Project = "C:\\project\nbad" }},
		{"brief", func(req *Request) { req.BriefPath = "C:\\brief\nbad" }},
		{"kind", func(req *Request) { req.Kind = "ship\nbad" }},
		{"mode", func(req *Request) { req.Mode = "no-mistakes\nbad" }},
		{"harness", func(req *Request) { req.Harness = harness.Kind("claude\nbad") }},
		{"model", func(req *Request) { req.Model = "x\nwindow=other" }},
		{"effort", func(req *Request) { req.Effort = "high\rmalformed" }},
		{"title", func(req *Request) { req.Title = "a title\nbackend=other" }},
		{"implement lane", func(req *Request) { req.Implement.Name = "light\nbackend=other" }},
		{"implement model", func(req *Request) { req.Implement.Model = "light-model\nwindow=other" }},
		{"implement effort", func(req *Request) { req.Implement.Effort = "high\rmalformed" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			test.set(&f.request)

			_, err := f.service.Spawn(context.Background(), f.request)
			if err == nil || !strings.Contains(err.Error(), "control character") {
				t.Fatalf("Spawn control injection error = %v, want control character refusal", err)
			}
			if f.runner.calls != 0 || len(f.events) != 0 || len(f.specs) != 0 {
				t.Fatalf("control injection ran work: calls=%d events=%v launches=%v", f.runner.calls, f.events, f.specs)
			}
			if _, statErr := os.Stat(filepath.Join(f.stateDir, f.request.ID+".meta")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("Spawn wrote injected metadata: stat error = %v", statErr)
			}
		})
	}
}

func TestSpawnPostAcquisitionFailuresReturnPartialResultAndStatus(t *testing.T) {
	tests := []struct {
		name string
		set  func(*fixture)
		want string
	}{
		{
			name: "validate",
			set:  func(f *fixture) { f.git.topErr = errors.New("worktree: not a git worktree") },
			want: "not a git worktree",
		},
		{
			name: "build",
			set: func(f *fixture) {
				f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, buildErr: errors.New("harness build refused")}
			},
			want: "harness build refused",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			test.set(f)

			result, err := f.service.Spawn(context.Background(), f.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Spawn error = %v, want %q", err, test.want)
			}
			if result.Meta.Worktree != f.worktree || result.Meta.Project != f.project || result.Meta.Backend != "native" {
				t.Fatalf("partial result = %+v, want the native task's worktree and project", result.Meta)
			}
			status, statusErr := state.TailStatus(f.stateDir, f.request.ID, 2)
			if statusErr != nil || len(status) != 1 {
				t.Fatalf("status = %v, %v; want one failed event containing %q", status, statusErr, test.want)
			}
			if _, event := state.SplitStatus(status[0]); !strings.HasPrefix(event, "failed: ") || !strings.Contains(event, test.want) {
				t.Fatalf("status = %v, %v; want one failed event containing %q", status, statusErr, test.want)
			}
			if _, metaErr := state.ReadTaskMeta(f.stateDir, f.request.ID); !errors.Is(metaErr, os.ErrNotExist) {
				t.Fatalf("post-acquisition failure wrote success metadata: %v", metaErr)
			}
			if hasTerminal(f.stateDir, f.request.ID) {
				t.Fatal("post-acquisition failure started a native terminal")
			}
			if f.git.returned != 1 {
				t.Fatalf("post-acquisition failure returned the lease %d times, want 1", f.git.returned)
			}
		})
	}
}

func TestSpawnPostAcquireFailureSurfacesReturnError(t *testing.T) {
	f := newFixture(t)
	f.git.topErr = errors.New("worktree: not a git worktree")
	f.git.returnErr = errors.New("worktree: return refused")

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "not a git worktree") || !strings.Contains(err.Error(), "return refused") {
		t.Fatalf("Spawn error = %v, want joined launch and return failures", err)
	}
	if f.git.returned != 1 {
		t.Fatalf("post-acquire failure returned the lease %d times, want 1", f.git.returned)
	}
}

func TestSpawnRejectsCaseInsensitiveMetadataExtensionBeforeTerminalOrWorktreeMutation(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.stateDir, "Foo.META"), "window=native\n")

	request := f.request
	request.ID = "foo"
	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("case-insensitive extension Spawn error = %v, want collision refusal", err)
	}
	if f.runner.calls != 0 || len(mutations(f.events)) != 0 {
		t.Errorf("commands = %d and events = %v, want none before the metadata-alias refusal", f.runner.calls, f.events)
	}
}

// A spawn that fails after the Go temporary directory exists must not orphan
// it. teardownLaunch removes <id>.meta, and cleanup reads that file to find a
// task at all, so a directory left behind here can never be removed by
// anything afterwards - and it sits under the user cache directory, out of
// sight of the state tree that would otherwise show it.
func TestSpawnFailureLeavesNoGoTemporaryDirectory(t *testing.T) {
	f := newFixture(t)
	f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, buildErr: errors.New("harness build refused")}

	if _, err := f.service.Spawn(context.Background(), f.request); err == nil {
		t.Fatal("Spawn succeeded, want the injected build failure")
	}
	goTmp := goTmpDir(t, f.stateDir, f.request.ID)
	if _, err := os.Stat(goTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("go temporary directory %q survived a failed spawn: %v", goTmp, err)
	}
	// The metadata is gone, which is what makes the leak permanent: prove the
	// removal happened before it rather than depending on a later cleanup.
	if _, err := os.Stat(filepath.Join(f.stateDir, f.request.ID+".meta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed spawn kept metadata: %v", err)
	}
}

// A task temporary directory left behind by something other than a clean
// teardown - a killed process, a crashed host - still claims its id. On a
// case-insensitive filesystem "Foo" and "foo" are the same directory, so
// letting a new task take the alias would hand it a live task's credential
// script directory. The guard has to fire before any terminal or worktree
// mutation, or the collision is discovered after the damage.
//
// A failed spawn no longer feeds this guard: it removes its own tasktmp, and
// a .status without live metadata is deliberately not a claim on the id (that
// is what forced a cleaned-up task to be respawned under an invented suffix).
// The leftover directory below is therefore created directly, which is the
// only way this state still arises.
func TestSpawnRejectsCaseAliasOfARetainedTaskTemporaryDirectory(t *testing.T) {
	f := newFixture(t)
	leftover := filepath.Join(f.stateDir, "tasktmp", "Foo")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	// Premise: the leftover really is present and really does alias the
	// requested id, so a refusal means the guard fired rather than the spawn
	// failing for some unrelated reason.
	if info, err := os.Stat(leftover); err != nil || !info.IsDir() {
		t.Fatalf("leftover tasktmp: info=%v err=%v, want a directory to collide with", info, err)
	}

	request := f.request
	request.ID = "foo"
	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("alias Spawn error = %v, want collision refusal", err)
	}
	if f.runner.calls != 0 || len(mutations(f.events)) != 0 {
		t.Errorf("commands = %d and events = %v after alias rejection, want none before a second task mutation", f.runner.calls, f.events)
	}
}

// A live task still claims its id through its metadata, which is the durable
// claim a failed spawn's removed tasktmp no longer has to stand in for.
func TestSpawnRejectsCaseAliasOfALiveTask(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.stateDir, "Foo.meta"), "id: Foo\n")

	request := f.request
	request.ID = "foo"
	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("alias Spawn error = %v, want collision refusal", err)
	}
	if f.runner.calls != 0 || len(mutations(f.events)) != 0 {
		t.Errorf("commands = %d and events = %v after alias rejection, want none before a second task mutation", f.runner.calls, f.events)
	}
}

// taskTmpProbe refuses to build the launch, after noting whether the task's
// temporary directory existed by then.
type taskTmpProbe struct {
	fixtureAdapter
	existed *bool
}

func (a taskTmpProbe) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	info, err := os.Stat(spec.TaskTmp)
	*a.existed = err == nil && info.IsDir()
	return harness.Launch{}, errors.New("harness build refused")
}

// A failed launch must leave no task temporary directory. cleanup finds a task
// through the task metadata, so once that is retired nothing can remove this
// directory again - and while it survives it refuses the retry of the very
// spawn that just failed. It also holds the rendered credential script.
func TestSpawnFailureLeavesNoTaskTemporaryDirectory(t *testing.T) {
	f := newFixture(t)
	existed := false
	f.service.Harness.Adapters[harness.Claude] = taskTmpProbe{fixtureAdapter: fixtureAdapter{events: &f.events}, existed: &existed}
	taskTmp := filepath.Join(f.stateDir, "tasktmp", f.request.ID)

	if _, err := f.service.Spawn(context.Background(), f.request); err == nil {
		t.Fatal("Spawn succeeded, want the build failure")
	}
	// Premise: the directory really was created, so its absence is a removal
	// rather than a launch that never got far enough to make one.
	if !existed {
		t.Fatal("tasktmp was never created, so its absence proves nothing")
	}
	if _, err := os.Stat(taskTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("task temporary directory %q survived a failed spawn: %v", taskTmp, err)
	}
	if _, err := os.Stat(filepath.Join(f.stateDir, f.request.ID+".meta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed spawn kept metadata: %v", err)
	}
}

// One heavy run goes at a time on the machine (the Overlord, 2026-10-10: "we
// don't want to just increase time out"), so every goblin's first
// instruction, a helper's too, and the one a goblin taking over a task gets,
// says how a heavy run stands in the line, what the wait looks like, and
// which runs still need the CEO's word because no turn covers them.
func TestAGoblinIsToldToPutAHeavyRunBehindTheLine(t *testing.T) {
	// Arrange
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(t.TempDir(), "brief.md")

	for name, instruction := range map[string]string{
		"a new goblin":         spawnInstruction(brief, state.TaskMeta{ID: "task-7", Kind: "ship"}, nil, ""),
		"a scout":              spawnInstruction(brief, state.TaskMeta{ID: "task-7", Kind: "scout"}, nil, ""),
		"a helper":             spawnInstruction(brief, state.TaskMeta{ID: "task-7-h1", Kind: "ship", Parent: "task-7"}, nil, ""),
		"a goblin taking over": handoffInstruction(filepath.Join(t.TempDir(), "handoff.md"), brief, state.TaskMeta{ID: "task-7", Kind: "ship"}),
	} {
		t.Run(name, func(t *testing.T) {
			// Assert
			for _, want := range []string{
				"One heavy run goes at a time on this machine.",
				"a whole test suite, a full build, a browser suite or any other heavy local run as " + exe + " gate turn -- <command>",
				"says on standard error which run it waits for",
				"125 means it took no turn and ran nothing",
				"start it in the background as " + exe + " gate turn --detach <log-file> -- <command>",
				"never with a process start of your own, such as Win32_Process Create, Start-Process or start, which opens a window on the Overlord's desktop",
				"A test you run by name needs no turn.",
				"A WSL or Docker run, and a load or stress run, still need the CEO's word first, since no turn covers them.",
				"Before you wait on or retry the gate daemon, the heavy-run line or the Docker engine, read their state with " + exe + " shared, the same under every harness",
				"says busy, never stopped, for a live daemon that missed a reply",
				"report it with --waiting-on gate-daemon, memory or docker and do not poll: you are told once when it is usable.",
				"Never start, stop or restart a shared service.",
			} {
				if !strings.Contains(instruction, want) {
					t.Errorf("instruction = %q, want %q", instruction, want)
				}
			}
		})
	}
}

// The Overlord on 2026-10-11: "make sure if your testign or goblins open up
// terminals or windows to check or run tests tataht it finsihes and celans
// up and montiros whats startgin so nothing gets fogtent or runs in loops".
// Every goblin's first instruction, a helper's too, and the one a goblin
// taking over a task gets, says what it starts is its own to end.
func TestAGoblinIsToldWhatItStartsIsItsOwnToEnd(t *testing.T) {
	// Arrange
	brief := filepath.Join(t.TempDir(), "brief.md")

	for name, instruction := range map[string]string{
		"a new goblin":         spawnInstruction(brief, state.TaskMeta{ID: "task-7", Kind: "ship"}, nil, ""),
		"a scout":              spawnInstruction(brief, state.TaskMeta{ID: "task-7", Kind: "scout"}, nil, ""),
		"a helper":             spawnInstruction(brief, state.TaskMeta{ID: "task-7-h1", Kind: "ship", Parent: "task-7"}, nil, ""),
		"a goblin taking over": handoffInstruction(filepath.Join(t.TempDir(), "handoff.md"), brief, state.TaskMeta{ID: "task-7", Kind: "ship"}),
	} {
		t.Run(name, func(t *testing.T) {
			// Assert
			for _, want := range []string{
				"Every process you start is yours to end.",
				"In tests run browsers headless and programs with their windows hidden, unless a picture needs a window.",
				"end it by that pid, never by its name, once you no longer need it and before you report done.",
				"Give every retry and every polling loop a cap.",
				"The supervisor tells the CEO of a window you show on the Overlord's screen, a command you start again and again, and anything you leave running.",
			} {
				if !strings.Contains(instruction, want) {
					t.Errorf("instruction = %q, want %q", instruction, want)
				}
			}
		})
	}
}

func TestNotifyInstructionTeachesWorkingAndWaitingReports(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	instruction := notifyInstruction(state.TaskMeta{ID: "task-7"})

	for _, want := range []string{
		exe + " notify task-7 --done --closed <issue-url> --decision <sequence>",
		exe + " notify task-7 --working \"<what>\"",
		exe + " notify task-7 --waiting-on <task-id|run-id|overlord|ci|deploy|memory|gate-daemon|docker> \"<why>\"",
		exe + " notify task-7 --waiting-on overlord \"<why>\" --lavish <html-file>",
		exe + " notify task-7 --waiting-on overlord \"<why>\" --run <command.ps1>",
		"runs it with one click in a terminal on his card, where he types into it",
		"never run lavish-axi poll yourself",
		"lead with one short sentence that is the actual question",
		"lines of their own that start with \"- \"",
		"mark with **two asterisks** only the verdict or the blocking item",
		"those lines say in plain words what you recommend and why and what each choice does, and run IDs, finding codes, paths and other evidence go after a line that reads only Details:, which the board folds away",
		"the Overlord personally (his sign-in, his click, his page)",
		"a wait on the Overlord becomes a card in his Command Center only with what he acts on, --lavish <html-file>, --run <command.ps1> or --link <https-url>, and one with none reaches the CEO alone",
		"a choice the CEO can make, such as whether to start something now or later, is a question, not a wait on the Overlord: ask it with --blocked and options",
		"the CEO answers it, and his answer arrives here as a message",
		"Never wait on the CEO for a choice you can undo: take the better option, say which with --working, and keep going; a choice you cannot undo or make yourself is a question for --blocked, never one asked in your reply.",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q", instruction, want)
		}
	}
	if strings.Contains(instruction, "the board shows them to the Supreme Overlord") {
		t.Error("goblin questions must name the CEO as their recipient")
	}
}

// The Overlord, 2026-10-14: a browser step only he can take reaches him as one
// Command Center card with the link, only his inputs and Done, and the goblin
// picks up the moment it closes. So every goblin's instruction says to send
// that card and wait on it, never a chat message or a Scrawl walkthrough,
// and never to take such a step itself.
func TestNotifyInstructionSendsOneCardForABrowserStepOnlyHeCanTake(t *testing.T) {
	// Act
	instruction := notifyInstruction(state.TaskMeta{ID: "task-7"})

	// Assert
	for _, want := range []string{
		"browser or computer-use work reaches a step only the Overlord can take",
		"never take it yourself",
		"auth request --project <project> --task task-7",
		"--link <the exact https page>",
		"--plain NAME --label NAME=",
		"--wait",
		"in the background",
		"--window",
		"auth request --done <card> --task task-7",
		"--withdraw <card>",
		"Exit 3 means he skipped it",
		"exit 4 means he answered in his own words",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q", instruction, want)
		}
	}
}

// The Overlord named the review page Scrawl (2026-10-01), so a goblin calls
// it that; the lavish-axi command and the --lavish flag keep their names.
func TestNotifyInstructionCallsTheReviewPageScrawl(t *testing.T) {
	// Act
	instruction := notifyInstruction(state.TaskMeta{ID: "task-7"})

	// Assert
	for _, want := range []string{
		"must answer on a Scrawl page",
		"call it Scrawl when you name it to him",
		"lavish-axi <html-file> --no-open",
		"--lavish <html-file>",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q", instruction, want)
		}
	}
	if strings.Contains(instruction, "Lavish") {
		t.Errorf("instruction = %q, want the page named Scrawl, never Lavish", instruction)
	}
}

// The Overlord, 2026-10-02: "in Scrawl the actual radio choice selection at
// the bottom doesn't exist anymore". A Scrawl page draws its choices only
// when it declares them, so the brief says how, and that his pick comes back
// as the option's exact text.
func TestNotifyInstructionTellsAPageThatAsksHimToPickToDeclareItsChoices(t *testing.T) {
	// Act
	instruction := notifyInstruction(state.TaskMeta{ID: "task-7"})

	// Assert
	for _, want := range []string{
		`<script type="application/json" data-lavish-choices>`,
		"radio list",
		"the option's exact text",
		"a page without it shows him no choices",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q", instruction, want)
		}
	}
}

// Every Claude goblin runs Opus 5.5 unless a model is named (the Supreme
// Overlord's directive of 2026-09-23), whether --harness claude came alone or
// a lane named claude without a model. A named model still wins, and another
// harness keeps its own default. The model the task records is the one its
// launch is built with; the build is refused here so nothing starts.
func TestSpawnRunsAClaudeGoblinWithNoNamedModelOnOpus55(t *testing.T) {
	for _, c := range []struct {
		name  string
		kind  harness.Kind
		model string
		want  string
	}{
		{"claude with no model", harness.Claude, "", "claude-opus-5-5"},
		{"claude with a named model", harness.Claude, "claude-sonnet-5", "claude-sonnet-5"},
		{"another harness with no model", harness.Codex, "", "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.service.Harness.Adapters[c.kind] = fixtureAdapter{events: &f.events, specs: &f.specs, buildErr: errors.New("nothing starts in this test")}
			f.request.Harness, f.request.Model = c.kind, c.model

			result, err := f.service.Spawn(context.Background(), f.request)

			if err == nil || !strings.Contains(err.Error(), "nothing starts in this test") {
				t.Fatalf("Spawn = %v, want the build refusal", err)
			}
			launched := f.specs[len(f.specs)-1].Model
			if result.Meta.Model != c.want || valueOrDefault(launched) != c.want {
				t.Fatalf("recorded model %q, launched %q; want %q", result.Meta.Model, launched, c.want)
			}
		})
	}
}

type fixture struct {
	service  Service
	request  Request
	stateDir string
	dataDir  string
	project  string
	worktree string
	brief    string
	events   []string
	specs    []harness.LaunchSpec
	runner   *commandRunner
	git      *worktreeGit
}

// goTmpDir returns the Go temporary directory an older build gave a task,
// under the isolated user cache directory, which a task recorded with no
// scratch folder still uses.
func goTmpDir(t *testing.T, stateDir, id string) string {
	t.Helper()
	dir, err := state.GoTmpDir(stateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// isolateUserCacheDir points os.UserCacheDir at a directory of the test's own,
// by setting the variables it reads. Without the isolation a spawn test writes
// into the operator's own cache directory and leaves the per-task Go temporary
// directory behind, which is the same class of leak as a test resolving the
// live fleet home.
// HOME is in the set because os.UserCacheDir reads it on darwin and on Linux
// whenever XDG_CACHE_HOME is unset; without it the isolation is vacuous there.
// The resolve afterwards is the premise assertion: an isolation helper that
// silently stops isolating on a platform nobody runs it on is how a test comes
// to write into the operator's own cache.
func isolateUserCacheDir(t *testing.T) {
	t.Helper()
	cache := t.TempDir()
	for _, name := range []string{"LOCALAPPDATA", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(name, cache)
	}
	resolved, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir = %v, want the isolated cache directory", err)
	}
	if rel, relErr := filepath.Rel(cache, resolved); relErr != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("UserCacheDir = %q, want it under the test's own directory %q", resolved, cache)
	}
}

// newFixture readies a spawn of task-7 that stops before any terminal starts:
// its claude adapter's screens are never drawn by anything here, so a test
// that needs the harness running uses newNativeFixture instead.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	isolateUserCacheDir(t)
	// A Codex spawn or switch reads the MCP servers of CODEX_HOME's
	// config.toml, which is never this machine's own.
	t.Setenv("CODEX_HOME", t.TempDir())
	// Canonical, as every folder made under it is, so the scratch and
	// worktrees folders are spelled as the state folder is, short names in a
	// runner's temp path included.
	root := makeDir(t, t.TempDir())
	stateDir := makeDir(t, filepath.Join(root, "state"))
	dataDir := makeDir(t, filepath.Join(root, "data"))
	project := makeDir(t, filepath.Join(root, "primary"))
	worktreeDir := makeDir(t, filepath.Join(root, "worktree"))
	brief := filepath.Join(root, "brief.md")
	writeFile(t, brief, completedSpawnBrief)
	writeFile(t, filepath.Join(project, "primary-marker.txt"), "unchanged")

	fixture := &fixture{stateDir: stateDir, dataDir: dataDir, project: project, worktree: worktreeDir, brief: brief}
	fixture.runner = &commandRunner{events: &fixture.events}
	fixture.git = &worktreeGit{events: &fixture.events, top: worktreeDir}
	fixture.service = Service{
		Worktrees: worktree.Service{
			Commands: fixture.runner,
			Git:      fixture.git,
			Root:     filepath.Join(root, "worktrees"),
			DataDir:  dataDir,
		},
		ScratchRoot: filepath.Join(root, "scratch"),
		Harness: harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
			harness.Claude: fixtureAdapter{events: &fixture.events, specs: &fixture.specs},
		}},
		HomeRoot:        root,
		StateDir:        stateDir,
		Project:         project,
		UserEnvironment: func() ([]string, error) { return os.Environ(), nil },
		Sleep: func(context.Context, time.Duration) error {
			fixture.events = append(fixture.events, "settle")
			return nil
		},
	}
	fixture.request = Request{
		ID:        "task-7",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Yolo:      true,
		Harness:   harness.Claude,
		Model:     "model-a",
		Effort:    "high",
	}
	return fixture
}

// mutations is events without the harness check, which a spawn runs before
// its turn because it only reads.
func mutations(events []string) []string {
	return slices.DeleteFunc(slices.Clone(events), func(event string) bool { return event == "validate-harness" })
}

// hasTerminal reports whether task id has a native terminal record.
func hasTerminal(stateDir, id string) bool {
	_, err := host.ReadRecord(stateDir, id)
	return !errors.Is(err, fs.ErrNotExist)
}

type fixtureAdapter struct {
	events   *[]string
	specs    *[]harness.LaunchSpec
	buildErr error
	// control replaces the fixture's stop and resume control, for a test
	// that needs a real harness's.
	control *harness.Control
}

func (a fixtureAdapter) Control() harness.Control {
	if a.control != nil {
		return *a.control
	}
	return harness.Control{
		StopKeys:    []string{"escape"},
		StopCommand: "/exit",
		ResumeArgs:  []string{"--continue"},
	}
}

func (a fixtureAdapter) Kind() harness.Kind {
	return harness.Claude
}

func (a fixtureAdapter) Validate(context.Context, execx.Runner) error {
	*a.events = append(*a.events, "validate-harness")
	return nil
}

func (a fixtureAdapter) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	*a.events = append(*a.events, "build-harness")
	if a.specs != nil {
		*a.specs = append(*a.specs, spec)
	}
	if a.buildErr != nil {
		return harness.Launch{}, a.buildErr
	}
	return harness.Launch{
		Args:       []string{"--dangerously-skip-permissions"},
		Env:        map[string]string{"GOTMPDIR": spec.Scratch, "TEMP": spec.Scratch, "TMPDIR": spec.Scratch, "TMP": home.SharedTempBeside(spec.Scratch)},
		PromptFile: spec.BriefPath,
	}, nil
}

type worktreeGit struct {
	events     *[]string
	acquireErr error
	acquired   string
	top        string
	topErr     error
	returnErr  error
	returned   int
}

func (g *worktreeGit) Acquire(_ context.Context, project, path, ref string) (string, error) {
	*g.events = append(*g.events, "worktree-acquire")
	if g.acquireErr != nil {
		return "", g.acquireErr
	}
	g.acquired = path
	if !filepath.IsAbs(path) || ref != "" {
		return "", fmt.Errorf("unexpected worktree %q on %q", path, ref)
	}
	if project == "" {
		return "", fmt.Errorf("project is required")
	}
	return g.top, nil
}

func (g *worktreeGit) Landing(context.Context, string) (worktree.Landing, error) {
	return worktree.Landing{Landed: true}, nil
}

func (g *worktreeGit) ArchiveTag(context.Context, string, worktree.Landing, string) (string, error) {
	return "", fmt.Errorf("spawn never archives")
}

func (g *worktreeGit) WorktreeTop(context.Context, string) (string, error) {
	*g.events = append(*g.events, "validate-worktree")
	if g.topErr != nil {
		return "", g.topErr
	}
	return g.top, nil
}

func (g *worktreeGit) Return(context.Context, string, string) error {
	g.returned++
	return g.returnErr
}

func (g *worktreeGit) EnsureSeeded(context.Context, string) (bool, error) {
	return false, nil
}

// commandRunner answers the commands a spawn runs besides its terminal:
// provisioning's git questions and the project's own installer.
type commandRunner struct {
	events          *[]string
	calls           int
	installer       string
	installerStderr string
	mcpTracked      bool
	// uvLocks is what the worktree's listing of its uv.lock files prints.
	uvLocks string
}

func (r *commandRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls++
	if req.Name == "git" && len(req.Args) > 0 && req.Args[0] == "check-ignore" {
		return execx.Result{}, nil
	}
	if req.Name == "git" && len(req.Args) > 0 && req.Args[0] == "ls-files" && !slices.Contains(req.Args, "--error-unmatch") {
		return execx.Result{Stdout: []byte(r.uvLocks)}, nil
	}
	if req.Name == "git" && len(req.Args) > 0 && req.Args[0] == "ls-files" {
		if r.mcpTracked {
			return execx.Result{Stdout: []byte(".mcp.json\n")}, nil
		}
		return execx.Result{ExitCode: 1}, nil
	}
	if r.installer != "" && req.Name == r.installer {
		*r.events = append(*r.events, "install")
		return execx.Result{ExitCode: 1, Stderr: []byte(r.installerStderr)}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected command: %#v", req)
}

func makeDir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := fsx.Canonical(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A brief that needs its project's local services for a project that
// declares none is refused before anything is built: the goblin would only
// find out at its full-stack check.
func TestSpawnRefusesABriefThatNeedsServicesItsProjectDoesNotDeclare(t *testing.T) {
	// Arrange
	f := newFixture(t)
	writeFile(t, f.brief, completedSpawnBrief+"services: needed\n")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	want := "spawn: the brief needs primary's local services, but the project declares none: write " + filepath.Join(f.dataDir, "projects", "primary", "services.json")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Spawn = %v, want %q", err, want)
	}
	if _, statErr := os.Stat(state.TaskMetaPath(f.stateDir, "task-7")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a refused spawn published its task: stat error = %v", statErr)
	}
	if f.git.acquired != "" {
		t.Fatalf("acquired worktree %q, want none", f.git.acquired)
	}
}

// A services line spawn does not know is refused rather than read as no
// need, so a typo cannot start a goblin without its services.
func TestSpawnRefusesAServicesLineItDoesNotKnow(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.brief, completedSpawnBrief+"services: yes\n")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), `spawn: the brief's Delivery line "services: yes" is not one spawn knows: write services: needed, or leave the line out`) {
		t.Fatalf("Spawn = %v, want the services line refused", err)
	}
}

// The goblin is told the one command that holds its project's services and
// the one that releases them, with its own task id.
func TestServicesInstructionNamesTheCommandsThatHoldAndReleaseThem(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := servicesInstruction(state.TaskMeta{ID: "pd-check", Project: `C:\dev\PrecisionDocs-AI`})
	for _, want := range []string{
		"Your brief needs PrecisionDocs-AI's local services for its full-stack check.",
		"run " + exe + ` services up C:\dev\PrecisionDocs-AI --task pd-check`,
		"run " + exe + ` services down C:\dev\PrecisionDocs-AI --task pd-check`,
		"waits while memory is short",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instruction = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, ";") {
		t.Errorf("instruction = %q, want no semicolons", got)
	}
}

// A goblin that opens its own pull request opens it as a draft and marks it
// ready once, on the head it means to merge, so the pushes before that start
// no hosted CI. A gate opens its pull request itself, at the head its review,
// tests and lint passed, and a helper opens none.
func TestNotifyInstructionOpensADirectPullRequestAsADraftUntilItsHeadIsFinal(t *testing.T) {
	// Arrange
	for _, test := range []struct {
		name   string
		meta   state.TaskMeta
		isTold bool
	}{
		{"a direct-PR goblin", state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "direct-PR"}, true},
		{"a gated goblin", state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "no-mistakes"}, false},
		{"a local-only goblin", state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "local-only"}, false},
		{"a helper", state.TaskMeta{ID: "task-7-h1", Kind: "ship", Mode: "local-only", Parent: "task-7"}, false},
		{"a scout", state.TaskMeta{ID: "task-7", Kind: "scout", Mode: "direct-PR"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			instruction := notifyInstruction(test.meta)

			// Assert
			isTold := strings.Contains(instruction, "gh pr create --draft") && strings.Contains(instruction, "gh pr ready <url>")
			if isTold != test.isTold {
				t.Errorf("told to open a draft and mark it ready = %v, want %v:\n%s", isTold, test.isTold, instruction)
			}
		})
	}
}

// A push while a ready pull request's CI runs cancels that run after it
// billed its minutes, so every goblin whose pull request is ready while it
// still works, its gate's or its own, pushes a later round once, whole, and
// only once that CI has ended.
func TestNotifyInstructionTellsAGoblinNeverToPushWhileItsPullRequestsCIRuns(t *testing.T) {
	// Arrange
	for _, test := range []struct {
		name   string
		meta   state.TaskMeta
		isTold bool
	}{
		{"a direct-PR goblin", state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "direct-PR"}, true},
		{"a gated goblin", state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "no-mistakes"}, true},
		{"a local-only goblin", state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "local-only"}, false},
		{"a helper", state.TaskMeta{ID: "task-7-h1", Kind: "ship", Mode: "no-mistakes", Parent: "task-7"}, false},
		{"a scout", state.TaskMeta{ID: "task-7", Kind: "scout", Mode: "no-mistakes"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			instruction := notifyInstruction(test.meta)

			// Assert
			isTold := strings.Contains(instruction, "only once") && strings.Contains(instruction, "CI has ended, since a push while it runs cancels it after it billed its minutes")
			if isTold != test.isTold {
				t.Errorf("told never to push while its CI runs = %v, want %v:\n%s", isTold, test.isTold, instruction)
			}
		})
	}
}

// A top-level local-only task opens no pull request, so its first
// instruction asks for neither a pipeline run nor a pull request: it reports
// done with its report. An operations task with no commit was told to drive
// goblins pipeline run and report --done --pr, which could not hold (#1675). A
// gated task and a helper keep their own done.
func TestSpawnInstructionTellsALocalOnlyTaskToReportDoneWithItsReport(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	taskTmp := filepath.Join(`C:\home\state`, "tasktmp", "task-7")
	for _, test := range []struct {
		name     string
		meta     state.TaskMeta
		wanted   []string
		unwanted []string
	}{
		{
			name:     "a local-only task",
			meta:     state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "local-only", DeliveryHash: "h", TaskTmp: taskTmp},
			wanted:   []string{exe + " notify task-7 --done --report <file>", "data/task-7/report.md"},
			unwanted: []string{"--pr <url>", "pipeline run", "opens the pull request", "on completion with a PR"},
		},
		{
			name:     "a gated task",
			meta:     state.TaskMeta{ID: "task-7", Kind: "ship", Mode: "no-mistakes", DeliveryHash: "h", TaskTmp: taskTmp},
			wanted:   []string{"on completion with a PR run: " + exe + " notify task-7 --done --pr <url>", "goblins pipeline run task-7", "you stay the one who opens the pull request"},
			unwanted: []string{"--report"},
		},
		{
			name:     "a local-only helper",
			meta:     state.TaskMeta{ID: "task-7-h1", Kind: "ship", Mode: "local-only", Parent: "task-7", DeliveryHash: "h", TaskTmp: taskTmp},
			wanted:   []string{exe + " notify task-7-h1 --done."},
			unwanted: []string{"--report", "--pr <url>"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			instruction := spawnInstruction(`C:\home\data\task-7\brief.md`, test.meta, nil, "")

			// Assert
			for _, want := range test.wanted {
				if !strings.Contains(instruction, want) {
					t.Errorf("instruction lacks %q:\n%s", want, instruction)
				}
			}
			for _, unwanted := range test.unwanted {
				if strings.Contains(instruction, unwanted) {
					t.Errorf("instruction says %q:\n%s", unwanted, instruction)
				}
			}
		})
	}
}

// A goblin's pull request names its task's ticket, which the supervisor
// opens after the goblin starts and names in the goblin's tasktmp folder.
func TestNotifyInstructionTellsAShipGoblinWhereItsTicketIsNamed(t *testing.T) {
	// Arrange
	taskTmp := filepath.Join(`C:\home\state`, "tasktmp", "task-7")
	ticket := filepath.Join(taskTmp, "ticket.md")

	// Act
	ship := notifyInstruction(state.TaskMeta{ID: "task-7", Kind: "ship", TaskTmp: taskTmp})
	helper := notifyInstruction(state.TaskMeta{ID: "task-7-h1", Kind: "ship", Parent: "task-7", TaskTmp: taskTmp})

	// Assert
	if !strings.Contains(ship, "Before you open a pull request, read "+ticket) || !strings.Contains(ship, "Part of #N") {
		t.Errorf("ship instruction does not say where its ticket is named:\n%s", ship)
	}
	if strings.Contains(helper, "ticket.md") {
		t.Errorf("a helper, which opens no pull request, is told of the ticket:\n%s", helper)
	}
}
