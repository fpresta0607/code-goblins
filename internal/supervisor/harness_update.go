package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervise"
)

// The board's Update button. A harness keeps running the install it started
// from, so an update of Claude Code, Codex or pi installed while the CFO or a
// goblin runs is taken only by a restart. The supervisor looks for one in
// every native terminal, and the board offers Update on the CFO's header and
// on the goblin's card while one waits. Nothing restarts until the Overlord
// presses it, and then only at a stopping point: the CFO on its own
// conversation through the board's Restart CFO path, a goblin through a
// same-values switch at the end of its turn (see applyEngineChoices).

// harnessUpdateEvery is how often the supervisor looks for harness updates,
// and for the end of the CFO's turn once its Update was pressed.
const harnessUpdateEvery = 10 * time.Second

// cfoUpdateFile holds the Overlord's press of the CFO's Update while it waits
// for the CFO's turn to end.
const cfoUpdateFile = "cfo-update.json"

// HarnessUpdate is an update of the harness a native terminal runs that was
// installed after that harness started. Line is the harness's own words for
// it on its screen and Installed when its program was installed since,
// whichever showed it.
type HarnessUpdate struct {
	Harness   string    `json:"harness"`
	Line      string    `json:"line,omitempty"`
	Installed time.Time `json:"installed,omitzero"`
}

