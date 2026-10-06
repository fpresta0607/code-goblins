package verify

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

const gigabyte = 1 << 30

// TestMain lets the test binary stand in for another run on the machine:
// started with VERIFY_TEST_HOLD_DIR it takes a turn there under a budget of
// 200ms, says so, and holds the turn until its standard input closes, as a
// run that hangs would.
func TestMain(m *testing.M) {
	dir := os.Getenv("VERIFY_TEST_HOLD_DIR")
	if dir == "" {
		os.Exit(m.Run())
	}
	holder := Admission{Dir: dir, Floor: 4 * gigabyte, Available: func() (uint64, error) { return 16 * gigabyte, nil }, Who: "a run that hangs", Budget: 200 * time.Millisecond, Limit: time.Minute, Poll: 5 * time.Millisecond}
	turn, err := holder.Wait(context.Background())
	if err != nil {
		fmt.Println("failed:", err)
		os.Exit(1)
	}
	fmt.Println("holding")
	bufio.NewReader(os.Stdin).ReadString('\n')
	turn.Release()
}

// admission takes turns in a folder of the test's own, on a machine with
// plenty of memory, under a budget no test reaches by accident.
func admission(t *testing.T) Admission {
	t.Helper()
	return Admission{
		Dir:       t.TempDir(),
		Floor:     4 * gigabyte,
		Available: func() (uint64, error) { return 16 * gigabyte, nil },
		Who:       "a test's run",
		Budget:    90 * time.Minute,
		Limit:     time.Minute,
		Poll:      5 * time.Millisecond,
	}
}

// take waits for a turn in the background and delivers it. A run still
// waiting when the test ends is cancelled there, before its folder goes.
func take(t *testing.T, a Admission) <-chan Turn {
	t.Helper()
	taken := make(chan Turn, 1)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	go func() {
		defer close(stopped)
		turn, err := a.Wait(ctx)
		if err != nil {
			if ctx.Err() == nil {
				t.Error(err)
			}
			return
		}
		taken <- turn
	}()
	return taken
}

func within(t *testing.T, taken <-chan Turn, what string) Turn {
	t.Helper()
	select {
	case turn := <-taken:
		return turn
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: no turn within 10 seconds", what)
		return Turn{}
	}
}

func notYet(t *testing.T, taken <-chan Turn, what string) {
	t.Helper()
	select {
	case <-taken:
		t.Fatalf("%s took a turn", what)
	case <-time.After(150 * time.Millisecond):
	}
}

// said collects what a waiting run says it waits for.
type said struct {
	mu    sync.Mutex
	lines []string
}

func (s *said) waiting(_ time.Duration, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, why)
}

// atLeast waits until the run has said count lines, for at most 10 seconds,
// and returns what it has said by then.
func (s *said) atLeast(t *testing.T, count int) []string {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		lines := append([]string(nil), s.lines...)
		s.mu.Unlock()
		if len(lines) >= count || time.Now().After(deadline) {
			return lines
		}
	}
}

// waits blocks until the run that delivers its turn on taken says that it
// waits, and returns what it said. A run that takes a turn instead fails the
// test.
func (s *said) waits(t *testing.T, taken <-chan Turn, what string) string {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		select {
		case <-taken:
			t.Fatalf("%s took a turn", what)
		default:
		}
		s.mu.Lock()
		if len(s.lines) > 0 {
			defer s.mu.Unlock()
			return s.lines[0]
		}
		s.mu.Unlock()
	}
	t.Fatalf("%s neither waited nor took a turn within 10 seconds", what)
	return ""
}

// With the slot held a run waits, and it takes the slot a finished run gives
// back.
func TestARunWaitsForAFreeSlotAndTakesTheOneGivenBack(t *testing.T) {
	// Arrange
	a := admission(t)
	first := within(t, take(t, a), "the first run")
	var waiter said
	a.Waiting = waiter.waiting

	// Act
	second := take(t, a)
	waiter.waits(t, second, "a second run, while the first held the slot,")
	waiting := time.Now()

	// Assert
	notYet(t, second, "a second run, while the first held the slot,")
	heldBack := time.Since(waiting)
	first.Release()
	turn := within(t, second, "the second run, after the first gave its slot back")
	defer turn.Release()
	if turn.Waited < heldBack || turn.Note != "" {
		t.Errorf("the second run's turn is %+v; want a wait of at least the %s it was held back and nothing to note", turn, heldBack)
	}
}

