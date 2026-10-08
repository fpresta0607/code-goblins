package janitor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// retiredGrace is how long a retired task's folders keep everything they
// hold, so what a task left can still be read the day it finishes.
const retiredGrace = 24 * time.Hour

// DataLimit is the size over which the sweep reports the data folder: the
// fleet's records take far less, so anything past it is files that belong
// somewhere else.
const DataLimit = 200 << 20

// keptBackups is how many backups of each kind state\backups keeps.
const keptBackups = 2

// archivedTask is a retired task's folder in state\archive, its task
// temporary folder that cleanup moved there, named for the task and the time
// it was archived.
var archivedTask = regexp.MustCompile(`^(.+)\.(\d{8}T\d{6}Z)$`)

// backupName is a backup in state\backups: its kind, then the time it was
// taken, then any extension, as home-migrate-20260926T221124Z or
// skills-20261005.tar.
var backupName = regexp.MustCompile(`^(.+?)-(\d{8}(?:T\d{6}Z)?)((?:\.[A-Za-z0-9]+)*)$`)

// isTextRecord reports whether a file at the top of a retired task's folder
// is part of its text record: its brief, report, decisions, handoffs and
// instruction, all Markdown, its notes in plain text, and its status log.
func isTextRecord(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".status")
}

// trimRetiredTasks keeps only the text record of every retired task: the
// folder cleanup archived its task temporary folder into, and its data
// folder once filing moved it to data\archive\finished, whose deliverables
// also stay. A task is retired when no live record of it is left; its
// folders keep everything for a day after that.
func (cfg Config) trimRetiredTasks(processes []string, record *Record) {
	if len(cfg.Inventory.UnreadableTasks) > 0 {
		record.Notes = append(record.Notes, "a task record could not be read, so no retired task's folder was trimmed")
		return
	}
	live := map[string]bool{}
	for _, task := range cfg.Inventory.Tasks {
		live[strings.ToLower(task.ID)] = true
	}
	archive := filepath.Join(cfg.Home.State, state.ArchiveDirName)
	for _, entry := range cfg.readFolder(archive, record) {
		match := archivedTask.FindStringSubmatch(entry.Name())
		if match == nil || !entry.IsDir() || live[strings.ToLower(match[1])] {
			continue
		}
		archived, err := time.Parse("20060102T150405Z", match[2])
		if err != nil || cfg.Now.Sub(archived) < retiredGrace {
			continue
		}
		cfg.keepTextRecord(filepath.Join(archive, entry.Name()), "a retired task's scratch, logs and build output", processes, record)
	}
	finished := filepath.Join(cfg.Home.Data, "archive", "finished")
	for _, entry := range cfg.readFolder(finished, record) {
		if !entry.IsDir() || live[strings.ToLower(entry.Name())] {
			continue
		}
		cfg.keepTextRecord(filepath.Join(finished, entry.Name()), "a finished task's evidence", processes, record)
	}
}

// readFolder lists dir, noting a folder that is there but cannot be read.
func (cfg Config) readFolder(dir string, record *Record) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		record.Notes = append(record.Notes, dir+" could not be read: "+err.Error())
	}
	return entries
}

// keepTextRecord removes everything at the top of folder but its text record
// and its deliverables, all or nothing: it removes nothing while a running
// process names the folder, anything in what it would remove was written in
// the last day, cannot be read or is a link, or a git repository is among
// it, whose work nothing here can prove landed. The last two are reported.
func (cfg Config) keepTextRecord(folder, what string, processes []string, record *Record) {
	for _, line := range processes {
		if names(line, folder) {
			return
		}
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		record.Notes = append(record.Notes, "left "+folder+": it could not be read: "+err.Error())
		return
	}
	var doomed []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && isTextRecord(entry.Name()) || entry.IsDir() && strings.EqualFold(entry.Name(), "deliverables") {
			continue
		}
		doomed = append(doomed, filepath.Join(folder, entry.Name()))
	}
	if len(doomed) == 0 {
		return
	}
	var bytes int64
	for _, path := range doomed {
		repository, err := findRepository(path)
		if err == nil && repository != "" {
			record.Strays = append(record.Strays, Item{Kind: "retired", Path: folder, Detail: "holds a git repository at " + repository + ", whose work nothing proves landed; remove it by hand once it is"})
			return
		}
		newest, size, walkErr := newestWrite(path)
		if err = errors.Join(err, walkErr); err != nil {
			record.Strays = append(record.Strays, Item{Kind: "retired", Path: folder, Detail: "left as it is, because what it holds could not all be read: " + err.Error()})
			return
		}
		if cfg.Now.Sub(newest) < retiredGrace {
			return
		}
		bytes += size
	}
	for _, path := range doomed {
		if err := os.RemoveAll(path); err != nil {
			record.Kept = append(record.Kept, Item{Kind: "retired", Path: path, Detail: "something still holds it open: " + err.Error()})
		}
	}
	record.Removed = append(record.Removed, Item{Kind: "retired", Path: folder, Bytes: bytes, Detail: what + "; its text record stays"})
}

// findRepository is the first git repository, a .git folder or file, at or
// under path. Links are not followed.
func findRepository(path string) (string, error) {
	found := ""
	err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.EqualFold(entry.Name(), ".git") {
			found = current
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// keepRecentBackups keeps the newest two backups of each kind in
// state\backups. A backup whose name carries no time is a kind of its own and
// stays.
func (cfg Config) keepRecentBackups(processes []string, record *Record) {
	dir := filepath.Join(cfg.Home.State, "backups")
	kinds := map[string][]string{}
	for _, entry := range cfg.readFolder(dir, record) {
		if match := backupName.FindStringSubmatch(entry.Name()); match != nil {
			kind := strings.ToLower(match[1] + match[3])
			kinds[kind] = append(kinds[kind], entry.Name())
		}
	}
	for _, names := range kinds {
		sort.Slice(names, func(i, j int) bool {
			return backupName.FindStringSubmatch(names[i])[2] > backupName.FindStringSubmatch(names[j])[2]
		})
		for _, name := range names[min(keptBackups, len(names)):] {
			cfg.removeBackup(filepath.Join(dir, name), processes, record)
		}
	}
}

func (cfg Config) removeBackup(path string, processes []string, record *Record) {
	for _, line := range processes {
		if names(line, path) {
			return
		}
	}
	_, bytes, err := newestWrite(path)
	if err != nil {
		record.Notes = append(record.Notes, "left "+path+": what it holds could not all be read")
		return
	}
	if err := os.RemoveAll(path); err != nil {
		record.Kept = append(record.Kept, Item{Kind: "backup", Path: path, Detail: "something still holds it open: " + err.Error()})
		return
	}
	record.Removed = append(record.Removed, Item{Kind: "backup", Path: path, Bytes: bytes, Detail: fmt.Sprintf("older than the newest %d backups of its kind", keptBackups)})
}

// reportDataSize reports a data folder over DataLimit, which holds files its
// records do not need.
func (cfg Config) reportDataSize(record *Record) {
	if record.Buckets.Data <= DataLimit {
		return
	}
	record.Strays = append(record.Strays, Item{Kind: "data", Path: cfg.Home.Data, Bytes: record.Buckets.Data, Detail: fmt.Sprintf("data holds %.0f MB, over the %d MB its records need: binaries, archives, logs and evidence belong in a task's scratch, which goes when the task does", float64(record.Buckets.Data)/(1<<20), DataLimit>>20)})
}
