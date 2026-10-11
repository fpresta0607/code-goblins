package verify

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// A command a turn was taken for may take turns itself, as cfo gate test
// does behind cfo gate turn. Started with the turn's token in its
// environment, such a run takes no second turn and does not wait for the one
// it is inside, which it would wait on for good.
func TestARunStartedInsideATurnDoesNotWaitForIt(t *testing.T) {
	// Arrange
	a := admission(t)
	outer := within(t, take(t, a), "the outer run")
	defer outer.Release()
	t.Setenv(TurnVariable, outer.Token())

	// Act
	inner := within(t, take(t, a), "a run started inside the outer run's turn")

	// Assert
	if inner.Waited != 0 || inner.Token() == "" || inner.Token() != outer.Token() {
		t.Errorf("the inner run's turn waited %s under token %q; want no wait, and the outer turn's token %q to hand on", inner.Waited, inner.Token(), outer.Token())
	}
	holding, waiting, err := Line(a.Dir)
	if err != nil || len(holding) != 1 || holding[0].Who != "a test's run" || len(waiting) != 0 {
		t.Errorf("Line = %+v, %+v, %v; want the outer run holding the one turn and no run waiting", holding, waiting, err)
	}
}

// The turn is the outer run's to give back: a run inside it that ends leaves
// it held, so the next run in line still waits for the outer run.
func TestARunInsideATurnLeavesItHeldWhenItEnds(t *testing.T) {
	// Arrange
	a := admission(t)
	outer := within(t, take(t, a), "the outer run")
	defer outer.Release()
	t.Setenv(TurnVariable, outer.Token())
	inner := within(t, take(t, a), "a run started inside the outer run's turn")

	// Act
	inner.Release()

	// Assert
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 1 {
		t.Fatalf("Line holding = %+v, %v; want the outer run still holding its turn", holding, err)
	}
	t.Setenv(TurnVariable, "")
	notYet(t, take(t, a), "a run outside the turn, after the inner run ended,")
}

// The holder of the turn is another process, as cfo gate turn is to the
// command it starts: its token is honoured from this process too.
func TestARunStartedInsideAnotherProcessTurnDoesNotWaitForIt(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := exec.Command(os.Args[0])
	holder.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+a.Dir)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if err := holder.Wait(); err != nil {
			t.Error(err)
		}
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		t.Fatalf("holder: %q, %v", line, err)
	}
	custody, err := lock.ReadNamedStrict(a.Dir, "slot-1")
	if err != nil || custody.PID != holder.Process.Pid || !custody.VerifiedAlive() {
		t.Fatalf("the holder's custody was not proved: %+v, %v", custody, err)
	}
	t.Setenv(TurnVariable, turnToken("slot-1", custody))

	// Act
	inner := within(t, take(t, a), "a run started inside another process's turn")

	// Assert
	holding, waiting, err := Line(a.Dir)
	if inner.Waited != 0 || err != nil || len(holding) != 1 || holding[0].PID != holder.Process.Pid || len(waiting) != 0 {
		t.Errorf("the inner run waited %s and Line = %+v, %+v, %v; want no wait, the other process holding the one turn and no run waiting", inner.Waited, holding, waiting, err)
	}
}

// Where the machine gives two turns, a run started inside the second is
// inside it, as one started inside the first is.
func TestARunStartedInsideTheSecondOfTwoTurnsDoesNotWaitForIt(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	first := within(t, take(t, a), "the first run")
	defer first.Release()
	second := within(t, take(t, a), "the second run")
	defer second.Release()
	t.Setenv(TurnVariable, second.Token())

	// Act
	inner := within(t, take(t, a), "a run started inside the second run's turn")

	// Assert
	holding, waiting, err := Line(a.Dir)
	if inner.Token() != second.Token() || first.Token() == second.Token() || err != nil || len(holding) != 2 || len(waiting) != 0 {
		t.Errorf("the inner run's token is %q beside the two turns %q and %q, and Line = %+v, %+v, %v; want the second turn's token, both turns held and no run waiting", inner.Token(), first.Token(), second.Token(), holding, waiting, err)
	}
}

