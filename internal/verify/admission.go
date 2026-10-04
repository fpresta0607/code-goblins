package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// Admission is how verification runs take turns on one machine. A run holds
// one of a fixed number of slots while its heavy checks run, runs take the
// slots in the order they asked, and a run starts only once the machine has
// memory to spare. Each run looking at the machine by itself is what let
// four test steps start together on memory each had seen free: a slot is
// taken or it is not.
type Admission struct {
	// Dir holds the slots and, under line, the runs that wait: a lock file
	// for each, naming its process, and beside it a card saying which run
	// that is.
	Dir string
	// Floor is the available memory, in bytes, a run waits for, and Available
	// reads it. An unavailable reading refuses admission.
	Floor     uint64
	Available func() (uint64, error)
	// Who names this run to the runs behind it, and Budget is how long its
	// checks may run. Expiry does not release a live or uncertain owner.
	Who    string
	Budget time.Duration
	// Limit bounds the floor wait at the head of the line. Expiry refuses a turn.
	Limit time.Duration
	// Poll is how often a waiting run looks again.
	Poll time.Duration
	// Waiting, when set, is told that the run waits, as it starts to, whenever
	// its place in line changes, and once a minute: how long so far, and what
	// for.
	Waiting func(waited time.Duration, why string)
}

// Turn is a run's turn on the machine.
type Turn struct {
	// Waited is how long the run waited for it, or waited before its wait
	// failed.
	Waited time.Duration
	// Note says what was out of the ordinary about how the turn was taken,
	// when anything was, including an admission failure.
	Note    string
	release func()
}

// Release gives the turn's slot back.
func (t Turn) Release() {
	if t.release != nil {
		t.release()
	}
}

// card is what a run says of itself beside its lock file.
type card struct {
	Who           string  `json:"who"`
	BudgetSeconds float64 `json:"budget_seconds,omitempty"`
}

// arrivals keeps the places of runs that join the line from one process
// apart.
var arrivals atomic.Int64

// Wait joins the line and returns once the run has its turn: no run that
// asked earlier still waits, the machine has the memory, and a slot is free,
// or was left by a process whose recorded identity is verifiably gone. A
// run whose wait fails gets the time it had waited with the error, so the
// wait is on record even though no turn came of it.
func (a Admission) Wait(ctx context.Context) (Turn, error) {
	if err := ctx.Err(); err != nil {
		return Turn{}, err
	}
	if os.Getenv("CFO_VERIFY_SLOTS") != "" {
		return Turn{}, errors.New("verify: CFO_VERIFY_SLOTS is unsupported; capacity is shared in the canonical admission store")
	}
	var err error
	a.Dir, err = admissionDir(a.Dir, isTestBinary())
	if err != nil {
		return Turn{}, err
	}
	if _, err := admissionCapacity(a.Dir); err != nil {
		return Turn{}, err
	}
	if a.Available == nil {
		return Turn{}, errors.New("verify: available memory cannot be read")
	}
	line := filepath.Join(a.Dir, "line")
	if err := os.MkdirAll(line, 0o755); err != nil {
		return Turn{}, err
	}
	place := fmt.Sprintf("%020d-%d-%d", time.Now().UTC().UnixNano(), os.Getpid(), arrivals.Add(1))
	owner, err := lock.AcquireExclusiveNamedStrict(line, place)
	if err != nil {
		return Turn{}, err
	}
	defer a.remove(line, place, owner)
	if err := a.leave(line, place, card{Who: a.Who}); err != nil {
		return Turn{}, err
	}

	start := time.Now()
	var told time.Time
	var toldAhead int
	// shortSince is when this run, at the head of the line, first found memory
	// under the floor, and zero while it is not the head or memory is not
	// short: the limit is on the wait for memory, not on the time in line.
	var shortSince time.Time
	for {
		if err := ctx.Err(); err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		capacity, err := admissionCapacity(a.Dir)
		if err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		waited := time.Since(start)
		ahead, err := inFront(line, place)
		if err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		why := a.heldBy()
		short := ""
		if ahead == 0 {
			short, err = a.short()
			if err != nil {
				return Turn{Waited: time.Since(start)}, err
			}
		}
		if short == "" {
			shortSince = time.Time{}
		} else if shortSince.IsZero() {
			shortSince = time.Now()
		}
		if ahead == 0 {
			if short != "" && time.Since(shortSince) >= a.Limit {
				return Turn{Waited: time.Since(start)}, fmt.Errorf("verify: memory floor wait expired: %s", short)
			}
			if short != "" {
				why = join(why, short)
			} else {
				if err := ctx.Err(); err != nil {
					return Turn{Waited: time.Since(start)}, err
				}
				release, err := a.take(capacity)
				if err != nil {
					return Turn{Waited: time.Since(start)}, err
				}
				if release != nil {
					if err := ctx.Err(); err != nil {
						release()
						return Turn{Waited: time.Since(start)}, err
					}
					return Turn{Waited: time.Since(start), release: release}, nil
				}
			}
		}
		if a.Waiting != nil && (told.IsZero() || ahead != toldAhead || time.Since(told) >= time.Minute) {
			a.Waiting(waited, join(why, standing(ahead)))
			told, toldAhead = time.Now(), ahead
		}
		select {
		case <-ctx.Done():
			return Turn{Waited: time.Since(start)}, ctx.Err()
		case <-time.After(a.Poll):
		}
	}
}

