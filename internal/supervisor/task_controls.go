package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
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
	Phase              string                `json:"phase"`
	Action             string                `json:"action"`
	At                 time.Time             `json:"at"`
	Kept               []string              `json:"kept"`
	Stopped            []string              `json:"stopped"`
	Problems           []string              `json:"problems"`
	HandoffSaved       bool                  `json:"handoff_saved"`
	ValidationRestarts bool                  `json:"validation_restarts"`
	Pause              *state.PauseCondition `json:"pause,omitempty"`
	// WithParent is a helper's pause or stop its parent's made: paused, the
	// scheduler resumes it once its parent runs again.
	WithParent bool `json:"with_parent,omitempty"`
}

func lifecycleStatus(record state.Lifecycle) *LifecycleStatus {
	return &LifecycleStatus{Phase: record.Phase, Action: record.Action, At: record.Updated, Kept: record.Kept, Stopped: record.Stopped, Problems: record.Problems, HandoffSaved: record.HandoffSaved, Pause: record.Pause, ValidationRestarts: record.GateRun != "" && (record.Phase == "paused" || record.Action == "resume" && record.Phase != "running"), WithParent: state.IsHelperOperation(record.Operation)}
}

type taskChangeError struct {
	Message    string
	Generation string
	Operation  string
	Updated    time.Time
	IsIdleRead bool
}

// lifecycleRequest is a Pause, Resume or Stop clicked on the board, naming
// the task, its session and queued revision, and the operation that makes a
// retry of the same click one operation.
type lifecycleRequest struct {
	Task       string `json:"task"`
	Generation string `json:"generation"`
	Revision   string `json:"revision"`
	Operation  string `json:"operation"`
	Action     string `json:"action"`
}

// taskRefusal is why a Pause, Resume or Stop does not run now. A passing one
// waits on what passes by itself, another goblin starting or resuming,
// memory or disk; any other says the click no longer applies, as for a
// session that changed.
type taskRefusal struct {
	reason    string
	isPassing bool
}

func (r taskRefusal) Error() string { return r.reason }

// lifecycleTask serves POST /api/tasks/lifecycle. A Resume waits its turn
// rather than being refused while another goblin starts or resumes, or while
// memory or disk is short; a Pause or Stop runs at once and takes back a
// Resume of the same task that still waits. A click on what is already under
// way is accepted and changes nothing.
func (h *HTTP) lifecycleTask(w http.ResponseWriter, r *http.Request) {
	var input lifecycleRequest
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
	var err error
	if input.Action == "resume" {
		err = s.askResume(input)
	} else {
		s.withdraw(input.Task)
		err = s.changeTask(input)
	}
	if err != nil {
		status := 500
		if errors.As(err, new(taskRefusal)) {
			status = 409
		}
		apiError(w, status, err.Error())
		return
	}
	s.mu.Lock()
	revision := s.revision
	s.mu.Unlock()
	respond(w, 202, struct {
		Accepted bool   `json:"accepted"`
		Revision uint64 `json:"revision"`
	}{true, revision})
}

// askResume files a Resume to run in its turn once its click still applies:
// the session it names is the task's, and the task is paused or its last
// resume was cut short.
func (s *Service) askResume(input lifecycleRequest) error {
	s.starts.Lock()
	isUnderWay := s.changing[input.Task] == "resume" || s.isAsked(input.Task)
	s.starts.Unlock()
	if isUnderWay {
		return nil
	}
	if _, _, _, err := s.checkChange(input); err != nil {
		return err
	}
	s.ask(askedChange{task: input.Task, resume: &input})
	return nil
}

// checkChange reads the task a click names and says whether the click still
// applies: the session it names is the task's, a queued task's Stop names
// its current revision, and a Resume finds the task paused or its last
// resume cut short, which isInterrupted says.
func (s *Service) checkChange(input lifecycleRequest) (meta state.TaskMeta, prior state.Lifecycle, isInterrupted bool, err error) {
	meta, err = state.ReadTaskMeta(s.Store.Home.State, input.Task)
	if errors.Is(err, os.ErrNotExist) && input.Action == "stop" {
		queued, readErr := fleet.ReadQueuedTask(s.Store.Home, input.Task)
		if readErr != nil || queued.Revision != input.Revision {
			return meta, prior, false, taskRefusal{reason: "The queued task changed; reopen its card"}
		}
	} else if err != nil || input.Generation == "" || input.Generation != meta.SpawnGen {
		return meta, prior, false, taskRefusal{reason: "The task session changed; refresh its card"}
	}
	prior, err = state.ReadLifecycle(s.Store.Home.State, input.Task)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return meta, prior, false, err
	}
	if input.Action != "resume" {
		return meta, prior, false, nil
	}
	isInterrupted = prior.Action == "resume" && (prior.Phase == "resuming" || prior.Phase == "failed") && meta.ResumeOperation == prior.Operation
	isEndedPause := pauseTookHold(s.Store.Home.State, meta.Backend, prior)
	if err != nil || prior.Generation != meta.SpawnGen && !isInterrupted || prior.Phase != "paused" && !isEndedPause && !(prior.Action == "resume" && (prior.Phase == "failed" || prior.Phase == "resuming")) {
		return meta, prior, false, taskRefusal{reason: "Only a paused task can resume"}
	}
	return meta, prior, isInterrupted, nil
}

