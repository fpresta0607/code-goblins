package verify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// Admission is how verification runs take turns on one machine. A run holds
// one of a fixed number of slots while its heavy checks run, runs take the
// slots in the order of their places in line (overdueLimit, priorityPlaces),
// and a run starts only once the machine has
// memory to spare. Each run looking at the machine by itself is what let
// four test steps start together on memory each had seen free: a slot is
// taken or it is not.
type Admission struct {
	// Dir holds the slots and, under line, the runs that wait: a lock file
	// for each, naming its process, and beside it a card saying which run
	// that is.
	Dir string
	// Floor may raise the shared minimum available memory, in bytes, a run
	// waits for, and Available reads it. An unavailable reading refuses admission.
	Floor     uint64
	Available func() (uint64, error)
	// Headroom compares physical and commit reserves independently. A caller
	// supplies this or Available, never both. Floor applies to both dimensions.
	Headroom func() (Headroom, error)
	Reserve  Headroom
	// Who names this run to the runs behind it, and Budget is how long its
	// checks may run. Expiry does not release a live or uncertain owner.
	Who    string
	Budget time.Duration
	// Estimate is the most memory, in bytes, this run is expected to use,
	// which the line weighs when it has two slots. Zero is defaultEstimate.
	Estimate uint64
	// Limit bounds the floor wait at the head of the line, and how long a run
	// tries again while the line's lock is busy. Expiry refuses a turn.
	Limit time.Duration
	// LineLimit bounds the whole wait for a turn, behind other runs, for
	// memory and for the line's lock alike, and zero sets none. A run that
	// has waited that long with no turn leaves the line with ErrLineLimit. It
	// is for a run whose caller counts the wait against a budget of its own,
	// as a gate counts it against its agent's invocation.
	LineLimit time.Duration
	// Poll is how often a waiting run looks again.
	Poll time.Duration
	// Waiting, when set, is told that the run waits, as it starts to, whenever
	// its place in line changes, and once a minute: how long so far, and what
	// for.
	Waiting func(waited time.Duration, why string)
	// IsLowPriority has the run wait at the lowest priority: until it is
	// overdue it takes its turn only while no ordinary run waits, and an
	// ordinary run that joins the line after it still goes first. Low-priority
	// runs keep the order they asked in among themselves. A turn it has taken,
	// or is in the moment of taking, is its own until it gives it back.
	IsLowPriority bool
	// IsTestStep says the run is a gate's Test step, which a whole delivery
	// waits on: it goes ahead of every waiting run that is not one, whichever
	// joined first.
	IsTestStep bool
	// Priority is the priority on the backlog row of the task whose run this
	// is, which priorityPlaces orders, and empty for a run with no task.
	Priority string
	// IsLight has the run take a turn of its own beside the slots instead of
	// standing in the line for one (waitLight): a run bounded in memory, by
	// its Estimate, and in time, by its Budget, which its caller ends it at.
	// How many runs hold the slots or wait for them does not hold it back;
	// free memory alone does.
	IsLight bool
	// overdueAfter is overdueLimit, which a test shortens.
	overdueAfter time.Duration
	// passAfter is passLimit, which a test shortens.
	passAfter time.Duration
}

// NoLimit is the Limit of a run that waits for memory for as long as the
// machine is short of it.
const NoLimit = time.Duration(math.MaxInt64)

// ErrLineLimit is why a run that waited its LineLimit with no turn took none.
var ErrLineLimit = errors.New("verify: no turn came within the run's line limit")

// Turn is a run's turn on the machine.
type Turn struct {
	// Waited is how long the run waited for it, or waited before its wait
	// failed.
	Waited time.Duration
	// Note says what was out of the ordinary about how the turn was taken,
	// when anything was, including an admission failure.
	Note  string
	token string
	dir   string
	slot  string
	// release gives the turn back, trying a busy line's lock again for
	// busyLimit.
	release func(ctx context.Context, busyLimit time.Duration) error
	say     func(now string)
	measure func(used uint64)
	// busyLimit is how long a custody change of the turn's tries again while
	// the line's lock is busy: its admission's limit.
	busyLimit time.Duration
}

// TurnVariable is the environment variable that names, to a command a turn
// was taken for, the turn it runs inside. A command that takes turns itself,
// as goblins gate test does behind goblins gate turn, would otherwise wait for the
// turn its own starter holds, for good.
const TurnVariable = "GOBLINS_VERIFY_TURN"

// RunsInTurn says process pid runs inside a turn of the heavy line: its
// environment names one, as it does to everything a run starts, a process
// whose parent has ended too. One whose environment cannot be read names
// none. It reads only that variable, and keeps nothing it read.
func RunsInTurn(pid int) bool {
	environment, err := proc.Environment(pid)
	if err != nil {
		return false
	}
	for _, entry := range environment {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, TurnVariable) && value != "" {
			return true
		}
	}
	return false
}

// Token names the turn for TurnVariable, or is empty for a turn that was
// never taken. A run inside a turn hands on the token it was started with.
func (t Turn) Token() string {
	return t.token
}

