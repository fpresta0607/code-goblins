package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
)

func TestBrowserSignInRepeatedFailureIssuesAFreshClickGeneration(t *testing.T) {
	service, _, _ := signInBoard(t)
	service.browsers.memory = func() (Memory, error) { return Memory{}, nil }
	if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		card := service.Store.Snapshot().BrowserSignIns[0]
		if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
			t.Fatal(err)
		}

		if err := service.processBrowserSignIns(context.Background()); err != nil {
			t.Fatal(err)
		}

		retry := service.Store.Snapshot().BrowserSignIns[0]
		if retry.State != "ready" || retry.Generation == card.Generation || retry.Reason == "" {
			t.Fatal("a failed attempt did not give the board a fresh retry")
		}
		if err := service.openBrowserSignIn(card.ID, card.Generation); err == nil {
			t.Fatal("a stale retry opened the window")
		}
	}
}

func TestBrowserSignInSuccessfulCloseSurvivesACardSaveFailure(t *testing.T) {
	for _, save := range []string{"verification", "completion", "completion after restart"} {
		t.Run(save, func(t *testing.T) {
			service, chrome, isSignedIn := signInBoard(t)
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
			*isSignedIn = true
			var restore func()
			if save == "verification" {
				restore = blockStoreWrites(t, service.Store)
			} else {
				service.browsers.end = func(ctx context.Context, process browserlogin.Process) error {
					if err := chrome.end(ctx, process); err != nil {
						return err
					}
					restore = blockStoreWrites(t, service.Store)
					return nil
				}
			}
			err := service.processBrowserSignIns(context.Background())
			if restore == nil {
				t.Fatal("the sign-in window was never closed")
			}
			restore()
			service.browsers.end = chrome.end
			if err == nil {
				t.Fatal("a failed card save was reported as successful")
			}
			if save == "verification" && len(chrome.endedPIDs(service.browsers)) != 0 {
				t.Fatal("the window closed before verification was persisted")
			}
			if save != "verification" {
				*isSignedIn = false
			}
			if save == "completion after restart" {
				service.Store, err = Open(service.Store.Home)
				if err != nil {
					t.Fatal(err)
				}
			}

			if err := service.processBrowserSignIns(context.Background()); err != nil {
				t.Fatal(err)
			}

			card = service.Store.Snapshot().BrowserSignIns[0]
			if card.State != "signed_in" || card.ClosedAt == nil || len(chrome.endedPIDs(service.browsers)) != 1 {
				t.Fatalf("card = %+v, want the successful sign-in recovered without another window", card)
			}
		})
	}
}

func TestBrowserSignInClickBoundsTheWindowLifetime(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	now := service.browsers.now()
	service.browsers.now = func() time.Time { return now }
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
	now = now.Add(browserLease)

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	card = service.Store.Snapshot().BrowserSignIns[0]
	if card.State != "expired" || card.ClosedAt == nil || len(chrome.endedPIDs(service.browsers)) != 1 {
		t.Fatalf("card = %+v, want the sign-in window closed after its lease", card)
	}
}

func TestBrowserSignInDeduplicatesByLoginInsteadOfProviderLabel(t *testing.T) {
	service, _, _ := signInBoard(t)
	provider := service.browsers.recipes.Providers[0]
	provider.ID = "anotherprovider"
	service.browsers.recipes.Providers = append(service.browsers.recipes.Providers, provider)
	first, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"})
	if err != nil {
		t.Fatal(err)
	}

	second, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: provider.ID})

	if err != nil || first.Login == second.Login || first.SignIn == second.SignIn {
		t.Fatalf("separate providers reused a sign-in: %+v, %+v, %v", first, second, err)
	}
}

func TestBrowserSignInIsHeldForThePersonUntilItCompletes(t *testing.T) {
	for _, state := range []string{"ready", "opening", "open", "signed_in", "expired"} {
		t.Run(state, func(t *testing.T) {
			isWaiting := state == "ready" || state == "opening" || state == "open"
			database := Database{BrowserSignIns: []BrowserSignIn{{ID: "signin-test", Task: "pd-keys", Provider: "Stripe", Account: "PrecisionDocs-AI", Project: "PrecisionDocs-AI", State: state}}}

			held := waitingOnOverlord(database)
			waiting, outcome := heldNow(database, "browser-signin:signin-test", time.Time{})

			if (len(held) == 1) != isWaiting || waiting != isWaiting {
				t.Fatalf("state %s held = %+v, waiting = %t", state, held, waiting)
			}
			if isWaiting && (held[0].key() != "browser-signin:signin-test" || held[0].task != "pd-keys") {
				t.Fatalf("held = %+v, want this task's sign-in", held)
			}
			if state == "signed_in" && !strings.Contains(outcome, "signed in") {
				t.Fatalf("outcome = %q, want signed in", outcome)
			}
			if state == "expired" && !strings.Contains(outcome, "expired") {
				t.Fatalf("outcome = %q, want expired", outcome)
			}
		})
	}
}

func TestBrowserSignInSnapshotCannotChangeItsPersistedVerification(t *testing.T) {
	service, _, _ := signInBoard(t)
	if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	verified := service.browsers.now()
	if err := service.Store.updateBrowserSignIn(card.ID, func(current *BrowserSignIn) error {
		current.VerifiedAt = &verified
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	snapshot := service.Store.Snapshot()
	want := verified
	*snapshot.BrowserSignIns[0].VerifiedAt = verified.Add(time.Hour)

	if got := service.Store.Snapshot().BrowserSignIns[0].VerifiedAt; got == nil || !got.Equal(want) {
		t.Fatal("a snapshot changed the stored sign-in verification")
	}
}

func TestBrowserSignInCapacityKeepsOpenCardsAndMakesRoomAmongCompletedOnes(t *testing.T) {
	for _, isClosed := range []bool{false, true} {
		t.Run(fmt.Sprint(isClosed), func(t *testing.T) {
			service, _, _ := signInBoard(t)
			now := service.browsers.now()
			for index := range 64 {
				card := BrowserSignIn{ID: fmt.Sprintf("older-%d", index), Login: fmt.Sprintf("other-%d", index), State: "ready", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
				if isClosed {
					card.State, card.ClosedAt = "signed_in", &now
				}
				service.Store.db.BrowserSignIns = append(service.Store.db.BrowserSignIns, card)
			}
			if err := service.Store.save(); err != nil {
				t.Fatal(err)
			}

			lease, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"})

			if (err == nil) != isClosed || len(service.Store.Snapshot().BrowserSignIns) != 64 {
				t.Fatalf("request = %v, want room only when a completed card can leave", err)
			}
			if isClosed && lease.SignIn == "" {
				t.Fatal("the new sign-in was not filed")
			}
		})
	}
}

func TestBrowserSignInDoesNotOpenTwiceAfterAStartupSaveFailure(t *testing.T) {
	service, chrome, _ := signInBoard(t)
	if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	card := service.Store.Snapshot().BrowserSignIns[0]
	if err := service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	var restore func()
	service.browsers.start = func(ctx context.Context, launch browserlogin.Launch) (browserlogin.Process, error) {
		process, err := chrome.start(ctx, launch)
		restore = blockStoreWrites(t, service.Store)
		return process, err
	}
	err := service.processBrowserSignIns(context.Background())
	if restore == nil {
		t.Fatal("no browser started")
	}
	restore()
	service.browsers.start = chrome.start
	if err == nil {
		t.Fatal("the failed save was hidden")
	}

	if err := service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(chrome.launches) != 1 || service.Store.Snapshot().BrowserSignIns[0].State != "open" {
		t.Fatal("recovering the open card started another browser")
	}
}