// Six runs asking at once fill the slots and no more: the next run in line is
// turned away with exactly as many turns held as there are slots, and every
// one of the six gets its turn as the holders leave.
func TestRunsAskingAtOnceNeverHoldMoreTurnsThanThereAreSlots(t *testing.T) {
	// Arrange
	a := admission(t)
	// A run says it is next in line only once it has tried to take a turn and
	// found none it could take, so the store then shows every turn there is
	// to hold, with no wait for the clock.
	turnedAway := make(chan struct{}, 1)
	a.Waiting = func(_ time.Duration, why string) {
		if strings.HasSuffix(why, "this run is next in line") {
			select {
			case turnedAway <- struct{}{}:
			default:
			}
		}
	}
	var finished atomic.Int32
	leave := make(chan struct{})
	var runs sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	for range 6 {
		runs.Add(1)
		go func() {
			defer runs.Done()
			turn, err := a.Wait(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			// A holder keeps its turn until the test lets it go.
			select {
			case <-leave:
			case <-ctx.Done():
			}
			turn.Release()
			finished.Add(1)
		}()
	}
	select {
	case <-turnedAway:
	case <-ctx.Done():
		t.Error("no run came to the head of the line and was turned away within 30 seconds")
	}
	holding, _, err := Line(a.Dir)
	for range 6 {
		select {
		case leave <- struct{}{}:
		case <-ctx.Done():
		}
	}
	runs.Wait()

	// Assert
	if err != nil || len(holding) != 1 || finished.Load() != 6 {
		t.Errorf("%d run(s) held a turn (%v) when the next in line was turned away, and %d of 6 had one in the end; want 1 and all 6", len(holding), err, finished.Load())
	}
}

// Runs take their turns in the order they asked: the one that has waited
// longest goes next, and the one behind it is told a run is ahead of it.
func TestRunsTakeTheirTurnsInTheOrderTheyAsked(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	var early, late said
	a.Waiting = early.waiting
	earlier := take(t, a)
	earlyLine := early.waits(t, earlier, "the run that asked first, while the holder had the turn,")
	a.Waiting = late.waiting
	later := take(t, a)
	lateLine := late.waits(t, later, "the run that asked second, while the holder had the turn,")

	// Act
	holder.Release()

	// Assert
	turn := within(t, earlier, "the run that asked first")
	notYet(t, later, "the run that asked second, while the first held the turn,")
	turn.Release()
	within(t, later, "the run that asked second").Release()
	if !strings.HasSuffix(earlyLine, "; this run is next in line") || !strings.HasSuffix(lateLine, "; 1 run is ahead of this one in line") {
		t.Errorf("the first said %q and the second %q; want the first next in line and the second with 1 run ahead", earlyLine, lateLine)
	}
}

// A waiting run says so again when its place in line changes, so its output
// shows it moving up: one run ahead of it, then next in line.
func TestAWaitingRunSaysWhenItsPlaceInLineChanges(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	var early, late said
	a.Waiting = early.waiting
	earlier := take(t, a)
	early.waits(t, earlier, "the run that asked first, while the holder had the turn,")
	a.Waiting = late.waiting
	later := take(t, a)
	late.waits(t, later, "the run that asked second, while the holder had the turn,")

	// Act
	holder.Release()
	turn := within(t, earlier, "the run that asked first")
	defer turn.Release()

	// Assert
	lines := late.atLeast(t, 2)
	if len(lines) < 2 || !strings.HasSuffix(lines[0], "; 1 run is ahead of this one in line") || !strings.HasSuffix(lines[1], "; this run is next in line") {
		t.Errorf("the run that asked second said %q; want it one run behind, then next in line", lines)
	}
}

// A waiting run says which run holds the turn, for how long it has, and
// against what budget, so whoever reads the line can see what it is behind.
func TestAWaitingRunSaysWhoHoldsTheTurnAndForHowLong(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Who = "code-goblins at 0123abcd, affected level"
	holder := within(t, take(t, a), "the holder")
	defer holder.Release()
	var waiter said
	a.Who, a.Waiting = "another run", waiter.waiting

	// Act
	line := waiter.waits(t, take(t, a), "a run behind the holder")

	// Assert
	prefix := fmt.Sprintf("the turn is held by code-goblins at 0123abcd, affected level (pid %d), for ", os.Getpid())
	if suffix := " of its 1h30m0s budget; this run is next in line"; !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		t.Errorf("the waiting run said %q; want %q, how long, then %q", line, prefix, suffix)
	}
}

