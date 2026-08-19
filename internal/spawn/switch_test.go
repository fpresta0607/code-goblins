package spawn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// switchRunner adds the two things a switch needs beyond a spawn: git, and an
// agent that stops answering once the harness has been told to exit.
type switchRunner struct {
	*herdrRunner
	gitStatus    string
	gitLog       string
	gitBranch    string
	stopped      bool
	restarted    bool
	stopAfter    int // agent-get calls before the harness reports gone
	agentGets    int
	neverStops   bool
	gitCalls     []execx.Request
	resumeDialog string // pane text shown after the relaunch until an Enter lands
	resumeReplay string // replayed conversation prepended to every post-relaunch read
	// agentUnreadable makes herdr answer agent get untrustworthily once the
	// relaunch has run, so the failure path cannot tell alive from gone.
	agentUnreadable bool
}

func (r *switchRunner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "git" {
		r.gitCalls = append(r.gitCalls, req)
		switch {
		case len(req.Args) > 0 && req.Args[0] == "status":
			return execx.Result{Stdout: []byte(r.gitStatus)}, nil
		case len(req.Args) > 1 && req.Args[0] == "rev-parse" && req.Args[1] == "--abbrev-ref":
			return execx.Result{Stdout: []byte(r.gitBranch + "\n")}, nil
		case len(req.Args) > 0 && req.Args[0] == "rev-parse":
			return execx.Result{Stdout: []byte("abc1234def\n")}, nil
		case len(req.Args) > 0 && req.Args[0] == "log":
			return execx.Result{Stdout: []byte(r.gitLog)}, nil
		}
		return execx.Result{}, nil
	}
	// A typed slash command is the harness being told to exit, so the fake
	// agent becomes stoppable again - otherwise a second switch in one test
	// would find an agent that can never die.
	if req.Name == "herdr" && len(req.Args) >= 4 && req.Args[0] == "pane" && req.Args[1] == "send-text" && strings.HasPrefix(req.Args[3], "/") {
		r.restarted = false
		r.agentGets = 0
	}
	// A resume dialog exists only once the new harness is up: it covers the
	// pane until an Enter lands, exactly like claude's resume-from-summary
	// prompt. Ctrl+U and typed text do not dismiss it.
	if r.resumeDialog != "" && r.restarted && req.Name == "herdr" && len(req.Args) >= 3 && req.Args[0] == "pane" {
		if req.Args[1] == "read" {
			return execx.Result{Stdout: []byte(r.resumeDialog)}, nil
		}
		if req.Args[1] == "send-keys" && len(req.Args) >= 4 && req.Args[3] == "enter" {
			r.resumeDialog = ""
		}
	}
	// `claude --continue` replays the prior conversation into the same pane
	// tail the dialog loop reads. No keystroke clears it.
	if r.resumeReplay != "" && r.restarted && req.Name == "herdr" && len(req.Args) >= 3 && req.Args[0] == "pane" && req.Args[1] == "read" {
		result, err := r.herdrRunner.Run(ctx, req)
		if err != nil {
			return result, err
		}
		return execx.Result{Stdout: append([]byte(r.resumeReplay), result.Stdout...)}, nil
	}
	// herdr puts the subcommand first and appends --session, so the head of
	// the argument list is what identifies the call.
	if req.Name == "herdr" && len(req.Args) >= 2 && req.Args[0] == "agent" {
		switch req.Args[1] {
		case "start":
			// The replacement harness registers, so the pane has an agent
			// again from here on.
			r.restarted = true
		case "get":
			r.agentGets++
			// An unexpected error code is herdr answering without being
			// trustworthy, which is neither "alive" nor "gone".
			if r.agentUnreadable && r.restarted {
				return execx.Result{Stdout: []byte(`{"error":{"code":"herdr_unavailable"}}`), ExitCode: 1}, nil
			}
			if !r.neverStops && !r.restarted && r.agentGets > r.stopAfter {
				return execx.Result{Stdout: []byte(`{"error":{"code":"agent_not_found"}}`), ExitCode: 1}, nil
			}
		}
	}
	return r.herdrRunner.Run(ctx, req)
}

// switchFixture is a task already spawned and running, ready to be switched.
type switchFixture struct {
	service  Service
	runner   *switchRunner
	stateDir string
	worktree string
	project  string
	meta     state.TaskMeta
}

