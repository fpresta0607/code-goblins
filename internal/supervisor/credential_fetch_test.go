package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestARequestAGoblinNeedsFromAReviewedPageOffersAFetchOnItsCard(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	declareLogin(t, b.broker, "PrecisionDocs-AI", auth.BrowserLogin{Provider: "stripe", Account: "PrecisionDocs"})

	// Act
	declared := mustAccept(b, func(r *CredentialRequest) { r.Names = []string{"STRIPE_TEST_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"} })
	unasked := mustAccept(b, func(r *CredentialRequest) { r.ID, r.Identity, r.Task = "cred-1111111111111111", strings.Repeat("e", 64), "" })
	unreviewed := mustAccept(b, func(r *CredentialRequest) { r.ID, r.Identity, r.Link = "cred-2222222222222222", strings.Repeat("f", 64), "https://dashboard.stripe.com/apikeys" })

	// Assert
	if offer := b.request.Fetch; offer == nil || offer.Provider != "Stripe" || offer.Login != "PrecisionDocs-AI" || !slices.Equal(offer.Names, []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}) {
		t.Fatalf("offer = %+v, want both Stripe test keys on the project's own login", b.request.Fetch)
	}
	if offer := declared.Fetch; offer == nil || offer.Login != "PrecisionDocs" || !slices.Equal(offer.Names, []string{"STRIPE_TEST_SECRET_KEY"}) {
		t.Fatalf("offer = %+v, want the one key the recipe reads on the login the project names", declared.Fetch)
	}
	if unasked.Fetch != nil || unreviewed.Fetch != nil {
		t.Fatalf("offers = %+v, %+v, want none for a request no goblin needs or no recipe reads", unasked.Fetch, unreviewed.Fetch)
	}
}

// fetchClick posts the card's Fetch it for me the way the board's page on
// this PC does, after change.
func fetchClick(t *testing.T, b *fetchBoard, generation string, change func(*http.Request)) int {
	t.Helper()
	handler := NewHTTP(b.service, credentialBoardHost, nil)
	request := httptest.NewRequest(http.MethodPost, "http://"+credentialBoardHost+"/api/credentials/fetch", strings.NewReader(fmt.Sprintf(`{"id":%q,"generation":%q}`, b.request.ID, generation)))
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://"+credentialBoardHost)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Header.Set("Content-Type", "application/json")
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

func TestTheCardsFetchStoresTheKeysOnHisClickOnThisPC(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	b.signedIn("acct_fixture")

	// Act
	code := fetchClick(t, b, b.request.Generation, nil)
	b.service.credentialWork.Wait()

	// Assert
	if code != http.StatusAccepted {
		t.Fatalf("click = %d, want the fetch started", code)
	}
	request := b.stored()
	if request.State != "saved" || !slices.Equal(request.Fetched, []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}) || request.Fetch.State != "" || request.Fetch.Reason != "" {
		t.Fatalf("request = %+v, fetch = %+v, want both keys fetched and the card settled", request, request.Fetch)
	}
	if leaks := b.leaked(); len(leaks) != 0 {
		t.Fatalf("a fetched key reached %v", leaks)
	}
}

func TestTheCardsFetchIsRefusedWhereItMustNotStart(t *testing.T) {
	for _, test := range []struct {
		name       string
		generation func(*fetchBoard) string
		change     func(*http.Request)
		code       int
	}{
		{"another PC", func(b *fetchBoard) string { return b.request.Generation }, func(r *http.Request) { r.RemoteAddr = "100.101.102.103:50000" }, http.StatusForbidden},
		{"a proxy", func(b *fetchBoard) string { return b.request.Generation }, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "100.101.102.103") }, http.StatusForbidden},
		{"a card of an earlier request", func(*fetchBoard) string { return "stale" }, nil, http.StatusConflict},
		{"a request no recipe reads", func(b *fetchBoard) string {
			b.request = mustAccept(b, func(r *CredentialRequest) { r.Link = "https://dashboard.stripe.com/apikeys" })
			return b.request.Generation
		}, nil, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newFetchBoard(t)
			b.signedIn("acct_fixture")
			generation := test.generation(b)

			// Act
			code := fetchClick(t, b, generation, test.change)
			b.service.credentialWork.Wait()

			// Assert
			if code != test.code || len(b.reads) != 0 || len(b.chrome.launches) != 0 {
				t.Fatalf("click = %d, reads %d, launches %d, want %d and nothing started", code, len(b.reads), len(b.chrome.launches), test.code)
			}
		})
	}
}

func TestACardFetchThatStoppedSaysWhyOnTheCard(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(*fetchBoard)
		want    string
	}{
		{"a page that changed", func(b *fetchBoard) { b.signedIn("acct_fixture"); b.pageErr = browserlogin.ErrPageChanged }, "the page changed"},
		{"a read that failed", func(b *fetchBoard) { b.signedIn("acct_fixture"); b.pageErr = errors.New("the key stayed hidden or masked on the page, so nothing was read") }, "the key stayed hidden"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newFetchBoard(t)
			test.arrange(b)

			// Act
			code := fetchClick(t, b, b.request.Generation, nil)
			b.service.credentialWork.Wait()

			// Assert
			request := b.stored()
			if code != http.StatusAccepted || request.State != "open" || request.Fetch.State != "" || !strings.Contains(request.Fetch.Reason, test.want) {
				t.Fatalf("click = %d, request = %+v, fetch = %+v, want it open saying %q", code, request, request.Fetch, test.want)
			}
			pending, err := wake.Pending(b.home.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || !strings.Contains(pending[0].Detail, "the fetch his card started for "+b.request.ID+" stopped") || !strings.Contains(pending[0].Detail, test.want) {
				t.Fatalf("wakes = %+v, want the CFO told why the fetch stopped", pending)
			}
		})
	}
}

func TestACardFetchMeetingASignInLeavesHisSignInCardToSaySo(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(*fetchBoard)
	}{
		{"a page that asks for a sign-in", func(b *fetchBoard) { b.signedIn("acct_fixture"); b.pageErr = browserlogin.ErrSignedOut }},
		{"a login he never signed in to", func(*fetchBoard) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newFetchBoard(t)
			test.arrange(b)

			// Act
			code := fetchClick(t, b, b.request.Generation, nil)
			b.service.credentialWork.Wait()

			// Assert
			request := b.stored()
			cards := b.service.Store.Snapshot().BrowserSignIns
			if code != http.StatusAccepted || request.Fetch.State != "" || request.Fetch.Reason != "" || len(cards) != 1 || cards[0].Fetch != b.request.ID {
				t.Fatalf("request fetch = %+v, cards = %+v, want the card quiet and his sign-in card carrying the fetch", request.Fetch, cards)
			}
		})
	}
}

func TestAStartingSupervisorSaysACardsFetchStopped(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	if _, err := b.service.Store.updateCredential(b.request.ID, func(r *CredentialRequest) { r.Fetch.State = "fetching" }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	b.service.keepBrowsers(ctx)

	// Assert
	if fetch := b.stored().Fetch; fetch.State != "" || !strings.Contains(fetch.Reason, "restarted") {
		t.Fatalf("fetch = %+v, want it stopped and saying so", fetch)
	}
}