// A run waits while the machine's available memory is under the floor, by
// however little, says what it waits for with the memory rounded down, so
// that a byte under the floor never reads as the floor, and starts once the
// memory is there.
func TestARunWaitsWhileMemoryIsUnderTheFloor(t *testing.T) {
	// Arrange
	a := admission(t)
	var available atomic.Uint64
	available.Store(4*gigabyte - 1)
	a.Available = func() (uint64, error) { return available.Load(), nil }
	var waiter said
	a.Waiting = waiter.waiting

	// Act
	waiting := take(t, a)
	line := waiter.waits(t, waiting, "a run with a byte less memory available than the 4 GB floor")

	// Assert
	notYet(t, waiting, "a run with a byte less memory available than the 4 GB floor")
	if want := "3.9 GB of memory is available and the floor is 4.0 GB; this run is next in line"; line != want {
		t.Errorf("the waiting run said %q; want %q", line, want)
	}
	available.Store(6 * gigabyte)
	turn := within(t, waiting, "the run, once memory was above the floor")
	defer turn.Release()
	if turn.Note != "" {
		t.Errorf("the turn notes %q; want nothing, as the memory came", turn.Note)
	}
}

// A run that is next in line behind a holder, on a machine short of memory,
// waits for both and says both: who holds the turn, and what memory there is.
func TestAWaitingRunSaysBothWhoHoldsTheTurnAndThatMemoryIsShort(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Who = "the holder"
	holder := within(t, take(t, a), "the holder")
	defer holder.Release()
	var waiter said
	a.Who, a.Waiting = "another run", waiter.waiting
	a.Available = func() (uint64, error) { return 1 * gigabyte, nil }

	// Act
	line := waiter.waits(t, take(t, a), "a run behind a holder, on a machine short of memory,")

	// Assert
	prefix := fmt.Sprintf("the turn is held by the holder (pid %d), for ", os.Getpid())
	if suffix := " of its 1h30m0s budget; 1.0 GB of memory is available and the floor is 4.0 GB; this run is next in line"; !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		t.Errorf("the waiting run said %q; want %q, how long, then %q", line, prefix, suffix)
	}
}

// An expired floor wait refuses admission and leaves the next run its place.
func TestARunRefusesTheFloorAfterItsWaitLimit(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Available = func() (uint64, error) { return 1 * gigabyte, nil }
	a.Limit = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	turn, err := a.Wait(ctx)

	// Assert
	defer turn.Release()
	if err == nil || turn.release != nil || turn.Waited < a.Limit {
		t.Fatalf("expired floor returned %+v, %v; want no turn and the recorded wait", turn, err)
	}
	a.Available = func() (uint64, error) { return 16 * gigabyte, nil }
	within(t, take(t, a), "the next run, after the expired waiter left").Release()
}

