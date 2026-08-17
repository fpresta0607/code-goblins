package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
)

const (
	stopPoll     = 1500 * time.Millisecond
	stopTries    = 20
	handoffLines = 20
)

// SwitchRequest changes one running goblin's harness, model, or effort in
// place: same task id, same worktree, same pane. An empty field keeps what
// the task already has.
type SwitchRequest struct {
	ID         string
	Harness    harness.Kind
	Model      string
	Effort     string
	ForceDirty bool
	Session    string
	// BriefPath is the fallback for a task whose metadata predates the brief
	// field.
	BriefPath string
}

// SwitchResult reports what the switch changed.
type SwitchResult struct {
	Meta    state.TaskMeta
	From    string
	Handoff string
	Resumed bool
	Output  string
}

// Switch stops the goblin's current harness in its own pane, then starts the
// requested one in the same worktree. Nothing is torn down: the tab, the
// worktree, the branch, and the task id all survive, so committed work and an
// open PR are untouched by construction.
//
// A same-harness switch resumes through the harness's own session continuation
// when it has one. A cross-harness switch cannot, so it writes a handoff note
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
		"switch session", req.Session,
	); err != nil {
		return SwitchResult{}, err
	}
	if s.Herdr == nil {
		return SwitchResult{}, errors.New("switch: Herdr client is required")
	}

	meta, err := state.ReadTaskMeta(s.StateDir, req.ID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: read task metadata: %w", err)
	}
	if meta.Backend != "herdr" {
		return SwitchResult{}, fmt.Errorf("switch: task %s is not a Herdr task (backend %q)", req.ID, meta.Backend)
	}
	for name, value := range map[string]string{
		"herdr_session": meta.HerdrSession,
		"herdr_pane_id": meta.HerdrPaneID,
		"worktree":      meta.Worktree,
		"project":       meta.Project,
		"tasktmp":       meta.TaskTmp,
	} {
		if value == "" {
			return SwitchResult{}, fmt.Errorf("switch: task %s metadata is missing %s", req.ID, name)
		}
	}

	target := requestedTarget(meta, req)
	if target == (switchTarget{}) || target.same(meta) {
		return SwitchResult{}, fmt.Errorf("switch: task %s already runs harness=%s model=%s effort=%s; nothing to switch", req.ID, meta.Harness, valueOrDefault(meta.Model), valueOrDefault(meta.Effort))
	}
	adapter, err := s.Harness.Get(target.Harness)
	if err != nil {
		return SwitchResult{}, err
	}

	herdrClient := *s.Herdr
	herdrClient.Session = meta.HerdrSession
	if req.Session != "" {
		herdrClient.Session = req.Session
	}
	paneTarget := herdr.Target{Session: herdrClient.Session, Pane: meta.HerdrPaneID}

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
	worktree, err := fsx.Canonical(meta.Worktree)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: canonicalize worktree %q: %w", meta.Worktree, err)
	}
	if fsx.SamePath(worktree, project) {
		return SwitchResult{}, fmt.Errorf("switch: worktree %q is the primary checkout", worktree)
	}
	git, err := s.treehouseGit()
	if err != nil {
		return SwitchResult{}, err
	}
	if err := treehouse.Validate(ctx, git, project, worktree); err != nil {
		return SwitchResult{}, fmt.Errorf("switch: validate worktree: %w", err)
	}

	dirty, err := s.worktreeStatus(ctx, worktree)
	if err != nil {
		return SwitchResult{}, err
	}
	if dirty != "" && !req.ForceDirty {
		return SwitchResult{}, fmt.Errorf("switch: worktree %q has uncommitted changes; commit them or rerun with --force-dirty:\n%s", worktree, dirty)
	}

	if err := adapter.Validate(ctx, herdrClient.Commands); err != nil {
		return SwitchResult{}, fmt.Errorf("switch: validate harness %s: %w", target.Harness, err)
	}

	// Stop before anything else is written, so a harness that refuses to exit
	// leaves the task exactly as it was.
	current, err := s.Harness.Get(harness.Kind(meta.Harness))
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: current harness %q has no adapter: %w", meta.Harness, err)
	}
	if err := s.stopHarness(ctx, &herdrClient, paneTarget, current.Control()); err != nil {
		return SwitchResult{}, err
	}

	resume := target.Harness == harness.Kind(meta.Harness) && len(adapter.Control().ResumeArgs) > 0
	briefPath := meta.Brief
	if briefPath == "" {
		briefPath = req.BriefPath
	}

	launch, err := adapter.Build(harness.LaunchSpec{
		BriefPath: briefPath,
		TaskTmp:   meta.TaskTmp,
		Model:     target.Model,
		Effort:    target.Effort,
	})
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch: build harness launch: %w", err)
	}
	launch.Dir = worktree
	if _, err := s.injectProjectCredentials(ctx, project, meta.TaskTmp, &launch); err != nil {
		return SwitchResult{}, err
	}

	if resume {
		// ResumeArgs lead because codex takes its resume as a subcommand.
		launch.Args = append(append([]string{}, adapter.Control().ResumeArgs...), launch.Args...)
		launch.Instruction = resumeInstruction(meta, target)
	} else {
		handoff, err := s.writeHandoff(ctx, meta, target, worktree, briefPath, dirty)
		if err != nil {
			return SwitchResult{}, err
		}
		result.Handoff = handoff
		launch.Instruction = handoffInstruction(handoff, briefPath)
	}

	if _, err := s.startHarness(ctx, &herdrClient, paneTarget, launchPlan{
		AgentName: "gb-" + req.ID,
		Harness:   target.Harness,
		Launch:    launch,
	}); err != nil {
		// The old harness is already stopped, so the metadata is corrected to
		// the harness that is actually installed and selected before the
		// failure is reported: leaving it naming the stopped harness would
		// make `cfo send` type the wrong submit key at the pane.
		if writeErr := s.publishSwitch(&meta, target); writeErr != nil {
			err = errors.Join(err, writeErr)
		}
		return SwitchResult{Meta: meta, From: meta.Harness}, err
	}

	from := describe(meta.Harness, meta.Model, meta.Effort)
	if err := s.publishSwitch(&meta, target); err != nil {
		return SwitchResult{}, err
	}
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
	result.Resumed = resume
	result.Output = fmt.Sprintf("switched %s %s -> %s worktree=%s window=%s", req.ID, from, describe(meta.Harness, meta.Model, meta.Effort), worktree, meta.Window)
	if result.Handoff != "" {
		result.Output += "\nhandoff " + result.Handoff
	}
	return result, nil
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
func requestedTarget(meta state.TaskMeta, req SwitchRequest) switchTarget {
	target := switchTarget{Harness: req.Harness, Model: req.Model, Effort: req.Effort}
	if target.Harness == "" {
		target.Harness = harness.Kind(meta.Harness)
	}
	if target.Model == "" {
		target.Model = meta.Model
	}
	if target.Effort == "" {
		target.Effort = meta.Effort
	}
	return target
}

