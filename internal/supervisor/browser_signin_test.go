package supervisor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/keypages"
)

func signInBoard(t *testing.T) (*Service, *fakeChrome, *bool) {
	t.Helper()
	store, h := testStore(t)
	broker, chrome, _, _ := testBroker(t)
	broker.state = h.State
	broker.recipes.Providers[0].KeyPage = "https://dashboard.stripe.com/test/apikeys"
	broker.recipes.Providers[0].SignedInPath = "^/test/apikeys$"
	isSignedIn := false
	broker.signedIn = func(context.Context, string, keypages.Provider) (bool, string, error) { return isSignedIn, "", nil }
	service := &Service{Store: store, Instance: "test-instance", browsers: broker}
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	t.Cleanup(func() {
		if err := broker.closeAll(); err != nil {
			t.Error(err)
		}
	})
	return service, chrome, &isSignedIn
}

func TestBrowserSignInWindowOpensOnlyForLocalClickAndCompletes(t *testing.T) {
	service, chrome, isSignedIn := signInBoard(t)
	request := BrowserRequest{Action: "sign-in", Task: "pd-keys", Provider: "stripe"}
	lease, err := service.requestBrowserSignIn(request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.requestBrowserSignIn(request)
	if err != nil || again.SignIn != lease.SignIn || lease.URL != "" || len(chrome.launches) != 0 {
		t.Fatalf("filing started a browser or changed a duplicate card: %+v, %+v, %v", lease, again, err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if len(chrome.launches) != 0 {
		t.Fatal("the HTTP action waited for browser startup")
	}

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The window is his own, a normal Chrome he signs in with, so it is not
	// held to the provider's hosts as a goblin's headless browser is.
	if len(chrome.launches) != 1 || chrome.launches[0].Headless || chrome.launches[0].Page != service.browsers.recipes.Providers[0].KeyPage || len(chrome.launches[0].Hosts) != 0 {
		t.Fatalf("launches = %+v, want one normal sign-in window on the reviewed page", chrome.launches)
	}
	card = service.Store.Snapshot().BrowserSignIns[0]
	if card.State != "open" || registered(t, service.browsers)[0].Browser != nil {
		t.Fatal("the window is not open or its address was lent to a task")
	}
	if _, err := service.browsers.attach(context.Background(), "pd-keys", "stripe"); err == nil {
		t.Fatal("a task borrowed the sign-in window")
	}
	*isSignedIn = true

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	if card.State != "signed_in" || card.ClosedAt == nil || len(chrome.endedPIDs(service.browsers)) != 1 {
		t.Fatalf("card = %+v, want signed in after its browser closed", card)
	}
	if _, err := service.browsers.attach(context.Background(), "pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	if !chrome.launches[1].Headless {
		t.Fatal("the login did not return to a headless browser")
	}
}

func TestBrowserSignInWindowCanBeRetriedAfterItCloses(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	if _, err := service.requestBrowserSignIn(BrowserRequest{Action: "sign-in", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, "old"); err == nil {
		t.Fatal("an earlier generation opened the window")
	}
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	chrome.mu.Lock()
	chrome.alive[1] = false
	chrome.mu.Unlock()

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	if card.State != "ready" || card.Reason == "" || card.ClosedAt != nil {
		t.Fatalf("card = %+v, want a retry without success", card)
	}
	// What is left of the closed window is ended in the background before
	// the profile opens again.
	service.browsers.work.Wait()
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(chrome.launches) != 2 {
		t.Fatalf("launches = %d, want 2", len(chrome.launches))
	}
}

func TestBrowserSignInRestartNeedsAnotherClickAndExpiredCardsCannotOpen(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	if _, err := service.requestBrowserSignIn(BrowserRequest{Action: "sign-in", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(service.Store.Home)
	if err != nil {
		t.Fatal(err)
	}
	service.Store = reopened
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	if card.State != "ready" || len(chrome.launches) != 0 {
		t.Fatal("a restart replayed an old click")
	}
	service.browsers.now = func() time.Time { return card.ExpiresAt.Add(time.Second) }
	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.openBrowserSignIn(card.ID, card.Generation); err == nil {
		t.Fatal("an expired card opened a window")
	}
}

func TestBrowserSignInOpenRefusesRemoteAndStaleCards(t *testing.T) {
	store, _ := testStore(t)
	service := &Service{Store: store, Instance: "test-instance"}
	handler := NewHTTP(service, credentialBoardHost, nil)
	for _, test := range []struct {
		name   string
		peer   string
		status int
	}{
		{"remote", "192.0.2.10:8000", http.StatusForbidden},
		{"stale card", "127.0.0.1:8000", http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+credentialBoardHost+"/api/browser-signins/open", strings.NewReader(`{"id":"signin-missing","generation":"old"}`))
			request.RemoteAddr = test.peer
			request.Header.Set("Origin", "http://"+credentialBoardHost)
			request.Header.Set("X-CFO-Token", service.Instance)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}
