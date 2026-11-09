package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ceoEngineNextStart says why a saved selection waits: the CEO's next start
// uses it.
const ceoEngineNextStart = "The CEO's next start uses the saved engine"

type CEOEngine struct {
	Harness string    `json:"harness"`
	Model   string    `json:"model"`
	Effort  string    `json:"effort"`
	Pending bool      `json:"pending,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	Problem string    `json:"problem,omitempty"`
	Waiting string    `json:"waiting,omitempty"`
	// Overridden is the harness an explicit start chose over this selection,
	// which every later start that names no harness keeps until the CEO
	// starts as the selection's own harness or a selection is saved again.
	Overridden string `json:"overridden,omitempty"`
}

func writeCEOEngine(stateDir string, choice CEOEngine) error {
	data, err := json.Marshal(choice)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, "ceo-engine.json"), data)
}

func (s *Service) ceoEngineActivity() string {
	record, err := host.ReadRecord(s.Store.Home.State, NativeCEOTerminal)
	if err != nil {
		return "Cannot inspect the CEO's terminal; reopen it before applying the selection"
	}
	inspect := s.Options.CEOJobActivity
	if inspect == nil {
		inspect = ceoJobActivity
	}
	reason, err := inspect(record)
	if err != nil {
		return "Cannot verify the CEO's terminal work; restore process access and try again: " + err.Error()
	}
	return reason
}

func (s *Service) noteCEOEngineWaiting(choice CEOEngine, reason string) {
	s.starts.Lock()
	defer s.starts.Unlock()
	current, err := ReadCEOEngine(s.Store.Home.State)
	if err != nil || current != choice || choice.Waiting == reason {
		return
	}
	choice.Waiting = reason
	if err := writeCEOEngine(s.Store.Home.State, choice); err != nil {
		s.publish(err)
		return
	}
	s.notify()
}

func (s *Service) applyCEOEngine(ctx context.Context) {
	stateDir := s.Store.Home.State
	choice, err := ReadCEOEngine(stateDir)
	if err != nil || !choice.Pending {
		return
	}
	ceo := readCEOState(stateDir)
	// A terminal that is starting holds the launch the selection is applied by,
	// and registers seconds later: while it does, the selection waits rather
	// than being reported as one a session left behind.
	if ceo.starting && !ceo.registered {
		s.noteCEOEngineWaiting(choice, "Waiting for the CEO's terminal to start")
		return
	}
	if choice.Since.IsZero() {
		if !ceo.registered || ceo.since.IsZero() {
			s.noteCEOEngineWaiting(choice, ceoEngineNextStart)
			return
		}
		s.starts.Lock()
		if current, err := ReadCEOEngine(stateDir); err == nil && current == choice {
			choice.Since, choice.Waiting = ceo.since, ceoEngineNextStart
			if err := writeCEOEngine(stateDir, choice); err != nil {
				s.publish(err)
			}
			s.notify()
		}
		s.starts.Unlock()
	}
	if !ceo.registered || !ceo.since.Equal(choice.Since) {
		problem := "The CEO session changed; the selection is saved for its next start"
		if !choice.Since.IsZero() && ceo.registered && ceo.harness == choice.Harness && ceo.since.After(choice.Since) {
			problem = ""
		}
		s.starts.Lock()
		if current, err := ReadCEOEngine(stateDir); err == nil && current == choice {
			choice.Pending = false
			choice.Problem = problem
			if err := writeCEOEngine(stateDir, choice); err != nil {
				s.publish(err)
			}
			s.notify()
		}
		s.starts.Unlock()
		return
	}
	if reason := s.ceoEngineActivity(); reason != "" {
		s.noteCEOEngineWaiting(choice, reason)
		return
	}
	if !s.ceoAtStoppingPoint(ctx, ceo.harness) {
		s.noteCEOEngineWaiting(choice, "Waiting for the CEO's turn, terminal draft and background work to finish")
		return
	}
	s.starts.Lock()
	defer s.starts.Unlock()
	current, err := ReadCEOEngine(stateDir)
	if err != nil || current != choice {
		return
	}
	ceo = readCEOState(stateDir)
	if !ceo.registered || !ceo.since.Equal(choice.Since) {
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
		choice.Problem = fmt.Sprintf("The selection could not be applied: %v. It stays saved, with model %s and effort %s, and is used at the CEO's next start", err, choice.Model, choice.Effort)
		if writeErr := writeCEOEngine(stateDir, choice); writeErr != nil {
			s.publish(writeErr)
		}
		s.notify()
		return
	}
	if reason := s.ceoEngineActivity(); reason != "" {
		choice.Waiting = reason
		if err := writeCEOEngine(stateDir, choice); err != nil {
			s.publish(err)
		}
		s.notify()
		return
	}
	if !s.ceoAtStoppingPoint(ctx, ceo.harness) {
		return
	}
	ceo = readCEOState(stateDir)
	if !ceo.registered || !ceo.since.Equal(choice.Since) {
		return
	}
	choice.Pending, choice.Waiting = false, ""
	if err := writeCEOEngine(stateDir, choice); err != nil {
		s.publish(err)
		return
	}
	if s.Options.FirstRun == nil {
		choice.Problem = "This board cannot restart the CEO"
	} else if _, _, err := s.Options.FirstRun.Restart(choice.Harness); err != nil {
		choice.Problem = err.Error()
	}
	if choice.Problem != "" {
		if err := writeCEOEngine(stateDir, choice); err != nil {
			s.publish(err)
		}
	}
	s.notify()
}

// ReadCEOEngine reads the engine the CEO was selected to run. It checks what
// it hands a harness's command line: a known harness, and a model and effort
// that are text rather than switches. Which models and efforts a harness
// offers is the installed catalog's to say, and is checked when the selection
// is saved, since a harness's own catalog can offer one the fleet's shared
// list does not.
func ReadCEOEngine(stateDir string) (CEOEngine, error) {
	path := filepath.Join(stateDir, "ceo-engine.json")
	data, err := fsx.ReadFile(path)
	if err != nil {
		return CEOEngine{}, err
	}
	var choice CEOEngine
	if err := json.Unmarshal(data, &choice); err != nil {
		return CEOEngine{}, fmt.Errorf("the CEO's selected engine in %s is unreadable: %w", path, err)
	}
	harnesses := []string{"claude", "codex", "pi"}
	if !slices.Contains(harnesses, choice.Harness) || !engineValue(choice.Model) || !engineValue(choice.Effort) || (choice.Overridden != "" && !slices.Contains(harnesses, choice.Overridden)) {
		return CEOEngine{}, fmt.Errorf("the CEO's selected engine in %s names no harness, model and effort the fleet can start: choose the CEO's engine again on the board, or delete it", path)
	}
	return choice, nil
}

// engineValue reports whether value can be handed to a harness's command line
// as a model or an effort: some text that is not a switch, and not the
// catalog's word for whichever one the harness picks.
func engineValue(value string) bool {
	return value != "" && value != "default" && !strings.HasPrefix(value, "-") && spawnValue.MatchString(value)
}

// NoteCEOEngineOverridden records that the CEO started as started rather than
// as the harness its saved selection names, which a start with an explicit
// harness does and which the board reports beside the selection, and keeps
// started as the harness a later start that names none runs, so a harness
// Update or goblins resume brings the CEO back on its own conversation. The
// selection stays saved for when the CEO runs as its own harness again, and
// nothing is written when it already names started or when no selection is
// saved.
func NoteCEOEngineOverridden(stateDir, started string) error {
	choice, err := ReadCEOEngine(stateDir)
	if err != nil {
		// Nothing is saved that names a harness, so there is nothing to note; a
		// selection the fleet cannot read is the board's to report.
		return nil
	}
	if choice.Harness == started {
		return nil
	}
	choice.Pending, choice.Overridden = false, started
	choice.Problem = fmt.Sprintf("The CEO started as %s, not as the %s its saved engine names; the selection is used when the CEO runs as %s again", started, choice.Harness, choice.Harness)
	return writeCEOEngine(stateDir, choice)
}

// NoteCEOEngineApplied records that the CEO started as started with its saved
// selection's model and effort, so a problem an earlier start or apply left on
// the selection, and a harness an explicit start chose over it, no longer
// hold. Whether a change is pending stays as it is, and nothing is written
// when the selection names another harness, carries neither, or none is
// saved.
func NoteCEOEngineApplied(stateDir, started string) error {
	choice, err := ReadCEOEngine(stateDir)
	if err != nil || choice.Harness != started || (choice.Problem == "" && choice.Overridden == "") {
		return nil
	}
	choice.Problem, choice.Overridden = "", ""
	return writeCEOEngine(stateDir, choice)
}

func (h *HTTP) selectCEOEngine(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Harness string `json:"harness"`
		Model   string `json:"model"`
		Effort  string `json:"effort"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	choice := CEOEngine{Harness: input.Harness, Model: input.Model, Effort: input.Effort}
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
	ceo := readCEOState(h.Service.Store.Home.State)
	if ceo.registered && ceo.terminal == NativeCEOTerminal {
		choice.Pending, choice.Since = true, ceo.since
	} else if ceo.starting {
		choice.Pending = true
	}
	err = writeCEOEngine(h.Service.Store.Home.State, choice)
	if err != nil {
		apiError(w, http.StatusInternalServerError, fmt.Sprintf("Save CEO engine: %v", err))
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, choice)
}
