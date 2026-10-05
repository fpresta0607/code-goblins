package spawn

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

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// switchGit answers a switch's git reads of the task's worktree, its status,
// branch and history, and hands every other command to the fixture's runner.
type switchGit struct {
	next   execx.Runner
	status string
	branch string
	log    string
}

func (g *switchGit) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "git" {
		switch {
		case len(req.Args) > 0 && req.Args[0] == "status":
			return execx.Result{Stdout: []byte(g.status)}, nil
		case len(req.Args) > 1 && req.Args[0] == "rev-parse" && req.Args[1] == "--abbrev-ref":
			return execx.Result{Stdout: []byte(g.branch + "\n")}, nil
		case len(req.Args) > 0 && req.Args[0] == "rev-parse":
			return execx.Result{Stdout: []byte("abc1234def\n")}, nil
		case len(req.Args) > 0 && req.Args[0] == "log":
			return execx.Result{Stdout: []byte(g.log)}, nil
		}
	}
	return g.next.Run(ctx, req)
}

// switchFixture is task-7, a native codex goblin spawned earlier whose
// terminal has since ended, ready to be switched: its record is what spawn
// published, so a switch starts its new harness at once, with nothing to stop.
type switchFixture struct {
	*nativeFixture
	git  *switchGit
	meta state.TaskMeta
}

func newSwitchFixture(t *testing.T, control harness.Control) *switchFixture {
	t.Helper()
	f := newQuickFixture(t)
	closeCurrentTerminal(t, f)
	git := &switchGit{next: f.runner, branch: "gb/task-7", log: "abc1234 first commit\ndef5678 second commit"}
	f.service.Commands = git
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: control, specs: &f.fixture.specs}}}
	meta := state.TaskMeta{
		ID:             "task-7",
		Window:         "native",
		EndpointTaskID: "task-7",
		Worktree:       f.worktree,
		Project:        f.project,
		Harness:        string(harness.Codex),
		Kind:           "ship",
		Mode:           "no-mistakes",
		Yolo:           "on",
		TaskTmp:        makeDir(t, filepath.Join(f.stateDir, "tasktmp", "task-7")),
		Brief:          f.brief,
		Model:          "default",
		Effort:         "default",
		Backend:        "native",
		SpawnGen:       "s1",
	}
	if err := state.WriteTaskMeta(f.stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return &switchFixture{nativeFixture: f, git: git, meta: meta}
}

// newRunningGoblin spawns task-7 as a native codex goblin, in a turn that
// never ends, so a switch has a running harness to stop or leave alone. It
// returns the goblin's terminal.
func newRunningGoblin(t *testing.T) (*nativeFixture, host.Record) {
	t.Helper()
	f := newQuickFixture(t)
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	record, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	return f, record
}

// assertLeftRunning fails unless task-7 still runs in the terminal it ran in,
// was never told to exit, and keeps its record as it was.
func assertLeftRunning(t *testing.T, f *nativeFixture, terminal host.Record, before state.TaskMeta) {
	t.Helper()
	current, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || current.HostPID != terminal.HostPID || !host.Running(current) {
		t.Errorf("the terminal is %+v, %v; want host %d still running", current, err, terminal.HostPID)
	}
	if submitted := submittedLines(t, f, 1); slices.Contains(submitted, "/exit") {
		t.Errorf("submitted = %q, want the harness never told to exit", submitted)
	}
	after, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil || after.Harness != before.Harness || after.Model != before.Model || after.SpawnGen != before.SpawnGen {
		t.Errorf("after the refusal the record is %+v, %v; want it unchanged from %+v", after, err, before)
	}
}

