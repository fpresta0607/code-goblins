package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/onboarding"
)

type FirstRun struct {
	Home         string
	DefaultAgent func() (string, error)
	Detect       func(context.Context, string) onboarding.Agent
	CFORuns      func() bool
	StartCFO     func(string) error
	Save         func(string) error
	Memory       func() (uint64, uint64, error)
	mu           sync.Mutex
}

// StartRefusal is a start the board cannot make, of the CFO from the
// first-run page or of a queued task; its message is the reason, in the
// board's words. Passing says a queued task's refusal has a cause that passes
// by itself and the board sees pass: memory under the floor, or another Start
// running.
type StartRefusal struct {
	Reason  string
	Passing bool
}

func (r StartRefusal) Error() string { return r.Reason }

type Setup struct {
	Home         string             `json:"home"`
	DefaultAgent string             `json:"default_agent"`
	Problem      string             `json:"problem,omitempty"`
	Agents       []onboarding.Agent `json:"agents"`
	CFORuns      bool               `json:"cfo_runs"`
}

func (f *FirstRun) Setup(ctx context.Context) Setup {
	setup := Setup{Home: f.Home, CFORuns: f.CFORuns()}
	chosen, err := f.DefaultAgent()
	if err != nil {
		setup.Problem = err.Error()
	}
	setup.DefaultAgent = chosen
	for _, name := range onboarding.Agents {
		setup.Agents = append(setup.Agents, f.Detect(ctx, name))
	}
	return setup
}

func (f *FirstRun) Start(ctx context.Context, agent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CFORuns() {
		return StartRefusal{Reason: "The CFO already runs; open its terminal from the board"}
	}
	if !slices.Contains(onboarding.Agents, agent) {
		return StartRefusal{Reason: "Choose Claude Code, Codex or pi"}
	}
	chosen := f.Detect(ctx, agent)
	if !chosen.Installed || !chosen.SignedIn {
		return StartRefusal{Reason: chosen.Reason + ". Run goblins setup in a terminal to continue."}
	}
	available, _, err := f.Memory()
	if err != nil {
		return StartRefusal{Reason: "Free memory could not be checked: " + err.Error()}
	}
	if available < 4<<30 {
		return StartRefusal{Reason: "At least 4 GB must be available to start the CFO", Passing: true}
	}
	if err := f.Save(agent); err != nil {
		return fmt.Errorf("save default agent: %w", err)
	}
	if err := f.StartCFO(agent); err != nil {
		return fmt.Errorf("the CFO could not be started: %w", err)
	}
	return nil
}

func (h *HTTP) setup(w http.ResponseWriter, r *http.Request) {
	if h.Service.Options.FirstRun == nil {
		apiError(w, http.StatusConflict, "This board cannot start a CFO")
		return
	}
	respond(w, http.StatusOK, h.Service.Options.FirstRun.Setup(r.Context()))
}

func (h *HTTP) startCFO(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Agent string `json:"agent"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.Service.Options.FirstRun == nil {
		apiError(w, http.StatusConflict, "This board cannot start a CFO")
		return
	}
	if err := h.Service.Options.FirstRun.Start(r.Context(), input.Agent); err != nil {
		status := http.StatusInternalServerError
		if errors.As(err, new(StartRefusal)) {
			status = http.StatusConflict
		}
		apiError(w, status, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, struct {
		Started bool `json:"started"`
	}{true})
}