// turnToken names slot as held by holder: the slot, the holder's process and
// the moment it took the slot. A slot is taken by creating its record, so no
// two holds of one slot share that moment.
func turnToken(slot string, holder *lock.Info) string {
	return fmt.Sprintf("%s:%d:%d", slot, holder.PID, holder.Acquired.UnixNano())
}

// Release gives the turn's slot back. A run inside a turn takes back what it
// said beside it and leaves the slot with the run that holds it.
func (t Turn) Release() error {
	return t.ReleaseContext(context.Background())
}

func (t Turn) ReleaseContext(ctx context.Context) error {
	ctx, cancel := awake.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if t.release != nil {
		return t.release(ctx, t.busyLimit)
	}
	return nil
}

// Say records what the run is doing now beside its turn, for whoever reads
// the line: a gate shows a step's output only once the step has ended. A run
// that released its turn, or no longer holds its slot's custody, says
// nothing.
func (t Turn) Say(now string) {
	if t.say != nil {
		t.say(now)
	}
}

// Measure records how much memory, in bytes, the run's job commits now,
// beside its turn, so a run that would take the other slot weighs what this
// one may still use. A turn taken inside another's measures nothing, and its
// holder is weighed by its estimate whole.
func (t Turn) Measure(used uint64) {
	if t.measure != nil {
		t.measure(used)
	}
}

// card is what a run says of itself beside its lock file. Now is what a run
// that holds a turn says it is doing.
type card struct {
	Who           string  `json:"who"`
	BudgetSeconds float64 `json:"budget_seconds,omitempty"`
	Now           string  `json:"now,omitempty"`
	// EstimateBytes is the most memory the run is expected to use, and
	// UsedBytes what its job commits now, as it last measured.
	EstimateBytes uint64 `json:"estimate_bytes,omitempty"`
	UsedBytes     uint64 `json:"used_bytes,omitempty"`
	// Priority is the priority of a waiting run's task, and Joined when a run
	// that moved up the line first joined it, which its new lock does not say.
	Priority string    `json:"priority,omitempty"`
	Joined   time.Time `json:"joined,omitzero"`
}

// defaultEstimate is a run's estimate when it names none: the most any run
// of a minute or longer committed in the store's record of 2026-10-14, 2.54
// GiB, rounded to 2.5 GiB.
const defaultEstimate uint64 = 5 << 29

// estimate is the most memory this run is expected to use.
func (a Admission) estimate() uint64 {
	if a.Estimate > 0 {
		return a.Estimate
	}
	return defaultEstimate
}

// weighedFloor is HeadFloor for this run, with its estimate and reserve.
func (a Admission) weighedFloor() (uint64, error) {
	return HeadFloor(a.Dir, a.estimate(), max(a.Reserve.PhysicalAvailable, a.Reserve.CommitAvailable))
}

// headShort says how memory is short of the floor of the run at the head of
// a line of capacity whose flat floor is floor, weighed for a line of two, or
// nothing when it is not.
func (a Admission) headShort(capacity int, floor uint64) (string, error) {
	if capacity == 2 {
		weighed, err := a.weighedFloor()
		if err != nil {
			return "", err
		}
		floor = weighed
	}
	return a.short(max(a.Floor, floor))
}

// HeadFloor is the memory the run at the head of the line kept in dir needs
// free to take a slot, for a run expected to use estimate, zero for the
// default, beside a desktop reserve of reserve. A line of one slot has its
// flat floor. A line of two weighs what each holder may still use, its
// estimate less what it commits now, the light runs beside the slots
// included, then the run's own estimate and the reserve, and never asks less
// than one slot's 4 GiB. The reserve is the one the run's monitor keeps: the
// store's, which reserve may raise and never lower.
func HeadFloor(dir string, estimate, reserve uint64) (uint64, error) {
	capacity, floor, err := AdmissionLimits(dir)
	if err != nil || capacity == 1 {
		return floor, err
	}
	remaining, err := holdersRemaining(dir)
	if err != nil {
		return 0, err
	}
	light, err := lightRemaining(dir)
	if err != nil {
		return 0, err
	}
	remaining += light
	stored, err := DesktopReserve(dir)
	if err != nil {
		return 0, err
	}
	if estimate == 0 {
		estimate = defaultEstimate
	}
	return max(4<<30, remaining+estimate+max(reserve, stored.PhysicalAvailable, stored.CommitAvailable)), nil
}