// A switch the harness cannot resume writes a handoff with everything the new
// harness needs, and points the new harness at it before anything else.
func TestSwitchWritesAHandoffAndPointsTheNewHarnessAtIt(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	if err := state.AppendStatus(f.stateDir, f.meta.ID, "progress: wrote the parser"); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if result.Handoff == "" || result.Resumed {
		t.Fatalf("result = %+v, want a handoff and no resume", result)
	}
	note, err := os.ReadFile(result.Handoff)
	if err != nil {
		t.Fatalf("read handoff: %v", err)
	}
	for _, want := range []string{
		f.meta.Brief,                 // the original brief, still the task
		f.worktree,                   // where the work is
		"gb/task-7",                  // the branch it is on
		"abc1234 first commit",       // what is already committed
		"progress: wrote the parser", // what the previous goblin reported
		"None.",                      // the worktree was clean
	} {
		if !strings.Contains(string(note), want) {
			t.Errorf("handoff lacks %q:\n%s", want, note)
		}
	}
	submitted := submittedLines(t, f.nativeFixture, 1)
	if len(submitted) != 1 {
		t.Fatalf("submitted = %q, want the new harness's instruction", submitted)
	}
	instruction := delivered(t, submitted[0])
	if !strings.Contains(instruction, result.Handoff) || !strings.Contains(instruction, "ask it again with cfo notify --blocked") {
		t.Errorf("instruction = %q, want it pointed at the handoff and told to ask a cancelled question again", instruction)
	}
}

func TestSwitchResumesInPlaceWhenOnlyTheModelChanges(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}})

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed || result.Handoff != "" {
		t.Fatalf("result = %+v, want the harness's own resume and no handoff", result)
	}
	if launches := named(f.events(t), "env"); len(launches) != 1 || !strings.HasPrefix(launches[0].Text, "resume --last ") {
		t.Errorf("launches = %+v, want the resume arguments first", launches)
	}
	after, _ := state.ReadTaskMeta(f.stateDir, f.meta.ID)
	if after.Model != "gpt-9" || after.Harness != f.meta.Harness {
		t.Errorf("meta = harness %q model %q, want the same harness on the new model", after.Harness, after.Model)
	}
}

func TestPausedResumeUsesTheSavedSessionInsteadOfTheLatestSession(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}})
	if err := state.WriteLifecycle(f.stateDir, state.Lifecycle{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "resume-1", Action: "resume", Phase: "resuming"}); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, IsResume: true, ResumeSession: "saved-session-42"})

	if err != nil {
		t.Fatal(err)
	}
	launches := named(f.events(t), "env")
	if !result.Resumed || len(launches) != 1 || !strings.HasPrefix(launches[0].Text, "resume saved-session-42 ") || strings.Contains(launches[0].Text, "--last") {
		t.Fatalf("result = %+v, launches = %+v; want the saved session resumed", result, launches)
	}
}

func TestSwitchRefusesADirtyWorktreeUnlessForced(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	f.git.status = " M internal/thing.go\n?? notes.txt\n"

	_, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"})
	if err == nil || !strings.Contains(err.Error(), "--force-dirty") {
		t.Fatalf("err = %v, want a refusal naming --force-dirty", err)
	}
	// Refusing must change nothing: the task is still what it was.
	after, _ := state.ReadTaskMeta(f.stateDir, f.meta.ID)
	if after.Model != f.meta.Model || after.SpawnGen != f.meta.SpawnGen {
		t.Errorf("a refused switch still mutated metadata: %+v", after)
	}

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9", ForceDirty: true})
	if err != nil {
		t.Fatalf("forced Switch: %v", err)
	}
	note, err := os.ReadFile(result.Handoff)
	if err != nil {
		t.Fatal(err)
	}
	// The new harness must know the mess is deliberate, or it will "tidy" it.
	if !strings.Contains(string(note), "--force-dirty") || !strings.Contains(string(note), "do not revert or stash it") {
		t.Errorf("handoff does not tell the new harness the dirty state is intentional:\n%s", note)
	}
	if !strings.Contains(string(note), "notes.txt") {
		t.Errorf("handoff omits the uncommitted files:\n%s", note)
	}
}

func TestSwitchRefusesANoOpWhileTheHarnessRuns(t *testing.T) {
	f, terminal := newRunningGoblin(t)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Harness: harness.Kind(before.Harness), Model: before.Model, Effort: before.Effort})

	if err == nil || !strings.Contains(err.Error(), "nothing to switch") {
		t.Fatalf("err = %v, want a refusal to restart a harness for no change", err)
	}
	assertLeftRunning(t, f, terminal, before)
}

