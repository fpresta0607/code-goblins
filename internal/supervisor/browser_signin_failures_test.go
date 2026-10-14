package supervisor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBrowserSignInRefusesRequestsOutsideALiveReviewedProject(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Service, *BrowserRequest)
	}{
		{"invalid task", func(_ *Service, request *BrowserRequest) { request.Task = "../other" }},
		{"missing task", func(_ *Service, request *BrowserRequest) { request.Task = "missing" }},
		{"unknown provider", func(_ *Service, request *BrowserRequest) { request.Provider = "missing" }},
		{"unreviewed page", func(service *Service, _ *BrowserRequest) { service.browsers.recipes.Providers[0].KeyPage = "" }},
		{"task ended", func(service *Service, _ *BrowserRequest) {
			service.browsers.live = func(string) error { return errors.New("task ended") }
		}},
		{"supervisor stopping", func(service *Service, _ *BrowserRequest) { service.browsers.closed = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, chrome, _ := signInBoard(t)
			request := BrowserRequest{Task: "pd-keys", Provider: "stripe"}
			test.change(service, &request)

			_, err := service.requestBrowserSignIn(request)

			if err == nil || len(service.Store.Snapshot().BrowserSignIns) != 0 || len(chrome.launches) != 0 {
				t.Fatalf("request = %v, want refusal without a card or window", err)
			}
		})
	}
}

func TestBrowserSignInRespectsAnotherTasksBrowserAndCard(t *testing.T) {
	for _, held := range []string{"browser", "card", "own browser"} {
		t.Run(held, func(t *testing.T) {
			service, chrome, _ := signInBoard(t)
			brokerTask(t, service.browsers, "pd-other", "PrecisionDocs-AI")
			if held == "card" {
				if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := service.browsers.attach(context.Background(), "pd-keys", "stripe"); err != nil {
				t.Fatal(err)
			}
			task := "pd-other"
			if held == "own browser" {
				task = "pd-keys"
			}

			_, err := service.requestBrowserSignIn(BrowserRequest{Task: task, Provider: "stripe"})

			if held == "own browser" {
				if err != nil || len(chrome.endedPIDs(service.browsers)) != 1 || len(service.Store.Snapshot().BrowserSignIns) != 1 {
					t.Fatalf("request = %v, want its own headless browser returned and a card filed", err)
				}
			} else if err == nil {
				t.Fatal("a task took another task's login")
			}
		})
	}
}

func TestBrowserSignInStartupFailuresLeaveARetryableCard(t *testing.T) {
	for _, failure := range []string{"memory", "executable", "start"} {
		t.Run(failure, func(t *testing.T) {
			service, _, _ := signInBoard(t)
			if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
				t.Fatal(err)
			}
			card := service.Store.Snapshot().BrowserSignIns[0]
			if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "memory":
				service.browsers.memory = func() (Memory, error) { return Memory{}, errors.New("unavailable") }
			case "executable":
				service.browsers.executable = func() (string, error) { return "", errors.New("Chrome is missing") }
			case "start":
				service.browsers.start = func(context.Context, browserlogin.Launch) (browserlogin.Process, error) {
					return browserlogin.Process{}, errors.New("Chrome did not start")
				}
			}

			if err := service.processBrowserSignIns(context.Background()); err != nil {
				t.Fatal(err)
			}

			card = service.Store.Snapshot().BrowserSignIns[0]
			if card.State != "ready" || card.Reason == "" || card.ClosedAt != nil {
				t.Fatalf("card = %+v, want a retryable failure", card)
			}
		})
	}
}

func TestBrowserSignInPageFailureClosesItsWindowWithoutPublishingPageText(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.browsers.signedIn = func(context.Context, string, keypages.Provider) (bool, string, error) {
		return false, "", errors.New("private-page-text")
	}

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	if card.State != "ready" || strings.Contains(card.Reason, "private-page-text") || len(chrome.endedPIDs(service.browsers)) != 1 {
		t.Fatalf("card = %+v, want a safe failure and a closed window", card)
	}
}

func TestBrowserSignInWindowEndsWhenItsTaskIsReplaced(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(service.browsers.state, card.Task)
	if err != nil {
		t.Fatal(err)
	}
	meta.SpawnGen = "replacement"
	if err := state.WriteTaskMeta(service.browsers.state, meta); err != nil {
		t.Fatal(err)
	}

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	if card.State != "expired" || card.ClosedAt == nil || len(chrome.endedPIDs(service.browsers)) != 1 {
		t.Fatalf("card = %+v, want the old task's window ended", card)
	}
}

// letGoOfFencedBrowsers forgets the stand-in browsers whose close a test
// made fail, which hold nothing, so the board's shutdown has none to report.
func letGoOfFencedBrowsers(broker *browserBroker) {
	broker.work.Wait()
	broker.mu.Lock()
	defer broker.mu.Unlock()
	clear(broker.processes)
	clear(broker.closing)
}

