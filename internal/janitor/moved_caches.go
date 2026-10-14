package janitor

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
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
// no record keeps. They go within a turn of the machine's heavy line, as the
// caches' prunes do (trimCaches).
func (cfg Config) retireMovedCaches(ctx context.Context, record *Record) {
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
	var present []string
	for _, name := range movedCaches {
		if _, err := os.Stat(filepath.Join(old, name)); err == nil {
			present = append(present, filepath.Join(old, name))
		}
	}
	if len(present) == 0 {
		return
	}
	// A run that builds against the old caches holds or waits for a turn,
	// so they go only within a turn of the sweep's own.
	release, err := cfg.takeHeavyTurn(ctx)
	if err != nil {
		record.Kept = append(record.Kept, Item{Kind: "cache", Path: old, Detail: "kept for the next sweep, which took no turn in the machine's heavy line: " + err.Error()})
		return
	}
	defer func() {
		if err := release(); err != nil {
			record.Notes = append(record.Notes, "the sweep's turn in the machine's heavy line could not be given back: "+err.Error())
		}
	}()
	for _, path := range present {
		bytes := Size(path)
		if err := fsx.RemoveAll(path); err != nil {
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
