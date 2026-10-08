package janitor

import (
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// movedCaches are the tool caches in a home's own caches folder that follow
// the caches to a Dev Drive: every cache a goblin's terminal is pointed at.
// The voice model is not one of them and stays in the home.
var movedCaches = []string{"uv", "pnpm", "npm", "go-build", "go-mod", "playwright"}

// retireMovedCaches removes the home's own package caches once its caches
// moved to a Dev Drive and nothing still uses them. A goblin's terminal takes
// its cache variables when it starts, so one started before the move still
// builds against the home's caches; they stay while any such terminal runs,
// and so does everything while a task still runs in a Herdr pane, whose start
// no record keeps.
func (cfg Config) retireMovedCaches(record *Record) {
	if cfg.Home.DevDrive == "" {
		return
	}
	config, err := home.ReadDevDriveConfig(cfg.Home.Root)
	if err != nil {
		record.Notes = append(record.Notes, "the Dev Drive setting could not be read, so the home's old caches were kept: "+err.Error())
		return
	}
	old := filepath.Join(cfg.Home.Root, home.CachesDir)
	if holder := cfg.terminalStartedBefore(config.MovedAt); holder != "" {
		record.Kept = append(record.Kept, Item{Kind: "cache", Path: old, Detail: holder + "'s terminal started before the caches moved to the Dev Drive and still builds against these"})
		return
	}
	for _, name := range movedCaches {
		path := filepath.Join(old, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		bytes := Size(path)
		if err := os.RemoveAll(path); err != nil {
			record.Kept = append(record.Kept, Item{Kind: "cache", Path: path, Detail: "could not be removed: " + err.Error()})
			continue
		}
		record.Removed = append(record.Removed, Item{Kind: "cache", Path: path, Bytes: bytes, Detail: "the caches moved to the Dev Drive at " + cfg.Home.DevDrive})
	}
}

// terminalStartedBefore names a task whose terminal started before moved and
// still runs, or "" when there is none.
func (cfg Config) terminalStartedBefore(moved time.Time) string {
	for _, task := range cfg.Inventory.Tasks {
		if task.Meta.Backend == "herdr" && !task.Terminal {
			return task.ID
		}
		terminal, err := host.ReadRecord(cfg.Home.State, task.ID)
		if err != nil {
			continue
		}
		for _, process := range cfg.Inventory.Processes {
			if process.PID == terminal.HostPID && process.Start.Before(moved) {
				return task.ID
			}
		}
	}
	return ""
}