// holdersRemaining is what the runs holding the slots in dir may still use:
// each one's estimate less what it commits now, never below nothing. A
// holder whose card names no estimate, as an older build's, is weighed at
// defaultEstimate.
func holdersRemaining(dir string) (uint64, error) {
	var remaining uint64
	for slot := 1; slot <= 2; slot++ {
		name := fmt.Sprintf("slot-%d", slot)
		holder, err := lock.ReadNamedStrict(dir, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !holder.Alive() {
			continue
		}
		says := readCard(filepath.Join(dir, name))
		estimate := says.EstimateBytes
		if estimate == 0 {
			estimate = defaultEstimate
		}
		if says.UsedBytes < estimate {
			remaining += estimate - says.UsedBytes
		}
	}
	return remaining, nil
}

// arrivals keeps the places of runs that join the line from one process
// apart.
var arrivals atomic.Int64

// lightPlace begins the name of the lock file a light run holds its turn by,
// which stands in the store beside the slots and never in the line. The rest
// of the name is the moment it took the turn, as a place in line is named, so
// no two light runs share one.
const lightPlace = "light-"

// lightName matches the lock file of a light run's turn, and nothing that
// could reach outside the store.
var lightName = regexp.MustCompile(`^light-[0-9]{20}-[0-9]+-[0-9]+$`)

// lightRemaining is what the light runs holding a turn in dir may still use:
// each one's estimate less what it commits now, never below nothing.
func lightRemaining(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var remaining uint64
	for _, entry := range entries {
		name := entry.Name()
		if !lightName.MatchString(name) {
			continue
		}
		holder, err := lock.ReadNamedStrict(dir, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !holder.Alive() {
			continue
		}
		says := readCard(filepath.Join(dir, name))
		if says.UsedBytes < says.EstimateBytes {
			remaining += says.EstimateBytes - says.UsedBytes
		}
	}
	return remaining, nil
}

// lightFloor is the memory, physical and commit alike, a light run expected
// to use estimate needs free to take its turn in dir beside a desktop reserve
// of reserve: what the slots' holders and the light runs already holding a
// turn may still use, each one's estimate less what it commits now, its own
// estimate and the reserve, the store's, which reserve may raise and never
// lower. A light run admitted on memory a heavy holder is still growing into
// would have that holder's monitor end it at the reserve, and two light runs
// never both take the memory one of them saw free.
func lightFloor(dir string, estimate, reserve uint64) (uint64, error) {
	remaining, err := holdersRemaining(dir)
	if err != nil {
		return 0, err
	}
	light, err := lightRemaining(dir)
	if err != nil {
		return 0, err
	}
	remaining += light
	stored, err := DesktopReserve(dir)
	if err != nil {
		return 0, err
	}
	return remaining + estimate + max(reserve, stored.PhysicalAvailable, stored.CommitAvailable), nil
}

// waitLight is Wait for a light run. It stands in no line and takes no slot:
// as soon as free physical memory and free commit both reach its floor
// (lightFloor) it takes a turn of its own beside the slots, however many runs
// hold them or wait for them. Its wait for memory is bounded by its Limit and
// its LineLimit as a run's at the head of the line is.
func (a Admission) waitLight(ctx context.Context, start time.Time) (Turn, error) {
	var told time.Time
	for {
		if err := ctx.Err(); err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		turn, short, err := a.takeLight()
		switch {
		case errors.Is(err, errCustodyBusy):
			short = "the line's lock is busy"
		case err != nil:
			return Turn{Waited: time.Since(start)}, err
		case turn.release != nil:
			turn.Waited = time.Since(start)
			return turn, nil
		}
		waited := time.Since(start)
		if waited >= a.Limit {
			return Turn{Waited: waited}, fmt.Errorf("verify: memory floor wait expired: %s", short)
		}
		if a.LineLimit > 0 && waited >= a.LineLimit {
			return Turn{Waited: waited}, fmt.Errorf("%w of %s", ErrLineLimit, a.LineLimit)
		}
		if a.Waiting != nil && (told.IsZero() || time.Since(told) >= time.Minute) {
			a.Waiting(waited, "a light run waits beside the line for memory: "+short)
			told = time.Now()
		}
		select {
		case <-ctx.Done():
			return Turn{Waited: time.Since(start)}, ctx.Err()
		case <-time.After(a.Poll):
		}
	}
}

// takeLight takes a light turn when free memory reaches the run's floor, and
// otherwise says how memory is short and takes none. The floor is read and
// the turn taken under the line's lock, which every other light run takes to
// do the same. A light run whose process is verifiably gone leaves its lock
// file behind, which no later run's name reuses, so it is taken away here,
// once its workers' custody has been recovered.
func (a Admission) takeLight() (turn Turn, short string, failure error) {
	unlock, err := custodyMutation(a.Dir)
	if err != nil {
		return Turn{}, "", err
	}
	defer func() { failure = errors.Join(failure, unlock()) }()
	if err := a.checkWorkerCustody(); err != nil {
		return Turn{}, "", err
	}
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return Turn{}, "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !lightName.MatchString(name) {
			continue
		}
		if holder, err := lock.ReadNamedStrict(a.Dir, name); err == nil && !holder.Alive() {
			if err := os.Remove(filepath.Join(a.Dir, name+".run")); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Turn{}, "", err
			}
			if err := os.Remove(filepath.Join(a.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Turn{}, "", err
			}
		}
	}
	floor, err := lightFloor(a.Dir, a.estimate(), max(a.Reserve.PhysicalAvailable, a.Reserve.CommitAvailable))
	if err != nil {
		return Turn{}, "", err
	}
	if short, err := a.short(floor); err != nil || short != "" {
		return Turn{}, short, err
	}
	name := lightPlace + fmt.Sprintf("%020d-%d-%d", time.Now().UTC().UnixNano(), os.Getpid(), arrivals.Add(1))
	owner, err := lock.AcquireExclusiveNamedStrict(a.Dir, name)
	if err != nil {
		return Turn{}, "", err
	}
	says := card{Who: a.Who, BudgetSeconds: a.Budget.Seconds(), EstimateBytes: a.estimate()}
	if err := a.leave(a.Dir, name, says); err != nil {
		lock.ReleaseExclusiveNamed(a.Dir, name)
		return Turn{}, "", err
	}
	return a.held(name, owner, says), "", nil
}

