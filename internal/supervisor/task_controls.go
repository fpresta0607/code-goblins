package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type LifecycleStatus struct {
	Phase              string    `json:"phase"`
	Action             string    `json:"action"`
	At                 time.Time `json:"at"`
	Kept               []string  `json:"kept"`
	Stopped            []string  `json:"stopped"`
	Teardown           []string  `json:"teardown"`
	Problems           []string  `json:"problems"`
	HandoffSaved       bool      `json:"handoff_saved"`
	ValidationRestarts bool      `json:"validation_restarts"`
}

func lifecycleStatus(record state.Lifecycle) *LifecycleStatus {
	return &LifecycleStatus{Phase: record.Phase, Action: record.Action, At: record.Updated, Kept: record.Kept, Stopped: record.Stopped, Teardown: record.TeardownLabels(), Problems: record.Problems, HandoffSaved: record.HandoffSaved, ValidationRestarts: record.GateRun != "" && (record.Phase == "paused" || record.Action == "resume" && record.Phase != "running")}
}

type taskChangeError struct {
	Message    string
	Generation string
	Operation  string
	Updated    time.Time
}

func (h *HTTP) lifecycleTask(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Task       string `json:"task"`
		Generation string `json:"generation"`
		Revision   string `json:"revision"`
		Operation  string `json:"operation"`
		Action     string `json:"action"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if state.ValidTaskID(input.Task) != nil || state.ValidTaskID(input.Operation) != nil || input.Action != "pause" && input.Action != "resume" && input.Action != "stop" {
		apiError(w, 400, "Invalid task action")
		return
	}
	s := h.Service
	if s.Options.Dispatch == nil {
		apiError(w, 409, "This board cannot control task processes")
		return
	}
	s.starts.Lock()
	defer s.starts.Unlock()
	if s.starting == input.Task || s.changing[input.Task] != "" {
		apiError(w, 409, "This task is already changing")
		return
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, input.Task)
	if errors.Is(err, os.ErrNotExist) && input.Action == "stop" {
		queued, readErr := fleet.ReadQueuedTask(s.Store.Home, input.Task)
		if readErr != nil || queued.Revision != input.Revision {
			apiError(w, 409, "The queued task changed; reopen its card")
			return
		}
	} else if err != nil || input.Generation == "" || input.Generation != meta.SpawnGen {
		apiError(w, 409, "The task session changed; refresh its card")
		return
	}
	prior, priorErr := state.ReadLifecycle(s.Store.Home.State, input.Task)
	if priorErr != nil && !errors.Is(priorErr, os.ErrNotExist) {
		apiError(w, 500, priorErr.Error())
		return
	}
	if input.Action == "resume" {
		if s.starting != "" {
			apiError(w, 409, "Another task is starting; resume once it is up")
			return
		}
		for _, action := range s.changing {
			if action == "resume" {
				apiError(w, 409, "Another task is resuming; try again once it is up")
				return
			}
		}
		isInterruptedResume := prior.Action == "resume" && (prior.Phase == "resuming" || prior.Phase == "failed") && meta.ResumeOperation == prior.Operation
		if priorErr != nil || prior.Generation != meta.SpawnGen && !isInterruptedResume || prior.Phase != "paused" && !(prior.Action == "resume" && (prior.Phase == "failed" || prior.Phase == "resuming")) {
			apiError(w, 409, "Only a paused task can resume")
			return
		}
		// An interrupted launch may already be using its memory. The shared
		// CLI proves it is running, or enforces the floor before a new launch.
		if !isInterruptedResume {
			available, _, err := s.Options.Dispatch.Memory()
			if err != nil || available < memoryNext {
				apiError(w, 409, "Resume needs 5 GB free to keep the 4 GB floor")
				return
			}
		}
	}
	if s.changing == nil {
		s.changing = map[string]string{}
		s.changeErrors = map[string]taskChangeError{}
	}
	s.changing[input.Task] = input.Action
	delete(s.changeErrors, input.Task)
	command := input.Action
	if command == "stop" {
		command = "kill"
	}
	args := []string{command, input.Task, "--generation", input.Generation, "--revision", input.Revision, "--operation", input.Operation, "--reason", "Requested from the board"}
	requestGeneration := input.Generation
	if requestGeneration == "" {
		requestGeneration = "queued"
	}
	go func() {
		output, err := s.Options.Dispatch.Spawn(context.Background(), args)
		s.starts.Lock()
		delete(s.changing, input.Task)
		if err != nil {
			failure := taskChangeError{Message: spawnFailure(output, err), Generation: input.Generation, Operation: prior.Operation, Updated: prior.Updated}
			if record, readErr := state.ReadLifecycle(s.Store.Home.State, input.Task); readErr == nil && record.Operation == input.Operation && record.RequestGeneration == requestGeneration {
				failure.Generation, failure.Operation, failure.Updated = record.Generation, record.Operation, record.Updated
				if failure.Generation == "queued" {
					failure.Generation = ""
				}
			}
			s.changeErrors[input.Task] = failure
		}
		s.starts.Unlock()
		s.notify()
	}()
	s.notify()
	s.mu.Lock()
	revision := s.revision
	s.mu.Unlock()
	respond(w, 202, struct {
		Accepted bool   `json:"accepted"`
		Revision uint64 `json:"revision"`
	}{true, revision})
}

type taskAdjustment struct {
	Task      string `json:"task"`
	Revision  string `json:"revision"`
	Operation string `json:"operation"`
	Text      string `json:"text"`
	Action    string `json:"action"`
}

type adjustmentResult struct {
	Saved    bool   `json:"saved"`
	Revision string `json:"revision"`
}

func (h *HTTP) adjustTask(w http.ResponseWriter, r *http.Request) {
	var input taskAdjustment
	if err := decodeBody(w, r, &input, 12<<10); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if state.ValidTaskID(input.Task) != nil || state.ValidTaskID(input.Operation) != nil || strings.TrimSpace(input.Text) == "" || len(input.Text) > 8000 || input.Action != "save" && input.Action != "note" {
		apiError(w, 400, "Enter a task change or note")
		return
	}
	s := h.Service
	s.starts.Lock()
	defer s.starts.Unlock()
	if s.starting == input.Task || s.changing[input.Task] != "" {
		apiError(w, 409, "This task is starting or stopping")
		return
	}
	name := ".queued-" + input.Task + ".lock"
	if _, err := lock.AcquireExclusiveNamed(s.Store.Home.State, name); err != nil {
		apiError(w, 409, "This task is being changed; try again")
		return
	}
	defer func() {
		if err := lock.ReleaseExclusiveNamed(s.Store.Home.State, name); err != nil {
			s.publish(err)
		}
	}()
	path := filepath.Join(s.Store.Home.State, "adjustments", input.Task, input.Operation+".json")
	var receipt struct {
		Input  taskAdjustment   `json:"input"`
		Result adjustmentResult `json:"result"`
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &receipt); err != nil {
			apiError(w, 500, "The adjustment receipt could not be read")
			return
		}
		if receipt.Input != input {
			apiError(w, 409, "This operation already recorded a different adjustment")
			return
		}
		respond(w, 200, receipt.Result)
		return
	}
	if !errors.Is(err, os.ErrNotExist) {
		apiError(w, 500, err.Error())
		return
	}
	if _, err := state.ReadTaskMeta(s.Store.Home.State, input.Task); !errors.Is(err, os.ErrNotExist) {
		apiError(w, 409, "This task already started")
		return
	}
	queued, err := fleet.ReadQueuedTask(s.Store.Home, input.Task)
	if err != nil || queued.Revision != input.Revision {
		apiError(w, 409, "The queued task changed; reopen its card")
		return
	}
	if input.Action == "save" {
		err = fleet.SaveQueuedTask(s.Store.Home, input.Task, input.Revision, input.Text)
	} else {
		_, err = wake.AppendOnce(s.Store.Home.State, "task-note/"+input.Task+"/"+input.Operation, "notify", input.Task, "task note: "+strings.TrimSpace(input.Text))
		if err == nil {
			_, err = wake.PublishEpisode(s.Store.Home.State)
		}
	}
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	updated, err := fleet.ReadQueuedTask(s.Store.Home, input.Task)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	receipt.Input, receipt.Result = input, adjustmentResult{true, updated.Revision}
	data, err = json.Marshal(receipt)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = fsx.AtomicWriteFile(path, data)
	}
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	s.notify()
	respond(w, 200, receipt.Result)
}
