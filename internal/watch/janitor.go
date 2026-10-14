package watch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// janitorBudget bounds one janitor pass: a tool's cache prune can take
// minutes, a worktree's removal seconds each.
const janitorBudget = 30 * time.Minute

// heavyTurnWait is how long the janitor waits for a turn in the machine's
// heavy line before it leaves its cache deletions to the next sweep.
const heavyTurnWait = 5 * time.Second

// heavyTurn takes a turn in the machine's heavy line for the janitor's
// deletions from the package caches, so no run that takes its turn starts
// building from them meanwhile, and returns what gives it back. It takes only
// a turn free within heavyTurnWait: a run that holds the line or waits in it
// makes it fail, naming that run as cfo gate turns does.
func heavyTurn(ctx context.Context) (func() error, error) {
	store, err := verify.AdmissionDir()
	if err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, heavyTurnWait)
	defer cancel()
	turn, err := verify.Admission{
		Dir: store,
		// A deletion needs no memory of its own: its turn only keeps builds
		// from starting beside it, so the line's memory floor never holds it
		// back.
		Available: func() (uint64, error) { return math.MaxUint64, nil },
		Who:       "the janitor, deleting from the package caches",
		Budget:    janitorBudget,
		Limit:     verify.NoLimit,
		Poll:      100 * time.Millisecond,
	}.Wait(wait)
	if err == nil {
		return turn.Release, nil
	}
	// A wait cut by its deadline as it took the turn still holds the slot.
	err = errors.Join(err, turn.Release())
	if wait.Err() == nil {
		return nil, err
	}
	holding, waiting, lineErr := verify.Line(store)
	switch {
	case lineErr != nil:
		return nil, fmt.Errorf("the line was not free within %s and cannot be read: %w", heavyTurnWait, lineErr)
	case len(holding) > 0:
		return nil, fmt.Errorf("%s (pid %d) holds the machine's heavy line", holding[0].Who, holding[0].PID)
	case len(waiting) > 0:
		return nil, fmt.Errorf("%s (pid %d) waits for the machine's heavy line", waiting[0].Who, waiting[0].PID)
	}
	return nil, fmt.Errorf("the machine's heavy line was not free within %s", heavyTurnWait)
}

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
	browserSessions := ""
	if profile, err := os.UserHomeDir(); err == nil {
		browserSessions = filepath.Join(profile, ".chrome-devtools-axi", "sessions")
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
		StopServices: services.Service{
			StateDir: cfg.Home.State,
			DataDir:  cfg.Home.Data,
			Docker:   services.CLI{Commands: cfg.Reap.Commands},
			IsLive:   services.LiveIn(cfg.Home.State),
			Now:      time.Now,
		}.Sweep,
		Processes:  janitor.ReadProcesses,
		Owners:     func() ([]janitor.Owner, []string) { return janitor.ReadOwners(cfg.Home) },
		EndProcess: janitor.EndProcess,
		Watched:    previous.Processes.Watched,

		BrowserSessions: browserSessions,
		HeavyTurn:       heavyTurn,
	})
	if err := janitor.WriteRecord(cfg.Home.State, record); err != nil {
		return
	}
	isWoken := false
	if detail := strayWake(previous, record); detail != "" {
		_, err := wake.Append(cfg.Home.State, "orphan", "strays", detail)
		isWoken = isWoken || err == nil
	}
	if detail := processWake(previous, record, time.Now()); detail != "" {
		_, err := wake.Append(cfg.Home.State, "orphan", "processes", detail)
		isWoken = isWoken || err == nil
	}
	if isWoken {
		_, _ = wake.PublishEpisode(cfg.Home.State)
	}
}

// processWake is the wake detail for a pass that left a process for the CFO
// that the last pass had not, and empty otherwise. It is one wake for all of
// them, each with its pid, its age, the memory of its tree and its command:
// the same processes again, or one of them gone, is nothing new.
func processWake(previous, record janitor.Record, now time.Time) string {
	told := make(map[string]bool, len(previous.Processes.Left))
	key := func(item janitor.ProcessItem) string {
		return fmt.Sprintf("%d %s", item.PID, item.Started.UTC().Format(time.RFC3339Nano))
	}
	for _, item := range previous.Processes.Left {
		told[key(item)] = true
	}
	isNew := false
	var lines []string
	for _, item := range record.Processes.Left {
		isNew = isNew || !told[key(item)]
		lines = append(lines, fmt.Sprintf("pid %d %s, %s old, %s, %s (%s)", item.PID, item.Name, now.Sub(item.Started).Round(time.Minute), memoryText(item.Memory), item.Command, item.Why))
	}
	if !isNew {
		return ""
	}
	return fmt.Sprintf("%d process(es) the janitor found and did not end, since nothing proves them the fleet's own: %s. End by pid what is the fleet's leftover and leave what is the Overlord's", len(lines), strings.Join(lines, ". "))
}

// memoryText is a count of bytes as a person reads memory.
func memoryText(bytes uint64) string {
	if bytes >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	}
	return fmt.Sprintf("%d MB", bytes>>20)
}

// strayWake is the wake detail for a pass that reports a stray the last pass
// did not, and empty otherwise: the same strays again, or one of them gone,
// is nothing new for the CFO.
func strayWake(previous, record janitor.Record) string {
	reported := make(map[string]bool, len(previous.Strays))
	for _, stray := range previous.Strays {
		reported[strings.ToLower(stray.Path)] = true
	}
	isNew := false
	for _, stray := range record.Strays {
		if !reported[strings.ToLower(stray.Path)] {
			isNew = true
		}
	}
	if !isNew {
		return ""
	}
	var paths []string
	for _, stray := range record.Strays {
		paths = append(paths, stray.Path+" ("+stray.Detail+")")
	}
	return fmt.Sprintf("%d stray item(s) the janitor reports and does not remove: %s; remove what is the fleet's leftovers, record a goblin's extra worktree with cfo worktree add, and leave what is the Overlord's", len(paths), strings.Join(paths, "; "))
}
