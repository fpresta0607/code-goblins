package janitor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/layout"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// tempAge is how long nothing in a temporary folder must have been written
// before the janitor takes it for a leak.
const tempAge = 24 * time.Hour

// tightScratchAge is how long nothing in a finished task's scratch folder must
// have been written before the janitor takes it while free disk is under the
// clean mark: the time a spawn may take between making a task's scratch
// folder and writing the record that names it. A folder the settings' temp
// patterns name keeps the day, since a test running now can use it unnamed.
const tightScratchAge = time.Hour

// tempCandidate is a folder the sweep may take for a leak, how long nothing in
// it must have been written first, and whether it is a finished task's
// scratch folder.
type tempCandidate struct {
	path      string
	age       time.Duration
	isScratch bool
}

// readableProcesses returns every running process's command line, lowercased,
// once the list is proven read: this process's own must be in it, naming its
// own program. A list that fails that is no evidence that nothing uses a
// folder, so no temporary folder is removed on its strength.
func (cfg Config) readableProcesses(record *Record) ([]string, bool) {
	self, err := os.Executable()
	if err != nil {
		record.Notes = append(record.Notes, "this program's own path could not be read, so the process list could not be proven read and no temporary folder was removed")
		return nil, false
	}
	var lines []string
	seen := false
	for _, process := range cfg.Inventory.Processes {
		line := strings.ToLower(process.CommandLine)
		lines = append(lines, line)
		if process.PID == cfg.SelfPID && names(line, self) {
			seen = true
		}
	}
	if !seen {
		record.Notes = append(record.Notes, "the process list does not show this process's own command line, so whether a running process uses a temporary folder is unknown and none was removed")
		return nil, false
	}
	return lines, true
}

// names reports whether a lowercased command line names path, as the path
// itself or a path inside it, ending where a path does: at its end, a
// separator, a quote or a space. Without the boundary a process using
// pd-12 would read as one using pd-1.
func names(line, path string) bool {
	target := strings.ToLower(filepath.Clean(path))
	for offset := 0; ; {
		at := strings.Index(line[offset:], target)
		if at < 0 {
			return false
		}
		end := offset + at + len(target)
		if end == len(line) || strings.ContainsRune(`\/" '`, rune(line[end])) {
			return true
		}
		offset += at + 1
	}
}