// join puts two remarks in one line, either of which may be empty.
func join(first, second string) string {
	if first == "" || second == "" {
		return first + second
	}
	return first + "; " + second
}

// standing says where a run stands with ahead runs in front of it.
func standing(ahead int) string {
	switch ahead {
	case 0:
		return "this run is next in line"
	case 1:
		return "1 run is ahead of this one in line"
	}
	return fmt.Sprintf("%d runs are ahead of this one in line", ahead)
}

// inFront counts the runs that joined the line before place and still wait.
// A run whose process is gone is taken out of the line.
func inFront(line, place string) (int, error) {
	entries, err := os.ReadDir(line)
	if err != nil {
		return 0, err
	}
	ahead := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".run") || name >= place {
			continue
		}
		waiter, err := lock.ReadNamedStrict(line, name)
		gone := err == nil && !waiter.Alive()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if gone {
			current, err := lock.ReadNamedStrict(line, name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || *current != *waiter {
				ahead++
				continue
			}
			if err := os.Remove(filepath.Join(line, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
			if err := os.Remove(filepath.Join(line, name+".run")); err != nil && !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
			continue
		}
		ahead++
	}
	return ahead, nil
}

// short says how the machine is short of memory, or nothing when it is not.
func (a Admission) short() (string, error) {
	available, err := a.Available()
	if err != nil {
		return "", fmt.Errorf("verify: available memory cannot be read: %w", err)
	}
	if available >= a.Floor {
		return "", nil
	}
	return fmt.Sprintf("%.1f GB of memory is available and the floor is %.1f GB", Gigabytes(available), Gigabytes(a.Floor)), nil
}

// Gigabytes is bytes in gigabytes, rounded down to a tenth, so memory just
// under a floor never reads as the floor itself.
func Gigabytes(bytes uint64) float64 {
	return math.Floor(float64(bytes)/(1<<30)*10) / 10
}

// heldBy says which runs hold the slots, each with how long it has and under
// what budget, or nothing when no slot is held.
func (a Admission) heldBy() string {
	var holders []string
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return "turn custody cannot be read: " + err.Error()
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "slot-") || strings.HasSuffix(name, ".run") {
			continue
		}
		if holder, err := lock.ReadNamedStrict(a.Dir, name); err == nil && holder.Alive() {
			says := readCard(filepath.Join(a.Dir, name))
			who, budget := says.Who, time.Duration(says.BudgetSeconds*float64(time.Second))
			held := fmt.Sprintf("%s (pid %d), for %s", who, holder.PID, time.Since(holder.Acquired).Round(time.Second))
			if budget > 0 {
				held += fmt.Sprintf(" of its %s budget", budget)
			}
			holders = append(holders, held)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			holders = append(holders, name+" with unreadable custody")
		}
	}
	if len(holders) == 0 {
		return ""
	}
	return "the turn is held by " + strings.Join(holders, ", and by ")
}

