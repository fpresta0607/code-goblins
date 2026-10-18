package supervisor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBrowserFailedRegistrySaveRetainsFailedCleanupOwnership(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "throwaway")
	broker.start = func(ctx context.Context, launch browserlogin.Launch) (browserlogin.Process, error) {
		process, err := chrome.start(ctx, launch)
		if err := os.Mkdir(browserlogin.RegistryPath(broker.state), 0o700); err != nil {
			t.Fatal(err)
		}
		return process, err
	}
	broker.end = func(context.Context, browserlogin.Process) error { return errors.New("cleanup unconfirmed") }
	broker.report = func(error) {}

	if _, err := broker.attach(context.Background(), "pd-keys", "stripe"); err == nil {
		t.Fatal("failed registry save passed")
	}
	broker.work.Wait()
	if err := os.Remove(browserlogin.RegistryPath(broker.state)); err != nil {
		t.Fatal(err)
	}
	_, err := broker.attach(context.Background(), "pd-keys", "stripe")

	if err == nil || !strings.Contains(err.Error(), "still closing") || len(chrome.launches) != 1 || len(broker.processes) != 1 {
		t.Fatalf("retry = %v, launches %d, owned %d, want fenced ownership", err, len(chrome.launches), len(broker.processes))
	}
}

func TestBrowserLaunchCannotLendAfterCancellationRevocationOrTaskReplacement(t *testing.T) {
	for _, reason := range []string{"cancelled", "revoked", "replaced"} {
		t.Run(reason, func(t *testing.T) {
			broker, chrome, _, _ := testBroker(t)
			brokerTask(t, broker, "pd-keys", "throwaway")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			broker.start = func(ctx context.Context, launch browserlogin.Launch) (browserlogin.Process, error) {
				process, err := chrome.start(ctx, launch)
				switch reason {
				case "cancelled":
					cancel()
				case "revoked":
					if err := state.WriteTaskCredentials(broker.state, "pd-keys", nil); err != nil {
						t.Fatal(err)
					}
				case "replaced":
					meta, err := state.ReadTaskMeta(broker.state, "pd-keys")
					if err != nil {
						t.Fatal(err)
					}
					meta.SpawnGen = "new-generation"
					if err := state.WriteTaskMeta(broker.state, meta); err != nil {
						t.Fatal(err)
					}
				}
				return process, err
			}

			lease, err := broker.attach(ctx, "pd-keys", "stripe")

			if err == nil || lease.URL != "" || len(chrome.endedPIDs(broker)) != 1 {
				t.Fatal("a launch lent access after its caller was cancelled, revoked or replaced")
			}
		})
	}
}

func TestBrowserFailedStartupRetainsAnUnclosedReturnedProcess(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "throwaway")
	broker.start = func(ctx context.Context, launch browserlogin.Launch) (browserlogin.Process, error) {
		process, _ := chrome.start(ctx, launch)
		return process, errors.New("readiness and cleanup failed")
	}
	broker.end = func(context.Context, browserlogin.Process) error { return errors.New("cleanup unconfirmed") }
	broker.report = func(error) {}

	if _, err := broker.attach(context.Background(), "pd-keys", "stripe"); err == nil {
		t.Fatal("failed startup passed")
	}
	broker.work.Wait()
	_, err := broker.attach(context.Background(), "pd-keys", "stripe")

	if err == nil || !strings.Contains(err.Error(), "still closing") || len(chrome.launches) != 1 || len(broker.processes) != 1 {
		t.Fatalf("retry = %v, launches %d, owned %d, want fenced ownership", err, len(chrome.launches), len(broker.processes))
	}
}

func TestBrowserRequestCancelledBehindTheMutexStartsNothing(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "throwaway")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond) // stall-accepted: the test means this context to run out while the request waits on the lock.
	defer cancel()
	broker.mu.Lock()
	answer := make(chan error, 1)
	go func() { _, err := broker.attach(ctx, "pd-keys", "stripe"); answer <- err }()
	<-ctx.Done()
	broker.mu.Unlock()

	select {
	case err := <-answer:
		if !errors.Is(err, context.DeadlineExceeded) || len(chrome.launches) != 0 {
			t.Fatalf("expired request = %v, launches %d, want no launch", err, len(chrome.launches))
		}
	case <-time.After(time.Minute):
		t.Fatal("cancelled request did not return")
	}
}

func TestBrowserShutdownReportsUnconfirmedCleanupAndRetainsOwnership(t *testing.T) {
	broker, _, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "throwaway")
	if _, err := broker.attach(context.Background(), "pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	broker.end = func(context.Context, browserlogin.Process) error { return errors.New("cleanup unconfirmed") }
	broker.report = func(error) {}

	err := broker.closeAll()

	if err == nil || !strings.Contains(err.Error(), "confirmed closed") || len(broker.processes) != 1 || !broker.closing["stripe-throwaway"] {
		t.Fatalf("shutdown = %v, owned %d, closing %v, want reported failure with retained ownership", err, len(broker.processes), broker.closing)
	}
}