// removeTempLeaks removes the temporary folders the fleet left behind: in the
// machine's temporary folder and in the folder every goblin's TMP names,
// those its settings name; in the home's scratch folder and the older builds'
// Go temporary folder, a task's whose record is gone. Each goes only once
// nothing in it was written for a day, or for an hour for a finished task's
// scratch folder while free disk is under the clean mark, no running process
// names it and no running Git Bash has it as /tmp. A finished task's scratch
// folder also stays while anything still read names a path in it.
func (cfg Config) removeTempLeaks(processes []string, record *Record) {
	scratchRoots := append(cfg.Home.ScratchRoots(), cfg.LegacyGoTmp)
	scratchAge := tempAge
	if cfg.IsTight {
		scratchAge = tightScratchAge
	}
	var candidates []tempCandidate
	temps := func(dir string, entries []os.DirEntry) {
		for _, path := range cfg.fleetTemps(dir, entries) {
			candidates = append(candidates, tempCandidate{path: path, age: tempAge})
		}
	}
	if cfg.TempDir != "" {
		entries, err := os.ReadDir(cfg.TempDir)
		if err != nil {
			record.Notes = append(record.Notes, "the temporary folder could not be read: "+err.Error())
		}
		temps(cfg.TempDir, entries)
	}
	// What asks Windows for the temporary folder in a goblin's terminal, as a
	// Go program's os.TempDir does, is given the folder every goblin shares,
	// so the fleet's leaks land there as they do in the machine's own, and go
	// by the same rule. Nothing else in it is looked at. A Go test's
	// t.TempDir follows GOTMPDIR and stays in its task's scratch folder.
	for _, root := range scratchRoots {
		if root == "" {
			continue
		}
		shared := filepath.Join(root, home.SharedTempDir)
		entries, _ := os.ReadDir(shared)
		temps(shared, entries)
	}
	if len(cfg.Inventory.UnreadableTasks) > 0 {
		record.Notes = append(record.Notes, "a task record could not be read, so no scratch folder was taken for a finished task's")
	} else {
		live := map[string]bool{}
		for _, task := range cfg.Inventory.Tasks {
			live[strings.ToLower(task.ID)] = true
		}
		for _, root := range scratchRoots {
			if root == "" {
				continue
			}
			entries, _ := os.ReadDir(root)
			for _, entry := range entries {
				// The folder every goblin's TMP names is left alone,
				// whatever is in it and however long ago it was written:
				// it is no task's, so no record names it, and Git Bash
				// has it as /tmp for every shell of the user whenever a
				// goblin's shell was the first to start. Removing it
				// would take /tmp from all of them, as removing a task's
				// scratch folder did on 2026-10-09.
				if strings.EqualFold(entry.Name(), home.SharedTempDir) {
					continue
				}
				if entry.IsDir() && !live[strings.ToLower(entry.Name())] {
					candidates = append(candidates, tempCandidate{path: filepath.Join(root, entry.Name()), age: scratchAge, isScratch: true})
				}
			}
		}
	}
	// Which folders are a running Git Bash's /tmp is read once a sweep, and
	// only when a folder is otherwise due to go, since reading it starts a
	// program for each running runtime.
	liveTmp := sync.OnceValues(func() ([]home.LiveTemp, error) {
		temps, err := liveTemps(context.Background())
		if err != nil {
			record.Notes = append(record.Notes, "which folders are a running Git Bash's /tmp could not be read, so no temporary folder was removed: "+err.Error())
		}
		return temps, err
	})
	// What the fleet and the Overlord still read is read once a sweep too,
	// and only when a finished task's scratch folder is otherwise due to go.
	liveText := sync.OnceValues(func() (string, error) {
		text, err := cfg.liveText()
		if err != nil {
			record.Notes = append(record.Notes, "what reports, cards, questions, briefs, the backlog and memory name could not all be read, so no finished task's scratch folder was removed: "+err.Error())
		}
		return text, err
	})
	for _, candidate := range candidates {
		cfg.removeTempLeak(candidate, processes, liveTmp, liveText, record)
	}
}