// A run's place in line is a file named for its tier and then the moment it
// joined, so runs of one tier keep the order they asked in. Every build orders
// the line by comparing those names and nothing else, so an older build
// agrees with a newer one on who goes first, and no two runs can each wait for
// the other. From the front of the line:
//
//   - overduePlace: a run that has waited overdueLimit, whatever its tier
//   - testStepPlace: a gate's Test step, then its task's priority as below
//   - production-defect
//   - no tier: priority 0, a row that names no priority or one the line does
//     not know, and a run with no task, which is every run's place before
//     tiers: only a Test step or a production defect passes it
//   - priorities 1, 2, 3 and ops-root, the last of the backlog's rows
//   - lowPriorityPlace
const (
	overduePlace     = "!"
	testStepPlace    = "+"
	lowPriorityPlace = "z-"
)

// priorityPlaces begins the place of a run by its task's priority. The dash
// sorts before a digit, and a letter after one.
var priorityPlaces = map[string]string{
	"production-defect": "-",
	"1":                 "a-",
	"2":                 "b-",
	"3":                 "c-",
	"ops-root":          "d-",
}

// overdueLimit is how long a run waits before no new run may pass it: it moves
// to overduePlace, where only runs that have waited longer stand ahead of it,
// so a run of any tier reaches its turn. Two hours is a gate's 90 minute turn
// budget and half an hour more. A busy morning's waits reached an hour, and a
// limit that short would turn the busy line back into the order runs asked in,
// just when the tiers matter. A limit much longer leaves a goblin idle for
// more of its session than a whole turn of the line is worth.
const overdueLimit = 2 * time.Hour

// passLimit is how long a run that does not fit the memory free may be passed
// by runs that do. A run at the head waits for memory no longer than its
// floor-wait limit, which every caller sets to an hour (gateWaitLimit in
// cmd/goblins), and a run that was passed starts that hour only once nothing
// can pass it, so a turn taken past it that ends within that hour leaves it
// its turn. Its longest wait for a turn is then passLimit and that hour, 80
// minutes, inside overdueLimit. A turn taken past it that holds its slot
// longer than that hour, as a gate turn's 90 minute budget (gateBudget in
// cmd/goblins/gate.go) allows, still uses up its wait, as a holder with more
// than an hour left does for any run at the head.
const passLimit = 20 * time.Minute

// tier begins the run's place in line.
func (a Admission) tier() string {
	switch {
	case a.IsLowPriority:
		return lowPriorityPlace
	case a.IsTestStep:
		return testStepPlace + priorityPlaces[a.Priority]
	}
	return priorityPlaces[a.Priority]
}

