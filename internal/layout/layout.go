// Package layout is where a CFO home keeps the operator's data: the backlog,
// the CFO's memory, each project's manifests, one folder per task, and the
// archive that finished and set-aside work is filed into. AGENTS.md describes the layout
// under "The CFO home"; this package is what creates it.
package layout

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

// Backlog is the open-work file. Its sections are Queued, Parked and Done.
const Backlog = "backlog.md"

// Marker marks a data folder this package laid out. A folder without it that
// already holds data predates the layout, and nothing may reorganise it until
// its owner has seen what that would move.
const Marker = ".layout"

const markerText = "This folder is a Code Goblins CFO home's data, laid out as AGENTS.md describes under \"The CFO home\".\r\n"

// MemoryIndex is the CFO's memory index, slash-separated under the data
// folder: one line per fact, each fact a file of its own beside it. It lives
// in the home rather than in any harness, so a CFO in every harness reads and
// writes the same memory.
const MemoryIndex = "memory/MEMORY.md"

// folders are the folders every laid-out home has, slash-separated under the
// data folder, each after its parent.
var folders = []string{"projects", "memory", "archive", "archive/finished", "archive/parked"}

// IgnoreFile is the data folder's .gitignore, for whoever keeps a backup of
// it in a git repository of their own.
const IgnoreFile = ".gitignore"

// ignoreHeader opens the lines that keep a backup of the data folder to its
// records: binaries, archives and logs belong in a task's scratch, which goes
// when the task does, and once committed they stay in the backup's history.
const ignoreHeader = "# Code Goblins: a backup of this folder keeps records, never binaries, archives or logs."

var ignoredInData = []string{"*.exe", "*.dll", "*.msi", "*.zip", "*.7z", "*.rar", "*.tar", "*.gz", "*.tgz", "*.bundle", "*.log", "*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.mp4", "*.webm", "*.pdf", "*.db", "*.sqlite"}

// seeds are the files every laid-out home starts with, written only where
// missing.
var seeds = []struct{ path, content string }{
	{Backlog, "# Backlog\n\n## Queued\n\n## Parked\n\n## Done\n"},
	{MemoryIndex, "# Memory index\n"},
}

// Ensure lays out the data folder of a new home, or repairs a laid-out one,
// creating only what is missing and never overwriting a file. It returns the
// slash-separated paths it created.
//
// A folder with no marker that holds anything besides the lane policy an
// install seeds is legacy: it has data from before this layout, so Ensure
// creates nothing there and says so, because laying it out means moving the
// operator's files.
func Ensure(dataDir string) (created []string, legacy bool, err error) {
	marker := filepath.Join(dataDir, Marker)
	if _, err := os.Stat(marker); errors.Is(err, fs.ErrNotExist) {
		entries, err := os.ReadDir(dataDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
		for _, entry := range entries {
			if !strings.EqualFold(entry.Name(), routing.FileName) {
				return nil, true, nil
			}
		}
		// The marker goes first, so a layout interrupted partway is repaired
		// by the next Ensure rather than taken for a legacy home.
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, false, err
		}
		if err := fsx.AtomicWriteFile(marker, []byte(markerText)); err != nil {
			return nil, false, err
		}
		created = append(created, Marker)
	} else if err != nil {
		return nil, false, err
	}
	for _, folder := range folders {
		path := filepath.Join(dataDir, filepath.FromSlash(folder))
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			continue
		}
		if err := os.Mkdir(path, 0o755); err != nil {
			return created, false, err
		}
		created = append(created, folder)
	}
	for _, seed := range seeds {
		path := filepath.Join(dataDir, filepath.FromSlash(seed.path))
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return created, false, err
		}
		if err := fsx.AtomicWriteFile(path, []byte(seed.content)); err != nil {
			return created, false, err
		}
		created = append(created, seed.path)
	}
	changed, err := ensureIgnored(filepath.Join(dataDir, IgnoreFile))
	if err != nil {
		return created, false, err
	}
	if changed {
		created = append(created, IgnoreFile)
	}
	return created, false, nil
}

// ensureIgnored gives the data folder's .gitignore the lines that keep
// binaries, archives and logs out of a backup, once, after every line already
// there: the one file Ensure adds to rather than only creates.
func ensureIgnored(path string) (bool, error) {
	current, err := fsx.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if strings.Contains(string(current), ignoreHeader) {
		return false, nil
	}
	text := string(current)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += ignoreHeader + "\n" + strings.Join(ignoredInData, "\n") + "\n"
	return true, fsx.AtomicWriteFile(path, []byte(text))
}
