package supervisor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/state"
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
			if save != "verification" {
				// A tick kept the verification, as one does before it closes
				// the window, and the page no longer shows him signed in.
				verified := service.browsers.now()
				if err := service.Store.updateBrowserSignIn(card.ID, func(current *BrowserSignIn) error {
					current.VerifiedAt = &verified
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				*isSignedIn = false
			}
			restore := blockStoreWrites(t, service.Store)
			err := service.processBrowserSignIns(context.Background())
			restore()
			if err == nil {
				t.Fatal("a failed card save was reported as successful")
			}
			if save == "verification" && len(chrome.endedPIDs(service.browsers)) != 0 {
				t.Fatal("the window closed before verification was persisted")
			}
			if save != "verification" && len(chrome.endedPIDs(service.browsers)) != 1 {
				t.Fatal("the verified sign-in's window was not closed")
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
	if err := state.WriteTaskCredentials(service.browsers.state, "pd-keys", []string{"stripe", provider.ID}); err != nil {
		t.Fatal(err)
	}
	brokerManifest(t, service.browsers, "PrecisionDocs-AI",
		auth.Service{Name: "stripe", Method: auth.MethodEnv, Env: []string{"STRIPE_TEST_SECRET_KEY"}},
		auth.Service{Name: provider.ID, Method: auth.MethodEnv, Env: []string{"ANOTHERPROVIDER_TEST_SECRET_KEY"}})
	first, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"})
	if err != nil {
		t.Fatal(err)
	}

	second, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: provider.ID})

	if err != nil || first.Login == second.Login || first.SignIn == second.SignIn {
		t.Fatalf("separate providers reused a sign-in: %+v, %+v, %v", first, second, err)
	}
}

// What waits on him, as AFK mode holds it and the CFO's open items list it,
// agrees with his board: a goblin's wait that follows a decision still open
// waits on that decision, which stands for it, and any other open wait is
// held, one whose decision has closed included.
func TestAWaitThatFollowsAnOpenDecisionIsHeldOnlyThroughIt(t *testing.T) {
	for name, test := range map[string]struct {
		status, follows string
		want            []string
	}{
		"its decision still open": {status: "pending", follows: "pd-connector-parity", want: []string{"question:pd-connector-parity"}},
		"its decision answered":   {status: "succeeded", follows: "pd-connector-parity", want: []string{"review:waiting-pd-mcp-only-9"}},
		"a wait following none":   {status: "pending", want: []string{"question:pd-connector-parity", "review:waiting-pd-mcp-only-9"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			database := Database{
				Questions: []Question{{ID: "pd-connector-parity", Text: "Build the parity tools?", Status: test.status, ForTask: "pd-mcp-only"}},
				Reviews:   []Review{{ID: "waiting-pd-mcp-only-9", Task: "pd-mcp-only", Title: "Waiting on you: the parity sizing", State: "open", Follows: test.follows}},
			}

			// Act
			held := waitingOnOverlord(database)

			// Assert
			var got []string
			for _, item := range held {
				got = append(got, item.key())
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("held %q, want %q", got, test.want)
			}
		})
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
