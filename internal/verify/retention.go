package verify

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The executions store keeps the newest passes, failures and refusals each
// apart, so a burst of one never evicts the evidence of another, and apart
// from them the runs that kept no logs. What it counts fits executionBudget
// together, passes going first, then refusals, then failures, but never past
// the newest pass and the newest keptAlwaysFailures failures, so a run's own
// evidence outlives a budget it overran alone. Runs that kept no logs are a
// record each, which would free next to nothing, so only their count bounds
// them.
const (
	keptPasses         = 100
	keptFailures       = 50
	keptRefusals       = 20
	keptAlwaysFailures = 5
	// keptUnlogged are the runs that kept no logs, as cfo gate turn's, whose
	// output their caller keeps. They are kept apart, so a burst of them
	// never evicts a cfo verify run's logs, which are kept nowhere else.
	keptUnlogged    = 200
	executionBudget = 2 << 30
	// abandonedExecutionAge is how long a record still pending or one that
	// cannot be read, and a log without its record, are kept: no run lasts
	// that long, so by then its controller has gone without finishing it.
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
	passes, err := passesDir()
	if err != nil {
		return err
	}
	return pruneExecutions(filepath.Join(store, "executions"), filepath.Join(admission, "custody"), passes, executionBudget, now)
}

// passPins are the run IDs of the records a pass in passesDir rests on while
// it may still be taken again: one kept within keptPassAge of now names each
// of its commands' records, which FindPass reads. The newest passes pin
// first, and only while the bytes of what they pin, read from sizes, stay
// within share, so passes never crowd out the evidence of failures. A pass
// past that pins nothing, and is taken again only while its records outlive
// the budget. A pass that cannot be parsed is never taken, so it pins
// nothing, but one that cannot be read may name any record, so it is an
// error. A passes folder that is not there pins nothing.
func passPins(passesDir string, now time.Time, sizes map[string]int64, share int64) (map[string]bool, error) {
	pins := map[string]bool{}
	entries, err := os.ReadDir(passesDir)
	if errors.Is(err, os.ErrNotExist) {
		return pins, nil
	}
	if err != nil {
		return nil, err
	}
	var passes []Pass
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := fsx.ReadFile(filepath.Join(passesDir, entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("verify: the pass %s could not be read, so no record it may rest on is removed: %w", entry.Name(), err)
		}
		var pass Pass
		if json.Unmarshal(data, &pass) != nil || now.Sub(pass.Kept) > keptPassAge {
			continue
		}
		passes = append(passes, pass)
	}
	slices.SortFunc(passes, func(a, b Pass) int { return b.Kept.Compare(a.Kept) })
	var pinned int64
	for _, pass := range passes {
		var ids []string
		var bytes int64
		for _, result := range pass.Results {
			if id, isRecord := strings.CutSuffix(filepath.Base(result.Execution), ".json"); isRecord && !pins[id] {
				ids = append(ids, id)
				bytes += sizes[id]
			}
		}
		if pinned+bytes > share {
			break
		}
		for _, id := range ids {
			pins[id] = true
		}
		pinned += bytes
	}
	return pins, nil
}

// stored is an execution the store counts: its run ID, when it was created
// and the bytes of its record and logs.
type stored struct {
	id      string
	created time.Time
	size    int64
}