// A token names a turn only while the process that took it still holds it.
// A run that carries any other value, as a process a finished command left
// running would, joins the line as every run does.
func TestARunWhoseTokenNamesNoHeldTurnWaitsInLine(t *testing.T) {
	a := admission(t)
	finished := within(t, take(t, a), "the run that finished")
	finishedToken := finished.Token()
	finished.Release()
	holder := within(t, take(t, a), "the run that holds the turn now")
	defer holder.Release()
	custody, err := lock.ReadNamedStrict(a.Dir, "slot-1")
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"a_finished_turn":          finishedToken,
		"another_process":          fmt.Sprintf("slot-1:%d:%d", custody.PID+1, custody.Acquired.UnixNano()),
		"another_moment":           fmt.Sprintf("slot-1:%d:%d", custody.PID, custody.Acquired.UnixNano()+1),
		"the_slot_by_another_path": fmt.Sprintf("%s:%d:%d", filepath.Join("..", filepath.Base(a.Dir), "slot-1"), custody.PID, custody.Acquired.UnixNano()),
		"the_slot_alone":           "slot-1",
		"more_than_the_token":      holder.Token() + ":1",
		"no_token":                 "",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			if token == holder.Token() {
				t.Fatalf("the token under test, %q, is the holder's own", token)
			}
			t.Setenv(TurnVariable, token)
			waiting := a
			var waiter said
			waiting.Waiting = waiter.waiting

			// Act
			late := take(t, waiting)

			// Assert
			waiter.waits(t, late, "a run whose token names no held turn")
			notYet(t, late, "a run whose token names no held turn")
		})
	}
}

// A holder that was ended leaves its record behind, and its token with any
// run it started. Such a run is inside no turn: it joins the line and takes
// the turn the dead holder left, under a token of its own.
func TestARunWhoseTokenNamesADeadHoldersTurnTakesItsOwn(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := exec.Command(os.Args[0])
	holder.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+a.Dir)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		holder.Process.Kill()
		holder.Wait()
		t.Fatalf("holder: %q, %v", line, err)
	}
	custody, err := lock.ReadNamedStrict(a.Dir, "slot-1")
	if err != nil || custody.PID != holder.Process.Pid {
		holder.Process.Kill()
		holder.Wait()
		t.Fatalf("the holder's custody was not proved: %+v, %v", custody, err)
	}
	dead := turnToken("slot-1", custody)
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	holder.Wait()
	if left, err := lock.ReadNamedStrict(a.Dir, "slot-1"); err != nil || *left != *custody {
		t.Fatalf("the ended holder's record is %+v, %v; want it left as it was, %+v", left, err, custody)
	}
	t.Setenv(TurnVariable, dead)

	// Act
	own := within(t, take(t, a), "a run whose token names a dead holder's turn")
	defer own.Release()

	// Assert
	holding, _, err := Line(a.Dir)
	if own.Token() == dead || own.Token() == "" || err != nil || len(holding) != 1 || holding[0].PID != os.Getpid() {
		t.Errorf("the run's token is %q and Line holding = %+v, %v; want a token other than the dead holder's %q, and this process holding the turn", own.Token(), holding, err, dead)
	}
}

// A run inside a turn says what it is doing beside that turn, where cfo gate
// turns reads it, and who holds the turn and its budget stay as they were.
// When it ends it takes back what it said and no more.
func TestARunInsideATurnSaysWhatItIsDoingBesideIt(t *testing.T) {
	// Arrange
	a := admission(t)
	outer := within(t, take(t, a), "the outer run")
	defer outer.Release()
	t.Setenv(TurnVariable, outer.Token())
	inside := a
	inside.Who, inside.Budget = "the run inside", time.Minute
	inner := within(t, take(t, inside), "a run started inside the outer run's turn")

	// Act
	inner.Say("go test: 3 packages done")
	said, _, saidErr := Line(a.Dir)
	inner.Release()
	inner.Say("go test: 4 packages done")
	after, _, afterErr := Line(a.Dir)

	// Assert
	if saidErr != nil || len(said) != 1 || said[0].Now != "go test: 3 packages done" || said[0].Who != "a test's run" || said[0].Budget != 90*time.Minute {
		t.Errorf("while the inner run ran the holder read %+v, %v; want what the inner run said, beside the outer run's name and its budget of 1h30m0s", said, saidErr)
	}
	if afterErr != nil || len(after) != 1 || after[0].Now != "" || after[0].Who != "a test's run" || after[0].Budget != 90*time.Minute {
		t.Errorf("after the inner run ended the holder read %+v, %v; want the outer run holding its turn and saying nothing", after, afterErr)
	}
}

// A run inside a turn that has since passed to another run no longer speaks
// for it: what it says is not written over the card of the run that holds
// the turn now.
func TestARunInsideAFinishedTurnSaysNothingOverTheNewHolder(t *testing.T) {
	// Arrange
	a := admission(t)
	outer := within(t, take(t, a), "the outer run")
	t.Setenv(TurnVariable, outer.Token())
	inner := within(t, take(t, a), "a run started inside the outer run's turn")
	outer.Release()
	t.Setenv(TurnVariable, "")
	next := a
	next.Who = "the run that took the turn"
	holder := within(t, take(t, next), "the next run")
	defer holder.Release()
	holder.Say("the next run's own tests")

	// Act
	inner.Say("go test: still going")
	inner.Release()

	// Assert
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 1 || holding[0].Who != "the run that took the turn" || holding[0].Now != "the next run's own tests" || holding[0].Budget != 90*time.Minute {
		t.Errorf("Line holding = %+v, %v; want the run that took the turn, with its budget and what it said itself", holding, err)
	}
}