func newSwitchFixture(t *testing.T) *switchFixture {
	t.Helper()
	base := newFixture(t)
	// The task must exist before it can be switched, so the fixture spawns it
	// the ordinary way and then swaps in a runner that can also stop it.
	if _, err := base.service.Spawn(context.Background(), base.request); err != nil {
		t.Fatalf("seed spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(base.stateDir, base.request.ID)
	if err != nil {
		t.Fatalf("read seeded metadata: %v", err)
	}

	runner := &switchRunner{
		herdrRunner: base.runner,
		gitBranch:   "gb/task-7",
		gitLog:      "abc1234 first commit\ndef5678 second commit",
		stopAfter:   1,
	}
	service := base.service
	service.Herdr.Commands = runner
	service.Treehouse.Commands = runner
	service.Commands = runner
	service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
		harness.Claude: fixtureAdapter{events: &base.events},
		harness.Kimi:   fixtureAdapter{events: &base.events},
	}}

	return &switchFixture{
		service:  service,
		runner:   runner,
		stateDir: base.stateDir,
		worktree: base.worktree,
		project:  base.project,
		meta:     meta,
	}
}

func TestSwitchKeepsTheTaskIDPaneAndWorktree(t *testing.T) {
	fixture := newSwitchFixture(t)

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID:      fixture.meta.ID,
		Harness: harness.Kimi,
		Session: "fleet",
	})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}

	after, err := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	// The whole point: nothing about the task's identity or its work moves.
	if after.ID != fixture.meta.ID || after.Worktree != fixture.meta.Worktree || after.HerdrPaneID != fixture.meta.HerdrPaneID || after.HerdrTabID != fixture.meta.HerdrTabID {
		t.Errorf("identity changed: %+v -> %+v", fixture.meta, after)
	}
	if after.Harness != string(harness.Kimi) {
		t.Errorf("harness = %q, want %q", after.Harness, harness.Kimi)
	}
	if after.SpawnGen == fixture.meta.SpawnGen {
		t.Error("spawn_gen was not bumped, so the watcher cannot tell the pane holds a new process")
	}
	if !strings.Contains(result.Output, "switched "+fixture.meta.ID) {
		t.Errorf("output = %q, want it to name the switched task", result.Output)
	}
}

func TestSwitchWritesAHandoffAcrossHarnessesAndInstructsTheNewOne(t *testing.T) {
	fixture := newSwitchFixture(t)
	if err := state.AppendStatus(fixture.stateDir, fixture.meta.ID, "progress: wrote the parser"); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID:      fixture.meta.ID,
		Harness: harness.Kimi,
		Session: "fleet",
	})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if result.Handoff == "" {
		t.Fatal("a cross-harness switch produced no handoff note")
	}
	if result.Resumed {
		t.Error("Resumed = true across harnesses, which cannot carry context")
	}

	note, err := os.ReadFile(result.Handoff)
	if err != nil {
		t.Fatalf("read handoff: %v", err)
	}
	for _, want := range []string{
		fixture.meta.Brief,           // the original brief, still the task
		fixture.worktree,             // where the work is
		"gb/task-7",                  // the branch it is on
		"abc1234 first commit",       // what is already committed
		"progress: wrote the parser", // what the previous goblin reported
		"None.",                      // the worktree was clean
	} {
		if !strings.Contains(string(note), want) {
			t.Errorf("handoff lacks %q:\n%s", want, note)
		}
	}
	// The new harness has to be told to read it before doing anything else.
	last := fixture.runner.literals[len(fixture.runner.literals)-1]
	if !strings.Contains(last, result.Handoff) {
		t.Errorf("delivered instruction = %q, want it to point at the handoff", last)
	}
}

func TestSwitchResumesInPlaceWhenOnlyTheModelChanges(t *testing.T) {
	fixture := newSwitchFixture(t)

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID:      fixture.meta.ID,
		Model:   "opus",
		Session: "fleet",
	})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed {
		t.Fatal("a same-harness switch did not use the harness's own resume")
	}
	if result.Handoff != "" {
		t.Errorf("Handoff = %q, want none when the harness resumes its own session", result.Handoff)
	}
	if !contains(fixture.runner.startArgs, "--continue") {
		t.Errorf("start args = %v, want the resume argument first", fixture.runner.startArgs)
	}
	after, _ := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if after.Model != "opus" || after.Harness != fixture.meta.Harness {
		t.Errorf("meta = harness %q model %q, want the same harness on the new model", after.Harness, after.Model)
	}
}