// Wait joins the line and returns once the run has its turn: no run that
// stands ahead of it in the line still waits, the machine has the memory, and a slot is free,
// or was left by a process whose recorded identity is verifiably gone. A
// run whose wait fails gets the time it had waited with the error, so the
// wait is on record even though no turn came of it. The wait is measured
// from the moment the run asks for its turn, the custody it takes and the
// line it joins included: a loaded machine stretches those past a second,
// and a wait measured from after them says a run waited far less than it did.
// If cancellation races admission and release fails, the returned turn still
// owns that slot. Its caller must retain and retry Release despite the error.
func (a Admission) Wait(ctx context.Context) (Turn, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return Turn{}, err
	}
	if os.Getenv("GOBLINS_VERIFY_SLOTS") != "" {
		return Turn{}, errors.New("verify: GOBLINS_VERIFY_SLOTS is unsupported; capacity is shared in the canonical admission store")
	}
	var err error
	a.Dir, err = admissionDir(a.Dir, isTestBinary())
	if err != nil {
		return Turn{}, err
	}
	if _, _, err := AdmissionLimits(a.Dir); err != nil {
		return Turn{}, err
	}
	if a.Available == nil && a.Headroom == nil {
		return Turn{}, errors.New("verify: available memory cannot be read")
	}
	if a.Available != nil && a.Headroom != nil {
		return Turn{}, errors.New("verify: admission requires one memory reader")
	}
	if a.IsLight && (a.Estimate == 0 || a.Budget <= 0) {
		return Turn{}, errors.New("verify: a light run names the memory it is expected to use and the bound it is ended at")
	}
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return Turn{}, err
	}
	// A run with a line limit tries a busy lock again no longer than that
	// limit, which bounds its whole wait, this first look at the line included.
	busyLimit := a.Limit
	if a.LineLimit > 0 {
		busyLimit = min(busyLimit, a.LineLimit)
	}
	unlock, err := custodyMutationWhileBusy(ctx, a.Dir, busyLimit)
	if errors.Is(err, errCustodyBusy) && a.LineLimit > 0 && time.Since(start) >= a.LineLimit {
		return Turn{Waited: time.Since(start)}, fmt.Errorf("%w of %s: %v", ErrLineLimit, a.LineLimit, err)
	}
	if err != nil {
		return Turn{}, err
	}
	custodyError := a.checkWorkerCustody()
	custodyError = errors.Join(custodyError, unlock())
	if custodyError != nil {
		return Turn{}, custodyError
	}
	if turn, isInside := a.inside(); isInside {
		return turn, nil
	}
	if a.IsLight {
		return a.waitLight(ctx, start)
	}
	line := filepath.Join(a.Dir, "line")
	if err := os.MkdirAll(line, 0o755); err != nil {
		return Turn{}, err
	}
	joined := time.Now()
	arrival := fmt.Sprintf("%020d-%d-%d", joined.UTC().UnixNano(), os.Getpid(), arrivals.Add(1))
	place := a.tier() + arrival
	owner, err := lock.AcquireExclusiveNamedStrict(line, place)
	if err != nil {
		return Turn{}, err
	}
	defer func() { a.remove(line, place, owner) }()
	says := card{Who: a.Who, EstimateBytes: a.estimate(), Priority: a.Priority}
	if err := a.leave(line, place, says); err != nil {
		return Turn{}, err
	}

	var told time.Time
	var toldAhead int
	// shortSince is when this run, at the head of the line, first found memory
	// under the floor, and zero while it is not the head or memory is not
	// short: the limit is on the wait for memory, not on the time in line. A
	// run that was passed waits for memory from when nothing can pass it.
	var shortSince time.Time
	// seen is the live holders of the slots this run has seen while at the
	// head of the line and short, and nil while it is not. A holder it has not
	// seen there took its turn past this run, which isPassed records.
	var seen map[lock.Info]bool
	isPassed := false
	// busySince is when this run, at the head of the line, first found the
	// line's lock busy as it took its turn, and zero while it is not.
	var busySince time.Time
	isToldBusy := false
	for {
		if err := ctx.Err(); err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		capacity, floor, err := AdmissionLimits(a.Dir)
		if err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		waited := time.Since(start)
		if waited >= cmp.Or(a.overdueAfter, overdueLimit) && !strings.HasPrefix(place, overduePlace) {
			moved, err := a.moveToFront(line, overduePlace+arrival, place, owner, says)
			if err != nil {
				return Turn{Waited: time.Since(start)}, err
			}
			place, owner = overduePlace+arrival, moved
		}
		passed, err := a.passing(line, capacity, floor)
		if err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		ahead, err := inFront(line, place, passed)
		if err != nil {
			return Turn{Waited: time.Since(start)}, err
		}
		why := a.heldBy()
		short := ""
		if ahead == 0 {
			short, err = a.headShort(capacity, floor)
			if err != nil {
				return Turn{Waited: time.Since(start)}, err
			}
		}
		if short == "" {
			shortSince, seen = time.Time{}, nil
		} else {
			if shortSince.IsZero() {
				shortSince = time.Now()
			}
			if capacity == 2 {
				isFirstLook := seen == nil
				if isFirstLook {
					seen = map[lock.Info]bool{}
				}
				for _, name := range []string{"slot-1", "slot-2"} {
					holder, err := lock.ReadNamedStrict(a.Dir, name)
					if errors.Is(err, os.ErrNotExist) {
						continue
					}
					if err != nil {
						return Turn{Waited: time.Since(start)}, err
					}
					if !holder.Alive() || seen[*holder] {
						continue
					}
					seen[*holder] = true
					if !isFirstLook {
						isPassed = true
					}
				}
			}
			if unpassable := joined.Add(cmp.Or(a.passAfter, passLimit)); isPassed && unpassable.After(shortSince) {
				shortSince = unpassable
			}
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
				turn, takenShort, err := a.take(capacity, floor)
				// A take that found the line's lock busy took no slot and
				// wrote no card, so the run keeps its place and tries again,
				// within its limit.
				if !errors.Is(err, errCustodyBusy) {
					busySince = time.Time{}
				} else if busySince.IsZero() {
					busySince = time.Now()
				}
				if errors.Is(err, errCustodyBusy) && time.Since(busySince) < a.Limit {
					why = join(why, "the line's lock is busy")
				} else if err != nil {
					turn.Waited = time.Since(start)
					return turn, err
				} else if takenShort != "" {
					why = join(why, takenShort)
				} else if turn.release != nil {
					if err := ctx.Err(); err != nil {
						if releaseError := turn.Release(); releaseError != nil {
							turn.Waited = time.Since(start)
							return turn, errors.Join(err, releaseError)
						}
						return Turn{Waited: time.Since(start)}, err
					}
					turn.Waited = time.Since(start)
					return turn, nil
				}
			}
		}
		// The run has just found no turn to take, so a limit that has passed
		// ends its wait here and never a turn it could have had.
		if a.LineLimit > 0 && time.Since(start) >= a.LineLimit {
			return Turn{Waited: time.Since(start)}, fmt.Errorf("%w of %s", ErrLineLimit, a.LineLimit)
		}
		isBusy := !busySince.IsZero()
		if a.Waiting != nil && (told.IsZero() || ahead != toldAhead || isBusy != isToldBusy || time.Since(told) >= time.Minute) {
			a.Waiting(waited, join(why, standing(ahead)))
			told, toldAhead, isToldBusy = time.Now(), ahead, isBusy
		}
		select {
		case <-ctx.Done():
			return Turn{Waited: time.Since(start)}, ctx.Err()
		case <-time.After(a.Poll):
		}
	}
}

