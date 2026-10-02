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
	// Slots is how many runs may hold a turn at once.
	Slots int
	// Floor is the available memory, in bytes, a run waits for, and Available
	// reads it. A reading that fails holds no run back: the slots still do.
	Floor     uint64
	Available func() (uint64, error)
	// Who names this run to the runs behind it, and Budget is how long its
	// turn may last: a run still holding its turn after that loses it to the
	// next in line, so a run that hangs cannot stop the line for ever. A
	// holder that named no budget is held to the waiting run's, and with none
	// on either side a holder is never past one.
	Who    string
	Budget time.Duration
	// Limit is how long a run waits for memory before it goes on under the
	// floor, which its Turn then notes.
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
	// Waited is how long the run waited for it.
	Waited time.Duration
	// Note says what was out of the ordinary about how the turn was taken,
	// when anything was: a holder past its budget, or memory under the floor.
	Note    string
	release func()
	say     func(now string)
}

// Release gives the turn's slot back.
func (t Turn) Release() {
	if t.release != nil {
		t.release()
	}
}

// Say records what the run is doing now beside its turn, for whoever reads
// the line: a gate shows a step's output only once the step has ended. A run
// whose turn was taken from it no longer speaks for the slot and says
// nothing.
func (t Turn) Say(now string) {
	if t.say != nil {
		t.say(now)
	}
}

// card is what a run says of itself beside its lock file. Now is what a run
// that holds a turn says it is doing.
type card struct {
	Who           string  `json:"who"`
	BudgetSeconds float64 `json:"budget_seconds,omitempty"`
	Now           string  `json:"now,omitempty"`
}

// arrivals keeps the places of runs that join the line from one process
// apart.
var arrivals atomic.Int64