func TestSwitchRefusesADirtyWorktreeUnlessForced(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.gitStatus = " M internal/thing.go\n?? notes.txt\n"

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"})
	if err == nil || !strings.Contains(err.Error(), "--force-dirty") {
		t.Fatalf("err = %v, want a refusal naming --force-dirty", err)
	}
	// Refusing must change nothing: the harness is still the original one.
	after, _ := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if after.Harness != fixture.meta.Harness || after.SpawnGen != fixture.meta.SpawnGen {
		t.Errorf("a refused switch still mutated metadata: %+v", after)
	}

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet", ForceDirty: true})
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

func TestSwitchStopsTheOldHarnessBeforeStartingTheNew(t *testing.T) {
	fixture := newSwitchFixture(t)

	if _, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	// The adapter's stop command has to reach the pane before the relaunch
	// prefix, or two harnesses end up sharing one shell.
	stopAt := -1
	for index, literal := range fixture.runner.literals {
		if literal == "/exit" {
			stopAt = index
			break
		}
	}
	if stopAt < 0 {
		t.Fatalf("the harness stop command was never sent: %q", fixture.runner.literals)
	}
	relaunched := false
	for _, literal := range fixture.runner.literals[stopAt+1:] {
		if strings.Contains(literal, "Set-Location") {
			relaunched = true
			break
		}
	}
	if !relaunched {
		t.Fatalf("no relaunch prefix followed the stop: %q", fixture.runner.literals)
	}
	if !contains(fixture.runner.keys, "escape") {
		t.Errorf("keys = %v, want the stream interrupted before the stop command", fixture.runner.keys)
	}
}

func TestSwitchRefusesWhenTheHarnessWillNotStop(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.neverStops = true

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"})
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("err = %v, want a refusal rather than a second harness in the same pane", err)
	}
	if !contains(fixture.runner.keys, "Ctrl-C") {
		t.Errorf("keys = %v, want an interrupt attempted before giving up", fixture.runner.keys)
	}
}

func TestSwitchRefusesANoOp(t *testing.T) {
	fixture := newSwitchFixture(t)

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID:      fixture.meta.ID,
		Harness: harness.Kind(fixture.meta.Harness),
		Model:   fixture.meta.Model,
		Effort:  fixture.meta.Effort,
		Session: "fleet",
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to switch") {
		t.Fatalf("err = %v, want a refusal to restart a harness for no change", err)
	}
}

// A same-harness request is only a no-op while a harness is actually running.
// When the pane is empty - the harness died on its own, or a previous switch
// stopped it and failed before the replacement started - the same request is
// the restart the recovery message tells the operator to run.
func TestSwitchAllowsARestartWhenThePaneIsEmpty(t *testing.T) {
	fixture := newSwitchFixture(t)
	// The first agent status read reports the pane as dead, so the switch must
	// proceed as a restart rather than refuse the request as a no-op.
	fixture.runner.stopAfter = 0

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID:      fixture.meta.ID,
		Harness: harness.Kind(fixture.meta.Harness),
		Model:   fixture.meta.Model,
		Effort:  fixture.meta.Effort,
		Session: "fleet",
	})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed {
		t.Error("Resumed = false, want the dead harness restarted through its own resume")
	}
	after, err := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.SpawnGen == fixture.meta.SpawnGen {
		t.Error("spawn_gen was not bumped, so the watcher cannot tell the restarted harness is a new process")
	}
}

// The recorded session is where the pane lives; a request naming a different
// session must not redirect the switch there.
func TestSwitchTargetsTheRecordedSessionNotTheRequestSession(t *testing.T) {
	fixture := newSwitchFixture(t)

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID:      fixture.meta.ID,
		Harness: harness.Kimi,
		Session: "elsewhere",
	})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !strings.Contains(result.Output, "switched "+fixture.meta.ID) {
		t.Errorf("output = %q, want it to name the switched task", result.Output)
	}
}

