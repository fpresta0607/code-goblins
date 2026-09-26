// Package layout is where a CFO home keeps the operator's data: the backlog,
// each project's manifests, one folder per task, and the archive that
// finished and set-aside work is filed into. AGENTS.md describes the layout
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

const backlogSeed = "# Backlog\n\n## Queued\n\n## Parked\n\n## Done\n"

// folders are the folders every laid-out home has, slash-separated under the
// data folder, each after its parent.
var folders = []string{"projects", "archive", "archive/finished", "archive/parked"}

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
	backlog := filepath.Join(dataDir, Backlog)
	if _, err := os.Stat(backlog); errors.Is(err, fs.ErrNotExist) {
		if err := fsx.AtomicWriteFile(backlog, []byte(backlogSeed)); err != nil {
			return created, false, err
		}
		created = append(created, Backlog)
	} else if err != nil {
		return created, false, err
	}
	return created, false, nil
}