// A launch the target refuses to build, such as an effort it does not take,
// is refused while the old harness still runs: the goblin is left as it was.
func TestSwitchRefusesALaunchTheTargetCannotBuildBeforeStoppingTheHarness(t *testing.T) {
	f, terminal := newRunningGoblin(t)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Harness.Adapters[harness.Codex] = nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}, buildErr: errors.New(`harness: Codex does not support effort "ultra"`)}

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Effort: "ultra"})

	if err == nil || !strings.Contains(err.Error(), `does not support effort "ultra"`) || !strings.Contains(err.Error(), "left running") {
		t.Fatalf("err = %v, want the harness's refusal and that the goblin was left running", err)
	}
	assertLeftRunning(t, f, terminal, before)
}

// A replacement whose terminal cannot start leaves a durable record that the
// goblin has no harness, and how to start one.
func TestSwitchRecordsAnEmptyTerminalWhenTheNewHarnessWillNotStart(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	f.service.HostCommand = []string{filepath.Join(t.TempDir(), "no-such-host.exe")}

	_, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"})

	assertEmptyTerminalRecorded(t, f, err)
}

// assertEmptyTerminalRecorded fails unless err tells the operator the goblin
// has no harness, that its work is safe and how to recover, and the task's
// status records it: without the record a goblin that stopped existing looks
// like one that is thinking.
func assertEmptyTerminalRecorded(t *testing.T, f *switchFixture, err error, more ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Switch = nil, want the failure surfaced")
	}
	for _, want := range append([]string{"native terminal now has no harness", "is untouched", "cfo switch " + f.meta.ID}, more...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	status, statusErr := state.TailStatus(f.stateDir, f.meta.ID, 5)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if !slices.ContainsFunc(status, func(line string) bool {
		_, event := state.SplitStatus(line)
		return strings.HasPrefix(event, "failed:") && strings.Contains(event, "no harness")
	}) {
		t.Errorf("status = %v, want a durable record of the empty terminal", status)
	}
}

// A failure after the new harness is up must not claim the terminal is empty:
// that sends the operator to `cfo switch` again, which stops a running goblin
// and loses its context.
func TestSwitchReportsALiveTerminalInsteadOfClaimingItIsEmpty(t *testing.T) {
	previous := nativeStartup
	nativeStartup = 5 * time.Second
	t.Cleanup(func() { nativeStartup = previous })
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	f.userEnv = append(f.userEnv, fakeCodexMode+"=silent")

	_, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"})

	if err == nil {
		t.Fatal("Switch = nil, want the launch failure surfaced")
	}
	for _, want := range []string{"still holds a live", "is untouched", "cfo peek " + f.meta.ID, "do NOT rerun `cfo switch`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "now has no harness") {
		t.Errorf("err = %v, want no empty-terminal claim while the harness runs", err)
	}
}

// The id-reuse wall: a finished task's status log is history, not a live claim
// on its id. Before this, respawning a cleaned-up id was refused and the task
// had to be given an invented suffix.
func TestSpawnAllowsAnIDWhoseOnlyRemainIsAFinishedTasksStatusLog(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	if err := state.AppendStatus(f.stateDir, f.request.ID, "done: returned worktree via cfo cleanup"); err != nil {
		t.Fatal(err)
	}

	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn after cleanup: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.ID != f.request.ID {
		t.Errorf("id = %q, want the original id reused", meta.ID)
	}
}