// The limit is on the wait for memory, not on the time in line: a run that
// stood behind a holder for longer than its limit, and finds memory short as
// the turn comes free, still waits its limit for memory before refusing it.
func TestARunThatStoodInLinePastItsLimitStillWaitsForMemory(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	var available atomic.Uint64
	available.Store(16 * gigabyte)
	// readSince counts the readings that began after the shortage started,
	// so the holder leaves only once the waiter has seen it: the run reads
	// memory and then tries for the turn, and a reading taken a moment before
	// the shortage, with the turn freed in that moment, rightly lets it run.
	var short atomic.Bool
	var readSince atomic.Int64
	a.Available = func() (uint64, error) {
		began := short.Load()
		value := available.Load()
		if began {
			readSince.Add(1)
		}
		return value, nil
	}
	a.Limit = 500 * time.Millisecond
	var waiter said
	a.Waiting = waiter.waiting
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ended := make(chan struct {
		turn Turn
		err  error
	}, 1)
	go func() {
		turn, err := a.Wait(ctx)
		ended <- struct {
			turn Turn
			err  error
		}{turn, err}
	}()
	if len(waiter.atLeast(t, 1)) == 0 {
		t.Fatal("the waiter never joined the line")
	}
	time.Sleep(600 * time.Millisecond)

	// Act
	shortAt := time.Now()
	available.Store(1 * gigabyte)
	short.Store(true)
	for readSince.Load() == 0 {
		if time.Since(shortAt) > 5*time.Second {
			t.Fatal("the waiter never read memory once it was short")
		}
		time.Sleep(time.Millisecond)
	}
	holder.Release()
	got := <-ended
	forMemory := time.Since(shortAt)

	// Assert
	defer got.turn.Release()
	if got.err == nil || got.turn.release != nil || forMemory < a.Limit || ctx.Err() != nil || got.turn.Waited < forMemory {
		t.Errorf("floor wait returned %+v, %v after %s; want refusal only after %s of floor waiting", got.turn, got.err, forMemory, a.Limit)
	}
}

// The note says how the turn was taken, not what the run saw while it waited:
// a run short of memory behind a holder, which takes its turn only
// once memory is back and the holder has left, has nothing to note.
func TestARunThatTakesItsTurnWithMemoryBackNotesNoShortage(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	var available atomic.Uint64
	available.Store(1 * gigabyte)
	var readings atomic.Int64
	a.Available = func() (uint64, error) {
		readings.Add(1)
		return available.Load(), nil
	}
	a.Limit = time.Second
	var waiter said
	a.Waiting = waiter.waiting
	waiting := take(t, a)
	waiter.waits(t, waiting, "a run short of memory behind the holder")
	time.Sleep(200 * time.Millisecond)
	notYet(t, waiting, "a run short of memory, while the holder had the turn,")

	// Act
	available.Store(16 * gigabyte)
	// The holder leaves only once the run has read the memory that is back,
	// so no pass that still saw it short can take the turn.
	for seen, deadline := readings.Load(), time.Now().Add(10*time.Second); readings.Load() == seen; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the waiting run did not read the memory again within 10 seconds")
		}
	}
	holder.Release()
	turn := within(t, waiting, "the run, once memory was back and the holder had left")

	// Assert
	defer turn.Release()
	if turn.Note != "" {
		t.Errorf("the turn notes %q; want nothing, as memory was above the floor when the turn was taken", turn.Note)
	}
}

// A run whose wait fails still says how long it waited, so the time is on
// record apart from the checks' own: with the line's folder gone while it
// waits behind a holder, it gets the error and the time it had waited.
func TestARunWhoseWaitFailsStillSaysHowLongItWaited(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	defer holder.Release()
	var waiter said
	type outcome struct {
		turn Turn
		err  error
	}
	ended := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	resume := make(chan struct{})
	stopped := make(chan struct{})
	defer func() {
		cancel()
		<-stopped
	}()
	// Pause after the line has been read, so removing it cannot race the
	// next directory read on Windows.
	a.Waiting = func(waited time.Duration, why string) {
		waiter.waiting(waited, why)
		select {
		case <-resume:
		case <-ctx.Done():
		}
	}
	go func() {
		defer close(stopped)
		turn, err := a.Wait(ctx)
		ended <- outcome{turn, err}
	}()
	if lines := waiter.atLeast(t, 1); len(lines) == 0 {
		t.Fatal("the run behind the holder had not said that it waits after 10 seconds")
	}
	saidItWaits := time.Now()
	time.Sleep(100 * time.Millisecond)

	// Act
	heldBack := time.Since(saidItWaits)
	if err := os.RemoveAll(filepath.Join(a.Dir, "line")); err != nil {
		t.Fatal(err)
	}
	close(resume)
	got := <-ended

	// Assert
	got.turn.Release()
	if got.err == nil || ctx.Err() != nil || got.turn.Waited < heldBack {
		t.Errorf("Wait returned %v and a turn that waited %s; want the error of a line that is gone, and a wait of at least the %s the run was held back", got.err, got.turn.Waited, heldBack)
	}
}

