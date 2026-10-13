package verify

import (
	"cmp"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The executions store keeps the newest passes and, apart from them, the
// newest of everything else, so a burst of passes never evicts the evidence
// of a failure. What it keeps fits executionBudget together, passes going
// first when it does not.
const (
	keptPasses      = 100
	keptFailures    = 50
	executionBudget = 2 << 30
	// abandonedExecutionAge is how long a record still pending, or a log
	// without its record, is kept: no run lasts that long, so by then its
	// controller has gone without finishing it.
	abandonedExecutionAge = 7 * 24 * time.Hour
)

// executionFile is a file an execution leaves in the store: its record and
// its two logs, named for its random run ID.
var executionFile = regexp.MustCompile(`^([A-Z2-7]{26})\.json(\.stdout\.log|\.stderr\.log)?$`)

// PruneExecutions removes the oldest execution records of the report store,
// with their logs, beyond what it keeps: see pruneExecutions.
func PruneExecutions(now time.Time) error {
	store, err := StoreDir()
	if err != nil {
		return err
	}
	admission, err := AdmissionDir()
	if err != nil {
		return err
	}
	err = pruneExecutions(filepath.Join(store, "executions"), filepath.Join(admission, "custody"), executionBudget, now)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// pruneExecutions removes from the executions folder dir the oldest records
// beyond keptPasses passes and keptFailures others, each with its logs, and
// then the oldest left, passes first, until what is kept fits budget. A
// record custody still pins, in custodyDir, or one still pending and younger
// than abandonedExecutionAge is never removed and never counted, nor is a
// file the store did not name. A record that cannot be read counts as no
// pass, by when it was last written. A log whose record is gone is removed
// once it is older than abandonedExecutionAge.
func pruneExecutions(dir, custodyDir string, budget int64, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type stored struct {
		id      string
		created time.Time
		size    int64
	}
	var passes, others []stored
	logs := map[string][]string{}
	sizes := map[string]int64{}
	var failure error
	for _, entry := range entries {
		match := executionFile.FindStringSubmatch(entry.Name())
		if match == nil || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failure = errors.Join(failure, err)
			continue
		}
		id := match[1]
		sizes[id] += info.Size()
		if match[2] != "" {
			logs[id] = append(logs[id], entry.Name())
		}
	}
	for _, entry := range entries {
		match := executionFile.FindStringSubmatch(entry.Name())
		if match == nil || match[2] != "" || !entry.Type().IsRegular() {
			continue
		}
		id := match[1]
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if _, err := os.Lstat(filepath.Join(custodyDir, id+".json")); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		var record ExecutionRecord
		data, err := fsx.ReadFile(filepath.Join(dir, entry.Name()))
		if err == nil {
			err = json.Unmarshal(data, &record)
		}
		if err != nil || record.ID != id {
			others = append(others, stored{id: id, created: info.ModTime(), size: sizes[id]})
			continue
		}
		switch {
		case record.Status == "pending" && now.Sub(info.ModTime()) < abandonedExecutionAge:
		case record.Status == "passed":
			passes = append(passes, stored{id: id, created: record.Created, size: sizes[id]})
		default:
			others = append(others, stored{id: id, created: record.Created, size: sizes[id]})
		}
	}
	newestFirst := func(a, b stored) int { return cmp.Or(b.created.Compare(a.created), strings.Compare(b.id, a.id)) }
	slices.SortFunc(passes, newestFirst)
	slices.SortFunc(others, newestFirst)
	var removed []string
	for _, old := range passes[min(len(passes), keptPasses):] {
		removed = append(removed, old.id)
	}
	for _, old := range others[min(len(others), keptFailures):] {
		removed = append(removed, old.id)
	}
	passes, others = passes[:min(len(passes), keptPasses)], others[:min(len(others), keptFailures)]
	var total int64
	for _, kept := range append(slices.Clone(passes), others...) {
		total += kept.size
	}
	for total > budget && len(passes)+len(others) > 0 {
		oldest := &passes
		if len(passes) == 0 {
			oldest = &others
		}
		last := (*oldest)[len(*oldest)-1]
		*oldest = (*oldest)[:len(*oldest)-1]
		removed = append(removed, last.id)
		total -= last.size
	}
	for _, id := range removed {
		for _, log := range logs[id] {
			failure = errors.Join(failure, ignoreMissing(os.Remove(filepath.Join(dir, log))))
		}
		delete(logs, id)
		failure = errors.Join(failure, ignoreMissing(os.Remove(filepath.Join(dir, id+".json"))))
	}
	// A log whose record is gone belongs to a run that never wrote one, or
	// whose record was removed while the log was held open.
	for id, names := range logs {
		if _, err := os.Lstat(filepath.Join(dir, id+".json")); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		for _, name := range names {
			info, err := os.Lstat(filepath.Join(dir, name))
			if err == nil && now.Sub(info.ModTime()) > abandonedExecutionAge {
				failure = errors.Join(failure, ignoreMissing(os.Remove(filepath.Join(dir, name))))
			}
		}
	}
	return failure
}

func ignoreMissing(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
