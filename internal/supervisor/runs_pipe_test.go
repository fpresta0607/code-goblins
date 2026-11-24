package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// runPipe serves run requests for s's home until the test ends, holding the
// home's watch lock the way the supervisor does, since clients send only to
// the process that holds it.
func runPipe(t *testing.T, s *Service) {
	t.Helper()
	if _, err := lock.AcquireExclusiveNamed(s.Store.Home.State, ".watch.lock"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(s.Store.Home.State, ".watch.lock") })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serveRunRequests(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		// A card step taken over the pipe tells the CEO after it replies, so the test ends only once that work has written into state.
		s.credentialWork.Wait()
	})
}

// shorten sets *wait to length for the test.
func shorten(t *testing.T, wait *time.Duration, length time.Duration) {
	t.Helper()
	previous := *wait
	*wait = length
	t.Cleanup(func() { *wait = previous })
}

