package state

import (
	"errors"
	"os"
	"strings"
)

// Scan is one listing of a home's state directory, split into the two id sets
// every fleet surface needs: the tasks that have a metadata record, and the
// status logs that have none.
//
// A status log with no meta beside it is history, not a live claim on the id
// (cleanup deliberately leaves the log behind when it retires a task), so it
// is reported separately rather than counted as a task.
//
// Dot-prefixed files are skipped throughout. state/ holds CFO's own
// bookkeeping under dotted names, and .reap.status in particular is a real
// status log that belongs to no task and must never read as an orphan.
type Scan struct {
	MetaIDs         []string
	OrphanStatusIDs []string
}

// ScanIDs reads stateDir once and splits it. A missing directory is an empty
// scan, not an error: a home with no goblins yet has no state directory.
func ScanIDs(stateDir string) (Scan, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Scan{}, nil
		}
		return Scan{}, err
	}

	var scan Scan
	metas := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if id, ok := strings.CutSuffix(entry.Name(), ".meta"); ok {
			scan.MetaIDs = append(scan.MetaIDs, id)
			metas[id] = true
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if id, ok := strings.CutSuffix(entry.Name(), ".status"); ok && !metas[id] {
			scan.OrphanStatusIDs = append(scan.OrphanStatusIDs, id)
		}
	}
	return scan, nil
}