// A run that was killed while it held a slot does not keep it: the next run
// takes the slot over once the holder's process is gone.
func TestARunTakesTheSlotOfARunThatIsGone(t *testing.T) {
	// Arrange
	a := admission(t)
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	gone, err := json.Marshal(lock.Info{PID: 4194300, OwnerPID: 4194300, Hostname: hostname, Start: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), Acquired: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Dir, "slot-1"), gone, 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	turn := within(t, take(t, a), "the run behind a holder that is gone")

	// Assert
	defer turn.Release()
	if turn.Note != "" {
		t.Errorf("the turn notes %q; want nothing, as the holder was gone", turn.Note)
	}
}

// A live overdue owner holds its slot until its checks actually finish.
func TestARunWaitsForAnOverdueHolderToFinish(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Who = "the next run"
	hung := exec.Command(os.Args[0])
	hung.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+a.Dir)
	stdin, err := hung.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := hung.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := hung.Start(); err != nil {
		t.Fatal(err)
	}
	hasEnded := false
	t.Cleanup(func() {
		stdin.Close()
		if !hasEnded {
			hung.Wait()
		}
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		t.Fatalf("the hung run said %q, %v; want holding", line, err)
	}

	// Act
	var waiter said
	a.Waiting = waiter.waiting
	waiting := take(t, a)
	waiter.waits(t, waiting, "the run behind a live holder")
	time.Sleep(250 * time.Millisecond)
	notYet(t, waiting, "the run behind a holder past its 200ms budget")
	stdin.Close()
	err = hung.Wait()
	hasEnded = true
	if err != nil {
		t.Fatalf("the holder ended with %v", err)
	}
	turn := within(t, waiting, "the run, after the holder finished")

	// Assert
	defer turn.Release()
	if turn.Note != "" {
		t.Errorf("the turn notes %q; want a normal turn after the owner finished", turn.Note)
	}
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 1 || holding[0].Who != "the next run" || holding[0].PID != os.Getpid() {
		t.Errorf("after the hung run let go the turn is held by %+v, %v; want the next run still holding it", holding, err)
	}
}