// CFOUpdate is the CFO's harness update as its Update button shows it:
// Pending once the Overlord pressed it, until the CFO's turn ends; Updating
// while the restart runs; Problem why the last press did not restart it.
type CFOUpdate struct {
	HarnessUpdate
	Pending  bool   `json:"pending,omitempty"`
	Updating bool   `json:"updating,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

// harnessUpdateRead is an update as the last look found it, for the terminal
// whose host started at Since, which a terminal started again replaces, and
// for a goblin's Generation.
type harnessUpdateRead struct {
	HarnessUpdate
	Since      time.Time
	Generation string
}

// cfoUpdatePress is the Overlord's press of the CFO's Update, for the CFO
// whose terminal's host started at Since: a press never outlives the CFO it
// was made for.
type cfoUpdatePress struct {
	Requested time.Time `json:"requested"`
	Since     time.Time `json:"since"`
}

// harnessUpdate says whether the harness kind, in a native terminal whose
// program started at started and shows screen, waits for a restart onto an
// update: its own screen says so, or its program was installed at installed,
// after it started.
func harnessUpdate(kind harness.Kind, screen []string, started, installed time.Time) (HarnessUpdate, bool) {
	update := HarnessUpdate{Harness: string(kind)}
	if line, shown := harness.UpdateNotice(kind, screen); shown {
		update.Line = line
	}
	if !started.IsZero() && installed.After(started) {
		update.Installed = installed
	}
	return update, update.Line != "" || !update.Installed.IsZero()
}

// keepHarnessUpdates looks for harness updates every interval until ctx ends,
// and restarts a CFO whose Update was pressed once its turn ends.
func (s *Service) keepHarnessUpdates(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.lookForHarnessUpdates()
		s.applyCFOUpdate(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// lookForHarnessUpdates reads every running native terminal for an update of
// its harness: the CFO's, while it runs a harness that resumes its
// conversation, and each live goblin's not paused or changing.
func (s *Service) lookForHarnessUpdates() {
	stateDir := s.Store.Home.State
	installed := map[harness.Kind]time.Time{}
	look := func(id string, kind harness.Kind) (harnessUpdateRead, bool) {
		record, err := host.ReadRecord(stateDir, id)
		if err != nil || !host.Running(record) {
			return harnessUpdateRead{}, false
		}
		at, seen := installed[kind]
		if !seen {
			// A harness not on PATH has no install to compare.
			_, at, _ = s.programInstalled(kind)
			installed[kind] = at
		}
		// An unreadable screen shows no line; the install still counts.
		screen, _ := s.readScreen(record)
		update, waits := harnessUpdate(kind, screen, record.ChildStart, at)
		return harnessUpdateRead{HarnessUpdate: update, Since: record.Started}, waits
	}
	var cfoRead *harnessUpdateRead
	if cfo := readCFOState(stateDir); cfo.registered && cfo.terminal == NativeCFOTerminal {
		if capability, known := CFOCapabilityFor(cfo.harness); known && capability.Resumes {
			if read, waits := look(NativeCFOTerminal, harness.Kind(cfo.harness)); waits {
				cfoRead = &read
			}
		}
	}
	goblins := map[string]harnessUpdateRead{}
	for _, meta := range liveTasks(stateDir) {
		if meta.Backend != "native" {
			continue
		}
		if record, err := state.ReadLifecycle(stateDir, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.Phase != "running" {
			continue
		}
		if read, waits := look(meta.ID, harness.Kind(meta.Harness)); waits {
			read.Generation = meta.SpawnGen
			goblins[meta.ID] = read
		}
	}
	s.mu.Lock()
	isChanged := !maps.Equal(s.harnessUpdates, goblins) || (s.cfoUpdateRead == nil) != (cfoRead == nil) || cfoRead != nil && *s.cfoUpdateRead != *cfoRead
	s.cfoUpdateRead, s.harnessUpdates = cfoRead, goblins
	s.mu.Unlock()
	if isChanged {
		s.notify()
	}
}

// readScreen reads a native terminal's console, through the test's reader
// when it has one.
func (s *Service) readScreen(record host.Record) ([]string, error) {
	if s.Options.ReadScreen != nil {
		return s.Options.ReadScreen(record)
	}
	return host.ReadScreen(record)
}

// programInstalled reads when kind's program was installed, through the
// test's reader when it has one.
func (s *Service) programInstalled(kind harness.Kind) (string, time.Time, error) {
	if s.Options.ProgramInstalled != nil {
		return s.Options.ProgramInstalled(kind)
	}
	return harness.ProgramInstalled(kind)
}

// cfoUpdateView is the CFO's update for the snapshot, shown only while the
// CFO it was read for still runs in the terminal started at since.
func (s *Service) cfoUpdateView(since time.Time) *CFOUpdate {
	s.mu.Lock()
	read, updating, problem := s.cfoUpdateRead, s.cfoUpdating, s.cfoUpdateProblem
	s.mu.Unlock()
	if read == nil || !read.Since.Equal(since) {
		return nil
	}
	view := &CFOUpdate{HarnessUpdate: read.HarnessUpdate, Updating: updating, Problem: problem}
	if press, err := readCFOUpdatePress(s.Store.Home.State); err == nil && press.Since.Equal(since) {
		view.Pending = true
	}
	return view
}

// goblinUpdate is a goblin's update for its card, while its generation runs.
func (s *Service) goblinUpdate(id, generation string) *HarnessUpdate {
	s.mu.Lock()
	read, waits := s.harnessUpdates[id]
	s.mu.Unlock()
	if !waits || read.Generation != generation {
		return nil
	}
	return &read.HarnessUpdate
}

// applyCFOUpdate restarts the CFO onto its harness update once the Overlord
// pressed Update for it and its turn has ended. A press made for a CFO that
// has since gone, or that runs the update already, is dropped.
func (s *Service) applyCFOUpdate(ctx context.Context) {
	stateDir := s.Store.Home.State
	press, err := readCFOUpdatePress(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	cfo := readCFOState(stateDir)
	s.mu.Lock()
	read := s.cfoUpdateRead
	s.mu.Unlock()
	if err != nil || !cfo.registered || !press.Since.Equal(cfo.since) || read == nil || !read.Since.Equal(cfo.since) {
		s.dropCFOUpdatePress(err)
		return
	}
	if !s.cfoAtStoppingPoint(ctx, cfo.harness) {
		return
	}
	// The press is spent before the restart, so a supervisor that dies in
	// it never restarts the CFO a second time.
	if err := os.Remove(filepath.Join(stateDir, cfoUpdateFile)); err != nil {
		s.setCFOUpdate(false, "The update could not start: "+err.Error())
		return
	}
	s.setCFOUpdate(true, "")
	problem := ""
	if s.Options.FirstRun == nil {
		problem = "This board cannot restart the CFO"
	} else if _, _, err := s.Options.FirstRun.Restart(); err != nil {
		problem = err.Error()
	}
	s.setCFOUpdate(false, problem)
}

// dropCFOUpdatePress removes a press that no longer stands, saying why when
// the press itself could not be read.
func (s *Service) dropCFOUpdatePress(readErr error) {
	problem := ""
	if readErr != nil {
		problem = "The pressed update could not be read: " + readErr.Error()
	}
	if err := os.Remove(filepath.Join(s.Store.Home.State, cfoUpdateFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		problem = "The pressed update could not be cleared: " + err.Error()
	}
	s.setCFOUpdate(false, problem)
}

func (s *Service) setCFOUpdate(updating bool, problem string) {
	s.mu.Lock()
	s.cfoUpdating, s.cfoUpdateProblem = updating, problem
	s.mu.Unlock()
	s.notify()
}

// cfoAtStoppingPoint reports whether the CFO's turn has ended, on two
// readings cfoIdleRecheck apart, so a CFO caught between two steps of one
// turn is not taken for one at rest.
func (s *Service) cfoAtStoppingPoint(ctx context.Context, agent string) bool {
	stateDir := s.Store.Home.State
	screens, readable := harness.NativeScreens(harness.Kind(agent))
	if !readable {
		return false
	}
	for reading := range 2 {
		if reading > 0 {
			select {
			case <-time.After(cfoIdleRecheck):
			case <-ctx.Done():
				return false
			}
		}
		record, err := host.ReadRecord(stateDir, NativeCFOTerminal)
		if err != nil {
			return false
		}
		screen, err := s.readScreen(record)
		if err != nil || !atStoppingPoint(screens, screen, s.cfoStopHookWaits()) {
			return false
		}
	}
	return true
}

// atStoppingPoint reports whether screen shows a harness whose turn has ended:
// no dialog shows, and either its Stop hook waits on the wake queue, or no
// turn, tool or background work runs and nothing waits unsent in its
// composer. A Claude Code CFO's Stop hook waits out the end of every turn
// while goblins work, drawn as a turn still running, so its wait is read
// from the hook rather than the screen.
func atStoppingPoint(screens harness.Screens, screen []string, isHookWaiting bool) bool {
	if _, dialog := screens.Dialog(screen); dialog {
		return false
	}
	if isHookWaiting {
		return true
	}
	_, running := harness.RunningWork(screen)
	return !running && !screens.IsWorking(screen) && screens.ComposerEmpty(screen)
}

// cfoStopHookWaits reports whether the Stop hook that holds the auto-arm lock
// runs under the registered CFO's own harness, so it waits out the CFO's turn
// and not another session's.
func (s *Service) cfoStopHookWaits() bool {
	hook, holds := supervise.StopHook(s.Store.Home.State)
	primary, live := livePrimary(s.Store.Home.State)
	if !holds || !live {
		return false
	}
	entries, err := proc.Ancestry(hook.PID, 32)
	return err == nil && descendsFrom(entries, primary.Process)
}

func readCFOUpdatePress(stateDir string) (cfoUpdatePress, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, cfoUpdateFile))
	if err != nil {
		return cfoUpdatePress{}, err
	}
	var press cfoUpdatePress
	if err := json.Unmarshal(data, &press); err != nil {
		return cfoUpdatePress{}, err
	}
	return press, nil
}

// updateCFO serves POST /api/cfo/update, the CFO's Update button: it records
// the Overlord's press for the CFO that runs now, which the supervisor acts on
// at the end of its turn, or with cancel takes the press back.
func (h *HTTP) updateCFO(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Cancel bool `json:"cancel"`
	}
	if err := decodeBody(w, r, &input, 1<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	s := h.Service
	path := filepath.Join(s.Store.Home.State, cfoUpdateFile)
	if input.Cancel {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.notify()
		respond(w, http.StatusOK, struct {
			Cancelled bool `json:"cancelled"`
		}{true})
		return
	}
	if s.Options.FirstRun == nil {
		apiError(w, http.StatusConflict, "This board cannot restart the CFO")
		return
	}
	cfo := readCFOState(s.Store.Home.State)
	if !cfo.registered || cfo.terminal != NativeCFOTerminal || s.cfoUpdateView(cfo.since) == nil {
		apiError(w, http.StatusConflict, "No harness update waits for the CFO")
		return
	}
	data, err := json.Marshal(cfoUpdatePress{Requested: time.Now().UTC(), Since: cfo.since})
	if err == nil {
		err = fsx.AtomicWriteFile(path, data)
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setCFOUpdate(false, "")
	respond(w, http.StatusAccepted, struct {
		Pending bool `json:"pending"`
	}{true})
}
