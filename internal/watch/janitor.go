package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// janitorBudget bounds one janitor pass: a tool's cache prune can take
// minutes, a worktree's removal seconds each.
const janitorBudget = 30 * time.Minute

// janitorRunning keeps this process to one janitor pass at a time.
var janitorRunning sync.Mutex

// tidyHome starts the janitor's pass over the home, with the inventory the
// orphan sweep just collected, once JanitorEvery has passed since the last
// one. It runs beside the watcher's pass rather than inside it, because the
// pass keeps the watcher's heartbeat and a prune can outlast its budget.
func tidyHome(cfg Config, inv reap.Inventory) {
	if cfg.JanitorEvery <= 0 {
		return
	}
	if last, err := janitor.ReadRecord(cfg.Home.State); err == nil && time.Since(last.Time) < cfg.JanitorEvery {
		return
	}
	if !janitorRunning.TryLock() {
		return
	}
	go func() {
		defer janitorRunning.Unlock()
		runJanitor(cfg, inv)
	}()
}

// runJanitor tidies the home once, keeps its record for the digest, the board
// and cfo runtime, and wakes the CFO when the strays it reports change.
func runJanitor(cfg Config, inv reap.Inventory) {
	previous, _ := janitor.ReadRecord(cfg.Home.State)
	settings, err := fleetconfig.Read(cfg.Home.Root)
	if err != nil {
		_ = janitor.WriteRecord(cfg.Home.State, janitor.Record{Time: time.Now().UTC(), Notes: []string{"config/fleet.json could not be read, so the janitor did nothing: " + err.Error()}})
		return
	}
	projects, err := install.MachineProjectsRoot()
	if err != nil {
		projects = ""
	}
	legacy := ""
	if goTmp, err := state.GoTmpDir(cfg.Home.State, "janitor"); err == nil {
		legacy = filepath.Dir(goTmp)
	}
	ctx, cancel := context.WithTimeout(context.Background(), janitorBudget)
	defer cancel()
	record := janitor.Sweep(ctx, janitor.Config{
		Home:         cfg.Home,
		Commands:     cfg.Reap.Commands,
		Settings:     settings,
		Inventory:    inv,
		TempDir:      os.TempDir(),
		LegacyGoTmp:  legacy,
		ProjectsRoot: projects,
		SelfPID:      os.Getpid(),
		Now:          time.Now(),
	})
	if err := janitor.WriteRecord(cfg.Home.State, record); err != nil {
		return
	}
	if len(record.Strays) == 0 || record.StrayKey() == previous.StrayKey() {
		return
	}
	var paths []string
	for _, stray := range record.Strays {
		paths = append(paths, stray.Path+" ("+stray.Detail+")")
	}
	detail := fmt.Sprintf("%d stray item(s) the janitor reports and does not remove: %s; remove what is the fleet's leftovers, record a goblin's extra worktree with cfo worktree add, and leave what is the Overlord's", len(paths), strings.Join(paths, "; "))
	if _, err := wake.Append(cfg.Home.State, "orphan", "strays", detail); err == nil {
		_, _ = wake.PublishEpisode(cfg.Home.State)
	}
}