// A failure between stopping the old harness and starting the new one - here
// the target adapter refusing to build its launch - must leave the same empty-
// pane record as a failed start, not a silent pane with stale metadata.
func TestSwitchRecordsAnEmptyPaneWhenTheTargetRefusesToBuild(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.service.Harness.Adapters[harness.Kimi] = fixtureAdapter{
		events:   &[]string{},
		buildErr: errors.New(`harness: Codex does not support effort "max"`),
	}

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"})
	if err == nil {
		t.Fatal("Switch = nil, want the build failure surfaced")
	}
	for _, want := range []string{"pane now has no harness", "is untouched", "cfo switch " + fixture.meta.ID, "--effort default"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	status, err := state.TailStatus(fixture.stateDir, fixture.meta.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range status {
		if strings.HasPrefix(line, "failed:") && strings.Contains(line, "no harness") {
			found = true
		}
	}
	if !found {
		t.Errorf("status = %v, want a durable record of the empty pane", status)
	}
}

// The id-reuse wall: a finished task's status log is history, not a live claim
// on its id. Before this, respawning a cleaned-up id was refused and the task
// had to be given an invented suffix.
func TestSpawnAllowsAnIDWhoseOnlyRemainIsAFinishedTasksStatusLog(t *testing.T) {
	fixture := newFixture(t)
	if err := state.AppendStatus(fixture.stateDir, fixture.request.ID, "done: returned worktree via cfo cleanup"); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Spawn(context.Background(), fixture.request); err != nil {
		t.Fatalf("Spawn after cleanup: %v", err)
	}
	meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.ID != fixture.request.ID {
		t.Errorf("id = %q, want the original id reused", meta.ID)
	}
}

func TestSpawnStillRefusesAnIDThatCollidesWithALiveTask(t *testing.T) {
	fixture := newFixture(t)
	// A live task keeps its metadata, and on Windows two ids differing only in
	// case would share every state file.
	if err := state.WriteTaskMeta(fixture.stateDir, state.TaskMeta{
		ID:               strings.ToUpper(fixture.request.ID),
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "workspace-1",
		HerdrTabID:       "tab-1",
		HerdrPaneID:      "pane-1",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "conflicts case-insensitively") {
		t.Fatalf("err = %v, want the live-task collision still refused", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSwitchHandoffNamesADetachedWorktreePlainly(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.gitBranch = "HEAD"

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"})
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

func TestSwitchDoesNotCarryAModelAcrossHarnesses(t *testing.T) {
	fixture := newSwitchFixture(t)
	// Give the task a model that only its current harness understands.
	if _, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Model: "opus", Session: "fleet"}); err != nil {
		t.Fatalf("seed model switch: %v", err)
	}

	if _, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	after, err := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	// "opus" means nothing to another harness; carrying it would either fail
	// the launch or silently select the wrong thing.
	if after.Model != "default" {
		t.Errorf("model = %q, want the new harness's default rather than the old harness's model", after.Model)
	}
	if after.Harness != string(harness.Kimi) {
		t.Errorf("harness = %q, want %q", after.Harness, harness.Kimi)
	}
}

func TestSwitchDoesNotCarryAnEffortAcrossHarnesses(t *testing.T) {
	fixture := newSwitchFixture(t)
	// Give the task an effort another harness may not honour: Kimi has no
	// effort knob at all, so carrying one across would loop the recovery.
	if _, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Effort: "xhigh", Session: "fleet"}); err != nil {
		t.Fatalf("seed effort switch: %v", err)
	}

	if _, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	after, err := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Effort != "default" {
		t.Errorf("effort = %q, want the new harness's default rather than the old harness's effort", after.Effort)
	}
	if after.Harness != string(harness.Kimi) {
		t.Errorf("harness = %q, want %q", after.Harness, harness.Kimi)
	}
}

func TestSwitchKeepsAnExplicitEffortAcrossHarnesses(t *testing.T) {
	fixture := newSwitchFixture(t)

	if _, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID: fixture.meta.ID, Harness: harness.Kimi, Effort: "xhigh", Session: "fleet",
	}); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	after, _ := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if after.Effort != "xhigh" {
		t.Errorf("effort = %q, want the effort the operator asked for", after.Effort)
	}
}

func TestSwitchKeepsAnExplicitModelAcrossHarnesses(t *testing.T) {
	fixture := newSwitchFixture(t)

	if _, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID: fixture.meta.ID, Harness: harness.Kimi, Model: "kimi-k2", Session: "fleet",
	}); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	after, _ := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if after.Model != "kimi-k2" {
		t.Errorf("model = %q, want the model the operator asked for", after.Model)
	}
}

