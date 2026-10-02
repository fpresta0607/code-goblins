package supervisor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// keptReads remembers what each fleet file gave the last time the snapshot
// read it, for as long as the file's size and modification time stay what
// they were. A snapshot reads several hundred files, nearly all unchanged
// since the one before, and on 2026-10-02 opening a file cost the fleet's
// machine 20 to 80 ms in bursts, while asking for its size and time cost
// almost nothing: one build took 3 to 13 seconds. Every file kept this way
// is written whole and renamed into place, or appended to and closed, so a
// change always shows in its size or its time.
type keptReads struct {
	mu    sync.Mutex
	reads map[string]keptRead
}

// fileSign is what tells one state of a file from another without opening it.
type fileSign struct {
	size int64
	mod  time.Time
	gone bool
}

type keptRead struct {
	signs []fileSign
	value any
	err   error
}

// keptLimit bounds what is remembered; past it everything is read again.
const keptLimit = 4096

// kept returns what read gave the last time for these paths, while each of
// them is as it was then, and otherwise calls read and remembers its result.
// kind tells apart two readings of the same file. A missing file is a state
// like any other; a result that failed for another reason is never kept.
// What it returns is shared with later callers, who must not change it.
func kept[T any](k *keptReads, kind string, paths []string, read func() (T, error)) (T, error) {
	signs := make([]fileSign, len(paths))
	for i, path := range paths {
		info, err := os.Stat(path)
		switch {
		case err == nil:
			signs[i] = fileSign{size: info.Size(), mod: info.ModTime()}
		case errors.Is(err, fs.ErrNotExist):
			signs[i] = fileSign{gone: true}
		default:
			return read()
		}
	}
	key := kind + "\x00" + strings.Join(paths, "\x00")
	k.mu.Lock()
	prior, found := k.reads[key]
	k.mu.Unlock()
	if found && slices.EqualFunc(prior.signs, signs, func(a, b fileSign) bool { return a.gone == b.gone && a.size == b.size && a.mod.Equal(b.mod) }) {
		if value, ok := prior.value.(T); ok {
			return value, prior.err
		}
	}
	value, err := read()
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		k.mu.Lock()
		if k.reads == nil || len(k.reads) >= keptLimit {
			k.reads = map[string]keptRead{}
		}
		k.reads[key] = keptRead{signs, value, err}
		k.mu.Unlock()
	}
	return value, err
}

// The readers below are the snapshot's: each reads as the function it names
// does, once per state of its file.

func (s *Service) taskMeta(id string) (state.TaskMeta, error) {
	directory := s.Store.Home.State
	return kept(&s.reads, "meta", []string{state.TaskMetaPath(directory, id)}, func() (state.TaskMeta, error) { return state.ReadTaskMeta(directory, id) })
}

func (s *Service) lifecycle(id string) (state.Lifecycle, error) {
	directory := s.Store.Home.State
	return kept(&s.reads, "lifecycle", []string{state.LifecyclePath(directory, id)}, func() (state.Lifecycle, error) { return state.ReadLifecycle(directory, id) })
}

func (s *Service) observation(id string) (monitor.Observation, error) {
	directory := s.Store.Home.State
	return kept(&s.reads, "observation", []string{monitor.ObservationPath(directory, id)}, func() (monitor.Observation, error) { return monitor.ReadObservation(directory, id) })
}

// statusTail is the last 200 lines of a task's status log.
func (s *Service) statusTail(id string) ([]string, error) {
	directory := s.Store.Home.State
	return kept(&s.reads, "status", []string{state.StatusPath(directory, id)}, func() ([]string, error) { return state.TailStatus(directory, id, 200) })
}

func (s *Service) backlog() (fleet.BacklogRows, error) {
	h := s.Store.Home
	return kept(&s.reads, "backlog", []string{filepath.Join(h.Data, "backlog.md")}, func() (fleet.BacklogRows, error) { return fleet.ReadBacklog(h) })
}

// queuedTask reads a queued task from everything fleet.ReadQueuedTask
// consults: the backlog, the task's brief, its task record and status log,
// and the archive's listing.
func (s *Service) queuedTask(id string) (fleet.QueuedTask, error) {
	h := s.Store.Home
	paths := []string{filepath.Join(h.Data, "backlog.md"), filepath.Join(h.Data, id, "brief.md"), state.TaskMetaPath(h.State, id), state.StatusPath(h.State, id), filepath.Join(h.State, state.ArchiveDirName)}
	return kept(&s.reads, "queued", paths, func() (fleet.QueuedTask, error) { return fleet.ReadQueuedTask(h, id) })
}

func (s *Service) briefProject(path string) string {
	project, _ := kept(&s.reads, "brief-project", []string{path}, func() (string, error) { return briefProject(path), nil })
	return project
}

// hasHandoff reports whether openTaskHandoff would open a handoff for task
// id, opening each place one may be only when that place changed.
func (s *Service) hasHandoff(id string, archived func() ([]os.DirEntry, error)) bool {
	h := s.Store.Home
	meta, metaErr := s.taskMeta(id)
	places, err := handoffPlaces(h, id, meta, metaErr, s.lifecycle, archived)
	if err != nil {
		return false
	}
	for _, place := range places {
		found, err := kept(&s.reads, "handoff", []string{filepath.Join(place.root, place.path)}, func() (bool, error) {
			file, err := openHandoff(place)
			if err != nil {
				return false, err
			}
			return true, file.Close()
		})
		if found || err != nil && !errors.Is(err, fs.ErrNotExist) {
			return found
		}
	}
	return false
}