// moveToFront gives a run that has waited the line's limit the place front and
// takes it out of place, so the name of its place stays all that orders it.
// For that moment it stands in both, the old one behind the new. A failure
// ends its wait, as any failure of the line's folder does.
func (a Admission) moveToFront(line, front, place string, owner *lock.Info, says card) (*lock.Info, error) {
	moved, err := lock.AcquireExclusiveNamedStrict(line, front)
	if err != nil {
		return nil, err
	}
	says.Joined = owner.Acquired
	if err = a.leave(line, front, says); err == nil {
		err = a.remove(line, place, owner)
	}
	if err != nil {
		return nil, errors.Join(err, a.remove(line, front, moved))
	}
	return moved, nil
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

// passing says which waiting runs this run goes past, for inFront: in a line
// of two slots, while this run fits the memory free, a run ahead of it that
// does not fit for its own expected memory holds it back no longer, unless
// that run stands at overduePlace or has stood passLimit in line, as the join
// time its place carries says. A run that was passed starts its floor wait
// only once nothing can pass it, so turns taken past it that end within its
// floor-wait limit leave it its turn. A place whose join time cannot be read
// is not passed.
// On 2026-10-29 a
// waiter expecting 8 GB with 7.1 GB free held eight runs that fit for 28
// minutes while the turn sat free (#1743). A line of one slot asks every run
// the same floor, so no run there fits where the one ahead does not, and it
// passes none.
func (a Admission) passing(line string, capacity int, floor uint64) (func(name string) (bool, error), error) {
	if capacity != 2 {
		return nil, nil
	}
	short, err := a.headShort(capacity, floor)
	if err != nil || short != "" {
		return nil, err
	}
	return func(name string) (bool, error) {
		if strings.HasPrefix(name, overduePlace) {
			return false, nil
		}
		at := strings.IndexAny(name, "0123456789")
		if at < 0 || len(name) < at+20 {
			return false, nil
		}
		joined, err := strconv.ParseInt(name[at:at+20], 10, 64)
		if err != nil || time.Since(time.Unix(0, joined)) >= cmp.Or(a.passAfter, passLimit) {
			return false, nil
		}
		ahead := a
		ahead.Estimate = readCard(filepath.Join(line, name)).EstimateBytes
		short, err := ahead.headShort(capacity, floor)
		return short != "", err
	}, nil
}

// inFront counts the runs whose places come before place and still wait,
// leaving out each one passed says this run goes past, when it is given. A
// run whose process is gone is taken out of the line.
func inFront(line, place string, passed func(name string) (bool, error)) (int, error) {
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
		if err == nil && passed != nil {
			isPassed, err := passed(name)
			if err != nil {
				return 0, err
			}
			if isPassed {
				continue
			}
		}
		ahead++
	}
	return ahead, nil
}

