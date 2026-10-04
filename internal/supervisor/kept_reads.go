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
// little: one build took 3 to 13 seconds. Every file kept this way is written
// whole and renamed into place, or appended to and closed, so a change always
// shows in its size or its time.
type keptReads struct {
	mu    sync.Mutex
	reads map[string]keptRead
	// While a snapshot is being built, the disk is asked about each path
	// once, and about the files of the folders the build reads whole
	// together, in one listing of each folder: builds counts the builds in
	// progress, looks is what this build has asked so far, and lists holds
	// the listing of each folder in folders once it was read.
	builds     int
	generation uint64
	folders    []string
	looks      map[string]look
	lists      map[string]map[string]fs.FileInfo
}

// look is what the disk said of a path, as os.Stat gives it.
type look struct {
	info fs.FileInfo
	err  error
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

// begin starts a build that reads folders whole, and the function it returns
// ends it. What an earlier build asked of the disk is asked again.
func (k *keptReads) begin(folders ...string) func() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.builds++
	k.generation++
	k.folders, k.looks, k.lists = folders, map[string]look{}, map[string]map[string]fs.FileInfo{}
	return func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.builds--; k.builds == 0 {
			k.looks, k.lists = nil, nil
		}
	}
}

// look is what the disk says of path now, as os.Stat gives it. During a
// build it is asked once, whoever asks and whatever for, and for a file in a
// folder the build reads whole it is taken from that folder's listing. A
// folder is always asked itself: a listing shows a folder's time as it was
// before the last changes inside it.
func (k *keptReads) look(path string) (fs.FileInfo, error) {
	k.mu.Lock()
	if k.builds == 0 {
		k.mu.Unlock()
		return os.Stat(path)
	}
	if seen, ok := k.looks[path]; ok {
		k.mu.Unlock()
		return seen.info, seen.err
	}
	folder := filepath.Dir(path)
	isListed := slices.Contains(k.folders, folder)
	list := k.lists[folder]
	generation := k.generation
	k.mu.Unlock()
	info, list, err := ask(path, isListed, list)
	k.mu.Lock()
	if k.builds > 0 && k.generation == generation && (err == nil || errors.Is(err, fs.ErrNotExist)) {
		k.looks[path] = look{info, err}
		if list != nil {
			k.lists[folder] = list
		}
	}
	k.mu.Unlock()
	return info, err
}

// ask reads the disk without holding the cache's lock.
func ask(path string, isListed bool, list map[string]fs.FileInfo) (fs.FileInfo, map[string]fs.FileInfo, error) {
	folder := filepath.Dir(path)
	if !isListed {
		info, err := os.Stat(path)
		return info, nil, err
	}
	if list == nil {
		entries, err := os.ReadDir(folder)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		list = make(map[string]fs.FileInfo, len(entries))
		for _, entry := range entries {
			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			list[strings.ToLower(entry.Name())] = info
		}
	}
	info, ok := list[strings.ToLower(filepath.Base(path))]
	switch {
	case !ok:
		return nil, list, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	case info.IsDir():
		info, err := os.Stat(path)
		return info, list, err
	}
	return info, list, nil
}

// kept returns what read gave the last time for these paths, while each of
// them is as it was then, and otherwise calls read and remembers its result.
// kind tells apart two readings of the same file. A missing file is a state
// like any other; a result that failed for another reason is never kept.
// What it returns is shared with later callers, who must not change it.
func kept[T any](k *keptReads, kind string, paths []string, read func() (T, error)) (T, error) {
	signs := make([]fileSign, len(paths))
	for i, path := range paths {
		info, err := k.look(path)
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

// created is when the file at path was created, zero when it is not there.
func (s *Service) created(path string) time.Time {
	info, err := s.reads.look(path)
	if err != nil {
		return time.Time{}
	}
	return created(info)
}

// sessionStarted is when a goblin started: its worktree is made fresh by cfo
// spawn and kept across a switch, which writes a new spawn generation, so
// the generation's time dates the session only when the folder cannot.
func (s *Service) sessionStarted(meta state.TaskMeta) time.Time {
	if at := s.created(meta.Worktree); !at.IsZero() {
		return at
	}
	return spawnTime(meta.SpawnGen)
}

// The readers below are the snapshot's: each reads as the function it names
// does, once per state of its file.

func (s *Service) taskMeta(id string) (state.TaskMeta, error) {
	directory := s.Store.Home.State
	return kept(&s.reads, "meta", []string{state.TaskMetaPath(directory, id)}, func() (state.TaskMeta, error) { return state.ReadTaskMeta(directory, id) })
}

// Lifecycle includes live teardown identities, so file signatures cannot cache it.
func (s *Service) lifecycle(id string) (state.Lifecycle, error) {
	return state.ReadLifecycle(s.Store.Home.State, id)
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
// and the archive's listing. That a task is not queued is an answer like any
// other, and is kept.
func (s *Service) queuedTask(id string) (fleet.QueuedTask, error) {
	type answer struct {
		task      fleet.QueuedTask
		notQueued bool
	}
	h := s.Store.Home
	paths := []string{filepath.Join(h.Data, "backlog.md"), filepath.Join(h.Data, id, "brief.md"), state.TaskMetaPath(h.State, id), state.StatusPath(h.State, id), filepath.Join(h.State, state.ArchiveDirName)}
	read, err := kept(&s.reads, "queued", paths, func() (answer, error) {
		backlog, err := s.backlog()
		if err != nil {
			return answer{}, err
		}
		task, err := backlog.ReadQueuedTask(h, id)
		if errors.Is(err, fleet.ErrNotQueued) {
			return answer{notQueued: true}, nil
		}
		return answer{task: task}, err
	})
	if read.notQueued {
		return fleet.QueuedTask{}, fleet.ErrNotQueued
	}
	return read.task, err
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