// changeTask runs a Pause, Resume or Stop through the CLI once the click
// still applies and, for a Resume, once nothing else starts or resumes and
// memory and disk allow; what it waits on is a passing taskRefusal. The
// card shows the change from the moment it is taken, and a change the CLI
// refuses or fails goes to the CFO unless the CLI's own lifecycle record
// already told it.
func (s *Service) changeTask(input lifecycleRequest) error {
	s.starts.Lock()
	if s.changing[input.Task] == input.Action {
		s.starts.Unlock()
		return nil
	}
	if s.starting == input.Task || s.changing[input.Task] != "" {
		s.starts.Unlock()
		return taskRefusal{reason: "This task is already changing", isPassing: input.Action == "resume"}
	}
	if launching := s.launching(); input.Action == "resume" && launching != "" {
		s.starts.Unlock()
		return taskRefusal{reason: launching + " is starting or resuming", isPassing: true}
	}
	if s.changing == nil {
		s.changing = map[string]string{}
	}
	if s.changeErrors == nil {
		s.changeErrors = map[string]taskChangeError{}
	}
	s.changing[input.Task] = input.Action
	s.starts.Unlock()
	isDispatched := false
	defer func() {
		if !isDispatched {
			s.starts.Lock()
			delete(s.changing, input.Task)
			s.starts.Unlock()
			s.notify()
		}
	}()
	_, prior, isInterrupted, err := s.checkChange(input)
	if err != nil {
		return err
	}
	// An interrupted launch may already be using its memory. The shared
	// CLI proves it is running, or enforces the floor before a new launch.
	if input.Action == "resume" && !isInterrupted {
		memory, err := s.Options.Dispatch.Memory()
		if err != nil {
			return fmt.Errorf("free memory cannot be read, so the goblin does not resume: %w", err)
		}
		if short := memory.shortfall(); short != "" {
			return taskRefusal{reason: short + "; Resume needs 5 GB to keep the 4 GB floor", isPassing: true}
		}
		disk, err := s.machineDisk()
		if err != nil {
			return fmt.Errorf("free disk cannot be read, so the goblin does not resume: %w", err)
		}
		if err := CheckLaunch(memory, disk); err != nil {
			return taskRefusal{reason: err.Error(), isPassing: true}
		}
	}
	command := input.Action
	if command == "stop" {
		command = "kill"
	}
	args := []string{command, input.Task, "--generation", input.Generation, "--revision", input.Revision, "--operation", input.Operation, "--reason", "Requested from the board"}
	if input.Action == "pause" {
		args[len(args)-1] = "overlord"
	}
	requestGeneration := input.Generation
	if requestGeneration == "" {
		requestGeneration = "queued"
	}
	s.starts.Lock()
	delete(s.changeErrors, input.Task)
	s.starts.Unlock()
	isDispatched = true
	go func() {
		output, err := s.runPastTheSpawnLock(s.Options.Dispatch, args)
		var failure taskChangeError
		isToldByItsRecord := false
		if err != nil {
			failure = taskChangeError{Message: spawnFailure(output, err), Generation: input.Generation, Operation: prior.Operation, Updated: prior.Updated}
			if record, readErr := state.ReadLifecycle(s.Store.Home.State, input.Task); readErr == nil && record.Operation == input.Operation && record.RequestGeneration == requestGeneration {
				failure.Generation, failure.Operation, failure.Updated = record.Generation, record.Operation, record.Updated
				if failure.Generation == "queued" {
					failure.Generation = ""
				}
				isToldByItsRecord = record.NoticeSent
			}
		}
		if err != nil && !isToldByItsRecord {
			s.tellCFOOfFailure(input.Task, input.Action+" failed: "+failure.Message)
		}
		s.starts.Lock()
		delete(s.changing, input.Task)
		if err != nil {
			s.changeErrors[input.Task] = failure
		}
		s.starts.Unlock()
		s.notify()
		s.runAsked()
	}()
	s.notify()
	return nil
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
	if s.starting == input.Task || s.changing[input.Task] != "" {
		s.starts.Unlock()
		apiError(w, 409, "This task is starting or stopping")
		return
	}
	if s.changing == nil {
		s.changing = map[string]string{}
	}
	s.changing[input.Task] = input.Action
	s.starts.Unlock()
	defer func() {
		s.starts.Lock()
		delete(s.changing, input.Task)
		s.starts.Unlock()
	}()
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
	data, err := fsx.ReadFile(path)
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