func TestSpawnStillRefusesAnIDThatCollidesWithALiveTask(t *testing.T) {
	f := newFixture(t)
	// A live task keeps its metadata, and on Windows two ids differing only in
	// case would share every state file.
	if err := state.WriteTaskMeta(f.stateDir, state.TaskMeta{ID: strings.ToUpper(f.request.ID), Window: "native", Backend: "native"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "conflicts case-insensitively") {
		t.Fatalf("err = %v, want the live-task collision still refused", err)
	}
}

// A task recorded in Herdr is not one this build can reach, so a switch
// refuses it by name before anything is stopped or written.
func TestSwitchRefusesATaskRecordedInHerdr(t *testing.T) {
	f := newFixture(t)
	if err := state.WriteTaskMeta(f.stateDir, state.TaskMeta{ID: "task-7", Window: "fleet:p1", Worktree: f.worktree, Project: f.project, Harness: "claude", TaskTmp: f.stateDir, Backend: "herdr", HerdrSession: "fleet", HerdrWorkspaceID: "w1", HerdrTabID: "t1", HerdrPaneID: "p1", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "opus"})

	if err == nil || !strings.Contains(err.Error(), `runs in backend "herdr"`) || !strings.Contains(err.Error(), "only a task in a native terminal can be switched") {
		t.Fatalf("err = %v, want the Herdr task refused by name", err)
	}
	if after, readErr := state.ReadTaskMeta(f.stateDir, "task-7"); readErr != nil || after.SpawnGen != "s1" || f.runner.calls != 0 {
		t.Errorf("record %+v, %v and %d commands after the refusal; want the task untouched", after, readErr, f.runner.calls)
	}
}

func TestSwitchHandoffNamesADetachedWorktreePlainly(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	f.git.branch = "HEAD"

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	note, err := os.ReadFile(result.Handoff)
	if err != nil {
		t.Fatal(err)
	}
	// git answers "HEAD" for a detached worktree, which reads as a branch
	// actually named HEAD and would send the new harness looking for it.
	if strings.Contains(string(note), "Branch: HEAD") {
		t.Errorf("handoff reports a branch named HEAD:\n%s", note)
	}
	if !strings.Contains(string(note), "detached HEAD") {
		t.Errorf("handoff does not say the worktree is detached:\n%s", note)
	}
}

// Neither a model name nor an effort survives a change of harness: "opus"
// means nothing to codex, and Kimi has no effort at all. A model or an effort
// the operator names explicitly is kept, a Claude goblin that names no model
// runs Opus 5.5, and a switch that changes only the effort keeps a named model.
func TestSwitchCarriesOnlyWhatTheNewHarnessUnderstands(t *testing.T) {
	for _, test := range []struct {
		name    string
		meta    state.TaskMeta
		request SwitchRequest
		want    switchTarget
	}{
		{"a model change keeps the effort", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Model: "sonnet"}, switchTarget{harness.Claude, "sonnet", "high"}},
		{"a new harness drops the model and the effort", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Harness: harness.Kimi}, switchTarget{harness.Kimi, "", ""}},
		{"an explicit effort crosses harnesses", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Harness: harness.Kimi, Effort: "xhigh"}, switchTarget{harness.Kimi, "", "xhigh"}},
		{"an explicit model crosses harnesses", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Harness: harness.Kimi, Model: "kimi-k2"}, switchTarget{harness.Kimi, "kimi-k2", ""}},
		{"claude with no model runs Opus 5.5", state.TaskMeta{Harness: "kimi", Model: "default", Effort: "default"}, SwitchRequest{Harness: harness.Claude}, switchTarget{harness.Claude, "claude-opus-5-5", ""}},
		{"an effort change keeps a named model", state.TaskMeta{Harness: "claude", Model: "claude-sonnet-5", Effort: "high"}, SwitchRequest{Effort: "max"}, switchTarget{harness.Claude, "claude-sonnet-5", "max"}},
		{"an effort change gives an unnamed claude model Opus 5.5", state.TaskMeta{Harness: "claude", Model: "default", Effort: "high"}, SwitchRequest{Effort: "max"}, switchTarget{harness.Claude, "claude-opus-5-5", "max"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := requestedTarget(test.meta, test.request); got != test.want {
				t.Errorf("target = %+v, want %+v", got, test.want)
			}
		})
	}
}