// Wait joins the line and returns once the run has its turn: no run that
// asked earlier still waits, the machine has the memory, and a slot is free,
// was left by a process that is gone, or is held by a run past its budget.
func (a Admission) Wait(ctx context.Context) (Turn, error) {
	line := filepath.Join(a.Dir, "line")
	if err := os.MkdirAll(line, 0o755); err != nil {
		return Turn{}, err
	}
	place := fmt.Sprintf("%020d-%d-%d", time.Now().UTC().UnixNano(), os.Getpid(), arrivals.Add(1))
	if _, err := lock.AcquireExclusiveNamed(line, place); err != nil {
		return Turn{}, err
	}
	a.leave(line, place, card{Who: a.Who})
	defer a.remove(line, place)

	start := time.Now()
	var told time.Time
	var toldAhead int
	var note string
	for {
		waited := time.Since(start)
		ahead, err := inFront(line, place)
		if err != nil {
			return Turn{}, err
		}
		why := a.heldBy()
		if ahead == 0 {
			short := a.short()
			if short != "" && waited >= a.Limit {
				note, short = fmt.Sprintf("it runs although %s, after waiting %s for it", short, waited.Round(time.Second)), ""
			}
			if short != "" {
				why = join(why, short)
			} else {
				slot, took, err := a.take()
				if err != nil {
					return Turn{}, err
				}
				if slot != "" {
					return Turn{
						Waited:  time.Since(start),
						Note:    join(note, took),
						release: func() { a.remove(a.Dir, slot) },
						say:     func(now string) { a.say(slot, now) },
					}, nil
				}
			}
		}
		if a.Waiting != nil && (told.IsZero() || ahead != toldAhead || time.Since(told) >= time.Minute) {
			a.Waiting(waited, join(why, standing(ahead)))
			told, toldAhead = time.Now(), ahead
		}
		select {
		case <-ctx.Done():
			return Turn{}, ctx.Err()
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

// unreadableGrace is how long a record in the line may stay unreadable and
// keep its place: a record is written in far less.
const unreadableGrace = 10 * time.Second

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
		waiter, err := lock.ReadNamed(line, name)
		gone := err == nil && !waiter.Alive()
		if err != nil {
			// A record that cannot be read is being written or removed this
			// moment and keeps its place, unless it has been unreadable for
			// longer than a write takes: then its run died writing it.
			info, statErr := entry.Info()
			gone = statErr == nil && time.Since(info.ModTime()) > unreadableGrace
		}
		if gone {
			os.Remove(filepath.Join(line, name))
			os.Remove(filepath.Join(line, name+".run"))
			continue
		}
		ahead++
	}
	return ahead, nil
}

// short says how the machine is short of memory, or nothing when it is not.
func (a Admission) short() string {
	if a.Available == nil {
		return ""
	}
	available, err := a.Available()
	if err != nil || available >= a.Floor {
		return ""
	}
	return fmt.Sprintf("%.1f GB of memory is available and the floor is %.1f GB", Gigabytes(available), Gigabytes(a.Floor))
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
	for slot := 1; slot <= a.Slots; slot++ {
		name := fmt.Sprintf("slot-%d", slot)
		if holder, err := lock.ReadNamed(a.Dir, name); err == nil {
			who, budget := a.holder(name)
			held := fmt.Sprintf("%s (pid %d), for %s", who, holder.PID, time.Since(holder.Acquired).Round(time.Second))
			if budget > 0 {
				held += fmt.Sprintf(" of its %s budget", budget)
			}
			holders = append(holders, held)
		}
	}
	if len(holders) == 0 {
		return ""
	}
	return "the turn is held by " + strings.Join(holders, ", and by ")
}

// take takes a free slot, or the slot of a run past its budget, which took
// then says, and returns the slot's name. With every slot held within its
// budget it takes none.
func (a Admission) take() (slot, took string, err error) {
	for slot := 1; slot <= a.Slots; slot++ {
		name := fmt.Sprintf("slot-%d", slot)
		_, err = lock.AcquireExclusiveNamed(a.Dir, name)
		if errors.Is(err, lock.ErrHeld) {
			holder, readErr := lock.ReadNamed(a.Dir, name)
			if readErr != nil {
				continue
			}
			who, budget := a.holder(name)
			has := time.Since(holder.Acquired)
			if budget <= 0 || has <= budget || !a.evict(name, holder) {
				continue
			}
			took = fmt.Sprintf("it took the turn from %s (pid %d), which had held it for %s against a budget of %s and still runs", who, holder.PID, has.Round(time.Second), budget)
			if _, err = lock.AcquireExclusiveNamed(a.Dir, name); errors.Is(err, lock.ErrHeld) {
				// Another run took the freed slot first.
				took = ""
				continue
			}
		}
		if err != nil {
			return "", "", err
		}
		a.leave(a.Dir, name, card{Who: a.Who, BudgetSeconds: a.Budget.Seconds()})
		return name, took, nil
	}
	return "", "", nil
}

// say rewrites the card of the slot this run holds with what the run is doing
// now. The card is replaced whole, so a run reading it never finds it half
// written and takes its holder for one that named no budget.
func (a Admission) say(slot, now string) {
	if _, err := lock.AcquireExclusiveNamed(a.Dir, "takeover"); err != nil {
		return
	}
	defer lock.ReleaseExclusiveNamed(a.Dir, "takeover")
	if !lock.HeldByNamed(a.Dir, slot, os.Getpid()) {
		return
	}
	if data, err := json.Marshal(card{Who: a.Who, BudgetSeconds: a.Budget.Seconds(), Now: now}); err == nil {
		fsx.AtomicWriteFile(filepath.Join(a.Dir, slot+".run"), data)
	}
}

// evict frees a slot whose holder is past its budget. One waiting run does
// so at a time, and it frees the slot only while the slot still names the
// holder it judged, so a run that has just taken the slot keeps it.
func (a Admission) evict(name string, judged *lock.Info) bool {
	if _, err := lock.AcquireExclusiveNamed(a.Dir, "takeover"); err != nil {
		return false
	}
	defer lock.ReleaseExclusiveNamed(a.Dir, "takeover")
	holder, err := lock.ReadNamed(a.Dir, name)
	if err != nil || holder.PID != judged.PID || !holder.Acquired.Equal(judged.Acquired) {
		return false
	}
	return os.Remove(filepath.Join(a.Dir, name)) == nil
}

// leave writes a run's card beside its lock file dir/name.
func (a Admission) leave(dir, name string, says card) {
	if data, err := json.Marshal(says); err == nil {
		os.WriteFile(filepath.Join(dir, name+".run"), data, 0o644)
	}
}

// remove gives up the lock file dir/name, and its card with it when this
// process still holds the lock: a run whose turn was taken from it leaves the
// new holder's card alone. The card goes first, while the lock still keeps
// every other run out.
func (a Admission) remove(dir, name string) {
	if lock.HeldByNamed(dir, name, os.Getpid()) {
		os.Remove(filepath.Join(dir, name+".run"))
	}
	lock.ReleaseExclusiveNamed(dir, name)
}

// holder reads who holds the slot name and under what budget. A holder that
// left no card is held to this run's own budget.
func (a Admission) holder(name string) (who string, budget time.Duration) {
	says := readCard(filepath.Join(a.Dir, name))
	if says.BudgetSeconds <= 0 {
		return says.Who, a.Budget
	}
	return says.Who, time.Duration(says.BudgetSeconds * float64(time.Second))
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
	// said so, and Now what such a run says it is doing.
	Budget time.Duration
	Now    string
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
			if entry.IsDir() || strings.HasSuffix(name, ".run") || !keep(name) {
				continue
			}
			if record, err := lock.ReadNamed(dir, name); err == nil && record.Alive() {
				says := readCard(filepath.Join(dir, name))
				runs = append(runs, Standing{Who: says.Who, PID: record.PID, Since: record.Acquired, Budget: time.Duration(says.BudgetSeconds * float64(time.Second)), Now: says.Now})
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
