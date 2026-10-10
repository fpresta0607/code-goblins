package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const (
	stopPoll     = 1500 * time.Millisecond
	stopTries    = 20
	handoffLines = 20
)

// SwitchRequest changes one running goblin's harness, model, or effort in
// place: same task id, same worktree, same terminal id. An empty field keeps
// what the task already has.
type SwitchRequest struct {
	ID            string
	Harness       harness.Kind
	Model         string
	Effort        string
	ForceDirty    bool
	IsResume      bool
	ResumeSession string
	ResumeHandoff string
	ResumeNote    string
	// Restart starts the task's own harness, model and effort again in
	// place while it runs, as for an update of its harness installed since
	// it started, which a switch to the same values otherwise refuses.
	Restart    bool
	Generation string
	// BriefPath is the fallback for a task whose metadata predates the brief
	// field.
	BriefPath string
	// Admit, when set, makes the relaunch a start: Resume and the comeback
	// add a running terminal, so they take the home's spawn lock as a spawn
	// does, from the check that the task's terminal is not running to its
	// host's launch, and are admitted on the machine's room under it by
	// Admit. A refusal comes back wrapped in state.ErrNoRoom. A switch only
	// replaces a running harness and sets none.
	Admit func() error
}

// SwitchResult reports what the switch changed.
type SwitchResult struct {
	Meta    state.TaskMeta
	From    string
	Handoff string
	Resumed bool
	Output  string
}

