// Package janitor keeps the CFO home small without anyone asking. The
// watcher's sweep runs it: it removes the worktrees no task owns once their
// work is safe (on the default branch, or kept as a local archive tag first),
// keeps bin to the current build and the two before it, removes the fleet's
// temporary folders a day after anything last touched them, trims the shared
// caches back under their cap with each tool's own prune, and reports what it
// must not touch: a worktree holding uncommitted work, a worktree no task
// records, and a new folder or file in the projects root that is no checkout.
// It never touches Docker, the Overlord's checkouts or files, or anything
// holding uncommitted or unpushed work, and every check that decides a removal
// refuses when it cannot read what it checks.
package janitor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// Config is one sweep's inputs.
type Config struct {
	Home     home.Home
	Commands execx.Runner
	Settings fleetconfig.Settings
	// Inventory is the reap sweep's evidence: the task records, the fleet's
	// worktrees with whether their projects register them, the agents'
	// panes and every process with its command line.
	Inventory reap.Inventory
	// TempDir is the machine's temporary folder, where the fleet's tests and
	// tools leave folders behind.
	TempDir string
	// LegacyGoTmp is the folder an older build gave this home's tasks' Go
	// temporary directories in, one per task.
	LegacyGoTmp string
	// ProjectsRoot is the folder holding the Overlord's checkouts, empty when
	// none is recorded.
	ProjectsRoot string
	// SelfPID is this process, whose own command line proves the process
	// list was read before anything is removed on the strength of it.
	SelfPID int
	Now     time.Time
}

// Item is one thing a sweep removed, kept or reports.
type Item struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Record is one sweep, kept in state for the digest, the board and cfo
// runtime to read.
type Record struct {
	Time    time.Time    `json:"time"`
	Removed []Item       `json:"removed,omitempty"`
	Kept    []Item       `json:"kept,omitempty"`
	Strays  []Item       `json:"strays,omitempty"`
	Disk    disk.Reading `json:"disk"`
	Buckets Buckets      `json:"buckets"`
	Notes   []string     `json:"notes,omitempty"`
}

// Freed is the bytes the sweep's removals gave back.
func (r Record) Freed() int64 {
	var freed int64
	for _, item := range r.Removed {
		freed += item.Bytes
	}
	return freed
}

// StrayKey is a stable digest of the strays, so the CFO is woken when the set
// changes and not on every sweep that finds the same ones.
func (r Record) StrayKey() string {
	paths := make([]string, 0, len(r.Strays))
	for _, stray := range r.Strays {
		paths = append(paths, strings.ToLower(stray.Path))
	}
	sort.Strings(paths)
	return strings.Join(paths, "\n")
}

// RecordName is the sweep's record in the home's state folder.
const RecordName = "janitor.json"

// WriteRecord keeps the sweep in stateDir.
func WriteRecord(stateDir string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, RecordName), append(data, '\n'))
}

// ReadRecord reads the last sweep stateDir keeps.
func ReadRecord(stateDir string) (Record, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, RecordName))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("janitor: read %s: %w", RecordName, err)
	}
	return record, nil
}

// Sweep tidies the home once. Each pass reports what it could not read as a
// note and takes nothing on the strength of it; one pass failing never stops
// the others.
func Sweep(ctx context.Context, cfg Config) Record {
	record := Record{Time: cfg.Now.UTC()}
	cfg.tidyWorktrees(ctx, &record)
	cfg.keepRecentBuilds(&record)
	processes, ok := cfg.readableProcesses(&record)
	if ok {
		cfg.removeTempLeaks(processes, &record)
	}
	cfg.trimCaches(ctx, &record)
	cfg.reportProjectsRoot(&record)
	metas := make([]state.TaskMeta, 0, len(cfg.Inventory.Tasks))
	for _, task := range cfg.Inventory.Tasks {
		metas = append(metas, task.Meta)
	}
	record.Buckets = Measure(cfg.Home, metas)
	if reading, err := disk.Read(cfg.Home.Root); err == nil {
		record.Disk = reading
	} else {
		record.Notes = append(record.Notes, "free disk could not be read: "+err.Error())
	}
	return record
}

// keepRecentBuilds keeps bin to the current build and the two before it,
// unless an update is under way, whose staged and moved-aside copies are its
// way back.
func (cfg Config) keepRecentBuilds(record *Record) {
	journal, err := update.ReadJournal(cfg.Home.State)
	if err == nil && !journal.Phase.Finished() {
		record.Notes = append(record.Notes, "an update is under way, so bin was left as it is")
		return
	}
	before := Size(cfg.Home.Bin())
	left := update.KeepRecent(cfg.Home.Bin(), update.KeptBuilds)
	if freed := before - Size(cfg.Home.Bin()); freed > 0 {
		record.Removed = append(record.Removed, Item{Kind: "bin", Path: cfg.Home.Bin(), Bytes: freed, Detail: "builds older than the two before the current one"})
	}
	for _, path := range left {
		record.Kept = append(record.Kept, Item{Kind: "bin", Path: path, Detail: "an old build something still runs"})
	}
}
