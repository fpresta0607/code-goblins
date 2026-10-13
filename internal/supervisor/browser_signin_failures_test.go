package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	service.browsers.signedIn = func(context.Context, string, keypages.Provider) (bool, error) {
		return false, errors.New("private-page-text")
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