// Switch stops the goblin's current harness, then starts the requested one in
// a native terminal of the same id, in the same worktree. Nothing is torn
// down: the worktree, the branch, and the task id all survive, so committed
// work and an open PR are untouched by construction.
//
// A same-harness switch resumes its recorded session when one is supplied.
// Otherwise it writes a handoff note
// into the task's temporary directory and instructs the new harness to read it
// before anything else.
func (s Service) Switch(ctx context.Context, req SwitchRequest) (result SwitchResult, err error) {
	if err := state.ValidTaskID(req.ID); err != nil {
		return SwitchResult{}, err
	}
	if err := validateLineValues(
		"switch harness", string(req.Harness),
		"switch model", req.Model,
		"switch effort", req.Effort,
		"resume session", req.ResumeSession,
		"resume handoff", req.ResumeHandoff,
	); err != nil {
		return SwitchResult{}, err
	}
	metadataLock := state.MetadataLockName(req.ID)
	if _, err := lock.AcquireExclusiveNamed(s.StateDir, metadataLock); err != nil {
		// A cleanup holds the task's record lock from before it removes
		// anything to its end, so a relaunch that meets it stops here,
		// before it has read, written or started anything.
		if cleaner, readErr := lock.ReadNamed(s.StateDir, state.CleanupLockName(req.ID)); readErr == nil && cleaner.Alive() {
			return SwitchResult{}, fmt.Errorf("switch: task %s is held by %s, so nothing was changed: %w", req.ID, state.CleanupPurpose(req.ID), err)
		}
		return SwitchResult{}, fmt.Errorf("switch: acquire metadata lock: %w", err)
	}
	defer func() {
		if releaseErr := s.releaseTaskLock(s.StateDir, metadataLock); releaseErr != nil {
			releaseErr = fmt.Errorf("switch: release metadata lock: %w", releaseErr)
			if err == nil {
				err = releaseErr
			} else {
				err = errors.Join(err, releaseErr)
			}
		}
	}()

	meta, err := state.ReadTaskMeta(s.StateDir, req.ID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: read task metadata: %w", err)
	}
	if req.Generation != "" && req.Generation != meta.SpawnGen {
		return SwitchResult{}, errors.New("switch: task session changed before resume")
	}
	if meta.Backend != "native" {
		return SwitchResult{}, fmt.Errorf("switch: task %s runs in backend %q; only a task in a native terminal can be switched", req.ID, meta.Backend)
	}
	required := map[string]string{"worktree": meta.Worktree, "project": meta.Project, "tasktmp": meta.TaskTmp}
	for name, value := range required {
		if value == "" {
			return SwitchResult{}, fmt.Errorf("switch: task %s metadata is missing %s", req.ID, name)
		}
	}

	target := requestedTarget(meta, req)
	if req.Restart {
		target = switchTarget{Harness: harness.Kind(meta.Harness), Model: meta.Model, Effort: meta.Effort}
	}
	if target == (switchTarget{}) {
		return SwitchResult{}, fmt.Errorf("switch: task %s has no harness to switch", req.ID)
	}

	if target.same(meta) && !req.Restart {
		if nativeTerminalRuns(s.StateDir, meta.ID) {
			if req.IsResume {
				return SwitchResult{Meta: meta, Resumed: true, Output: "task already resumed " + meta.ID}, nil
			}
			return SwitchResult{}, fmt.Errorf("switch: task %s already runs harness=%s model=%s effort=%s; nothing to switch", req.ID, meta.Harness, valueOrDefault(meta.Model), valueOrDefault(meta.Effort))
		}
	}

	adapter, err := s.Harness.Get(target.Harness)
	if err != nil {
		return SwitchResult{}, err
	}
	if _, ok := harness.NativeScreens(target.Harness); !ok {
		return SwitchResult{}, fmt.Errorf("switch: %s cannot run in a native terminal yet; task %s was left running as it was", target.Harness, req.ID)
	}

	if _, err := lock.AcquireExclusiveNamed(s.StateDir, switchLockName(req.ID)); err != nil {
		return SwitchResult{}, fmt.Errorf("switch: acquire task lock: %w", err)
	}
	defer func() {
		if releaseErr := s.releaseTaskLock(s.StateDir, switchLockName(req.ID)); releaseErr != nil {
			releaseErr = fmt.Errorf("switch: release task lock: %w", releaseErr)
			if err == nil {
				err = releaseErr
			} else {
				err = errors.Join(err, releaseErr)
			}
		}
	}()

	project, err := fsx.Canonical(meta.Project)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: canonicalize project %q: %w", meta.Project, err)
	}
	worktreePath, err := fsx.Canonical(meta.Worktree)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: canonicalize worktree %q: %w", meta.Worktree, err)
	}
	if fsx.SamePath(worktreePath, project) {
		return SwitchResult{}, fmt.Errorf("switch: worktree %q is the primary checkout", worktreePath)
	}
	git, err := s.worktreeGit()
	if err != nil {
		return SwitchResult{}, err
	}
	if err := worktree.Validate(ctx, git, project, worktreePath); err != nil {
		return SwitchResult{}, fmt.Errorf("switch: validate worktree: %w", err)
	}

	dirty, err := s.worktreeStatus(ctx, worktreePath)
	if err != nil {
		return SwitchResult{}, err
	}
	isResumeInPlace := target.same(meta)
	if dirty != "" && !req.ForceDirty && !isResumeInPlace {
		return SwitchResult{}, fmt.Errorf("switch: worktree %q has uncommitted changes; commit them or rerun with --force-dirty:\n%s", worktreePath, dirty)
	}

	if err := adapter.Validate(ctx, s.commands()); err != nil {
		return SwitchResult{}, fmt.Errorf("switch: validate harness %s: %w", target.Harness, err)
	}
	// The relaunched harness needs the project's declared environment
	// redirects, and a malformed manifest is knowable now. Reading it here
	// rather than after the stop keeps a config typo from leaving the goblin
	// with no harness at all. Nothing is provisioned or installed by a switch.
	manifest, err := worktree.Resolve(s.Worktrees.DataDir, project)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: resolve worktree manifest: %w", err)
	}
	// A Codex goblin turns off the operator's MCP servers, and a server it
	// cannot turn off is refused while the old harness still runs.
	codexServers, err := codexMCPServers(target.Harness)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: %w", err)
	}
	// A relaunch reuses the task's own scratch folder and recreates it, and
	// the shared folder beside it, if it is missing; cleanup removes it with
	// the task. Both the path and the folder are knowable now - an unwritable
	// one is a fleet-wide misconfiguration, and discovering it after the stop
	// would leave the goblin with no harness.
	scratch, err := state.TaskScratch(s.StateDir, meta)
	if err != nil {
		return SwitchResult{}, err
	}
	if err := createScratch(scratch); err != nil {
		return SwitchResult{}, fmt.Errorf("switch: create the task's scratch folder: %w", err)
	}
	briefPath := meta.Brief
	if briefPath == "" {
		briefPath = req.BriefPath
	}
	// The new launch is built while the old harness still runs, so a model or
	// an effort the new one refuses leaves the goblin as it was.
	launch, err := adapter.Build(harness.LaunchSpec{
		BriefPath:       briefPath,
		TaskTmp:         meta.TaskTmp,
		Scratch:         scratch,
		Model:           target.Model,
		Effort:          target.Effort,
		MCPConfig:       goblinMCPConfig(meta.TaskTmp),
		CodexMCPServers: codexServers,
	})
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: build harness launch: %w; task %s was left running as it was", err, req.ID)
	}

	// Stop before anything else is written, so a harness that refuses to exit
	// leaves the task exactly as it was.
	current, err := s.Harness.Get(harness.Kind(meta.Harness))
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: current harness %q has no adapter: %w", meta.Harness, err)
	}
	from := describe(meta.Harness, meta.Model, meta.Effort)
	// A switch re-injects the same credentials a spawn would, but never
	// refuses on a red service: the goblin is already running, and stranding
	// work in a stopped harness would cost more than the missing credential.
	// The probes run before the stop, so one that fails leaves the harness
	// running, and before any turn, since they can take seconds each.
	preflight, err := s.preflightCredentials(ctx, project)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("%w; task %s was left running as it was", err, req.ID)
	}
	endTurn := func() error { return nil }
	if req.Admit != nil {
		if endTurn, err = s.takeLaunchTurn(ctx, "the relaunch of "+req.ID); err != nil {
			return SwitchResult{}, err
		}
		defer func() {
			if turnErr := endTurn(); turnErr != nil {
				err = errors.Join(err, turnErr)
			}
		}()
		// The start this relaunch can race is a spawn of the same task: it
		// publishes the task's record before its terminal runs and holds its
		// turn until it does. A relaunch that read the record in between, as
		// cfo goblins resume does for every recorded task whose terminal is
		// not running, finds that terminal running once its own turn comes,
		// and must leave it alone.
		if nativeTerminalRuns(s.StateDir, meta.ID) {
			if req.IsResume {
				return SwitchResult{Meta: meta, Resumed: true, Output: "task already resumed " + meta.ID}, nil
			}
			return SwitchResult{}, fmt.Errorf("switch: task %s already runs; another start launched it while this relaunch waited for its turn", req.ID)
		}
		if err := req.Admit(); err != nil {
			return SwitchResult{}, fmt.Errorf("%w: %w", state.ErrNoRoom, err)
		}
	}
	// The look at the task above can be minutes old by now: the credential
	// probes ran since, and a relaunch may have waited for its turn on the
	// spawn lock. What took the task away meanwhile without its record lock,
	// as the teardown of the failed spawn it waited on does, is found here,
	// before anything is stopped, written or started.
	if err := s.stillThere(ctx, git, meta.ID, project, worktreePath); err != nil {
		return SwitchResult{}, err
	}
	// A native terminal ends with its harness, and its job ends everything the
	// harness started, so nothing is left to wait on.
	if err := s.stopNative(ctx, meta.ID, current.Control()); err != nil {
		return SwitchResult{}, err
	}

	launchMeta := meta
	var resumeRecord state.Lifecycle
	meta.ResumeOperation = ""
	if req.IsResume {
		var err error
		resumeRecord, err = state.ReadLifecycle(s.StateDir, meta.ID)
		if err != nil {
			return SwitchResult{}, err
		}
		if resumeRecord.Action != "resume" || resumeRecord.Phase != "resuming" || resumeRecord.Generation != meta.SpawnGen {
			return SwitchResult{}, errors.New("resume controller changed before launching the harness")
		}
		meta.ResumeOperation = resumeRecord.Operation
	}
	// Publish the replacement generation before its first native hook can run.
	if err := s.publishSwitch(&meta, target); err != nil {
		return SwitchResult{}, err
	}
	if req.IsResume {
		resumeRecord.Generation = meta.SpawnGen
		if err := state.WriteLifecycle(s.StateDir, resumeRecord); err != nil {
			return SwitchResult{}, err
		}
	}
	launchMeta.SpawnGen = meta.SpawnGen
	handoff, resumed, nativeHost, err := s.relaunchHarness(ctx, launchMeta, target, adapter, launch, worktreePath, briefPath, dirty, req.ID, manifest.Env, preflight, req, endTurn)
	if err != nil {
		// The failure may have come after the new harness was already
		// running, so the terminal is checked again before it is described.
		// Reporting an empty terminal that actually holds a live harness sends
		// the operator to `cfo switch` again, which would stop a working
		// goblin and lose its context.
		to := describe(string(target.Harness), target.Model, target.Effort)
		recovery := fmt.Sprintf("the native terminal now has no harness: %s was stopped and %s did not start. Work in %s is untouched; start one with `cfo switch %s --harness <h>`",
			from, to, worktreePath, req.ID)
		if nativeTerminalRuns(s.StateDir, meta.ID) {
			recovery = fmt.Sprintf("the native terminal still holds a live %s: it started but the switch did not complete cleanly. Work in %s is untouched. Steer the native terminal directly or inspect it with `cfo peek %s` - do NOT rerun `cfo switch`, which would stop a running harness.",
				to, worktreePath, req.ID)
		}
		err = fmt.Errorf("%w\n%s", err, recovery)
		if appendErr := state.AppendStatus(s.StateDir, req.ID, "failed: "+bounded(state.NormalizeStatusDetail(err.Error()), 1000)); appendErr != nil {
			err = errors.Join(err, appendErr)
		}
		return SwitchResult{Meta: meta, From: from, Handoff: handoff, Resumed: resumed}, err
	}

	result.Handoff = handoff
	result.Resumed = resumed
	line := fmt.Sprintf("switched: %s -> %s", from, describe(meta.Harness, meta.Model, meta.Effort))
	if result.Handoff != "" {
		line += " handoff=" + result.Handoff
	} else {
		line += " (resumed in place)"
	}
	if err := state.AppendStatus(s.StateDir, req.ID, line); err != nil {
		return SwitchResult{}, fmt.Errorf("switch: record switch: %w", err)
	}

	result.Meta = meta
	result.From = from
	result.Output = fmt.Sprintf("switched %s %s -> %s worktree=%s window=%s", req.ID, from, describe(meta.Harness, meta.Model, meta.Effort), worktreePath, meta.Window)
	if result.Handoff != "" {
		result.Output += "\nhandoff " + result.Handoff
	}
	if notice := containedNotice(nativeHost); notice != "" {
		result.Output += "\n" + notice
	}
	return result, nil
}

