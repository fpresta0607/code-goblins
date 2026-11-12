package supervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cutover"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/update"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// crossingEvery is how often the supervisor looks at the move of its home to
// goblins' names that an update or an install made while the fleet worked,
// and finishes what it left: the terminals from before it, which hold the old
// settings, restart under the new names, the GitHub labels are renamed, and
// the old programs it kept for them go.
const crossingEvery = 30 * time.Second

// ceoCrossingTries bounds how often one supervisor restarts the CEO under the
// new names, so a CEO that does not come back on a new terminal is left
// running and the board says why.
const ceoCrossingTries = 3

// CrossingView is the move of the home to goblins' names for the board's one
// notice about it, shown before any terminal restarts.
type CrossingView struct {
	// At is when the home moved, which keeps one dismissal to one move.
	At time.Time `json:"at"`
	// Undo is the command that moves the home back, run with the fleet
	// paused.
	Undo string `json:"undo"`
	// Waiting names each terminal from before the move that restarts under
	// the new names once it is idle with nothing typed on its prompt line.
	Waiting []string `json:"waiting"`
	// Problem says in plain words what did not go as it should; empty when
	// all did.
	Problem string `json:"problem,omitempty"`
}

// keepCrossing finishes the home's move to goblins' names every interval
// until ctx ends. Its first look restarts nothing, so the board's notice and
// the CEO's wake say what restarts before anything does.
func (s *Service) keepCrossing(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for mayRestart := false; ; mayRestart = true {
		s.finishCrossing(ctx, mayRestart)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// finishCrossing looks at the home's move to goblins' names once, and does
// what it can of what the move left.
func (s *Service) finishCrossing(ctx context.Context, mayRestart bool) {
	stateDir := s.Store.Home.State
	crossing, isCrossed, err := cutover.ReadCrossing(stateDir)
	if err != nil {
		s.setCrossing(&CrossingView{Problem: "The record of the home's move to goblins' names could not be read: " + err.Error()})
		return
	}
	if !isCrossed {
		s.setCrossing(nil)
		return
	}
	view := CrossingView{At: crossing.At, Undo: fmt.Sprintf("goblins cutover --undo '%s'", crossing.Folder)}
	var problems []string
	waiting, restartProblems := s.restartFromBefore(ctx, crossing.At, mayRestart)
	view.Waiting, problems = waiting, append(problems, restartProblems...)
	if len(waiting) > 0 {
		detail := fmt.Sprintf("Code Goblins updated and moved this home to goblins' names: the command is goblins and the settings begin GOBLINS_, and the command and settings used before are gone. These restart under the new names, each once it is idle with nothing typed on its prompt line, on its own conversation: %s. The way back, with the fleet paused: %s", strings.Join(waiting, ", "), view.Undo)
		if _, _, err := wake.AppendFirst(stateDir, "crossing/"+crossing.At.Format(time.RFC3339Nano), "notify", "goblins", detail); err != nil {
			problems = append(problems, "The CEO could not be told of the move: "+err.Error())
		}
	}
	if !crossing.IsRelabelled && s.Options.Relabel != nil && !s.isRelabelTried {
		s.isRelabelTried = true
		crossing.Relabelling = ""
		if err := s.Options.Relabel(crossing.Folder); err != nil {
			crossing.Relabelling = err.Error()
		} else {
			crossing.IsRelabelled = true
		}
		if err := cutover.WriteCrossing(stateDir, crossing); err != nil {
			problems = append(problems, "The record of the move could not be kept: "+err.Error())
		}
	}
	if crossing.Relabelling != "" {
		problems = append(problems, "The GitHub labels were not renamed yet ("+crossing.Relabelling+"), and the board tries again when it next starts")
	}
	if len(waiting) == 0 && len(crossing.Bridges) > 0 {
		switch inUse, err := s.bridgesInUse(crossing.Bridges); {
		case err != nil:
			problems = append(problems, "What runs the old programs could not be read: "+err.Error())
		case !inUse:
			if err := cutover.RemoveBridges(s.Store.Home.Root, crossing.Bridges); err != nil {
				problems = append(problems, "The old programs were not removed yet: "+err.Error())
				break
			}
			crossing.Bridges = nil
			if err := cutover.WriteCrossing(stateDir, crossing); err != nil {
				problems = append(problems, "The record of the move could not be kept: "+err.Error())
			}
		}
	}
	view.Problem = strings.Join(problems, ". ")
	s.setCrossing(&view)
}

// restartFromBefore names each terminal that started before the home moved
// at at and still runs, the CEO's and each live goblin's, and, when it may,
// restarts each one at its stopping point under the new names: the CEO on its
// own conversation, a goblin by a pending restart that its turn's end takes
// (see applyEngineChoices). A paused goblin starts under the new names when
// it is resumed.
func (s *Service) restartFromBefore(ctx context.Context, at time.Time, mayRestart bool) (waiting, problems []string) {
	stateDir := s.Store.Home.State
	isBefore := func(id string) bool {
		record, err := host.ReadRecord(stateDir, id)
		return err == nil && host.Running(record) && record.Started.Before(at)
	}
	if ceo := readCEOState(stateDir); ceo.terminal == NativeCEOTerminal && isBefore(NativeCEOTerminal) {
		waiting = append(waiting, "the CEO")
		if mayRestart && ceo.registered {
			s.takeBackOldWakeLine(ceo.harness)
		}
		if mayRestart && ceo.registered && s.Options.FirstRun != nil && s.ceoCrossingTries < ceoCrossingTries && s.ceoAtStoppingPoint(ctx, ceo.harness) {
			s.ceoCrossingTries++
			if _, _, err := s.Options.FirstRun.Restart(""); err != nil {
				problems = append(problems, "The CEO was not restarted under the new names: "+err.Error())
			}
		}
	}
	for _, meta := range liveTasks(stateDir) {
		if meta.Backend != "native" || !isBefore(meta.ID) {
			continue
		}
		name := meta.GoblinName
		if name == "" {
			name = meta.ID
		}
		waiting = append(waiting, name)
		if !mayRestart {
			continue
		}
		if _, err := state.ReadEngineChoice(stateDir, meta.ID); err == nil {
			continue
		}
		choice := state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Model: meta.Model, Effort: meta.Effort, When: "renamed", Requested: time.Now().UTC()}
		if err := state.WriteEngineChoice(stateDir, choice); err != nil {
			problems = append(problems, fmt.Sprintf("%s was not set to restart under the new names: %v", name, err))
		}
	}
	return waiting, problems
}

// takeBackOldWakeLine takes a wake line that a board from before the move
// typed into the CEO's terminal, and its harness never took, back off the
// prompt line when it is all the line holds. The crossing restarts the CEO
// only with nothing unsent on its prompt line, and no board of this build
// types over words there, so nothing else would ever take it off. The wakes
// it named stay queued, and the restarted CEO is told to drain them. Words of
// his on the line keep it his.
func (s *Service) takeBackOldWakeLine(agent string) {
	screens, readable := harness.NativeScreens(harness.Kind(agent))
	if !readable {
		return
	}
	record, err := host.ReadRecord(s.Store.Home.State, NativeCEOTerminal)
	if err != nil {
		return
	}
	read := func() ([]string, error) { return s.readScreen(record) }
	holdsOldWakeLine := func() (string, bool) {
		screen, err := read()
		if err != nil {
			return "", false
		}
		draft, isRead := screens.Draft(screen)
		return draft, isRead && cutover.IsOldWakeLine(draft)
	}
	if _, holds := holdsOldWakeLine(); !holds {
		return
	}
	if s.Options.CEO != nil {
		s.Options.CEO.typing.Lock()
		defer s.Options.CEO.typing.Unlock()
	}
	draft, holds := holdsOldWakeLine()
	if !holds {
		return
	}
	client, err := host.Dial(record)
	if err != nil {
		return
	}
	defer client.Close()
	spawn.TakeBack(read, client.Input, screens, draft)
}

// bridgesInUse says whether something still needs the old programs a
// crossing kept: a process runs one, or the update of a release before the
// rename that ran this crossing has not finished, whose last step runs one.
func (s *Service) bridgesInUse(bridges []string) (bool, error) {
	if holder, err := lock.ReadNamed(update.Dir(s.Store.Home.State), update.ReleaseLockName); err == nil && holder.Alive() {
		return true, nil
	}
	programs := s.Options.Programs
	if programs == nil {
		programs = cutover.RunningPrograms{}
	}
	running, err := programs.Running()
	if err != nil {
		return false, err
	}
	for _, process := range running {
		for _, bridge := range bridges {
			if strings.EqualFold(filepath.Clean(process.Image), filepath.Clean(bridge)) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) setCrossing(view *CrossingView) {
	s.mu.Lock()
	isChanged := (s.crossing == nil) != (view == nil) || view != nil && (s.crossing.Problem != view.Problem || strings.Join(s.crossing.Waiting, "\x00") != strings.Join(view.Waiting, "\x00") || !s.crossing.At.Equal(view.At))
	s.crossing = view
	s.mu.Unlock()
	if isChanged {
		s.notify()
	}
}

// crossingView is the home's move to goblins' names for the snapshot.
func (s *Service) crossingView() *CrossingView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crossing == nil {
		return nil
	}
	view := *s.crossing
	return &view
}
