package awake

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// pausedClock makes the clock of each timeout started from now on run as a
// machine's does through a pause: the returned func moves it on by the
// pause's length at once.
func pausedClock(t *testing.T) (pause func(length time.Duration)) {
	t.Helper()
	var paused atomic.Int64
	now = func() time.Time { return time.Now().Add(time.Duration(paused.Load())) } // stall-accepted: it moves the clock as a pause does and holds nothing to it.
	t.Cleanup(func() { now = time.Now })
	return func(length time.Duration) { paused.Add(int64(length)) }
}

// endsWithin waits for ctx to end, for at most a minute.
func endsWithin(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Minute):
		t.Fatal("the context did not end within a minute")
	}
}

// A timeout ends as context.WithTimeout's does, once its time has run and
// no sooner.
func TestATimeoutEndsOnceItsTimeHasRun(t *testing.T) {
	// Arrange
	const timeout = 200 * time.Millisecond
	began := time.Now()

	// Act
	ctx, cancel := WithTimeout(context.Background(), timeout)
	defer cancel()
	endsWithin(t, ctx)
	waited := time.Since(began)

	// Assert
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) || ctx.Left() != 0 {
		t.Errorf("Err() = %v, Left() = %s once the timeout ran out, want context.DeadlineExceeded and none left", ctx.Err(), ctx.Left())
	}
	if waited < timeout {
		t.Errorf("the timeout of %s ended after %s", timeout, waited)
	}
}

// A pause moves the wall clock on and leaves the timeout as it was: an hour
// of pause leaves a minute's timeout running, with the minute left.
func TestAPauseSpendsNoneOfATimeout(t *testing.T) {
	// Arrange
	pause := pausedClock(t)
	ctx, cancel := WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	pause(time.Hour)
	leftAsItRuns := ctx.Left()
	time.Sleep(3 * longestStep)

	// Assert
	if leftAsItRuns < 30*time.Second {
		t.Errorf("Left() = %s just after a pause, before the watch saw it, want about the minute", leftAsItRuns)
	}
	if ctx.Err() != nil || ctx.Left() < 30*time.Second {
		t.Errorf("an hour's pause left Err() = %v, Left() = %s of a minute's timeout, want it running with about the minute left", ctx.Err(), ctx.Left())
	}
}

// A pause that falls in a timeout's last step only lengthens it: the
// timeout still ends, once its time has run outside the pause.
func TestATimeoutAPauseFellInStillEnds(t *testing.T) {
	// Arrange
	pause := pausedClock(t)
	const timeout = 150 * time.Millisecond
	ctx, cancel := WithTimeout(context.Background(), timeout)
	defer cancel()

	// Act
	pause(time.Hour)
	endsWithin(t, ctx)

	// Assert
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want context.DeadlineExceeded", ctx.Err())
	}
}

// A timeout ends with its parent, with the parent's error.
func TestATimeoutEndsWithItsParent(t *testing.T) {
	for name, test := range map[string]struct {
		parent  func() (context.Context, context.CancelFunc)
		timeout time.Duration
		want    error
	}{
		"a parent cancelled while it runs": {
			parent: func() (context.Context, context.CancelFunc) {
				parent, cancel := context.WithCancel(context.Background())
				time.AfterFunc(50*time.Millisecond, cancel) // stall-accepted: it only makes the cancel come later.
				return parent, cancel
			},
			timeout: time.Minute,
			want:    context.Canceled,
		},
		"a parent whose deadline passes while it runs": {
			parent: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond) // stall-accepted: the test means this context to run out.
			},
			timeout: time.Minute,
			want:    context.DeadlineExceeded,
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			parent, cancelParent := test.parent()
			defer cancelParent()

			// Act
			ctx, cancel := WithTimeout(parent, test.timeout)
			defer cancel()
			endsWithin(t, ctx)

			// Assert
			if !errors.Is(ctx.Err(), test.want) {
				t.Errorf("Err() = %v, want %v", ctx.Err(), test.want)
			}
		})
	}
}

// A timeout that ends already, as a parent that ended or no time to wait
// gives, has ended as it is made, as context.WithTimeout's has.
func TestATimeoutWithNothingToWaitForHasEndedAsItIsMade(t *testing.T) {
	// Arrange
	ended, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	fromEnded, cancelFromEnded := WithTimeout(ended, time.Minute)
	defer cancelFromEnded()
	untimed, cancelUntimed := WithTimeout(context.Background(), 0)
	defer cancelUntimed()

	// Assert
	if fromEnded.Err() == nil || untimed.Err() == nil {
		t.Errorf("Err() = %v of an ended parent's timeout and %v of no time, want both ended", fromEnded.Err(), untimed.Err())
	}
}

// Cancelled, a timeout ends with context.Canceled and has nothing left,
// and so does a context made from it.
func TestCancelEndsATimeoutAndWhatIsMadeFromIt(t *testing.T) {
	// Arrange
	ctx, cancel := WithTimeout(context.Background(), time.Minute)
	child, cancelChild := context.WithCancel(ctx)
	defer cancelChild()

	// Act
	cancel()
	endsWithin(t, child)

	// Assert
	if !errors.Is(ctx.Err(), context.Canceled) || ctx.Left() != 0 {
		t.Errorf("Err() = %v, Left() = %s once cancelled, want context.Canceled and none left", ctx.Err(), ctx.Left())
	}
	if !errors.Is(child.Err(), context.Canceled) {
		t.Errorf("the child's Err() = %v, want context.Canceled", child.Err())
	}
}

// A context made from a timeout that runs out ends as it does, with
// context.DeadlineExceeded, and its cause says so too.
func TestAContextMadeFromATimeoutRunsOutWithIt(t *testing.T) {
	// Arrange
	ctx, cancel := WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	child, cancelChild := context.WithCancel(ctx)
	defer cancelChild()

	// Act
	endsWithin(t, child)

	// Assert
	if !errors.Is(child.Err(), context.DeadlineExceeded) || !errors.Is(context.Cause(child), context.DeadlineExceeded) {
		t.Errorf("the child's Err() = %v, Cause = %v, want context.DeadlineExceeded", child.Err(), context.Cause(child))
	}
}