// relaunchHarness injects credentials into the target's launch, writes the
// resume instruction or handoff, and starts the new harness, ending the
// relaunch's turn once its host runs. Every step after the old harness has
// stopped lives here, so any failure returns through the same empty-terminal
// recovery. Anything knowable before the stop is resolved by Switch and handed
// in: the built launch, the project's redirects and its credentials.
func (s Service) relaunchHarness(ctx context.Context, meta state.TaskMeta, target switchTarget, adapter harness.Adapter, launch harness.Launch, worktreePath, briefPath, dirty, id string, redirects map[string]string, preflight auth.Result, request SwitchRequest, endTurn func() error) (handoff string, resumed bool, nativeHost host.Record, err error) {
	resumed = target.Harness == harness.Kind(meta.Harness) && len(adapter.Control().ResumeArgs) > 0 && request.ResumeSession != ""
	if request.IsResume {
		resumed = request.ResumeSession != "" && (target.Harness == harness.Claude || target.Harness == harness.Codex)
	}
	launch.Dir = worktreePath
	// The launch is rebuilt from scratch, so the project's declared
	// environment redirects have to be re-applied or the new harness runs
	// without the caches the spawned one had.
	mergeProvisionEnv(launch.Env, redirects)
	mergeProvisionEnv(launch.Env, preflight.Caches)
	nativeEnvironment(launch.Env, meta)

	if resumed {
		launch.Env["CFO_PARENT_SESSION_ID"], launch.Env["CFO_PARENT_HARNESS"] = "", ""
		resumeArgs := []string{"--resume", request.ResumeSession}
		if target.Harness == harness.Codex {
			resumeArgs = []string{"resume", request.ResumeSession}
		}
		// ResumeArgs lead because codex takes its resume as a subcommand.
		launch.Args = append(append([]string{}, resumeArgs...), launch.Args...)
		launch.Instruction = resumeInstruction(meta, target)
		launch.Resumed = true
	} else {
		handoff, err = s.writeHandoff(ctx, meta, target, worktreePath, briefPath, dirty)
		if err != nil {
			return "", false, host.Record{}, err
		}
		launch.Instruction = handoffInstruction(handoff, briefPath, meta)
	}
	if request.IsResume && request.ResumeHandoff != "" {
		launch.Instruction += " Read the retained pause handoff at " + request.ResumeHandoff + "."
	}
	if request.ResumeNote != "" {
		launch.Instruction += "\n" + request.ResumeNote
	}
	if meta.PipelineHash != "" {
		launch.Instruction += " Continue with the frozen pipeline policy at " + filepath.Join(meta.TaskTmp, "pipeline.json") + "; use cfo pipeline run/respond for this task. Do not reset review budgets or bypass them with native AXI."
	}

	// Its credentials ride its terminal's environment, as its spawn gave them.
	userEnv, err := s.userEnvironment()
	if err != nil {
		return handoff, resumed, host.Record{}, fmt.Errorf("switch: read the user's environment: %w", err)
	}
	if nativeHost, err = s.launchNativeHost(id, target.Harness, launch, userEnv, preflight.Env); err != nil {
		return handoff, resumed, nativeHost, err
	}
	// Its release error, if any, reaches Switch's caller with the result.
	_ = endTurn()
	return handoff, resumed, nativeHost, s.briefNativeHarness(ctx, id, nativeHost, target.Harness, launch)
}

