package lifecycle

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func StopQueued(h home.Home, request Request, revision string) (record state.Lifecycle, err error) {
	if err := state.ValidTaskID(request.ID); err != nil {
		return record, err
	}
	if err := state.ValidTaskID(request.Operation); err != nil {
		return record, err
	}
	name := ".queued-" + request.ID + ".lock"
	if _, err := lock.AcquireExclusiveNamed(h.State, name); err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, name)) }()
	if _, err := state.ReadTaskMeta(h.State, request.ID); !errors.Is(err, os.ErrNotExist) {
		return record, errors.New("task started; refresh its card before stopping")
	}
	prior, err := state.ReadLifecycle(h.State, request.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return record, err
	}
	service := Service{StateDir: h.State, Operations: Operations{Notify: func(record state.Lifecycle) error { return Report(h.State, record) }}}
	if err == nil && prior.Operation == request.Operation {
		if prior.Action != "stop" || request.Generation != "" && prior.RequestGeneration != request.Generation {
			return record, errors.New("operation identity was already used for another request")
		}
		if prior.Phase == "stopped" {
			return service.finish(prior)
		}
		if prior.Phase == "failed" {
			finished, finishErr := service.finish(prior)
			return finished, errors.Join(finishErr, errors.New(strings.Join(prior.Problems, "; ")))
		}
		if prior.Phase == "stopping" {
			outcome, err := state.ReadOutcome(h.State, request.ID)
			if err == nil && outcome.Generation == prior.Generation && outcome.Phase == "stopped" {
				prior.Phase = "stopped"
				return service.finish(prior)
			}
		}
	}
	if request.Generation != "" && request.Generation != "queued" {
		return record, errors.New("task session ended; an active task request cannot stop a replacement queued task")
	}
	queued, err := fleet.ReadQueuedTask(h, request.ID)
	isRowRemoved := errors.Is(err, fleet.ErrNotQueued) || err == nil && queued.IsBriefOnly
	if isRowRemoved && prior.Operation == request.Operation && prior.Generation == "queued" && prior.Phase == "stopping" {
		prior.Phase = "stopped"
		return service.finish(prior)
	}
	if err != nil {
		return record, err
	}
	if revision == "" {
		revision = queued.Revision
	}
	if queued.Revision != revision {
		return record, fleet.ErrQueueChanged
	}
	if err := fleet.WriteQueuedBrief(h, queued); err != nil && !errors.Is(err, os.ErrExist) {
		return record, err
	}
	now := time.Now().UTC()
	record = state.Lifecycle{ID: request.ID, Generation: "queued", RequestGeneration: "queued", Operation: request.Operation, Action: "stop", Phase: "stopping", Title: queued.Row.Title, Project: queued.Row.Repo, Started: now, Updated: now, Reason: request.Reason, Kept: []string{"task brief"}, Teardown: prior.Teardown}
	if err := state.WriteLifecycle(h.State, record); err != nil {
		return record, err
	}
	if err := fleet.RemoveQueuedTask(h, request.ID, revision); err != nil {
		record.Phase = "failed"
		record.Problems = append(record.Problems, err.Error())
		finished, finishErr := service.finish(record)
		return finished, errors.Join(err, finishErr)
	}
	record.Phase = "stopped"
	return service.finish(record)
}
