package janitor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tempAge is how long nothing in a temporary folder must have been written
// before the janitor takes it for a leak.
const tempAge = 24 * time.Hour

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
// machine's temporary folder, those its settings name; in the home's scratch
// folder and the older builds' Go temporary folder, a task's whose record is
// gone. Each goes only once nothing in it was written for a day and no
// running process names it.
func (cfg Config) removeTempLeaks(processes []string, record *Record) {
	var candidates []string
	if cfg.TempDir != "" {
		entries, err := os.ReadDir(cfg.TempDir)
		if err != nil {
			record.Notes = append(record.Notes, "the temporary folder could not be read: "+err.Error())
		}
		for _, entry := range entries {
			if entry.IsDir() && cfg.isFleetTemp(entry.Name()) {
				candidates = append(candidates, filepath.Join(cfg.TempDir, entry.Name()))
			}
		}
	}
	if len(cfg.Inventory.UnreadableTasks) > 0 {
		record.Notes = append(record.Notes, "a task record could not be read, so no scratch folder was taken for a finished task's")
	} else {
		live := map[string]bool{}
		for _, task := range cfg.Inventory.Tasks {
			live[strings.ToLower(task.ID)] = true
		}
		for _, root := range []string{cfg.Home.Scratch(), cfg.LegacyGoTmp} {
			if root == "" {
				continue
			}
			entries, _ := os.ReadDir(root)
			for _, entry := range entries {
				if entry.IsDir() && !live[strings.ToLower(entry.Name())] {
					candidates = append(candidates, filepath.Join(root, entry.Name()))
				}
			}
		}
	}
	for _, path := range candidates {
		cfg.removeTempLeak(path, processes, record)
	}
}

func (cfg Config) isFleetTemp(name string) bool {
	for _, pattern := range cfg.Settings.TempPatterns {
		if matched, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(name)); err == nil && matched {
			return true
		}
	}
	return false
}

func (cfg Config) removeTempLeak(path string, processes []string, record *Record) {
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
	if cfg.Now.Sub(newest) < tempAge {
		return
	}
	if err := os.RemoveAll(path); err != nil {
		record.Kept = append(record.Kept, Item{Kind: "temp", Path: path, Detail: "something still holds it open: " + err.Error()})
		return
	}
	record.Removed = append(record.Removed, Item{Kind: "temp", Path: path, Bytes: bytes, Detail: "nothing written in it for a day and no running process names it"})
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
