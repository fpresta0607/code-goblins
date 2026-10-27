package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type CFOEngine struct {
	Harness string    `json:"harness"`
	Model   string    `json:"model"`
	Effort  string    `json:"effort"`
	Pending bool      `json:"pending,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	Problem string    `json:"problem,omitempty"`
	Waiting string    `json:"waiting,omitempty"`
}

func writeCFOEngine(stateDir string, choice CFOEngine) error {
	data, err := json.Marshal(choice)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, "cfo-engine.json"), data)
}

func (s *Service) cfoEngineActivity() string {
	record, err := host.ReadRecord(s.Store.Home.State, NativeCFOTerminal)
	if err != nil {
		return "Cannot inspect the CFO's terminal; reopen it before applying the selection"
	}
	inspect := s.Options.CFOJobActivity
	if inspect == nil {
		inspect = cfoJobActivity
	}
	reason, err := inspect(record)
	if err != nil {
		return "Cannot verify the CFO's terminal work; restore process access and try again: " + err.Error()
	}
	return reason
}

func (s *Service) noteCFOEngineWaiting(choice CFOEngine, reason string) {
	s.starts.Lock()
	defer s.starts.Unlock()
	current, err := ReadCFOEngine(s.Store.Home.State)
	if err != nil || current != choice || choice.Waiting == reason {
		return
	}
	choice.Waiting = reason
	if err := writeCFOEngine(s.Store.Home.State, choice); err != nil {
		s.publish(err)
		return
	}
	s.notify()
}

func (s *Service) applyCFOEngine(ctx context.Context) {
	stateDir := s.Store.Home.State
	choice, err := ReadCFOEngine(stateDir)
	if err != nil || !choice.Pending {
		return
	}
	cfo := readCFOState(stateDir)
	if !cfo.registered || !cfo.since.Equal(choice.Since) {
		s.starts.Lock()
		if current, err := ReadCFOEngine(stateDir); err == nil && current == choice {
			choice.Pending = false
			choice.Problem = "The CFO session changed; the selection is saved for its next start"
			if err := writeCFOEngine(stateDir, choice); err != nil {
				s.publish(err)
			}
			s.notify()
		}
		s.starts.Unlock()
		return
	}
	if reason := s.cfoEngineActivity(); reason != "" {
		s.noteCFOEngineWaiting(choice, reason)
		return
	}
	if !s.cfoAtStoppingPoint(ctx, cfo.harness) {
		s.noteCFOEngineWaiting(choice, "Waiting for the CFO's turn, terminal draft and background work to finish")
		return
	}
	s.starts.Lock()
	defer s.starts.Unlock()
	current, err := ReadCFOEngine(stateDir)
	if err != nil || current != choice {
		return
	}
	cfo = readCFOState(stateDir)
	if !cfo.registered || !cfo.since.Equal(choice.Since) {
		return
	}
	catalogContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	catalog, err := s.engineCatalog(catalogContext, execx.OSRunner{})
	cancel()
	if err == nil {
		err = validateEngineSelection(catalog, state.EngineChoice{Harness: choice.Harness, Model: choice.Model, Effort: choice.Effort}, false)
	}
	if err != nil {
		choice.Pending = false
		choice.Problem = "The selection could not be applied: " + err.Error()
		if writeErr := writeCFOEngine(stateDir, choice); writeErr != nil {
			s.publish(writeErr)
		}
		s.notify()
		return
	}
	if reason := s.cfoEngineActivity(); reason != "" {
		choice.Waiting = reason
		if err := writeCFOEngine(stateDir, choice); err != nil {
			s.publish(err)
		}
		s.notify()
		return
	}
	if !s.cfoAtStoppingPoint(ctx, cfo.harness) {
		return
	}
	cfo = readCFOState(stateDir)
	if !cfo.registered || !cfo.since.Equal(choice.Since) {
		return
	}
	choice.Pending, choice.Waiting = false, ""
	if err := writeCFOEngine(stateDir, choice); err != nil {
		s.publish(err)
		return
	}
	if s.Options.FirstRun == nil {
		choice.Problem = "This board cannot restart the CFO"
	} else if _, _, err := s.Options.FirstRun.Restart(choice.Harness); err != nil {
		choice.Problem = err.Error()
	}
	if choice.Problem != "" {
		if err := writeCFOEngine(stateDir, choice); err != nil {
			s.publish(err)
		}
	}
	s.notify()
}

func ReadCFOEngine(stateDir string) (CFOEngine, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, "cfo-engine.json"))
	if err != nil {
		return CFOEngine{}, err
	}
	var choice CFOEngine
	if err := json.Unmarshal(data, &choice); err != nil {
		return CFOEngine{}, err
	}
	if !slices.Contains([]string{"claude", "codex", "pi"}, choice.Harness) || choice.Model == "" || choice.Model == "default" || !spawnValue.MatchString(choice.Model) || !slices.Contains(harness.Efforts(harness.Kind(choice.Harness)), choice.Effort) {
		return CFOEngine{}, errors.New("invalid saved CFO engine selection")
	}
	return choice, nil
}

func (h *HTTP) selectCFOEngine(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Harness string `json:"harness"`
		Model   string `json:"model"`
		Effort  string `json:"effort"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	choice := CFOEngine{Harness: input.Harness, Model: input.Model, Effort: input.Effort}
	if choice.Model == "" || choice.Model == "default" || choice.Effort == "" || choice.Effort == "default" {
		apiError(w, http.StatusConflict, "Choose an explicit model and effort")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	catalog, err := h.Service.engineCatalog(ctx, execx.OSRunner{})
	if err == nil {
		err = validateEngineSelection(catalog, state.EngineChoice{Harness: choice.Harness, Model: choice.Model, Effort: choice.Effort}, false)
	}
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.starts.Lock()
	defer h.Service.starts.Unlock()
	cfo := readCFOState(h.Service.Store.Home.State)
	if cfo.registered && cfo.terminal == NativeCFOTerminal {
		choice.Pending, choice.Since = true, cfo.since
	}
	err = writeCFOEngine(h.Service.Store.Home.State, choice)
	if err != nil {
		apiError(w, http.StatusInternalServerError, fmt.Sprintf("Save CFO engine: %v", err))
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, choice)
}
