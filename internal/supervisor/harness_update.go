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
// from, so an update of Claude Code, Codex or pi installed while the CEO or a
// goblin runs is taken only by a restart. The supervisor looks for one in
// every native terminal, and the board offers Update on the CEO's header and
// on the goblin's card while one waits. Nothing restarts until the Overlord
// presses it, and then only at a stopping point: the CEO on its own
// conversation through the board's Restart CEO path, a goblin through a
// same-values switch at the end of its turn (see applyEngineChoices).

// harnessUpdateEvery is how often the supervisor looks for harness updates,
// and for the end of the CEO's turn once its Update was pressed.
const harnessUpdateEvery = 10 * time.Second

// ceoUpdateFile holds the Overlord's press of the CEO's Update while it waits
// for the CEO's turn to end.
const ceoUpdateFile = "ceo-update.json"

// HarnessUpdate is an update of the harness a native terminal runs that was
// installed after that harness started. Line is the harness's own words for
// it on its screen and Installed when its program was installed since,
// whichever showed it.
type HarnessUpdate struct {
	Harness   string    `json:"harness"`
	Line      string    `json:"line,omitempty"`
	Installed time.Time `json:"installed,omitzero"`
}

// CEOUpdate is the CEO's harness update as its Update button shows it:
// Pending once the Overlord pressed it, until the CEO's turn ends; Updating
// while the restart runs; Problem why the last press did not restart it.
type CEOUpdate struct {
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

// ceoUpdatePress is the Overlord's press of the CEO's Update, for the CEO
// whose terminal's host started at Since: a press never outlives the CEO it
// was made for.
type ceoUpdatePress struct {
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
// and restarts a CEO whose Update was pressed once its turn ends.
func (s *Service) keepHarnessUpdates(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.lookForHarnessUpdates()
		s.applyCEOUpdate(ctx)
		s.applyCEOEngine(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// lookForHarnessUpdates reads every running native terminal for an update of
// its harness: the CEO's, while it runs a harness that resumes its
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
	var ceoRead *harnessUpdateRead
	if ceo := readCEOState(stateDir); ceo.registered && ceo.terminal == NativeCEOTerminal {
		if capability, known := CEOCapabilityFor(ceo.harness); known && capability.Resumes {
			if read, waits := look(NativeCEOTerminal, harness.Kind(ceo.harness)); waits {
				ceoRead = &read
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
	isChanged := !maps.Equal(s.harnessUpdates, goblins) || (s.ceoUpdateRead == nil) != (ceoRead == nil) || ceoRead != nil && *s.ceoUpdateRead != *ceoRead
	s.ceoUpdateRead, s.harnessUpdates = ceoRead, goblins
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

// ceoUpdateView is the CEO's update for the snapshot, shown only while the
// CEO it was read for still runs in the terminal started at since.
func (s *Service) ceoUpdateView(since time.Time) *CEOUpdate {
	s.mu.Lock()
	read, updating, problem := s.ceoUpdateRead, s.ceoUpdating, s.ceoUpdateProblem
	s.mu.Unlock()
	if read == nil || !read.Since.Equal(since) {
		return nil
	}
	view := &CEOUpdate{HarnessUpdate: read.HarnessUpdate, Updating: updating, Problem: problem}
	if press, err := readCEOUpdatePress(s.Store.Home.State); err == nil && press.Since.Equal(since) {
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

// applyCEOUpdate restarts the CEO onto its harness update once the Overlord
// pressed Update for it and its turn has ended. A press made for a CEO that
// has since gone, or that runs the update already, is dropped.
func (s *Service) applyCEOUpdate(ctx context.Context) {
	stateDir := s.Store.Home.State
	press, err := readCEOUpdatePress(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	ceo := readCEOState(stateDir)
	s.mu.Lock()
	read := s.ceoUpdateRead
	s.mu.Unlock()
	if err != nil || !ceo.registered || !press.Since.Equal(ceo.since) || read == nil || !read.Since.Equal(ceo.since) {
		s.dropCEOUpdatePress(err)
		return
	}
	if !s.ceoAtStoppingPoint(ctx, ceo.harness) {
		return
	}
	// The press is spent before the restart, so a supervisor that dies in
	// it never restarts the CEO a second time.
	if err := os.Remove(filepath.Join(stateDir, ceoUpdateFile)); err != nil {
		s.setCEOUpdate(false, "The update could not start: "+err.Error())
		return
	}
	s.setCEOUpdate(true, "")
	problem := ""
	if s.Options.FirstRun == nil {
		problem = "This board cannot restart the CEO"
	} else if _, _, err := s.Options.FirstRun.Restart(""); err != nil {
		problem = err.Error()
	}
	s.setCEOUpdate(false, problem)
}

// dropCEOUpdatePress removes a press that no longer stands, saying why when
// the press itself could not be read.
func (s *Service) dropCEOUpdatePress(readErr error) {
	problem := ""
	if readErr != nil {
		problem = "The pressed update could not be read: " + readErr.Error()
	}
	if err := os.Remove(filepath.Join(s.Store.Home.State, ceoUpdateFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		problem = "The pressed update could not be cleared: " + err.Error()
	}
	s.setCEOUpdate(false, problem)
}

func (s *Service) setCEOUpdate(updating bool, problem string) {
	s.mu.Lock()
	s.ceoUpdating, s.ceoUpdateProblem = updating, problem
	s.mu.Unlock()
	s.notify()
}

// ceoAtStoppingPoint reports whether the CEO's turn has ended, on two
// readings ceoIdleRecheck apart, so a CEO caught between two steps of one
// turn is not taken for one at rest.
func (s *Service) ceoAtStoppingPoint(ctx context.Context, agent string) bool {
	stateDir := s.Store.Home.State
	screens, readable := harness.NativeScreens(harness.Kind(agent))
	if !readable {
		return false
	}
	for reading := range 2 {
		if reading > 0 {
			select {
			case <-time.After(ceoIdleRecheck):
			case <-ctx.Done():
				return false
			}
		}
		record, err := host.ReadRecord(stateDir, NativeCEOTerminal)
		if err != nil {
			return false
		}
		screen, err := s.readScreen(record)
		if err != nil || !atStoppingPoint(screens, screen, s.ceoStopHookWaits()) {
			return false
		}
	}
	return true
}

// atStoppingPoint reports whether screen shows a harness whose turn has ended:
// no dialog shows, its composer is empty, and either its Stop hook waits on
// the wake queue, or no turn, tool or background work runs. A Claude Code CEO's Stop hook waits out the end of every turn
// while goblins work, drawn as a turn still running, so its wait is read
// from the hook rather than the screen.
func atStoppingPoint(screens harness.Screens, screen []string, isHookWaiting bool) bool {
	if _, dialog := screens.Dialog(screen); dialog {
		return false
	}
	if isHookWaiting {
		return screens.ComposerEmpty(screen) && !harness.HasBackgroundWork(screen)
	}
	_, running := harness.RunningWork(screen)
	return !running && !screens.IsWorking(screen) && screens.ComposerEmpty(screen)
}

// ceoStopHookWaits reports whether the Stop hook that holds the auto-arm lock
// runs under the registered CEO's own harness, so it waits out the CEO's turn
// and not another session's.
func (s *Service) ceoStopHookWaits() bool {
	hook, holds := supervise.StopHook(s.Store.Home.State)
	primary, live := livePrimary(s.Store.Home.State)
	if !holds || !live {
		return false
	}
	entries, err := proc.Ancestry(hook.PID, 32)
	return err == nil && descendsFrom(entries, primary.Process)
}

func readCEOUpdatePress(stateDir string) (ceoUpdatePress, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, ceoUpdateFile))
	if err != nil {
		return ceoUpdatePress{}, err
	}
	var press ceoUpdatePress
	if err := json.Unmarshal(data, &press); err != nil {
		return ceoUpdatePress{}, err
	}
	return press, nil
}

// updateCEO serves POST /api/ceo/update, the CEO's Update button: it records
// the Overlord's press for the CEO that runs now, which the supervisor acts on
// at the end of its turn, or with cancel takes the press back.
func (h *HTTP) updateCEO(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Cancel bool `json:"cancel"`
	}
	if err := decodeBody(w, r, &input, 1<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	s := h.Service
	path := filepath.Join(s.Store.Home.State, ceoUpdateFile)
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
		apiError(w, http.StatusConflict, "This board cannot restart the CEO")
		return
	}
	ceo := readCEOState(s.Store.Home.State)
	if !ceo.registered || ceo.terminal != NativeCEOTerminal || s.ceoUpdateView(ceo.since) == nil {
		apiError(w, http.StatusConflict, "No harness update waits for the CEO")
		return
	}
	data, err := json.Marshal(ceoUpdatePress{Requested: time.Now().UTC(), Since: ceo.since})
	if err == nil {
		err = fsx.AtomicWriteFile(path, data)
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setCEOUpdate(false, "")
	respond(w, http.StatusAccepted, struct {
		Pending bool `json:"pending"`
	}{true})
}