// switchTarget is the harness, model, and effort the task should run after
// the switch.
type switchTarget struct {
	Harness harness.Kind
	Model   string
	Effort  string
}

func (t switchTarget) same(meta state.TaskMeta) bool {
	return string(t.Harness) == meta.Harness &&
		valueOrDefault(t.Model) == valueOrDefault(meta.Model) &&
		valueOrDefault(t.Effort) == valueOrDefault(meta.Effort)
}

// requestedTarget fills the request's blanks from the task's current values,
// so `--model` alone keeps the harness it is already running.
//
// Neither a model name nor an effort survives a change of harness - "opus"
// means nothing to codex, and pi may lack an effort claude has - so changing
// harness without naming them resets both to the new harness's defaults
// rather than carrying values the new harness cannot honour. An effort the
// operator still passes explicitly is refused loudly when its launch is
// built.
func requestedTarget(meta state.TaskMeta, req SwitchRequest) switchTarget {
	target := switchTarget{Harness: req.Harness, Model: req.Model, Effort: req.Effort}
	if target.Harness == "" {
		target.Harness = harness.Kind(meta.Harness)
	}
	if target.Model == "" && target.Harness == harness.Kind(meta.Harness) {
		target.Model = meta.Model
	}
	if target.Effort == "" && target.Harness == harness.Kind(meta.Harness) {
		target.Effort = meta.Effort
	}
	if model := harness.DefaultModel(target.Harness); model != "" && req.Model == "" && (target.Model == "" || target.Model == "default") {
		target.Model = model
	}
	return target
}

