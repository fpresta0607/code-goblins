package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func defaultTaskLifecycle(ctx context.Context, h home.Home, request lifecycle.Request, revision string) (state.Lifecycle, error) {
	meta, err := state.ReadTaskMeta(h.State, request.ID)
	if errors.Is(err, os.ErrNotExist) && request.Action == "stop" {
		return lifecycle.StopQueued(h, request, revision)
	}
	if err != nil {
		return state.Lifecycle{}, err
	}
	// A pause keeps the goblin's conversation for its resume, which goes on
	// in it rather than from a handoff.
	if request.Session, err = taskSession(h.State, meta); err != nil {
		return state.Lifecycle{}, err
	}
	root, err := pipeline.DefaultRoot()
	if err != nil {
		return state.Lifecycle{}, err
	}
	commands := gatedb.Runner{Next: execx.OSRunner{}}
	gate := pipeline.Reader{Root: root, Commands: commands}
	runtime := defaultCommandRuntime()
	var resources lifecycle.Resources
	admit := func() error {
		memory, err := supervisor.MachineMemory()
		if err != nil {
			return err
		}
		disk, err := supervisor.MachineDisk(h)
		if err != nil {
			return fmt.Errorf("free disk cannot be read, so nothing resumes: %w", err)
		}
		return supervisor.CheckLaunch(memory, disk)
	}
	service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Helpers: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) ([]string, error) {
			return reachHelpers(ctx, h, meta, record, runtime.taskLifecycle)
		},
		Prepare:  pauseInstruction(runtime, h),
		Handoff:  lifecycle.HandoffWriter{StateDir: h.State, Commands: commands, GitHub: runtime.githubAPI, Owner: lifecycle.OwnerOf(h)}.Handoff,
		Withdraw: pauseWithdrawal(runtime, h),
		Stop: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) ([]string, error) {
			var stopped []string
			var err error
			resources, stopped, err = lifecycle.StopTask(ctx, h, meta, gate, record)
			return stopped, err
		},
		Checkpoint: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) error {
			if resources.Gate.ID == "" || resources.Gate.IsTerminal() || record.Action == "pause" {
				return nil
			}
			preserved, err := gate.Interrupt(ctx, meta.Project, meta.Worktree, resources.Gate.Branch, resources.Gate.ID)
			if err != nil {
				return err
			}
			record.GateHead = preserved.Head
			record.Kept = append(record.Kept, "gate commits "+preserved.Head+"; Resume restores the contributor with the retained validation context")
			if preserved.WorktreeEdits != "" {
				record.Kept = append(record.Kept, "gate worktree edits "+preserved.WorktreeEdits+" under "+pipeline.WorktreeRef(preserved.ID)+"; restore them with git cherry-pick -n "+preserved.WorktreeEdits)
			}
			record.Stopped = append(record.Stopped, "validation run "+preserved.ID)
			return nil
		},
		Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
			return resumeTask(ctx, h, runtime, commands, gate, meta, prior, admit)
		},
		IsRunning: func(ctx context.Context, meta state.TaskMeta) (bool, error) {
			if meta.Backend == "native" {
				record, err := host.ReadRecord(h.State, meta.ID)
				if errors.Is(err, os.ErrNotExist) {
					return false, nil
				}
				if err != nil || !host.Running(record) {
					return false, err
				}
				client, err := host.Dial(record)
				if err != nil {
					return false, err
				}
				if err := client.Close(); err != nil {
					return false, err
				}
				return host.Running(record), nil
			}
			client := &herdr.Client{Commands: commands, Session: meta.HerdrSession}
			status, err := client.AgentStatus(ctx, herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID})
			return status == herdr.AgentAlive, err
		},
		Archive: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) (lifecycle.Preservation, error) {
			holder := ""
			if meta.Parent != "" {
				if parent, err := state.ReadTaskMeta(h.State, meta.Parent); err == nil {
					holder = parent.Worktree
				}
			}
			preserved, err := lifecycle.PreserveWork(ctx, commands, meta, holder)
			if err != nil {
				return preserved, err
			}
			record.Kept = preserved.Kept
			if err := state.WriteLifecycle(h.State, *record); err != nil {
				return preserved, err
			}
			if queued, err := fleet.ReadQueuedTask(h, meta.ID); err == nil {
				if err := fleet.RemoveQueuedTask(h, meta.ID, queued.Revision); err != nil {
					return preserved, err
				}
			} else if !errors.Is(err, fleet.ErrNotQueued) && !errors.Is(err, os.ErrNotExist) {
				return preserved, err
			}
			_, err = runtime.cleanup(ctx, h, meta.ID, !preserved.CanRemove)
			return preserved, err
		},
		Memory: func() (uint64, uint64, error) {
			memory, err := supervisor.MachineMemory()
			return memory.Available, memory.CommitAvailable, err
		},
		Admit: admit,
		Notify: func(record state.Lifecycle) error {
			return lifecycle.Report(h.State, record)
		},
		HandOver: func(meta state.TaskMeta, prior state.Lifecycle) (bool, error) {
			return supervisor.HandOverReplacedNote(h.State, meta, prior)
		},
		HandOverRelaunched: func(prior state.Lifecycle) (bool, error) {
			return supervisor.HandOverRelaunchedNote(h.State, prior)
		},
		HandOverEnded: func(meta state.TaskMeta, prior state.Lifecycle, action string) (bool, error) {
			return supervisor.HandOverEndedNote(h.State, meta, prior, action)
		},
	}}
	return service.Run(ctx, request)
}