// short says how the machine is short of memory, or nothing when it is not.
func (a Admission) short(floor uint64) (string, error) {
	if a.Headroom != nil {
		memory, err := a.Headroom()
		if err != nil {
			return "", fmt.Errorf("verify: machine headroom cannot be read: %w", err)
		}
		physical := max(floor, a.Reserve.PhysicalAvailable)
		commit := max(floor, a.Reserve.CommitAvailable)
		if memory.PhysicalAvailable >= physical && memory.CommitAvailable >= commit {
			return "", nil
		}
		// The tighter of free memory and free commit that is short, as a run
		// without a headroom reader says it.
		available, required := memory.PhysicalAvailable, physical
		if memory.PhysicalAvailable >= physical || memory.CommitAvailable < commit && memory.CommitAvailable < memory.PhysicalAvailable {
			available, required = memory.CommitAvailable, commit
		}
		return fmt.Sprintf("%.1f GB of memory is available and the floor is %.1f GB", Gigabytes(available), Gigabytes(required)), nil
	}
	available, err := a.Available()
	if err != nil {
		return "", fmt.Errorf("verify: available memory cannot be read: %w", err)
	}
	if available >= floor {
		return "", nil
	}
	return fmt.Sprintf("%.1f GB of memory is available and the floor is %.1f GB", Gigabytes(available), Gigabytes(floor)), nil
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

// inside returns the turn this run was started inside, a slot's or a light
// run's, when its environment names one and the process that took it holds
// it still. Such a run takes no
// slot and gives none back: what it says goes beside the holder's turn, and
// its release takes that back. A token that names no held turn, as one a
// process inherited from a run that has ended, is no turn, and the run joins
// the line.
func (a Admission) inside() (Turn, bool) {
	token := os.Getenv(TurnVariable)
	name, _, _ := strings.Cut(token, ":")
	if name != "slot-1" && name != "slot-2" && !lightName.MatchString(name) {
		return Turn{}, false
	}
	if !a.isHeldAs(name, token) {
		return Turn{}, false
	}
	var mu sync.Mutex
	isReleased := false
	say := func(now string) {
		mu.Lock()
		defer mu.Unlock()
		if !isReleased {
			a.sayInside(name, token, now)
		}
	}
	release := func(ctx context.Context, _ time.Duration) error {
		if err := lockBefore(ctx, &mu); err != nil {
			return err
		}
		defer mu.Unlock()
		if !isReleased {
			if err := a.sayInsideContext(ctx, name, token, ""); err != nil {
				return err
			}
		}
		isReleased = true
		return nil
	}
	return Turn{token: token, dir: a.Dir, slot: name, release: release, say: say, busyLimit: a.Limit}, true
}

// isHeldAs reports whether the slot name is held, by a process that is
// verifiably running, under exactly token.
func (a Admission) isHeldAs(name, token string) bool {
	holder, err := lock.ReadNamedStrict(a.Dir, name)
	return err == nil && turnToken(name, holder) == token && holder.VerifiedAlive()
}

// sayInside rewrites what the card of the slot name says its run is doing
// now, while the slot is still held under token, and keeps who holds it and
// under what budget: those are the holder's.
func (a Admission) sayInside(name, token, now string) {
	_ = a.sayInsideContext(context.Background(), name, token, now)
}

func (a Admission) sayInsideContext(ctx context.Context, name, token, now string) error {
	holder, err := lock.ReadNamedStrictContext(ctx, a.Dir, name)
	if err != nil {
		return err
	}
	if turnToken(name, holder) != token || !holder.VerifiedAlive() {
		return errors.New("verify: nested turn ownership changed")
	}
	var says card
	data, err := fsx.ReadFileContext(ctx, filepath.Join(a.Dir, name+".run"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &says); err != nil {
		return err
	}
	says.Now = now
	data, err = json.Marshal(says)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFileContext(ctx, filepath.Join(a.Dir, name+".run"), data)
}

// take takes a free slot or reclaims verifiably dead custody, and returns a
// turn with no release when no slot is free. The floor of the run at the head
// of a line of capacity whose flat floor is floor is read again under the
// line's lock, as a light run reads its own, so a light run admitted since the
// head last read memory leaves it short, said in short, and takes no slot. A
// live or uncertain holder keeps the slot regardless of its elapsed budget.
// Once release has run, say writes nothing, so a progress note can never land
// beside a slot the run has given back.
func (a Admission) take(capacity int, floor uint64) (turn Turn, short string, failure error) {
	unlock, err := custodyMutation(a.Dir)
	if err != nil {
		return Turn{}, "", err
	}
	defer func() { failure = errors.Join(failure, unlock()) }()
	if err := a.checkWorkerCustody(); err != nil {
		return Turn{}, "", err
	}
	if short, err := a.headShort(capacity, floor); err != nil || short != "" {
		return Turn{}, short, err
	}
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return Turn{}, "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "slot-") && !strings.HasSuffix(name, ".run") && name != "slot-1" && (capacity != 2 || name != "slot-2") {
			return Turn{}, "", fmt.Errorf("verify: custody outside shared capacity %d: %s", capacity, name)
		}
	}
	// Inspect every configured slot before choosing a free one: uncertain
	// custody in another slot cannot be bypassed by an earlier empty slot.
	for slot := 1; slot <= capacity; slot++ {
		holder, err := lock.ReadNamedStrict(a.Dir, fmt.Sprintf("slot-%d", slot))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Turn{}, "", err
		}
		if holder.Alive() && !holder.VerifiedAlive() {
			return Turn{}, "", nil
		}
	}
	for slot := 1; slot <= capacity; slot++ {
		name := fmt.Sprintf("slot-%d", slot)
		owner, err := lock.AcquireExclusiveNamedStrict(a.Dir, name)
		if errors.Is(err, lock.ErrHeld) {
			continue
		}
		if err != nil {
			return Turn{}, "", err
		}
		says := card{Who: a.Who, BudgetSeconds: a.Budget.Seconds(), EstimateBytes: a.estimate()}
		if err := a.leave(a.Dir, name, says); err != nil {
			lock.ReleaseExclusiveNamed(a.Dir, name)
			return Turn{}, "", err
		}
		return a.held(name, owner, says), "", nil
	}
	return Turn{}, "", nil
}