func (s Service) publishSwitch(meta *state.TaskMeta, target switchTarget) error {
	meta.Harness = string(target.Harness)
	meta.Model = valueOrDefault(target.Model)
	meta.Effort = valueOrDefault(target.Effort)
	// A new spawn generation is what tells the watcher and the hooks that the
	// pane's agent is a different process than the one they last observed.
	meta.SpawnGen = fmt.Sprintf("s%d", time.Now().UTC().UnixNano())
	if err := state.WriteTaskMeta(s.StateDir, *meta); err != nil {
		return fmt.Errorf("switch: publish task metadata: %w", err)
	}
	return nil
}

func switchLockName(id string) string {
	return ".switch-" + id + ".lock"
}

// stopHarness asks the harness to exit on its own terms and proves it did:
// Herdr reporting no agent on the pane is the shell prompt being back. A
// harness that ignores its own exit command is interrupted once rather than
// left running beside its replacement.
func (s Service) stopHarness(ctx context.Context, client *herdr.Client, target herdr.Target, control harness.Control) error {
	status, err := client.AgentStatus(ctx, target)
	if err != nil {
		return fmt.Errorf("switch: read agent status: %w", err)
	}
	if status == herdr.AgentMissing {
		return fmt.Errorf("switch: pane %s no longer exists; the task's tab was closed", target.Pane)
	}
	if status == herdr.AgentDead {
		// Nothing is running: an erroring or already-exited harness is the
		// common reason to switch, so this is a normal entry point.
		return nil
	}

	for _, key := range control.StopKeys {
		if err := client.SendKey(ctx, target, key); err != nil {
			return fmt.Errorf("switch: send %s before stopping the harness: %w", key, err)
		}
		if err := s.sleep(ctx, launchSettle); err != nil {
			return fmt.Errorf("switch: wait between stop keys: %w", err)
		}
	}
	if control.StopCommand != "" {
		if err := client.SendLiteral(ctx, target, control.StopCommand); err != nil {
			return fmt.Errorf("switch: type the harness stop command: %w", err)
		}
		if err := s.sleep(ctx, launchSettle); err != nil {
			return fmt.Errorf("switch: wait before submitting the stop command: %w", err)
		}
		if err := client.SendKey(ctx, target, "Enter"); err != nil {
			return fmt.Errorf("switch: submit the harness stop command: %w", err)
		}
	}

	if stopped, err := s.waitForStop(ctx, client, target, stopTries/2); err != nil || stopped {
		return err
	}
	if err := client.SendKey(ctx, target, "Ctrl-C"); err != nil {
		return fmt.Errorf("switch: interrupt the harness after it ignored %q: %w", control.StopCommand, err)
	}
	stopped, err := s.waitForStop(ctx, client, target, stopTries)
	if err != nil {
		return err
	}
	if !stopped {
		return fmt.Errorf("switch: harness on pane %s is still running after %q and an interrupt; refusing to start a second one beside it", target.Pane, control.StopCommand)
	}
	return nil
}