// resumeTask relaunches a paused task in place. admit is the machine's room,
// which the relaunch checks again under the home's spawn lock, around its
// terminal's launch only.
func resumeTask(ctx context.Context, h home.Home, runtime commandRuntime, commands execx.Runner, gate pipeline.Reader, meta state.TaskMeta, prior state.Lifecycle, admit func() error) error {
	if meta.Backend != "native" {
		return fmt.Errorf("resume: task %s runs in backend %q; only a task in a native terminal can resume; retire it with goblins cleanup %s --force-archive", meta.ID, meta.Backend, meta.ID)
	}
	choice, choiceErr := state.ReadEngineChoice(h.State, meta.ID)
	if choiceErr != nil && !errors.Is(choiceErr, os.ErrNotExist) {
		return choiceErr
	}
	hasChoice := choiceErr == nil && choice.Generation == meta.SpawnGen
	var retained pipeline.InterruptedRun
	if prior.GateRun != "" {
		branch, err := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"symbolic-ref", "--short", "HEAD"}})
		if err != nil || branch.ExitCode != 0 {
			return errors.New("cannot read the paused validation branch")
		}
		branchName := strings.TrimSpace(string(branch.Stdout))
		retained, err = gate.ResumeInterrupted(ctx, meta.Project, pipeline.InterruptedRun{ID: prior.GateRun, Branch: branchName, Intent: prior.GateIntent})
		// A direct-PR task may now be on a branch with no gate of its own.
		if err != nil && !(meta.Mode == "direct-PR" && errors.Is(err, pipeline.ErrNoProgress)) {
			return err
		}
	} else {
		current, err := lifecycle.TaskGate(ctx, meta, gate)
		if err != nil {
			return err
		}
		if current.ID != "" {
			return errors.New("the paused task has no saved validation identity; inspect its original run before resuming")
		}
	}
	handoff := ""
	if prior.HandoffSaved {
		handoff = prior.Handoff
	}
	session := prior.Session
	pausedAt := prior.Started
	if prior.Pause != nil {
		pausedAt = prior.Pause.At
	}
	if pausedAt.IsZero() || time.Since(pausedAt) >= 24*time.Hour || pausedAt.After(time.Now()) {
		session = ""
	}
	// A resume falls back to the task's brief under the home exactly as a switch
	// and a comeback do, so a task whose record names none still reaches its
	// relaunch with a real path and the worker is not sent to a relative one.
	briefPath := meta.Brief
	if briefPath == "" && h.Data != "" {
		briefPath = filepath.Join(h.Data, meta.ID, "brief.md")
	}
	request := spawn.SwitchRequest{ID: meta.ID, Generation: meta.SpawnGen, ForceDirty: true, BriefPath: briefPath, IsResume: true, ResumeSession: session, ResumeHandoff: handoff, ResumeNote: prior.ResumeNote, Admit: admit}
	if retained.ID != "" {
		told := "Your retained validation run " + retained.ID + " is " + retained.Status + ". Resuming your terminal starts no validation and returns no branch custody; inspect its supported source-bound continuation before requesting validation, and never replace this run or reset its spent allowance."
		if !retained.IsTerminal() {
			told = "Your validation run " + retained.ID + " is still running; inspect its status and carry on from that original run, and start no other run beside it."
		}
		request.ResumeNote = strings.TrimPrefix(request.ResumeNote+"\n"+told, "\n")
	}
	if hasChoice {
		request.Harness, request.Model, request.Effort = harness.Kind(choice.Harness), choice.Model, choice.Effort
		if choice.Harness != meta.Harness {
			request.ResumeSession = ""
		}
	}
	_, err := runtime.switchTask(ctx, h, request)
	if err == nil && hasChoice {
		return state.RemoveEngineChoice(h.State, meta.ID)
	}
	return err
}

