package supervisor

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The memory floor is a safety rail of AFK mode. While the Overlord is away
// and two readings in a row find memory or commit under the floor, the
// supervisor pauses the newest live goblin that is not pushing or merging,
// through the pause the allowance floor uses, one goblin at a time, and the
// scheduler resumes it once memory is back at the next-start mark. While he
// is here the CFO and he decide what to pause.

// gitPushOrMerge are the git commands that push or merge, which a goblin is
// left to finish rather than paused in the middle of.
var gitPushOrMerge = []string{"push", "merge", "pull", "rebase", "cherry-pick", "am"}

// pauseAtMemoryFloor takes a reading under the floor, the w.MemoryBelow-th in
// a row, of memory: from the second on, while AFK mode is on and nothing else
// starts or changes, it pauses the newest goblin it may. A change under way
// frees what it frees only once it ends, so the count starts again after it.
func (s *Service) pauseAtMemoryFloor(w *fleetWakes, memory Memory) error {
	if w.MemoryBelow < 2 || !s.afkOn() {
		return nil
	}
	s.starts.Lock()
	isChanging := s.starting != "" || len(s.changing) > 0
	s.starts.Unlock()
	if isChanging {
		w.MemoryBelow = 0
		return nil
	}
	meta, record, found, problems := s.newestToPause()
	if !found {
		return problems
	}
	evidence := fmt.Sprintf("%.1f GB of memory and %.1f GB of commit free on two readings in a row, under the %.0f GB floor; %s was the newest goblin not pushing or merging",
		gigabytes(memory.Available), gigabytes(memory.CommitAvailable), gigabytes(memoryFloor), meta.ID)
	began, err := s.pauseAtFloor(meta, record, "memory", "", evidence)
	if began {
		w.MemoryBelow = 0
	}
	return errors.Join(problems, err)
}

// newestToPause is the goblin the memory floor pauses: of the native goblins
// whose terminal host runs, that are not paused or changing and whose last
// pause at the memory floor did not fail, the newest by when its host
// started that is not pushing or merging. Processes that cannot be listed
// tell nothing of either, so the newest is paused then.
func (s *Service) newestToPause() (state.TaskMeta, state.Lifecycle, bool, error) {
	stateDir := s.Store.Home.State
	type candidate struct {
		meta   state.TaskMeta
		record state.Lifecycle
		host   host.Record
	}
	var candidates []candidate
	var problems error
	for _, meta := range liveTasks(stateDir) {
		if meta.Backend != "native" {
			continue
		}
		hostRecord, err := host.ReadRecord(stateDir, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if !host.Running(hostRecord) {
			continue
		}
		record, err := state.ReadLifecycle(stateDir, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = errors.Join(problems, err)
			continue
		}
		failed := record.Action == "pause" && record.Phase == "failed" && record.Pause != nil && record.Pause.Reason == "memory"
		if record.Generation == meta.SpawnGen && (record.SuppressesMonitoring(stateDir) || failed) {
			continue
		}
		candidates = append(candidates, candidate{meta, record, hostRecord})
	}
	slices.SortFunc(candidates, func(a, b candidate) int { return b.host.Started.Compare(a.host.Started) })
	processes, unlisted := proc.Processes()
	for _, one := range candidates {
		if unlisted == nil && pushingOrMerging(one.host.HostPID, processes, proc.Arguments) {
			continue
		}
		return one.meta, one.record, true, problems
	}
	return state.TaskMeta{}, state.Lifecycle{}, false, problems
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