// The switch rebuilds the launch from the preflight, so a credential the
// operator stored after the spawn reaches the new harness only if the
// preflight reads the store the way a refresh does.
func TestSwitchKeepsACredentialStoredAfterSpawn(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	for _, name := range []string{"DATABASE_URL", "FIXTURE_TOKEN"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	t.Setenv(auth.StoreDirEnv, filepath.Join(t.TempDir(), "credentials"))
	manifestPath := auth.ManifestPath(f.dataDir, f.project)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"project": "primary", "services": [{"name": "db", "method": "env", "env": ["DATABASE_URL"]}]}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Scoped(f.project, "DATABASE_URL"), "postgres://declared"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Scoped(f.project, "FIXTURE_TOKEN"), "stored-midtask"); err != nil {
		t.Fatal(err)
	}
	f.service.Auth = auth.SpawnPreflight{DataDir: f.dataDir, Runner: f.runner}

	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	started := named(f.events(t), "env")[0].Env
	for name, want := range map[string]string{"DATABASE_URL": "postgres://declared", "FIXTURE_TOKEN": "stored-midtask"} {
		if got := started[name]; got == nil || *got != want {
			t.Errorf("the new harness started with %s = %v, want %q", name, got, want)
		}
	}
}

// The launch is rebuilt from scratch by the switch, so a redirect the spawn
// injected is only there afterwards if the switch re-applies it. GOTMPDIR is
// declared alongside it to prove the launch contract still wins over a
// manifest that tries to redirect a reserved name, in any case and including
// a name the relaunch only writes at harness start.
func TestSwitchReappliesTheProjectEnvironmentRedirects(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	writeWorktreeManifest(t, f.dataDir, f.project, worktree.Manifest{
		Project: "primary",
		Env: map[string]string{
			"PLAYWRIGHT_BROWSERS_PATH": `C:\cache\ms-playwright`,
			"GOTMPDIR":                 `C:\hijacked`,
			"gotmpdir":                 `C:\hijacked-lower`,
			"cfo_state_override":       `C:\hijacked-state`,
		},
	})

	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	started := named(f.events(t), "env")[0].Env
	for name, want := range map[string]string{
		"PLAYWRIGHT_BROWSERS_PATH": `C:\cache\ms-playwright`,
		"GOTMPDIR":                 goTmpDir(t, f.stateDir, f.meta.ID),
		"CFO_STATE_OVERRIDE":       f.stateDir,
	} {
		if got := started[name]; got == nil || *got != want {
			t.Errorf("the new harness started with %s = %v, want %q", name, got, want)
		}
	}
}

// A relaunch has to land in the task's own Go temporary directory - not
// merely in something that is not the manifest's - and has to recreate it: a
// long-lived task can outlive its scratch, and a harness launched at a
// GOTMPDIR that does not exist fails its first go build.
func TestSwitchRelaunchesIntoTheTasksOwnGoTmpDir(t *testing.T) {
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	goTmp := goTmpDir(t, f.stateDir, f.meta.ID)
	if err := os.RemoveAll(goTmp); err != nil {
		t.Fatal(err)
	}

	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	if got := named(f.events(t), "env")[0].Env["GOTMPDIR"]; got == nil || *got != goTmp {
		t.Errorf("the new harness started with GOTMPDIR = %v, want the task's own %q", got, goTmp)
	}
	if info, err := os.Stat(goTmp); err != nil || !info.IsDir() {
		t.Errorf("stat %q = %v, %v, want the relaunch to have recreated the directory", goTmp, info, err)
	}
}

// An unresolvable user cache directory is a fleet-wide misconfiguration, so a
// switch has to refuse before it stops the running harness. Discovered after
// the stop it would leave the goblin with no harness at all.
func TestSwitchRefusesAnUnresolvableGoTmpDirBeforeStoppingTheHarness(t *testing.T) {
	f, terminal := newRunningGoblin(t)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	// os.UserCacheDir reads these and errors when the one it needs is empty.
	for _, name := range []string{"LOCALAPPDATA", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(name, "")
	}

	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"}); err == nil {
		t.Fatal("Switch succeeded without a resolvable Go temporary directory, want refusal")
	}

	assertLeftRunning(t, f, terminal, before)
}

