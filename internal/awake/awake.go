// Package awake times a wait on a clock that a pause of the whole machine
// does not move.
//
// A machine can stop every process at once: a laptop that sleeps does, and
// so does a hosted runner, an Azure virtual machine its host pauses for
// about ten seconds (#1048). The wall clock runs on through the pause, so a
// wait held to a deadline on it finds the deadline passed once the machine
// runs again, though what it waited on had no time to act, and it reports a
// failure, a refusal or a timeout that only the pause caused (#1102). A
// timeout here is spent only by time the machine ran, so a pause makes a
// wait slower, never wrong.
package awake

import (
	"context"
	"sync"
	"time"
)

// longestStep is the longest step a timeout's watch counts at once. A pause
// costs the step it falls in, so it lengthens a wait by one step at most.
const longestStep = 100 * time.Millisecond

// pauseAfter is how much later than it was due a step ends when a pause fell
// in it. A timer ends within milliseconds of its time even on a loaded
// machine, where Windows keeps a starved thread running, and the pauses a
// wait must outlast last seconds.
const pauseAfter = time.Second

// now is time.Now, which a test replaces to move the clock as a pause does.
// Each timeout reads the one it started with.
var now = time.Now

// Context is a context whose own timeout counts only the time the machine
// ran. It ends with context.DeadlineExceeded once that time has run out, as
// context.WithTimeout's does, and with its parent's error if the parent
// ends first. Its Deadline is its parent's, since its own timeout is no
// moment on the wall clock.
type Context struct {
	context.Context
	done chan struct{}
	now  func() time.Time

	mu  sync.Mutex
	err error
	// left is the timeout left as of seen, and step how long the watch's
	// step from seen is to take.
	left time.Duration
	step time.Duration
	seen time.Time
}

// WithTimeout is context.WithTimeout for a timeout that only the time the
// machine ran spends.
func WithTimeout(parent context.Context, timeout time.Duration) (*Context, context.CancelFunc) {
	c := &Context{Context: parent, done: make(chan struct{}), now: now, left: timeout}
	c.seen = c.now()
	switch {
	case parent.Err() != nil:
		c.end(parent.Err())
	case timeout <= 0:
		c.end(context.DeadlineExceeded)
	default:
		go c.watch()
	}
	return c, func() { c.end(context.Canceled) }
}

// watch spends the timeout step by step until it has run out, the context is
// cancelled or its parent ends.
func (c *Context) watch() {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		c.mu.Lock()
		if c.left <= 0 {
			c.mu.Unlock()
			c.end(context.DeadlineExceeded)
			return
		}
		c.step = min(c.left, longestStep)
		timer.Reset(c.step)
		c.mu.Unlock()
		select {
		case <-c.done:
			return
		case <-c.Context.Done():
			c.end(c.Context.Err())
			return
		case <-timer.C:
		}
		c.mu.Lock()
		seen := c.now()
		c.left -= ran(c.step, seen.Sub(c.seen))
		c.seen = seen
		c.mu.Unlock()
	}
}

// ran is how much of a step that was to take asked, and took took, the
// machine ran: all of it, unless it ended more than pauseAfter late. Then a
// pause fell in it, and none of it counts: some of it ran, before the pause
// or after it, but how much is unknown, and a wait that counts none of it is
// one step slower at most, while one that counted it all could end with no
// time left to what it waits on.
func ran(asked, took time.Duration) time.Duration {
	if took > asked+pauseAfter {
		return 0
	}
	return took
}

// Left is how much of the timeout is left: none once the context has ended.
func (c *Context) Left() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return 0
	}
	return max(c.left-ran(c.step, c.now().Sub(c.seen)), 0)
}

func (c *Context) Done() <-chan struct{} {
	return c.done
}

func (c *Context) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// end ends the context with err, unless it has ended already.
func (c *Context) end(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
}

// Deadline sets a deadline with set, a file's SetReadDeadline or its
// SetDeadline, that falls once timeout of the time the machine ran is spent:
// a deadline on the wall clock falls once a pause of the whole machine has
// passed, though the other side, which the pause stopped too, had no time to
// act. lift lifts the deadline again; call it once.
func Deadline(set func(time.Time) error, timeout time.Duration) (lift func()) {
	limit, cancelLimit := WithTimeout(context.Background(), timeout)
	limited := make(chan struct{})
	stopLimit := context.AfterFunc(limit, func() {
		_ = set(time.Now())
		close(limited)
	})
	return func() {
		if !stopLimit() {
			<-limited
		}
		cancelLimit()
		_ = set(time.Time{})
	}
}
