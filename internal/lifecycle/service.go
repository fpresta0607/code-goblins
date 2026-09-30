package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type Request struct {
	ID         string
	Generation string
	Operation  string
	Action     string
	Reason     string
	Session    string
}

type Operations struct {
	Prepare    func(context.Context, state.TaskMeta, string) error
	Stop       func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error)
	Checkpoint func(context.Context, state.TaskMeta, *state.Lifecycle) error
	Resume     func(context.Context, state.TaskMeta, state.Lifecycle) error
	IsRunning  func(context.Context, state.TaskMeta) (bool, error)
	Archive    func(context.Context, state.TaskMeta, *state.Lifecycle) (Preservation, error)
	Memory     func() (uint64, error)
	Notify     func(state.Lifecycle) error
}

type Service struct {
	StateDir   string
	PauseWait  time.Duration
	Operations Operations
}

func (service Service) Run(ctx context.Context, request Request) (result state.Lifecycle, err error) {
	if err := state.ValidTaskID(request.ID); err != nil {
		return result, err
	}
	if err := state.ValidTaskID(request.Operation); err != nil {
		return result, fmt.Errorf("operation identity: %w", err)
	}
	if request.Action != "pause" && request.Action != "resume" && request.Action != "stop" {
		return result, errors.New("action must be pause, resume or stop")
	}
	lockName := ".lifecycle-" + request.ID + ".lock"
	if _, err := lock.AcquireExclusiveNamed(service.StateDir, lockName); err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(service.StateDir, lockName)) }()
	prior, priorErr := state.ReadLifecycle(service.StateDir, request.ID)
	if priorErr != nil && !errors.Is(priorErr, os.ErrNotExist) {
		return result, priorErr
	}
	if prior.Operation == request.Operation {
		if prior.RequestGeneration != request.Generation || prior.Action != request.Action {
			return result, errors.New("operation identity was already used for another request")
		}
		if prior.Phase == "paused" || prior.Phase == "running" || prior.Phase == "stopped" || prior.Phase == "failed" {
			finished, finishErr := service.finish(prior)
			if prior.Phase == "failed" {
				finishErr = errors.Join(finishErr, errors.New(strings.Join(prior.Problems, "; ")))
			}
			return finished, finishErr
		}
	}
	if request.Action != "resume" {
		metadataLock := state.MetadataLockName(request.ID)
		if _, err := lock.AcquireExclusiveNamed(service.StateDir, metadataLock); err != nil {
			return result, err
		}
		defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(service.StateDir, metadataLock)) }()
	}
	meta, err := state.ReadTaskMeta(service.StateDir, request.ID)
	if err != nil {
		return result, err
	}
	isSameResume := request.Action == "resume" && prior.Action == "resume" && prior.Phase == "resuming" && prior.Operation == request.Operation && meta.ResumeOperation == request.Operation
	if meta.SpawnGen != request.Generation && !isSameResume {
		return result, errors.New("task session changed; refresh before trying again")
	}
	if request.Action == "pause" && prior.Generation == meta.SpawnGen && prior.Phase == "paused" {
		return service.finish(prior)
	}
	isAlreadyRunning := false
	if request.Action == "resume" {
		isInterruptedResume := prior.Action == "resume" && (prior.Phase == "resuming" || prior.Phase == "failed") && meta.ResumeOperation == prior.Operation
		if prior.Generation != meta.SpawnGen && !isInterruptedResume || prior.Phase != "paused" && !(prior.Action == "resume" && (prior.Phase == "failed" || prior.Phase == "resuming")) {
			return result, errors.New("only a paused task can resume")
		}
		if _, err := lock.AcquireExclusiveNamed(service.StateDir, ".spawn.lock"); err != nil {
			return result, fmt.Errorf("another task is starting: %w", err)
		}
		defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(service.StateDir, ".spawn.lock")) }()
		if isInterruptedResume && service.Operations.IsRunning != nil {
			isAlreadyRunning, err = service.Operations.IsRunning(ctx, meta)
			if err != nil {
				return result, fmt.Errorf("verify the previous Resume launch: %w", err)
			}
		}
		if !isAlreadyRunning {
			available, err := service.Operations.Memory()
			if err != nil {
				return result, fmt.Errorf("read available memory: %w", err)
			}
			if available < 5<<30 {
				return result, errors.New("Resume needs at least 5 GB of available memory to keep the 4 GB floor")
			}
		}
	}
	result = state.Lifecycle{ID: request.ID, Generation: meta.SpawnGen, RequestGeneration: request.Generation, Operation: request.Operation, Action: request.Action, Started: time.Now().UTC(), Reason: request.Reason, Title: meta.Title, Project: meta.Project, Kept: []string{"worktree " + meta.Worktree, "task session and branch"}, Session: prior.Session}
	if prior.Phase != "running" {
		result.GateRun, result.GateIntent, result.GateHead = prior.GateRun, prior.GateIntent, prior.GateHead
	}
	if request.Session != "" {
		result.Session = request.Session
	}
	result.Teardown = prior.Teardown
	switch request.Action {
	case "pause":
		result.Phase = "pausing"
		result.Handoff = filepath.Join(meta.TaskTmp, "pause-"+request.Operation+".md")
	case "stop":
		result.Phase = "stopping"
	case "resume":
		result.Phase = "resuming"
		result.Handoff, result.HandoffSaved = prior.Handoff, prior.HandoffSaved
	}
	if err := service.save(&result); err != nil {
		return result, err
	}
	if request.Action == "resume" {
		if !isAlreadyRunning {
			err = service.Operations.Resume(ctx, meta, prior)
		}
		if current, readErr := state.ReadTaskMeta(service.StateDir, meta.ID); readErr == nil {
			result.Generation = current.SpawnGen
		}
		result.Phase = "running"
	} else {
		if request.Action == "pause" {
			wait := service.PauseWait
			if wait <= 0 {
				wait = 5 * time.Second
			}
			prepare, cancel := context.WithTimeout(ctx, wait)
			prepareErr := service.Operations.Prepare(prepare, meta, result.Handoff)
			for prepareErr == nil {
				if info, statErr := os.Stat(result.Handoff); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
					result.HandoffSaved = true
					break
				}
				select {
				case <-prepare.Done():
					prepareErr = prepare.Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
			cancel()
			if !result.HandoffSaved {
				result.Problems = append(result.Problems, "Stopping-point deadline reached or request failed; no new handoff was saved")
			}
		}
		result.Stopped, err = service.Operations.Stop(ctx, meta, &result)
		if err == nil && service.Operations.Checkpoint != nil {
			err = service.Operations.Checkpoint(ctx, meta, &result)
		}
		if err == nil {
			if request.Action == "pause" {
				result.Phase = "paused"
			} else {
				var kept Preservation
				kept, err = service.Operations.Archive(ctx, meta, &result)
				result.Kept = kept.Kept
				result.Phase = "stopped"
			}
		}
	}
	if err != nil {
		result.Phase = "failed"
		result.Problems = append(result.Problems, err.Error())
	}
	finished, finishErr := service.finish(result)
	return finished, errors.Join(err, finishErr)
}

