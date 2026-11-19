package supervisor

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The memory floor keeps the machine working whether or not the Overlord is
// away (the Overlord, 2026-10-08: "it should work regardless of afk thats a
// critical core feature"). When one reading of the memory guard finds memory
// or commit under the floor, the supervisor pauses at once the live goblin
// whose process tree grew most since the reading before, or the one lowest in
// the Overlord's In progress order when none grew, that is not pushing or
// merging, through the pause the allowance floor uses, one goblin at a time,
// and the scheduler resumes it once memory is back at the next-start mark and
// a new session leaves the machine's heavy line its next floor. A goblin whose
// turn of the heavy line runs grows most because the line admitted it, so it
// is paused only when no other goblin may be (#2808). While AFK mode is on,
// its log keeps each pause for the report of the stretch.

// gitPushOrMerge are the git commands that push or merge, which a goblin is
// left to finish rather than paused in the middle of.
var gitPushOrMerge = []string{"push", "merge", "pull", "rebase", "cherry-pick", "am"}

// freshResume is how long a goblin resumed from a memory pause is paused at
// the memory floor only when no other goblin may be. Its resume cost a
// session start and a handoff, and on 2026-11-02 the floor paused goblins
// within ten minutes of their start or resume, each the lowest in the order
// (#1926).
const freshResume = 10 * time.Minute

// floorPick is the goblin the memory floor pauses, and why it is that one.
type floorPick struct {
	meta   state.TaskMeta
	record state.Lifecycle
	why    string
}

// floorCandidate is a goblin the memory floor may pause, how many bytes of
// private working set its process tree grew since the guard's reading before,
// and whether it holds a turn of the machine's heavy line.
type floorCandidate struct {
	meta          state.TaskMeta
	record        state.Lifecycle
	host          host.Record
	growth        uint64
	isHoldingTurn bool
}

// pausableGoblins are the goblins of live, in the order they are given, that
// are not paused or changing and whose last pause at the memory floor did not
// fail, with their growth from growth by task and whether holders names them.
func (s *Service) pausableGoblins(live []guardedGoblin, growth map[string]uint64, holders []string) ([]floorCandidate, error) {
	stateDir := s.Store.Home.State
	var candidates []floorCandidate
	var problems error
	for _, goblin := range live {
		record, err := state.ReadLifecycle(stateDir, goblin.meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = errors.Join(problems, err)
			continue
		}
		failed := record.Action == "pause" && record.Phase == "failed" && record.Pause != nil && record.Pause.Reason == "memory"
		if record.Generation == goblin.meta.SpawnGen && (record.SuppressesMonitoring(stateDir) || failed) {
			continue
		}
		candidates = append(candidates, floorCandidate{goblin.meta, record, goblin.host, growth[goblin.meta.ID], slices.Contains(holders, goblin.meta.ID)})
	}
	return candidates, problems
}

// hostsPushingOrMerging reads the machine's processes and returns whether a
// terminal host has a process pushing or merging under it. Processes that
// cannot be listed tell nothing of pushing or merging, so no host is taken to
// push or merge then.
func hostsPushingOrMerging() func(hostPID int) bool {
	processes, err := proc.Processes()
	if err != nil {
		return func(int) bool { return false }
	}
	return func(hostPID int) bool { return pushingOrMerging(hostPID, processes, proc.Arguments) }
}

