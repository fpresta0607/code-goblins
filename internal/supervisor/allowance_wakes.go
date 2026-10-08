package supervisor

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// allowanceWarnAt is the percent of a window used past which the CFO hears
// once, while goblins run on it, that it nears its end.
const allowanceWarnAt = 85

// allowanceWall is a session window seen used up while goblins ran on it,
// kept until it renews so the CFO is woken then to set going again the ones
// it stopped.
type allowanceWall struct {
	Provider string    `json:"provider"`
	Window   string    `json:"window"`
	Reset    time.Time `json:"reset"`
	Goblins  []string  `json:"goblins"`
}

// raiseAllowanceWakes reads report against the goblins running now: one
// allowance wake for each window bounding a goblin's model that passed
// allowanceWarnAt, once per window and reset, and one when a session window
// seen used up renews, naming the goblins it stopped. Claude Code goes on by
// itself at a renewal only when its screen offered to, so the CFO looks.
// Nothing here writes the words a harness refuses with, since the CFO's own
// terminal shows these wakes.
func (s *Service) raiseAllowanceWakes(report quota.Report, watched *fleetWakes, now time.Time) error {
	stateDir := s.Store.Home.State
	goblins, problems := runningGoblins(stateDir)
	type warning struct {
		provider string
		window   quota.Window
		goblins  []string
	}
	warnings := map[string]*warning{}
	for _, meta := range goblins {
		reading, ok := report.Providers[meta.Harness]
		if !ok || reading.Stale || !reading.Known {
			continue
		}
		scope := boundScope(reading, meta.Model)
		for _, window := range reading.Windows {
			if !slices.Contains(scope.BoundedBy, window.ID) || window.PercentUsed < allowanceWarnAt || !window.ResetsAt.After(now) {
				continue
			}
			// quota-axi's reset times move by microseconds between
			// readings of one window.
			identity := meta.Harness + "/" + window.ID + "/" + window.ResetsAt.UTC().Round(time.Minute).Format(time.RFC3339)
			if warnings[identity] == nil {
				warnings[identity] = &warning{provider: meta.Harness, window: window}
			}
			warnings[identity].goblins = append(warnings[identity].goblins, meta.ID)
			if window.Kind != "session" || window.PercentUsed < 100 {
				continue
			}
			if watched.AllowanceWalls == nil {
				watched.AllowanceWalls = map[string]allowanceWall{}
			}
			key := meta.Harness + "/" + window.ID
			wall := watched.AllowanceWalls[key]
			wall.Provider, wall.Window, wall.Reset = meta.Harness, window.ID, window.ResetsAt
			if !slices.Contains(wall.Goblins, meta.ID) {
				wall.Goblins = append(wall.Goblins, meta.ID)
				slices.Sort(wall.Goblins)
			}
			watched.AllowanceWalls[key] = wall
		}
	}
	raised := false
	for _, identity := range slices.Sorted(maps.Keys(warnings)) {
		found := warnings[identity]
		slices.Sort(found.goblins)
		detail := fmt.Sprintf("allowance_warning: %s's %s window is %s percent used and renews at %s, while goblins run on it: %s",
			found.provider, found.window.ID, strconv.FormatFloat(found.window.PercentUsed, 'f', -1, 64), found.window.ResetsAt.UTC().Format(time.RFC3339), strings.Join(found.goblins, ", "))
		_, isNew, err := wake.AppendFirst(stateDir, "allowance-warning/"+identity, "allowance", found.provider, detail)
		problems = errors.Join(problems, err)
		raised = raised || isNew
	}
	for _, key := range slices.Sorted(maps.Keys(watched.AllowanceWalls)) {
		wall := watched.AllowanceWalls[key]
		if now.Before(wall.Reset) {
			continue
		}
		renewed := wall.Reset.UTC().Format(time.RFC3339)
		detail := fmt.Sprintf("allowance_renewed: %s's %s window was used up and renewed at %s; goblins that stopped on it: %s. Peek each: one that went on by itself needs nothing, and one idle at its prompt needs a send to continue where it stopped and to run again any gate step that died on it",
			wall.Provider, wall.Window, renewed, strings.Join(wall.Goblins, ", "))
		_, isNew, err := wake.AppendFirst(stateDir, "allowance-renewed/"+key+"/"+wall.Reset.UTC().Round(time.Minute).Format(time.RFC3339), "allowance", wall.Provider, detail)
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		raised = raised || isNew
		delete(watched.AllowanceWalls, key)
	}
	if raised {
		_, err := wake.PublishEpisode(stateDir)
		problems = errors.Join(problems, err)
	}
	return problems
}

// runningGoblins are the live goblins whose native terminal runs now.
func runningGoblins(stateDir string) ([]state.TaskMeta, error) {
	var running []state.TaskMeta
	var problems error
	for _, meta := range liveTasks(stateDir) {
		if meta.Backend != "native" {
			continue
		}
		record, err := host.ReadRecord(stateDir, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if host.Running(record) {
			running = append(running, meta)
		}
	}
	return running, problems
}