// liveText is every text that keeps the files it names by path: the open
// backlog rows, the directives, both memory folders, the briefs of live and
// undispatched tasks and the open Command Center items (layout.LiveText), the
// wakes the CEO has not acknowledged, and each task's status log, which holds
// its reports. It is lowercased, with
// every slash a backslash, as names reads a command line.
func (cfg Config) liveText() (string, error) {
	text, err := layout.LiveText(cfg.Home, layout.ClaudeMemoryFolder(cfg.Home.Root))
	if err != nil {
		return "", err
	}
	var all strings.Builder
	all.WriteString(text)
	pending, err := wake.Pending(cfg.Home.State)
	if err != nil {
		return "", err
	}
	for _, record := range pending {
		all.WriteString(record.Detail + "\n")
	}
	for _, task := range cfg.Inventory.Tasks {
		status, err := fsx.ReadFile(state.StatusPath(cfg.Home.State, task.ID))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		all.Write(status)
		all.WriteString("\n")
	}
	return strings.ReplaceAll(strings.ToLower(all.String()), "/", `\`), nil
}

// textNames reports whether text, as liveText gives it, names path or a path
// inside it, written as Windows writes it or as Git Bash does, /d/... for
// D:\.... The path is spelled as the text is, lowercased with every slash a
// backslash. A name ends at any character a task id cannot go on with, so a
// text that names gone-task-2 does not keep gone-task, while one that ends a
// sentence with gone-task. does.
func textNames(text, path string) bool {
	target := strings.ReplaceAll(strings.ToLower(filepath.Clean(path)), "/", `\`)
	targets := []string{target}
	if volume := filepath.VolumeName(target); len(volume) == 2 && volume[1] == ':' {
		targets = append(targets, `\`+volume[:1]+target[2:])
	}
	for _, target := range targets {
		for offset := 0; ; {
			at := strings.Index(text[offset:], target)
			if at < 0 {
				break
			}
			end := offset + at + len(target)
			if end == len(text) || !isNameRune(rune(text[end])) {
				return true
			}
			offset += at + 1
		}
	}
	return false
}

// isNameRune is a character a task id or a folder name can go on with.
func isNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

// fleetTemps are the folders among dir's entries that the settings' temp
// patterns name.
func (cfg Config) fleetTemps(dir string, entries []os.DirEntry) []string {
	var folders []string
	for _, entry := range entries {
		if entry.IsDir() && cfg.isFleetTemp(entry.Name()) {
			folders = append(folders, filepath.Join(dir, entry.Name()))
		}
	}
	return folders
}

func (cfg Config) isFleetTemp(name string) bool {
	for _, pattern := range cfg.Settings.TempPatterns {
		if matched, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(name)); err == nil && matched {
			return true
		}
	}
	return false
}

func (cfg Config) removeTempLeak(candidate tempCandidate, processes []string, liveTmp func() ([]home.LiveTemp, error), liveText func() (string, error), record *Record) {
	path := candidate.path
	for _, line := range processes {
		if names(line, path) {
			return
		}
	}
	newest, bytes, err := newestWrite(path)
	if err != nil {
		record.Notes = append(record.Notes, "left "+path+": what it holds could not all be read")
		return
	}
	if cfg.Now.Sub(newest) < candidate.age {
		return
	}
	if candidate.isScratch {
		text, err := liveText()
		if err != nil {
			return
		}
		if textNames(text, path) {
			record.Kept = append(record.Kept, Item{Kind: "temp", Path: path, Bytes: bytes, Detail: "a finished task's scratch folder that a report, card, question, brief, backlog row or memory file still names a path in"})
			return
		}
	}
	repository, err := findRepository(path)
	if err != nil {
		record.Kept = append(record.Kept, Item{Kind: "temp", Path: path, Detail: "its repositories could not all be read: " + err.Error()})
		return
	}
	if repository != "" {
		record.Kept = append(record.Kept, Item{Kind: "temp", Path: path, Detail: "it still holds a git worktree or repository at " + repository})
		return
	}
	// A folder that is a running Git Bash's /tmp, or holds it, stays however
	// old: the runtime mounts /tmp once, at the TMP of its first shell, for
	// every shell of the user until the last one ends, and no command line
	// names it. When that cannot be read, nothing goes on the strength of it.
	temps, err := liveTmp()
	if err != nil {
		return
	}
	if temp, isHeld := home.LiveTempIn(path, temps); isHeld {
		record.Kept = append(record.Kept, Item{Kind: "temp", Path: path, Detail: "it is /tmp for every shell of the Git Bash in " + temp.Runtime + ", so it stays until no running Git Bash has it"})
		return
	}
	if err := fsx.RemoveAll(path); err != nil {
		record.Kept = append(record.Kept, Item{Kind: "temp", Path: path, Detail: "something still holds it open: " + err.Error()})
		return
	}
	detail := "nothing written in it for a day and no running process names it"
	if candidate.age != tempAge {
		detail = "free disk is under the clean mark, nothing written in it for an hour and no running process names it"
	}
	record.Removed = append(record.Removed, Item{Kind: "temp", Path: path, Bytes: bytes, Detail: detail})
}

// newestWrite is the latest modification time of path and everything under
// it, and the bytes it holds. Any entry that cannot be read is an error, so a
// folder is never judged old on what could be seen of it, and so is a link or
// junction: removing a folder that holds one must never reach what it points
// at.
func newestWrite(path string) (time.Time, int64, error) {
	var newest time.Time
	var bytes int64
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return errors.New("it holds a link")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
		}
		return nil
	})
	if newest.IsZero() && err == nil {
		err = errors.New("nothing could be read")
	}
	return newest, bytes, err
}

// liveTemps reads which folders running msys runtimes have as /tmp.
var liveTemps = home.LiveTemps