// With no budget on record there is none to be past: a run that has no budget
// of its own waits behind a holder that named none, however long the holder
// has had the turn, and says who holds it without a budget.
func TestAHolderWithNoBudgetOnRecordKeepsItsTurn(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Budget = 0
	// A record from another machine cannot be checked, so its run counts as
	// still holding the turn.
	record, err := json.Marshal(lock.Info{PID: 4242, OwnerPID: 4242, Hostname: "another-machine", Start: time.Now().Add(-3 * time.Hour), Acquired: time.Now().Add(-2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Dir, "slot-1"), record, 0o644); err != nil {
		t.Fatal(err)
	}
	var waiter said
	a.Waiting = waiter.waiting

	// Act
	waiting := take(t, a)
	line := waiter.waits(t, waiting, "a run with no budget, behind a holder that named none,")

	// Assert
	notYet(t, waiting, "a run with no budget, behind a holder that named none,")
	if want := "the turn is held by a run that did not name itself (pid 4242), for 2h0m"; !strings.HasPrefix(line, want) || strings.Contains(line, "budget") {
		t.Errorf("the waiting run said %q; want it to start %q and to name no budget", line, want)
	}
}

// An unreadable old record retains its place until custody is resolved.
func TestAnUnreadableRecordInTheLineKeepsItsPlace(t *testing.T) {
	// Arrange
	a := admission(t)
	line := filepath.Join(a.Dir, "line")
	if err := os.MkdirAll(line, 0o755); err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(line, "00000000000000000001-1-1")
	if err := os.WriteFile(left, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	aMinuteAgo := time.Now().Add(-time.Minute)
	if err := os.Chtimes(left, aMinuteAgo, aMinuteAgo); err != nil {
		t.Fatal(err)
	}

	// Act
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	turn, err := a.Wait(ctx)

	// Assert
	defer turn.Release()
	kept, readErr := os.ReadFile(left)
	if err == nil || turn.release != nil || readErr != nil || len(kept) != 0 {
		t.Errorf("unreadable custody was lost or admitted: turn=%+v err=%v kept=%q read=%v", turn, err, kept, readErr)
	}
}

// A record that cannot be read yet, because its run is writing it this
// moment, keeps its place in the line.
func TestARecordBeingWrittenKeepsItsPlaceInTheLine(t *testing.T) {
	// Arrange
	a := admission(t)
	line := filepath.Join(a.Dir, "line")
	if err := os.MkdirAll(line, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(line, "00000000000000000001-1-1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var waiter said
	a.Waiting = waiter.waiting

	// Act
	waiting := take(t, a)
	first := waiter.waits(t, waiting, "a run behind a record being written")

	// Assert
	notYet(t, waiting, "a run behind a record being written")
	if want := "1 run is ahead of this one in line"; first != want {
		t.Errorf("the waiting run said %q; want %q", first, want)
	}
}

// A run that is cancelled while it waits leaves the line, so the runs behind
// it do not wait for a run that will never take its turn.
func TestACancelledRunLeavesTheLine(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	defer holder.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Act
	_, err := a.Wait(ctx)

	// Assert
	_, waiting, lineErr := Line(a.Dir)
	if err == nil || lineErr != nil || len(waiting) != 0 {
		t.Errorf("Wait returned %v and the line then held %+v (%v); want the context's error and an empty line", err, waiting, lineErr)
	}
}

// The line can be read from outside: who holds the turn, since when and
// under what budget, and who waits, in the order they asked.
func TestLineNamesTheHolderAndTheWaitingRunsInOrder(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Who = "the holder"
	holder := within(t, take(t, a), "the holder")
	defer holder.Release()
	for _, who := range []string{"asked first", "asked second"} {
		var waiter said
		a.Who, a.Waiting = who, waiter.waiting
		waiter.waits(t, take(t, a), "the run that "+who+", behind the holder,")
	}

	// Act
	holding, waiting, err := Line(a.Dir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(holding) != 1 || holding[0].Who != "the holder" || holding[0].PID != os.Getpid() || holding[0].Budget != 90*time.Minute || time.Since(holding[0].Since) > time.Minute {
		t.Errorf("Line holding = %+v; want the holder, this process, a 90 minute budget, taken just now", holding)
	}
	if len(waiting) != 2 || waiting[0].Who != "asked first" || waiting[1].Who != "asked second" {
		t.Errorf("Line waiting = %+v; want the run that asked first, then the one that asked second", waiting)
	}
}

// The waiting runs are listed by their place in line, which is the order
// they take their turns in, whatever the moment each record was written.
func TestLineListsWaitingRunsByTheirPlaceInLine(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	line := filepath.Join(dir, "line")
	if err := os.MkdirAll(line, 0o755); err != nil {
		t.Fatal(err)
	}
	// A record from another machine cannot be checked, so its run counts as
	// still waiting.
	now := time.Now()
	for place, written := range map[string]time.Time{"00000000000000000001-10-1": now, "00000000000000000002-11-1": now.Add(-time.Minute)} {
		record, err := json.Marshal(lock.Info{PID: 4242, OwnerPID: 4242, Hostname: "another-machine", Start: time.Now().Add(-3 * time.Hour), Acquired: written})
		if err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{place: record, place + ".run": []byte(`{"who":"the run at ` + place + `"}`)} {
			if err := os.WriteFile(filepath.Join(line, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Act
	_, waiting, err := Line(dir)

	// Assert
	if err != nil || len(waiting) != 2 || waiting[0].Who != "the run at 00000000000000000001-10-1" || waiting[1].Who != "the run at 00000000000000000002-11-1" {
		t.Errorf("Line waiting = %+v, %v; want the run at place 1, then the run at place 2", waiting, err)
	}
}

// A holder whose card is not there, not written yet or unreadable, still
// shows in the line, as a run that did not name itself, with no budget.
func TestLineShowsAHolderWithoutACardAsUnnamed(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	defer holder.Release()
	if err := os.Remove(filepath.Join(a.Dir, "slot-1.run")); err != nil {
		t.Fatal(err)
	}

	// Act
	holding, _, err := Line(a.Dir)

	// Assert
	if err != nil || len(holding) != 1 || holding[0].Who != "a run that did not name itself" || holding[0].Budget != 0 || holding[0].PID != os.Getpid() {
		t.Errorf("Line holding = %+v, %v; want this process as a run that did not name itself, with no budget", holding, err)
	}
}

// A folder no run has used yet has no line, and reading it is no error.
func TestLineOfAFolderNoRunHasUsedIsEmpty(t *testing.T) {
	// Act
	holding, waiting, err := Line(filepath.Join(t.TempDir(), "slots"))

	// Assert
	if err != nil || len(holding) != 0 || len(waiting) != 0 {
		t.Errorf("Line = %+v, %+v, %v; want nothing and no error", holding, waiting, err)
	}
}

// A run that holds a turn can say what it is doing now, and whoever reads the
// line sees it beside who the run is and its budget, which stay as they were.
func TestARunSaysWhatItIsDoingWhileItHoldsItsTurn(t *testing.T) {
	// Arrange
	a := admission(t)
	turn := within(t, take(t, a), "the run")
	defer turn.Release()

	// Act
	turn.Say("go test: 3 packages done")
	turn.Say("go test: 4 packages done")

	// Assert
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 1 {
		t.Fatalf("Line holding = %+v, %v; want the one run", holding, err)
	}
	if holding[0].Now != "go test: 4 packages done" || holding[0].Who != "a test's run" || holding[0].Budget != 90*time.Minute {
		t.Errorf("the holder reads %+v; want the last thing it said, with its name and its budget of 1h30m0s kept", holding[0])
	}
}

// A run that has given its turn back says nothing more: a progress line that
// arrives after release must not leave a card beside a slot no run holds.
func TestARunSaysNothingAfterReleasingItsTurn(t *testing.T) {
	// Arrange
	a := admission(t)
	turn := within(t, take(t, a), "the run")
	turn.Say("go test: 3 packages done")
	turn.Release()

	// Act
	turn.Say("go test: 4 packages done")

	// Assert
	if _, err := os.Stat(filepath.Join(a.Dir, "slot-1.run")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("slot-1.run after release: %v; want no card left beside the free slot", err)
	}
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 0 {
		t.Errorf("Line holding = %+v, %v; want no run holding a turn", holding, err)
	}
}

// A run whose turn was taken from it no longer speaks for the slot: what it
// says then is not written over the card of the run that holds the turn now.
func TestARunThatLostItsTurnSaysNothingOverTheNewHolder(t *testing.T) {
	// Arrange: the run's slot now names a holder on another machine, which
	// cannot be checked and so counts as running.
	a := admission(t)
	turn := within(t, take(t, a), "the run")
	defer turn.Release()
	other, err := json.Marshal(lock.Info{PID: 4242, OwnerPID: 4242, Hostname: "another-machine", Start: time.Now().Add(-time.Hour), Acquired: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"slot-1": other, "slot-1.run": []byte(`{"who":"the run that took the turn","budget_seconds":5400}`)} {
		if err := os.WriteFile(filepath.Join(a.Dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	turn.Say("go test: still going")

	// Assert
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 1 || holding[0].Who != "the run that took the turn" || holding[0].Now != "" {
		t.Errorf("Line holding = %+v, %v; want the run that took the turn, saying nothing", holding, err)
	}
}

// A turn that was never taken, as when a run can take none, says nothing and
// does not fail.
func TestATurnThatWasNeverTakenSaysNothing(t *testing.T) {
	// Act
	Turn{}.Say("go test: 1 package done")
}