// pruneExecutions removes from the executions folder dir the oldest records
// beyond keptPasses passes, keptFailures failures and keptRefusals runs that
// never started, each with its logs, and then the oldest left until what it
// counts fits budget, within the floor the constants above give. A record
// custody still pins, in custodyDir, a pending one or one it cannot read
// younger than abandonedExecutionAge, and a file the store did not name are
// never removed and never counted. A record a pass in passesDir rests on,
// within half the budget (passPins), is counted and never removed, and a
// pass that cannot be read removes nothing. A log whose record is gone is removed once it is older
// than abandonedExecutionAge. A folder that is not there is no error, and
// one that is not a plain folder is refused.
func pruneExecutions(dir, custodyDir, passesDir string, budget int64, now time.Time) error {
	folder, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !folder.Mode().IsDir() {
		return fmt.Errorf("verify: the executions store %s is not a plain folder", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	logs := map[string][]string{}
	sizes := map[string]int64{}
	records := map[string]os.FileInfo{}
	var failure error
	for _, entry := range entries {
		match := executionFile.FindStringSubmatch(entry.Name())
		if match == nil || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failure = errors.Join(failure, ignoreMissing(err))
			continue
		}
		id := match[1]
		sizes[id] += info.Size()
		if match[2] != "" {
			logs[id] = append(logs[id], entry.Name())
		} else {
			records[id] = info
		}
	}
	// Passes pin within half the budget, so the other half is always the
	// failures', refusals' and other passes'.
	pins, err := passPins(passesDir, now, sizes, budget/2)
	if err != nil {
		return err
	}
	var passes, failures, refusals, unlogged []stored
	var pinned int64
	for id, info := range records {
		if _, err := os.Lstat(filepath.Join(custodyDir, id+".json")); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if pins[id] {
			pinned += sizes[id]
			continue
		}
		isYoung := now.Sub(info.ModTime()) < abandonedExecutionAge
		var record ExecutionRecord
		data, err := fsx.ReadFile(filepath.Join(dir, id+".json"))
		if err == nil {
			err = json.Unmarshal(data, &record)
		}
		if err != nil || record.ID != id || record.Version != 1 {
			// A record being written, or one a newer cfo wrote, is not
			// taken for an old failure while it is young.
			if !isYoung {
				failures = append(failures, stored{id: id, created: info.ModTime(), size: sizes[id]})
			}
			continue
		}
		entry := stored{id: id, created: record.Created, size: sizes[id]}
		if len(logs[id]) == 0 {
			if record.Status != "pending" || !isYoung {
				unlogged = append(unlogged, entry)
			}
			continue
		}
		switch record.Status {
		case "pending":
			if !isYoung {
				failures = append(failures, entry)
			}
		case "passed":
			passes = append(passes, entry)
		case "not_started":
			refusals = append(refusals, entry)
		default:
			failures = append(failures, entry)
		}
	}
	newestFirst := func(a, b stored) int { return cmp.Or(b.created.Compare(a.created), strings.Compare(b.id, a.id)) }
	var removed []string
	keep := func(list []stored, count int) []stored {
		slices.SortFunc(list, newestFirst)
		for _, old := range list[min(len(list), count):] {
			removed = append(removed, old.id)
		}
		return list[:min(len(list), count)]
	}
	passes, failures, refusals, unlogged = keep(passes, keptPasses), keep(failures, keptFailures), keep(refusals, keptRefusals), keep(unlogged, keptUnlogged)
	total := pinned
	for _, list := range [][]stored{passes, failures, refusals, unlogged} {
		for _, kept := range list {
			total += kept.size
		}
	}
	for _, evictable := range []struct {
		list  *[]stored
		floor int
	}{{&passes, 1}, {&refusals, 0}, {&failures, keptAlwaysFailures}} {
		for total > budget && len(*evictable.list) > evictable.floor {
			last := (*evictable.list)[len(*evictable.list)-1]
			*evictable.list = (*evictable.list)[:len(*evictable.list)-1]
			removed = append(removed, last.id)
			total -= last.size
		}
	}
	for _, id := range removed {
		// The record goes only once its logs are gone, so no log is left
		// that nothing counts.
		var logFailure error
		for _, log := range logs[id] {
			logFailure = errors.Join(logFailure, ignoreMissing(fsx.Remove(filepath.Join(dir, log))))
		}
		delete(logs, id)
		if logFailure != nil {
			failure = errors.Join(failure, logFailure)
			continue
		}
		failure = errors.Join(failure, ignoreMissing(fsx.Remove(filepath.Join(dir, id+".json"))))
	}
	// A log whose record is gone belongs to a run that never wrote one, or
	// whose record was removed while the log was held open.
	for id, names := range logs {
		if _, isRecorded := records[id]; isRecorded {
			continue
		}
		for _, name := range names {
			info, err := os.Lstat(filepath.Join(dir, name))
			if err == nil && now.Sub(info.ModTime()) > abandonedExecutionAge {
				failure = errors.Join(failure, ignoreMissing(fsx.Remove(filepath.Join(dir, name))))
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