// clickSignIn files pd-keys' Stripe sign-in and clicks its button, which
// leaves the window for the keeper's next tick to open.
func clickSignIn(t *testing.T, service *Service) {
	t.Helper()
	if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserSignInRequiresItsDeclaredServicesEffectiveGrant(t *testing.T) {
	for _, test := range browserGrants {
		t.Run(test.name, func(t *testing.T) {
			service, chrome, _ := signInBoard(t)
			grantBrowserTask(t, service.browsers, "PrecisionDocs-AI", test)

			_, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"})

			cards, logins := len(service.Store.Snapshot().BrowserSignIns), len(registered(t, service.browsers))
			if (err == nil) != test.isAllowed || (cards == 1) != test.isAllowed || (logins == 1) != test.isAllowed || len(chrome.launches) != 0 {
				t.Fatalf("request = %v, cards %d, logins %d, launches %d, allowed %t", err, cards, logins, len(chrome.launches), test.isAllowed)
			}
		})
	}
}

func TestBrowserSignInRevokedGrantRefusesTheClickAndClosesAnOpenWindow(t *testing.T) {
	for _, stage := range []string{"before the window opens", "while the window is open"} {
		t.Run(stage, func(t *testing.T) {
			service, chrome, _ := signInBoard(t)
			clickSignIn(t, service)
			launches := 0
			if stage == "while the window is open" {
				if err := service.processBrowserSignIns(context.Background()); err != nil {
					t.Fatal(err)
				}
				launches = 1
			}
			if err := state.WriteTaskCredentials(service.browsers.state, "pd-keys", nil); err != nil {
				t.Fatal(err)
			}

			if err := service.processBrowserSignIns(context.Background()); err != nil {
				t.Fatal(err)
			}

			card := service.Store.Snapshot().BrowserSignIns[0]
			if card.State != "expired" || card.ClosedAt == nil || len(chrome.launches) != launches || len(chrome.endedPIDs(service.browsers)) != launches {
				t.Fatalf("card = %+v, launches %d, want the revoked task's sign-in ended with no window left", card, len(chrome.launches))
			}
		})
	}
}

func TestBrowserSignInWindowStartingWhileItsTaskChangesIsNotKept(t *testing.T) {
	for _, change := range []string{"revoked", "replaced", "ended"} {
		t.Run(change, func(t *testing.T) {
			service, chrome, _ := signInBoard(t)
			clickSignIn(t, service)
			service.browsers.start = func(ctx context.Context, launch browserlogin.Launch) (browserlogin.Process, error) {
				process, err := chrome.start(ctx, launch)
				switch change {
				case "revoked":
					if err := state.WriteTaskCredentials(service.browsers.state, "pd-keys", nil); err != nil {
						t.Fatal(err)
					}
				case "replaced":
					meta, err := state.ReadTaskMeta(service.browsers.state, "pd-keys")
					if err != nil {
						t.Fatal(err)
					}
					meta.SpawnGen = "new-generation"
					if err := state.WriteTaskMeta(service.browsers.state, meta); err != nil {
						t.Fatal(err)
					}
				case "ended":
					service.browsers.live = func(string) error { return errors.New("task ended") }
				}
				return process, err
			}

			if err := service.processBrowserSignIns(context.Background()); err != nil {
				t.Fatal(err)
			}

			card := service.Store.Snapshot().BrowserSignIns[0]
			if card.State == "open" || len(chrome.endedPIDs(service.browsers)) != 1 {
				t.Fatalf("card = %+v, want the window of a task that changed during startup closed", card)
			}
		})
	}
}

func TestBrowserSignInFailedStartupKeepsItsUnclosedWindowFenced(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	clickSignIn(t, service)
	service.browsers.start = func(ctx context.Context, launch browserlogin.Launch) (browserlogin.Process, error) {
		process, _ := chrome.start(ctx, launch)
		return process, errors.New("readiness and cleanup failed")
	}
	service.browsers.end = func(context.Context, browserlogin.Process) error { return errors.New("cleanup unconfirmed") }
	service.browsers.report = func(error) {}
	t.Cleanup(func() { letGoOfFencedBrowsers(service.browsers) })
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.browsers.work.Wait()
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	_, attachErr := service.browsers.attach(context.Background(), "pd-keys", "stripe")
	if card.State != "ready" || !strings.Contains(card.Reason, "still closing") || len(chrome.launches) != 1 || len(service.browsers.processes) != 1 || attachErr == nil {
		t.Fatalf("card = %+v, launches %d, owned %d, attach %v, want the unclosed window's profile fenced", card, len(chrome.launches), len(service.browsers.processes), attachErr)
	}
}

func TestBrowserSignInWindowWhoseCloseFailsStaysFencedAndIsReportedOnce(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	clickSignIn(t, service)
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	var reported sync.Mutex
	reports := 0
	service.browsers.report = func(error) {
		reported.Lock()
		reports++
		reported.Unlock()
	}
	service.browsers.end = func(context.Context, browserlogin.Process) error { return errors.New("cleanup unconfirmed") }
	t.Cleanup(func() { letGoOfFencedBrowsers(service.browsers) })
	chrome.mu.Lock()
	chrome.alive[1] = false
	chrome.mu.Unlock()

	for range 3 {
		if err := service.processBrowserSignIns(context.Background()); err != nil {
			t.Fatalf("a close left to the broker failed the board's tick: %v", err)
		}
		service.browsers.work.Wait()
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	reported.Lock()
	defer reported.Unlock()
	if reports != 1 || card.State != "ready" || !strings.Contains(card.Reason, "still closing") || len(chrome.launches) != 1 || len(service.browsers.processes) != 1 {
		t.Fatalf("card = %+v, reports %d, launches %d, owned %d, want one report and a fenced profile", card, reports, len(chrome.launches), len(service.browsers.processes))
	}
}

func TestBrowserSignInWindowCancelledBehindTheBrokerLockStartsNothing(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	clickSignIn(t, service)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	service.browsers.mu.Lock()
	answer := make(chan error, 1)
	go func() { answer <- service.processBrowserSignIns(ctx) }()
	<-ctx.Done()
	service.browsers.mu.Unlock()

	select {
	case <-answer:
		if len(chrome.launches) != 0 {
			t.Fatalf("launches = %d, want none after the keeper's context ended", len(chrome.launches))
		}
	case <-time.After(time.Second):
		t.Fatal("the cancelled tick did not return")
	}
}