func TestSwitchRecordsAnEmptyPaneWhenTheNewHarnessWillNotStart(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.startErr = errStartFailed
	// A harness that never started leaves no registered agent behind, which
	// is what the empty-pane recovery is read off.
	fixture.runner.agentNotFound = true

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"})
	if err == nil {
		t.Fatal("Switch = nil, want the start failure surfaced")
	}
	// The operator has to learn three things: the pane is empty, the work is
	// safe, and how to recover.
	for _, want := range []string{"pane now has no harness", "is untouched", "cfo switch " + fixture.meta.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	status, err := state.TailStatus(fixture.stateDir, fixture.meta.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range status {
		if strings.HasPrefix(line, "failed:") && strings.Contains(line, "no harness") {
			found = true
		}
	}
	if !found {
		// Without a record the task looks merely idle, and a goblin that
		// stopped existing is indistinguishable from one that is thinking.
		t.Errorf("status = %v, want a durable record of the empty pane", status)
	}
}

var errStartFailed = &startFailure{}

type startFailure struct{}

func (*startFailure) Error() string { return "herdr: timed out waiting for agent startup" }

// The switch failure path used to assert the pane was empty no matter where
// the failure came from. A rejected instruction read-back happens after the
// new harness is up, so that claim sent the operator to `cfo switch` again -
// which stops a running goblin and loses its context.
func TestSwitchReportsALivePaneInsteadOfClaimingItIsEmpty(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.startErr = errStartFailed

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"})
	if err == nil {
		t.Fatal("Switch = nil, want the start failure surfaced")
	}
	for _, want := range []string{"still holds a live", "is untouched", "cfo peek " + fixture.meta.ID, "do NOT rerun `cfo switch`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "pane now has no harness") {
		t.Errorf("err = %v, want no empty-pane claim while the agent is alive", err)
	}
}

// An unreadable probe is herdr admitting it does not know, and the recovery
// text must admit it too. Treating "could not answer" as "no harness" sends
// the operator back to `cfo switch`, which stops whatever live goblin is
// actually holding the pane - the exact context loss the re-probe prevents.
func TestSwitchDoesNotGuessWhenTheProbeCannotReadThePane(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.startErr = errStartFailed
	fixture.runner.agentUnreadable = true

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Harness: harness.Kimi, Session: "fleet"})
	if err == nil {
		t.Fatal("Switch = nil, want the start failure surfaced")
	}
	for _, want := range []string{"could not verify what the pane holds", "may still be running", "is untouched", "cfo peek " + fixture.meta.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	for _, unwanted := range []string{"pane now has no harness", "still holds a live"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("err = %v, want no %q claim from an unreadable probe", err, unwanted)
		}
	}
}

// The resume markers must not match the conversation `--continue` replays
// into the same pane tail. A goblin that discussed the dialog's option labels
// ("Resume from summary") replays them on every read, so option-label markers
// held the dialog loop open until the switch failed on a harness that was
// answering normally.
func TestSwitchIgnoresAReplayedTranscriptThatNamesTheResumeOptions(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.resumeReplay = "> the dialog offers Resume from summary and Resume full session as-is\n\n"

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Model: "opus", Session: "fleet"})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed {
		t.Fatal("Resumed = false, want the same-harness switch to resume in place")
	}
}

// A resumed claude can open its interactive resume dialog (resume from
// summary / full session) before the composer accepts input. The switch must
// clear it like any startup dialog - Enter accepts the summary default -
// otherwise the resume instruction is typed into the dialog, every read-back
// mismatches, and a healthy relaunch reports failed.
func TestSwitchClearsTheResumeDialogBeforeInstructingTheResumedHarness(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.resumeDialog = "This session is 6 hours old and 120k tokens.\n\nResuming the full session will consume a substantial portion of your usage limits.\nWe recommend resuming from a summary.\n\n> 1. Resume from summary\n  2. Resume full session as-is\n  3. Don't ask me again\n"

	result, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, Model: "opus", Session: "fleet"})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed {
		t.Fatal("Resumed = false, want the same-harness switch to resume in place")
	}
}