// pauseInstruction types the instruction to write its handoff file into the
// goblin of a task being paused. A goblin in a turn takes it at its next tool
// call, so the pause then waits for the handoff as for any delivered one.
func pauseInstruction(runtime commandRuntime, h home.Home) func(context.Context, state.TaskMeta, string) error {
	return func(ctx context.Context, meta state.TaskMeta, handoff string) error {
		if err := runtime.sendText(ctx, h, meta.ID, lifecycle.PauseInstruction(handoff)); !errors.Is(err, fleet.ErrQueuedForToolCall) {
			return err
		}
		return nil
	}
}

// pauseWithdrawal types into the goblin of a pause that failed while it ran
// that it is not paused. A goblin in a turn takes it after the instruction
// queued ahead of it.
func pauseWithdrawal(runtime commandRuntime, h home.Home) func(context.Context, state.TaskMeta) error {
	return func(ctx context.Context, meta state.TaskMeta) error {
		if err := runtime.sendText(ctx, h, meta.ID, lifecycle.PAUSE_WITHDRAWAL); !errors.Is(err, fleet.ErrQueuedForToolCall) {
			return err
		}
		return nil
	}
}

// endLeftProcesses ends what a task's closed terminal left running, for the
// cleanup that closed it.
func endLeftProcesses(ctx context.Context, h home.Home, meta state.TaskMeta) ([]string, error) {
	return lifecycle.EndLeft(ctx, h, meta)
}

// openGateRun names the task's gate run while it is still open, for the
// cleanup that refuses to retire a task around it. Before either run is read,
// a task whose project checkout is gone, or whose gate holds no database,
// names no run, as lifecycle.TaskGate rules for the stop: its kill can abort
// nothing, and its own retirement must still go through (issue #1578). The
// run a pause saved is read by its id, whatever state the worktree is in,
// since a pause leaves its run running and a worktree gone or no longer a Git
// worktree names no branch to find it by. The worktree's branch is read as
// well, and only while the folder is still a Git worktree, since a goblin
// that runs again, as after a resume or a failed pause, can start a newer run
// there; a folder gone or no longer one with no saved run retires unread.
// Either run still open names it.
func openGateRun(h home.Home) func(context.Context, state.TaskMeta) (string, error) {
	return func(ctx context.Context, meta state.TaskMeta) (string, error) {
		root, err := pipeline.DefaultRoot()
		if err != nil {
			return "", err
		}
		gate := pipeline.Reader{Root: root, Commands: gatedb.Runner{Next: execx.OSRunner{}}}
		record, err := state.ReadLifecycle(h.State, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if _, statErr := os.Stat(meta.Project); errors.Is(statErr, os.ErrNotExist) {
			return "", nil
		}
		if _, statErr := os.Stat(filepath.Join(root, "state.sqlite")); errors.Is(statErr, os.ErrNotExist) {
			return "", nil
		}
		if err == nil && record.GateRun != "" {
			run, err := gate.Run(ctx, record.GateRun)
			if err != nil && !errors.Is(err, pipeline.ErrNoProgress) {
				return "", err
			}
			if err == nil && !run.IsTerminal() {
				return run.RunID, nil
			}
		}
		isWorktree, err := isGitWorktree(ctx, gate.Commands, meta.Worktree)
		if err != nil || !isWorktree {
			return "", err
		}
		run, err := lifecycle.TaskGate(ctx, meta, gate)
		if err != nil || run.ID == "" || run.IsTerminal() {
			return "", err
		}
		return run.ID, nil
	}
}

// isGitWorktree reports whether dir is still a Git worktree: it holds the
// .git link Git reads it by, and Git still resolves that link. A folder gone,
// one whose link went, or one whose link names a worktree Git pruned is none.
func isGitWorktree(ctx context.Context, commands execx.Runner, dir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"rev-parse", "--git-dir"}})
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		if stderr := string(result.Stderr); strings.Contains(stderr, "not a git repository") || strings.Contains(stderr, "gitfile does not point to a valid repository") {
			return false, nil
		}
		return false, fmt.Errorf("read whether %s is still a Git worktree: git exited %d: %s", dir, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return true, nil
}