// pickToPause is the goblin the memory floor pauses at a reading at now, of
// the lowest-first candidates: of those isPushing says are not pushing or
// merging, hold no turn of the heavy line and did not resume from a memory
// pause in the last freshResume, or of those that did when no other may be
// paused, or of those holding a turn when no other may be paused, the one
// whose process tree grew most, the lowest in the Overlord's In progress order
// when none grew or they grew alike. When it finds none it says why in its
// last result.
func pickToPause(candidates []floorCandidate, isPushing func(hostPID int) bool, now time.Time) (floorPick, bool, string) {
	var free []floorCandidate
	for _, one := range candidates {
		if !isPushing(one.host.HostPID) {
			free = append(free, one)
		}
	}
	switch {
	case len(candidates) == 0:
		return floorPick{}, false, "nothing to pause: no live goblin may be paused"
	case len(free) == 0:
		return floorPick{}, false, "nothing to pause: every goblin it could pause is pushing or merging"
	}
	isFresh := func(one floorCandidate) bool {
		return one.record.Generation == one.meta.SpawnGen && one.record.Action == "resume" && one.record.Phase == "running" && one.record.Pause != nil && one.record.Pause.Reason == "memory" && now.Sub(one.record.Updated) < freshResume
	}
	var settled, fresh, holding []floorCandidate
	for _, one := range free {
		switch {
		case one.isHoldingTurn:
			holding = append(holding, one)
		case isFresh(one):
			fresh = append(fresh, one)
		default:
			settled = append(settled, one)
		}
	}
	pool, passing := settled, ""
	switch ideal := mostGrown(free); {
	case len(settled) == 0 && len(fresh) == 0:
		pool, passing = holding, ", and the last goblin it could pause, though its turn of the heavy line runs"
	case len(settled) == 0 && len(holding) > 0:
		pool, passing = fresh, fmt.Sprintf(" past %s, whose turn of the heavy line runs, and every other goblin it could pause resumed from a memory pause in the last %d minutes", mostGrown(holding).meta.ID, int(freshResume/time.Minute))
	case len(settled) == 0:
		pool, passing = fresh, fmt.Sprintf(", and every goblin it could pause resumed from a memory pause in the last %d minutes", int(freshResume/time.Minute))
	case ideal.isHoldingTurn:
		passing = fmt.Sprintf(" past %s, whose turn of the heavy line runs", ideal.meta.ID)
	case isFresh(ideal):
		passing = fmt.Sprintf(" past %s, which resumed from a memory pause in the last %d minutes", ideal.meta.ID, int(freshResume/time.Minute))
	}
	chosen := mostGrown(pool)
	why := "the lowest goblin in the In progress order not pushing or merging"
	if chosen.growth > 0 {
		why = fmt.Sprintf("the goblin whose process tree grew most since the reading before, by %.1f MB", float64(chosen.growth)/(1<<20))
	}
	return floorPick{chosen.meta, chosen.record, why + passing}, true, ""
}

// mostGrown is the candidate of the non-empty candidates whose process tree
// grew most, the first of them on a tie.
func mostGrown(candidates []floorCandidate) floorCandidate {
	most := candidates[0]
	for _, one := range candidates[1:] {
		if one.growth > most.growth {
			most = one
		}
	}
	return most
}

// pushingOrMerging reports whether a process under the terminal host hostPID
// pushes or merges: a git push, merge, pull, rebase, cherry-pick or am, or a
// gh pr merge. A process whose command line cannot be read says neither.
func pushingOrMerging(hostPID int, processes []proc.Entry, arguments func(pid int) ([]string, error)) bool {
	children := map[int][]proc.Entry{}
	for _, entry := range processes {
		children[entry.ParentPID] = append(children[entry.ParentPID], entry)
	}
	seen := map[int]bool{hostPID: true}
	for queue := []int{hostPID}; len(queue) > 0; queue = queue[1:] {
		for _, child := range children[queue[0]] {
			if seen[child.PID] {
				continue
			}
			seen[child.PID] = true
			queue = append(queue, child.PID)
			program := strings.TrimSuffix(strings.ToLower(child.ExeBase), ".exe")
			if program != "git" && program != "gh" {
				continue
			}
			args, err := arguments(child.PID)
			if err != nil || len(args) < 2 {
				continue
			}
			words := commandWords(args[1:])
			if program == "git" && len(words) > 0 && slices.Contains(gitPushOrMerge, words[0]) || program == "gh" && len(words) > 1 && words[0] == "pr" && words[1] == "merge" {
				return true
			}
		}
	}
	return false
}

// commandWords are a git or gh command line's words after the program, its
// options and the values of git's options that take one left out.
func commandWords(args []string) []string {
	var words []string
	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains([]string{"-c", "-C", "--git-dir", "--work-tree", "--namespace"}, args[i]):
			i++
		case !strings.HasPrefix(args[i], "-"):
			words = append(words, args[i])
		}
	}
	return words
}
