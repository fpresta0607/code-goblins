package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type engineIdleReading struct {
	Requested time.Time
	At        time.Time
}

func validateEngineSelection(catalog EngineCatalog, choice state.EngineChoice) error {
	for _, item := range catalog.Harnesses {
		if item.ID != choice.Harness {
			continue
		}
		if item.Reason != "" {
			return errors.New(item.Reason)
		}
		for _, model := range item.Models {
			if model.ID == choice.Model {
				if choice.Effort != "default" && !slices.Contains(model.Efforts, choice.Effort) {
					return fmt.Errorf("Effort %s is unavailable for %s", choice.Effort, model.Name)
				}
				return nil
			}
		}
		return fmt.Errorf("Model %s is unavailable for %s", choice.Model, item.Name)
	}
	return errors.New("Choose an installed, signed-in harness")
}

func (h *HTTP) selectTaskEngine(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Task       string `json:"task"`
		Generation string `json:"generation"`
		Revision   string `json:"revision"`
		Harness    string `json:"harness"`
		Model      string `json:"model"`
		Effort     string `json:"effort"`
		When       string `json:"when"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if state.ValidTaskID(input.Task) != nil || !slices.Contains([]string{"", "turn-end", "now", "cancel"}, input.When) {
		apiError(w, 400, "Invalid task engine selection")
		return
	}
	s := h.Service
	choice := state.EngineChoice{ID: input.Task, Generation: input.Generation, Harness: input.Harness, Model: input.Model, Effort: input.Effort, Requested: time.Now().UTC()}
	if input.When != "cancel" {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		catalog, err := s.engineCatalog(ctx, execx.OSRunner{})
		if err != nil {
			apiError(w, 503, err.Error())
			return
		}
		if err := validateEngineSelection(catalog, choice); err != nil {
			apiError(w, 409, err.Error())
			return
		}
	}
	s.starts.Lock()
	defer s.starts.Unlock()
	if s.starting == input.Task || s.changing[input.Task] != "" {
		apiError(w, 409, "This task is already changing")
		return
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, input.Task)
	if errors.Is(err, os.ErrNotExist) && input.Generation == "" && input.When != "cancel" {
		if err := fleet.SaveQueuedEngine(s.Store.Home, input.Task, input.Revision, input.Harness, input.Model, input.Effort); err != nil {
			apiError(w, 409, err.Error())
			return
		}
		updated, err := fleet.ReadQueuedTask(s.Store.Home, input.Task)
		if err != nil {
			apiError(w, 500, err.Error())
			return
		}
		s.reportEngineChoice(choice, "queued settings saved")
		s.notify()
		respond(w, 200, adjustmentResult{Saved: true, Revision: updated.Revision})
		return
	}
	if err != nil || input.Generation == "" || meta.SpawnGen != input.Generation {
		apiError(w, 409, "The task session changed; reopen its card")
		return
	}
	if input.When == "cancel" {
		if err := state.RemoveEngineChoice(s.Store.Home.State, input.Task); err != nil {
			apiError(w, 500, err.Error())
			return
		}
		delete(s.engineIdle, input.Task)
		s.notify()
		respond(w, 200, struct {
			Saved bool `json:"saved"`
		}{true})
		return
	}
	if meta.Backend != "native" {
		apiError(w, 409, "Only a task in a native terminal can switch")
		return
	}
	if record, err := state.ReadLifecycle(s.Store.Home.State, input.Task); err == nil && record.Generation == meta.SpawnGen {
		if record.Phase == "paused" {
			choice.When = "resume"
		} else if record.Phase != "running" {
			apiError(w, 409, "Wait until this task finishes changing")
			return
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		apiError(w, 500, err.Error())
		return
	}
	if choice.When == "resume" || input.When == "turn-end" {
		if choice.When == "" {
			choice.When = "turn-end"
		}
		if err := state.WriteEngineChoice(s.Store.Home.State, choice); err != nil {
			apiError(w, 500, err.Error())
			return
		}
		delete(s.engineIdle, input.Task)
		delete(s.changeErrors, input.Task)
		if choice.When == "resume" {
			s.reportEngineChoice(choice, "Resume settings saved")
		}
		s.notify()
		status := 202
		if choice.When == "resume" {
			status = 200
		}
		respond(w, status, struct {
			Saved bool `json:"saved"`
		}{true})
		return
	}
	if input.When != "now" {
		apiError(w, 400, "Choose when to switch the running task")
		return
	}
	if s.Options.Dispatch == nil {
		apiError(w, 409, "This board cannot switch task processes")
		return
	}
	s.startEngineSwitch(meta, choice)
	respond(w, 202, struct {
		Accepted bool `json:"accepted"`
	}{true})
}

func (s *Service) reportEngineChoice(choice state.EngineChoice, outcome string) {
	detail := fmt.Sprintf("engine: %s from the board; harness=%s model=%s effort=%s", outcome, choice.Harness, choice.Model, choice.Effort)
	if _, err := wake.Append(s.Store.Home.State, "notify", choice.ID, detail); err != nil {
		s.publish(err)
	} else if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		s.publish(err)
	}
}

// The caller holds starts until the changing marker is published.
func (s *Service) startEngineSwitch(meta state.TaskMeta, choice state.EngineChoice) {
	if s.changing == nil {
		s.changing = map[string]string{}
	}
	if s.changeErrors == nil {
		s.changeErrors = map[string]taskChangeError{}
	}
	if s.engineFrom == nil {
		s.engineFrom = map[string]state.TaskMeta{}
	}
	s.engineFrom[meta.ID] = meta
	s.changing[meta.ID] = "switch"
	delete(s.changeErrors, meta.ID)
	go func() {
		args := []string{"switch", meta.ID, "--generation", meta.SpawnGen, "--harness", choice.Harness, "--model", choice.Model, "--effort", choice.Effort, "--force-dirty"}
		output, err := s.Options.Dispatch.Spawn(context.Background(), args)
		s.starts.Lock()
		if removeErr := state.RemoveEngineChoice(s.Store.Home.State, meta.ID); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		outcome := "session switched"
		if err != nil {
			generation := meta.SpawnGen
			if current, readErr := state.ReadTaskMeta(s.Store.Home.State, meta.ID); readErr == nil {
				generation = current.SpawnGen
			}
			reason := spawnFailure(output, err)
			failure := taskChangeError{Message: reason, Generation: generation}
			if record, readErr := state.ReadLifecycle(s.Store.Home.State, meta.ID); readErr == nil {
				failure.Operation, failure.Updated = record.Operation, record.Updated
			}
			s.changeErrors[meta.ID] = failure
			outcome = "switch failed: " + reason
		}
		s.starts.Unlock()
		s.reportEngineChoice(choice, outcome)
		s.starts.Lock()
		delete(s.changing, meta.ID)
		delete(s.engineFrom, meta.ID)
		delete(s.engineIdle, meta.ID)
		s.starts.Unlock()
		s.notify()
	}()
	s.notify()
}

func (s *Service) applyEngineChoices(ctx context.Context, now time.Time) error {
	entries, err := os.ReadDir(filepath.Join(s.Store.Home.State, "engine"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures error
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || entry.IsDir() {
			continue
		}
		choice, err := state.ReadEngineChoice(s.Store.Home.State, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if choice.When != "turn-end" || s.Options.Dispatch == nil {
			continue
		}
		s.starts.Lock()
		if s.changing[id] != "" || s.starting == id {
			s.starts.Unlock()
			continue
		}
		meta, err := state.ReadTaskMeta(s.Store.Home.State, id)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			delete(s.engineIdle, id)
			s.starts.Unlock()
			failures = errors.Join(failures, err)
			continue
		}
		if err != nil || meta.SpawnGen != choice.Generation {
			if err := state.RemoveEngineChoice(s.Store.Home.State, id); err != nil {
				s.starts.Unlock()
				failures = errors.Join(failures, err)
				continue
			}
			delete(s.engineIdle, id)
			s.starts.Unlock()
			s.reportEngineChoice(choice, "pending choice expired because its session changed")
			continue
		}
		s.starts.Unlock()
		isIdle, err := s.engineTaskIdle(ctx, meta)
		if err == nil && isIdle {
			isIdle, err = s.engineGateIdle(ctx, meta)
		}
		s.starts.Lock()
		if s.engineIdle == nil {
			s.engineIdle = map[string]engineIdleReading{}
		}
		if err != nil || !isIdle {
			delete(s.engineIdle, id)
			if err != nil {
				s.recordEngineFailure(id, choice.Generation, err)
			}
			s.starts.Unlock()
			continue
		}
		reading, seen := s.engineIdle[id]
		if !seen || !reading.Requested.Equal(choice.Requested) {
			s.engineIdle[id] = engineIdleReading{Requested: choice.Requested, At: now}
			s.starts.Unlock()
			continue
		}
		if now.Sub(reading.At) < time.Second || s.changing[id] != "" {
			s.starts.Unlock()
			continue
		}
		s.starts.Unlock()
		catalogContext, cancel := context.WithTimeout(ctx, 8*time.Second)
		catalog, err := s.engineCatalog(catalogContext, execx.OSRunner{})
		cancel()
		if err != nil {
			s.starts.Lock()
			delete(s.engineIdle, id)
			s.starts.Unlock()
			failures = errors.Join(failures, err)
			continue
		}
		if unavailable := validateEngineSelection(catalog, choice); unavailable != nil {
			s.starts.Lock()
			delete(s.engineIdle, id)
			current, readErr := state.ReadEngineChoice(s.Store.Home.State, id)
			if readErr != nil || current != choice {
				s.starts.Unlock()
				if !errors.Is(readErr, os.ErrNotExist) {
					failures = errors.Join(failures, readErr)
				}
				continue
			}
			if err := state.RemoveEngineChoice(s.Store.Home.State, id); err != nil {
				s.starts.Unlock()
				failures = errors.Join(failures, err)
				continue
			}
			s.recordEngineFailure(id, choice.Generation, unavailable)
			s.starts.Unlock()
			s.reportEngineChoice(choice, "pending choice removed: "+unavailable.Error())
			s.notify()
			continue
		}
		isIdle, err = s.engineTaskIdle(ctx, meta)
		if err == nil && isIdle {
			isIdle, err = s.engineGateIdle(ctx, meta)
		}
		s.starts.Lock()
		if err != nil || !isIdle {
			delete(s.engineIdle, id)
			if err != nil {
				s.recordEngineFailure(id, choice.Generation, err)
			}
			s.starts.Unlock()
			continue
		}
		current, readErr := state.ReadEngineChoice(s.Store.Home.State, id)
		latest, metaErr := state.ReadTaskMeta(s.Store.Home.State, id)
		if readErr == nil && metaErr == nil && current == choice && latest.SpawnGen == meta.SpawnGen && s.changing[id] == "" && s.starting != id {
			s.startEngineSwitch(meta, choice)
		}
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) || metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
			delete(s.engineIdle, id)
			failures = errors.Join(failures, readErr, metaErr)
		}
		s.starts.Unlock()
	}
	return failures
}

// The caller holds starts.
func (s *Service) recordEngineFailure(id, generation string, err error) {
	if s.changeErrors == nil {
		s.changeErrors = map[string]taskChangeError{}
	}
	failure := taskChangeError{Message: err.Error(), Generation: generation}
	if record, readErr := state.ReadLifecycle(s.Store.Home.State, id); readErr == nil {
		failure.Operation, failure.Updated = record.Operation, record.Updated
	}
	s.changeErrors[id] = failure
}

func (s *Service) engineTaskIdle(ctx context.Context, meta state.TaskMeta) (bool, error) {
	if record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.Phase != "running" {
		return false, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if idle := s.Options.Dispatch.Idle; idle != nil {
		return idle(ctx, meta)
	}
	screens, ok := harness.NativeScreens(harness.Kind(meta.Harness))
	if !ok {
		return false, errors.New("This harness has no native screen reader")
	}
	record, err := host.ReadRecord(s.Store.Home.State, meta.ID)
	if err != nil {
		return false, err
	}
	screen, err := host.ReadScreen(record)
	if err != nil {
		return false, err
	}
	return idleAtEmptyComposer(screens, screen), nil
}

func (s *Service) engineGateIdle(ctx context.Context, meta state.TaskMeta) (bool, error) {
	if s.Options.Gate == nil {
		return false, errors.New("Gate activity cannot be checked")
	}
	branch, err := s.Git.Branch(ctx, meta.Worktree)
	if err != nil {
		return false, err
	}
	progress, err := s.Options.Gate.Progress(ctx, meta.Project, branch)
	if errors.Is(err, pipeline.ErrNoProgress) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !slices.ContainsFunc(progress.Steps, func(step pipeline.ProgressStep) bool {
		return step.Status == "running" || step.Status == "fixing" || step.Status == "fix_review"
	}), nil
}