// held is the turn of the lock file name in the store, a slot's or a light
// run's, that owner has just taken and written the card says beside. Once
// release has run, say writes nothing, so a progress note can never land
// beside a turn the run has given back.
func (a Admission) held(name string, owner *lock.Info, says card) Turn {
	var mu sync.Mutex
	isReleased := false
	release := func(ctx context.Context, busyLimit time.Duration) (failure error) {
		if err := lockBefore(ctx, &mu); err != nil {
			return err
		}
		defer mu.Unlock()
		// A run that took its turn gives it back through a busy moment
		// of the line's lock, as it took it.
		unlock, err := custodyReleaseWhileBusy(ctx, a.Dir, busyLimit)
		if err != nil {
			return err
		}
		defer func() { failure = errors.Join(failure, unlock()) }()
		if !isReleased {
			guards, err := a.workerCustodyContext(ctx)
			if err != nil {
				return err
			}
			// A nested controller that ended inside this turn leaves its
			// custody to be recovered before the slot can go back.
			var abandoned []Custody
			for _, guard := range guards {
				if guard.Token != turnToken(name, owner) {
					continue
				}
				if !guard.isAbandoned() {
					return fmt.Errorf("verify: execution %s still holds worker custody", guard.RunID)
				}
				abandoned = append(abandoned, guard)
			}
			if len(abandoned) > 0 {
				if err := recoverAbandoned(ctx, abandoned); err != nil {
					return err
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := a.removeContext(ctx, a.Dir, name, owner); err != nil {
				return err
			}
			isReleased = true
		}
		return nil
	}
	// says is what the card says now. A use within 64 MiB of the one on
	// the card is not written again, so a run measured every second is
	// not a file write every second.
	say := func(now string) {
		mu.Lock()
		defer mu.Unlock()
		if !isReleased {
			says.Now = now
			a.say(name, owner, says)
		}
	}
	measure := func(used uint64) {
		mu.Lock()
		defer mu.Unlock()
		if !isReleased && max(used, says.UsedBytes)-min(used, says.UsedBytes) >= 64<<20 {
			says.UsedBytes = used
			a.say(name, owner, says)
		}
	}
	return Turn{token: turnToken(name, owner), dir: a.Dir, slot: name, release: release, say: say, measure: measure, busyLimit: a.Limit}
}

// say rewrites the card of the slot this run holds with says, while the
// slot's custody is still owner's. The card is replaced whole, so a run
// reading it never finds it half written and takes its holder for one that
// named no budget or estimate.
func (a Admission) say(name string, owner *lock.Info, says card) {
	current, err := lock.ReadNamedStrict(a.Dir, name)
	if err != nil || *current != *owner || !current.VerifiedAlive() {
		return
	}
	if data, err := json.Marshal(says); err == nil {
		fsx.AtomicWriteFile(filepath.Join(a.Dir, name+".run"), data)
	}
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
func (a Admission) remove(dir, name string, owner *lock.Info) error {
	return a.removeContext(context.Background(), dir, name, owner)
}

func (a Admission) removeContext(ctx context.Context, dir, name string, owner *lock.Info) error {
	current, err := lock.ReadNamedStrictContext(ctx, dir, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if *current != *owner || !current.VerifiedAlive() {
		return errors.New("verify: turn ownership changed before release")
	}
	if err := os.Remove(filepath.Join(dir, name+".run")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return lock.ReleaseExclusiveOwned(ctx, dir, name, *owner)
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
	// IsLowPriority says a waiting run waits at the lowest priority.
	IsLowPriority bool
	// IsLight says a run holds a light turn beside the slots, not a slot.
	IsLight bool
	// Estimate is the most memory it is expected to use, and Used what it
	// commits now, as a run that holds a turn last measured it.
	Estimate uint64
	Used     uint64
	// Behind says why a waiting run stands behind the one before it in the
	// line, and is empty for the first.
	Behind string
	// place and priority are what Behind is read from.
	place    string
	priority string
}

// behind says why the waiting run back stands behind front, the run before it
// in the line.
func behind(front, back Standing) string {
	isFrontOverdue := strings.HasPrefix(front.place, overduePlace)
	isBackOverdue := strings.HasPrefix(back.place, overduePlace)
	switch {
	case isFrontOverdue && !isBackOverdue:
		return fmt.Sprintf("it has waited past the line's limit of %s", overdueLimit)
	case front.IsLowPriority != back.IsLowPriority:
		return "any other run goes ahead of one at the lowest priority"
	case strings.HasPrefix(front.place, testStepPlace) != strings.HasPrefix(back.place, testStepPlace):
		return "a gate's Test step goes ahead of a goblin's own turn"
	case !isFrontOverdue && !front.IsLowPriority && priorityPlaces[front.priority] != priorityPlaces[back.priority]:
		return priorityName(front.priority) + " goes ahead of " + priorityName(back.priority)
	}
	return "it asked first"
}

// priorityName names a priority in a sentence.
func priorityName(priority string) string {
	if priority == "" {
		return "no task priority"
	}
	return "task priority " + priority
}

// Line reads who holds the turns kept in dir, the light runs beside the
// slots and then slot by slot, and who waits, in the order they take their
// turns. A run whose process is gone is left out.
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
				since := record.Acquired
				if !says.Joined.IsZero() {
					since = says.Joined
				}
				runs = append(runs, Standing{Who: says.Who, PID: record.PID, Since: since, Budget: time.Duration(says.BudgetSeconds * float64(time.Second)), Now: says.Now, IsLowPriority: strings.HasPrefix(name, lowPriorityPlace), IsLight: lightName.MatchString(name), Estimate: says.EstimateBytes, Used: says.UsedBytes, place: name, priority: says.Priority})
			}
		}
		return runs, nil
	}
	if holding, err = read(dir, func(name string) bool { return strings.HasPrefix(name, "slot-") || lightName.MatchString(name) }); err != nil {
		return nil, nil, err
	}
	if waiting, err = read(filepath.Join(dir, "line"), func(string) bool { return true }); err != nil {
		return nil, nil, err
	}
	for place := 1; place < len(waiting); place++ {
		waiting[place].Behind = behind(waiting[place-1], waiting[place])
	}
	return holding, waiting, nil
}