func (s Service) waitForStop(ctx context.Context, client *herdr.Client, target herdr.Target, tries int) (bool, error) {
	for attempt := 0; attempt < tries; attempt++ {
		if err := s.sleep(ctx, stopPoll); err != nil {
			return false, fmt.Errorf("switch: wait for the harness to exit: %w", err)
		}
		status, err := client.AgentStatus(ctx, target)
		if err != nil {
			// A momentarily unreadable pane during shutdown is expected, so
			// the poll keeps going rather than failing the switch.
			continue
		}
		if status == herdr.AgentDead || status == herdr.AgentMissing {
			return true, nil
		}
	}
	return false, nil
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
	if s.Herdr != nil && s.Herdr.Commands != nil {
		return s.Herdr.Commands
	}
	return s.Treehouse.Commands
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
	if branch := s.gitLine(ctx, worktree, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
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

func handoffInstruction(handoff, briefPath string) string {
	instruction := "You are taking over a task in progress. Read the handoff at " + handoff + " first"
	if briefPath != "" {
		instruction += ", then the brief at " + briefPath
	}
	return instruction + ", then continue the work."
}

func resumeInstruction(meta state.TaskMeta, target switchTarget) string {
	return fmt.Sprintf("Your session was restarted as %s (was %s). Your prior context is intact; continue the task where you left off.",
		describe(string(target.Harness), target.Model, target.Effort), describe(meta.Harness, meta.Model, meta.Effort))
}

// recentStatus returns the tail of the task's status log, which is the only
// record of the previous goblin's own reporting.
func (s Service) recentStatus(id string) string {
	data, err := os.ReadFile(filepath.Join(s.StateDir, id+".status"))
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