func (s Service) publishSwitch(meta *state.TaskMeta, target switchTarget) error {
	meta.Harness = string(target.Harness)
	meta.Model = valueOrDefault(target.Model)
	meta.Effort = valueOrDefault(target.Effort)
	// A new spawn generation is what tells the watcher and the hooks that the
	// terminal's harness is a different process than the one they last
	// observed.
	meta.SpawnGen = fmt.Sprintf("s%d", time.Now().UTC().UnixNano())
	if err := state.WriteTaskMeta(s.StateDir, *meta); err != nil {
		return fmt.Errorf("switch: publish task metadata: %w", err)
	}
	return nil
}

// stillThere proves a task a relaunch looked at earlier is still one: its
// record is there and its worktree still validates.
func (s Service) stillThere(ctx context.Context, git worktree.Git, id, project, worktreePath string) error {
	const leftAlone = "so nothing was written and no terminal was started"
	if _, err := state.ReadTaskMeta(s.StateDir, id); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("switch: task %s is gone, its record retired since this relaunch looked at it, %s", id, leftAlone)
	} else if err != nil {
		return fmt.Errorf("switch: read task metadata again: %w", err)
	}
	if err := worktree.Validate(ctx, git, project, worktreePath); err != nil {
		return fmt.Errorf("switch: task %s is gone, its worktree removed since this relaunch looked at it, %s: %w", id, leftAlone, err)
	}
	return nil
}