// take takes a free slot or reclaims verifiably dead custody. A live or
// uncertain holder keeps the slot regardless of its elapsed budget.
func (a Admission) take(capacity int) (release func(), err error) {
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "slot-") && !strings.HasSuffix(name, ".run") && name != "slot-1" {
			return nil, fmt.Errorf("verify: custody outside shared capacity one: %s", name)
		}
	}
	for slot := 1; slot <= capacity; slot++ {
		name := fmt.Sprintf("slot-%d", slot)
		owner, err := lock.AcquireExclusiveNamedStrict(a.Dir, name)
		if errors.Is(err, lock.ErrHeld) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := a.leave(a.Dir, name, card{Who: a.Who, BudgetSeconds: a.Budget.Seconds()}); err != nil {
			lock.ReleaseExclusiveNamed(a.Dir, name)
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { a.remove(a.Dir, name, owner) }) }, nil
	}
	return nil, nil
}

// leave writes a run's card beside its lock file dir/name.
func (a Admission) leave(dir, name string, says card) error {
	data, err := json.Marshal(says)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".run"), data, 0o644)
}

// remove gives up the lock file dir/name, and its card with it when this
// process still holds the lock: a run whose turn was taken from it leaves the
// new holder's card alone. The card goes first, while the lock still keeps
// every other run out.
func (a Admission) remove(dir, name string, owner *lock.Info) {
	current, err := lock.ReadNamedStrict(dir, name)
	if err != nil || *current != *owner || !current.VerifiedAlive() {
		return
	}
	os.Remove(filepath.Join(dir, name+".run"))
	lock.ReleaseExclusiveNamed(dir, name)
}

// readCard reads what the run holding lockFile says of itself. A run whose
// card is not there, not written yet or unreadable, did not name itself.
func readCard(lockFile string) card {
	var says card
	if data, err := fsx.ReadFile(lockFile + ".run"); err == nil {
		json.Unmarshal(data, &says)
	}
	if says.Who == "" {
		says.Who = "a run that did not name itself"
	}
	return says
}

// Standing is one run that holds a turn or waits in line.
type Standing struct {
	Who string
	PID int
	// Since is when it took its turn or joined the line.
	Since time.Time
	// Budget is how long its turn may last, for a run that holds one and
	// said so.
	Budget time.Duration
}

// Line reads who holds the turns kept in dir, slot by slot, and who waits, in
// the order they take their turns. A run whose process is gone is left out.
func Line(dir string) (holding, waiting []Standing, err error) {
	// read lists the runs on record in dir, in the order of their files'
	// names, which for the line is the order of its places.
	read := func(dir string, keep func(string) bool) ([]Standing, error) {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var runs []Standing
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(name, ".run") || !keep(name) {
				continue
			}
			record, err := lock.ReadNamedStrict(dir, name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("verify: unreadable custody in %s: %w", filepath.Join(dir, name), err)
			}
			if record.Alive() {
				says := readCard(filepath.Join(dir, name))
				runs = append(runs, Standing{Who: says.Who, PID: record.PID, Since: record.Acquired, Budget: time.Duration(says.BudgetSeconds * float64(time.Second))})
			}
		}
		return runs, nil
	}
	if holding, err = read(dir, func(name string) bool { return strings.HasPrefix(name, "slot-") }); err != nil {
		return nil, nil, err
	}
	if waiting, err = read(filepath.Join(dir, "line"), func(string) bool { return true }); err != nil {
		return nil, nil, err
	}
	return holding, waiting, nil
}