// PauseInstruction asks the task to reach its stopping point. The handoff's
// appearance ends the pause wait, so it is published last, after any push,
// and by rename so a partly written file never counts.
func PauseInstruction(handoff string) string {
	return "Pause requested. You have five seconds to reach a stopping point: finish the step in hand, then push your branch if its mode allows. As your very last action, write your handoff to " + handoff + ".partial and rename it to " + handoff + "; that file ends the wait, and your session and processes stop right after it appears. Retain all work and never bypass a gate."
}

func (service Service) save(record *state.Lifecycle) error {
	record.Updated = time.Now().UTC()
	return state.WriteLifecycle(service.StateDir, *record)
}

func (service Service) finish(record state.Lifecycle) (state.Lifecycle, error) {
	if record.NoticeSent {
		return record, nil
	}
	if err := service.save(&record); err != nil {
		return record, err
	}
	if record.Phase == "stopped" && record.Generation == "queued" {
		if err := state.WriteOutcome(service.StateDir, state.Outcome{ID: record.ID, Generation: record.Generation, Title: record.Title, Project: record.Project, Phase: "stopped", Reason: record.Reason, At: record.Updated}); err != nil {
			return record, err
		}
	}
	if record.Generation != "queued" || record.Phase == "stopped" {
		detail := "lifecycle-" + record.Phase + ": " + state.NormalizeStatusDetail(strings.Join(append([]string{record.Reason}, record.Kept...), "; "))
		if status := record.TeardownStatus(); status != "" {
			detail += "; " + status
		}
		if err := state.AppendStatus(service.StateDir, record.ID, detail); err != nil {
			return record, err
		}
	}
	if err := service.Operations.Notify(record); err != nil {
		return record, err
	}
	record.NoticeSent = true
	return record, service.save(&record)
}