func switchLockName(id string) string {
	return ".switch-" + id + ".lock"
}

// worktreeStatus returns the porcelain status of the worktree, empty when it
// is clean.
func (s Service) worktreeStatus(ctx context.Context, worktree string) (string, error) {
	runner := s.commands()
	if runner == nil {
		return "", errors.New("switch: command runner is required")
	}
	result, err := runner.Run(ctx, execx.Request{
		Dir:  worktree,
		Name: "git",
		Args: []string{"status", "--porcelain=v1", "--untracked-files=all"},
	})
	if err != nil {
		return "", fmt.Errorf("switch: inspect worktree status: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("switch: git status exited with code %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func (s Service) commands() execx.Runner {
	if s.Commands != nil {
		return s.Commands
	}
	return s.Worktrees.Commands
}

// writeHandoff records everything the new harness needs to pick the task up
// without the old harness's context: where the brief is, what branch the work
// is on, what is committed, and what the goblin last reported.
func (s Service) writeHandoff(ctx context.Context, meta state.TaskMeta, target switchTarget, worktree, briefPath, dirty string) (string, error) {
	var note strings.Builder
	fmt.Fprintf(&note, "# Handoff for %s\n\n", meta.ID)
	fmt.Fprintf(&note, "You are taking over a task in progress. The previous harness (%s) was stopped and you were started in its place as %s. Its conversation is gone; this note is what survives.\n\n",
		describe(meta.Harness, meta.Model, meta.Effort), describe(string(target.Harness), target.Model, target.Effort))

	fmt.Fprintf(&note, "## Read first\n\n")
	if briefPath != "" {
		fmt.Fprintf(&note, "The original brief is at %s. It is still the task; nothing about it changed.\n\n", briefPath)
	} else {
		fmt.Fprintf(&note, "The original brief path was not recorded. Ask the CFO for it before assuming the task.\n\n")
	}

	fmt.Fprintf(&note, "## Where the work is\n\n")
	fmt.Fprintf(&note, "- Worktree: %s\n", worktree)
	fmt.Fprintf(&note, "- Project: %s\n", meta.Project)
	// A detached worktree answers "HEAD" to --abbrev-ref, which reads as a
	// branch named HEAD. Say what it actually is instead.
	switch branch := s.gitLine(ctx, worktree, "rev-parse", "--abbrev-ref", "HEAD"); branch {
	case "":
	case "HEAD":
		fmt.Fprintf(&note, "- Branch: none (detached HEAD) - create one before committing\n")
	default:
		fmt.Fprintf(&note, "- Branch: %s\n", branch)
	}
	if head := s.gitLine(ctx, worktree, "rev-parse", "HEAD"); head != "" {
		fmt.Fprintf(&note, "- HEAD: %s\n", head)
	}
	note.WriteString("\n")

	if log := s.gitOutput(ctx, worktree, "log", "--oneline", "-10"); log != "" {
		fmt.Fprintf(&note, "## Commits so far\n\n```\n%s\n```\n\n", log)
	}

	if dirty != "" {
		fmt.Fprintf(&note, "## Uncommitted changes\n\nThis switch was made with --force-dirty, so the worktree deliberately carries uncommitted work. It is intentional; do not revert or stash it.\n\n```\n%s\n```\n\n", dirty)
	} else {
		note.WriteString("## Uncommitted changes\n\nNone. The worktree was clean at the switch, so everything below the last commit is already recorded.\n\n")
	}

	if status := s.recentStatus(meta.ID); status != "" {
		fmt.Fprintf(&note, "## What the previous goblin reported\n\n```\n%s\n```\n\n", status)
	}

	note.WriteString("## What to do\n\nContinue the task from here. Re-read the brief, check the branch state above against it, and carry on. Do not restart work that is already committed, and do not open a second branch or PR for this task.\n")

	path := filepath.Join(meta.TaskTmp, fmt.Sprintf("handoff-%d.md", time.Now().UTC().Unix()))
	if err := os.MkdirAll(meta.TaskTmp, 0o755); err != nil {
		return "", fmt.Errorf("switch: create task temporary directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(note.String()), 0o600); err != nil {
		return "", fmt.Errorf("switch: write handoff note: %w", err)
	}
	return path, nil
}

func handoffInstruction(handoff, briefPath string, meta state.TaskMeta) string {
	instruction := "You are taking over a task in progress. Read the handoff at " + handoff + " first"
	if briefPath != "" {
		instruction += ", then the brief at " + briefPath
	}
	return instruction + ", then continue the work. A question you asked the CFO before the restart was cancelled with it: if you were waiting on an answer, ask it again with cfo notify --blocked." + notifyInstruction(meta)
}

func resumeInstruction(meta state.TaskMeta, target switchTarget) string {
	return fmt.Sprintf("Your session was restarted as %s (was %s). Your prior context is intact; continue the task where you left off. A question you asked the CFO before the restart was cancelled with it: if you were waiting on an answer, ask it again with cfo notify --blocked.",
		describe(string(target.Harness), target.Model, target.Effort), describe(meta.Harness, meta.Model, meta.Effort)) + notifyInstruction(meta)
}

// recentStatus returns the tail of the task's status log, which is the only
// record of the previous goblin's own reporting.
func (s Service) recentStatus(id string) string {
	data, err := fsx.ReadFile(filepath.Join(s.StateDir, id+".status"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	if len(lines) > handoffLines {
		lines = lines[len(lines)-handoffLines:]
	}
	return strings.Join(lines, "\n")
}

func (s Service) gitOutput(ctx context.Context, dir string, args ...string) string {
	runner := s.commands()
	if runner == nil {
		return ""
	}
	result, err := runner.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(string(result.Stdout))
}

func (s Service) gitLine(ctx context.Context, dir string, args ...string) string {
	line, _, _ := strings.Cut(s.gitOutput(ctx, dir, args...), "\n")
	return strings.TrimSpace(line)
}

func describe(harnessName, model, effort string) string {
	return fmt.Sprintf("%s/%s/%s", harnessName, valueOrDefault(model), valueOrDefault(effort))
}