// Provisioning materializes the filtered configuration under the task's
// temporary directory, never inside the checkout, and a relaunch hands over
// that file and no other: a .mcp.json in the worktree is the project's own
// unfiltered file or one the goblin wrote. The build is refused here so
// nothing starts.
func TestSwitchHandsTheNewHarnessOnlyTheProvisionedMCPConfig(t *testing.T) {
	for _, test := range []struct {
		name          string
		isProvisioned bool
	}{
		{name: "provisioning materialized one", isProvisioned: true},
		{name: "provisioning materialized none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
			specs := []harness.LaunchSpec{}
			f.service.Harness.Adapters[harness.Codex] = nativeAdapter{kind: harness.Codex, specs: &specs, buildErr: errors.New("nothing starts in this test")}
			provisioned := filepath.Join(f.meta.TaskTmp, "mcp.json")
			if test.isProvisioned {
				writeFile(t, provisioned, `{"mcpServers":{"neon":{"command":"npx"}}}`)
			}
			writeFile(t, filepath.Join(f.worktree, ".mcp.json"), `{"mcpServers":{"oauth":{"url":"https://example.com/mcp"}}}`)

			_, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9", ForceDirty: true})

			if err == nil || len(specs) != 1 {
				t.Fatalf("Switch = %v with %d builds, want the one refused build", err, len(specs))
			}
			want := ""
			if test.isProvisioned {
				want = provisioned
			}
			if specs[0].MCPConfig != want {
				t.Errorf("MCPConfig = %q, want %q", specs[0].MCPConfig, want)
			}
		})
	}
}

// writeWorktreeManifest declares one project's worktree environment where
// worktree.Resolve reads it.
func writeWorktreeManifest(t *testing.T, dataDir, project string, manifest worktree.Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := worktree.ManifestPath(dataDir, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A hand-edited worktree.json with a typo is fully knowable before anything
// is touched. Discovering it after the stop would leave the goblin with no
// harness at all over a config error.
func TestSwitchRefusesAMalformedManifestBeforeStoppingTheHarness(t *testing.T) {
	f, terminal := newRunningGoblin(t)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	writeWorktreeManifest(t, f.dataDir, f.project, worktree.Manifest{
		Project:      "primary",
		Dependencies: worktree.Dependencies{Strategy: "instal"},
	})

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})

	if err == nil || !strings.Contains(err.Error(), "unknown dependency strategy") {
		t.Fatalf("err = %v, want a refusal naming the malformed manifest", err)
	}
	assertLeftRunning(t, f, terminal, before)
}

// A Codex MCP server the operator's configuration names in a form a -c
// override cannot address is knowable before anything is touched, so a switch
// to Codex refuses it while the old harness still runs rather than leaving the
// goblin with no harness at all.
func TestSwitchRefusesAnUnaddressableCodexMCPServerBeforeStoppingTheHarness(t *testing.T) {
	f, terminal := newRunningGoblin(t)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"), "[mcp_servers.\"my.server\"]\ncommand = \"npx\"\n")

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})

	if err == nil || !strings.Contains(err.Error(), `"my.server"`) {
		t.Fatalf("err = %v, want a refusal naming the server", err)
	}
	assertLeftRunning(t, f, terminal, before)
}

// /exit on a Claude goblin with background work opens Claude's "Background
// work is running" menu instead of exiting. A switch answers it with the
// harness's own exit keys, which pick Exit and stop tasks, so the harness
// ends on its own terms rather than by its terminal being closed.
func TestSwitchAnswersAnExitMenuWithTheHarnessesOwnKeys(t *testing.T) {
	claude, err := harness.DefaultRegistry().Get(harness.Claude)
	if err != nil {
		t.Fatal(err)
	}
	exit := claude.Control()
	f := newNativeFixture(t, harness.Codex, "exitmenu")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit", ExitMarkers: exit.ExitMarkers, ExitKeys: exit.ExitKeys}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)

	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"}); err != nil {
		t.Fatalf("Switch over the exit menu: %v", err)
	}

	if answered := named(f.events(t), "exit menu"); len(answered) != 1 || answered[0].Text != "1. Exit and stop tasks" {
		t.Errorf("exit menu answers = %+v, want Exit and stop tasks chosen once", answered)
	}
}
