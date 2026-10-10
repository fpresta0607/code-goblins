// Package janitor keeps the CFO home small without anyone asking. The
// watcher's sweep runs it: it removes the worktrees in its home no task owns
// once their work is safe (on the default branch, or kept as a local archive
// tag first), keeps bin to the current build alone, removes
// the fleet's temporary folders a day after anything last touched them, keeps
// a retired task's folders to their text record and deliverables, keeps the
// newest two backups of each kind, trims the shared caches back under their
// cap with each tool's own prune, and reports what it must not touch: a
// worktree holding uncommitted work, a worktree no task records, a retired
// task's folder holding a git repository, a data folder over 200 MB, and a
// new folder or file in the projects root that is no checkout. It stops the
// local services stacks cfo services started that no live task holds, and
// beyond those never touches Docker, the Overlord's checkouts or files, or
// anything holding uncommitted or unpushed work, and every check that decides
// a removal refuses when it cannot read what it checks. It ends the processes
// a terminal of the home left running once the terminal is gone, and a
// detached tree of a running terminal once it has sat idle for an hour, each
// proven the terminal's own as its teardown proves it, and names for the CFO
// what it cannot prove.
package janitor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	// StopServices stops every local services stack cfo services started
	// that no live task holds, and the Docker engine cfo started once
	// nothing it started runs on it, and says what it stopped. Nil stops
	// nothing.
	StopServices func(ctx context.Context) ([]string, error)
	// Processes reads every running process with the evidence of whose it
	// is and what it costs. Nil sweeps no process.
	Processes func(ctx context.Context) ([]Process, error)
	// Owners lists the home's terminals whose processes the sweep judges,
	// and what it could not read of them.
	Owners func() ([]Owner, []string)
	// EndProcess ends one process the sweep proved a terminal's own.
	EndProcess func(ctx context.Context, item ProcessItem) error
	// Watched are the detached trees the last sweep went on watching.
	Watched []Watched
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
	// Processes are the processes the sweep ended, those it left for the
	// CFO, and the detached trees it goes on watching.
	Processes ProcessSweep `json:"processes,omitzero"`
}

// Freed is the bytes the sweep's removals gave back.
func (r Record) Freed() int64 {
	var freed int64
	for _, item := range r.Removed {
		freed += item.Bytes
	}
	return freed
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
	cfg.keepCurrentBuild(&record)
	processes, ok := cfg.readableProcesses(&record)
	if ok {
		cfg.removeTempLeaks(processes, &record)
		cfg.trimRetiredTasks(processes, &record)
		cfg.keepRecentBackups(processes, &record)
		cfg.retireMovedCaches(&record)
	}
	cfg.trimCaches(ctx, &record)
	cfg.stopServices(ctx, &record)
	cfg.sweepProcesses(ctx, &record)
	cfg.reportProjectsRoot(&record)
	metas := make([]state.TaskMeta, 0, len(cfg.Inventory.Tasks))
	for _, task := range cfg.Inventory.Tasks {
		metas = append(metas, task.Meta)
	}
	record.Buckets = Measure(cfg.Home, metas)
	cfg.reportDataSize(&record)
	if reading, err := disk.ReadLeast(cfg.Home.Root, cfg.Home.DevDrive); err == nil {
		record.Disk = reading
	} else {
		record.Notes = append(record.Notes, "free disk could not be read: "+err.Error())
	}
	return record
}

// stopServices stops the local services stacks no live task holds. One that
// cannot be stopped is a note, and the next sweep tries again.
func (cfg Config) stopServices(ctx context.Context, record *Record) {
	if cfg.StopServices == nil {
		return
	}
	lines, err := cfg.StopServices(ctx)
	for _, line := range lines {
		record.Removed = append(record.Removed, Item{Kind: "services", Detail: line})
	}
	if err != nil {
		record.Notes = append(record.Notes, "the local services no live task holds could not be stopped, so the next sweep tries again: "+err.Error())
	}
}

// keepCurrentBuild keeps the folder holding the home's build, bin or the root
// of a home a build before bin set up, to the current build alone, unless an
// update is under way, whose staged and moved-aside copies are its way back.
func (cfg Config) keepCurrentBuild(record *Record) {
	programs := cfg.Home.Programs()
	journal, err := update.ReadJournal(cfg.Home.State)
	if err == nil && !journal.Phase.Finished() {
		record.Notes = append(record.Notes, "an update is under way, so "+programs+" was left as it is")
		return
	}
	freed, left := update.RemoveAsideCopies(programs)
	if freed > 0 {
		record.Removed = append(record.Removed, Item{Kind: "bin", Path: programs, Bytes: freed, Detail: "earlier builds moved aside"})
	}
	for _, path := range left {
		record.Kept = append(record.Kept, Item{Kind: "bin", Path: path, Detail: "an old build something still runs"})
	}
}
