package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const fetchLogin = "stripe-precisiondocs-ai"

// fetchBoard is a board with an open request of goblin pd-keys for its
// project's Stripe test keys, a file credential store outside the home, the
// repository's reviewed Stripe recipe, and a fake Chrome whose key page the
// board's page stands in for.
type fetchBoard struct {
	t       *testing.T
	service *Service
	broker  *browserBroker
	chrome  *fakeChrome
	gone    map[string]bool
	home    home.Home
	vault   string
	request CredentialRequest
	mu      sync.Mutex
	// page is what the stand-in key page shows, and pageErr how it fails.
	page    map[string]string
	pageErr error
	reads   []fetchRead
	// reading, when set, holds each read until it is closed.
	reading chan struct{}
}

// fetchRead is one read of the stand-in key page: the browser it ran in,
// the account it read on, and the keys it read.
type fetchRead struct {
	URL, Account string
	Names        []string
}

func newFetchBoard(t *testing.T, names ...string) *fetchBoard {
	t.Helper()
	store, h := testStore(t)
	broker, chrome, gone, _ := testBroker(t)
	broker.state = h.State
	recipes, err := keypages.Parse(codegoblins.KeyPages)
	if err != nil {
		t.Fatal(err)
	}
	broker.recipes = recipes
	b := &fetchBoard{t: t, broker: broker, chrome: chrome, gone: gone, home: h, vault: t.TempDir(), page: map[string]string{
		"STRIPE_TEST_PUBLISHABLE_KEY": "pk" + "_test_" + strings.TrimPrefix(newCanary(t), "sk_test_"),
		"STRIPE_TEST_SECRET_KEY":      newCanary(t),
	}}
	broker.readKeys = func(_ context.Context, url string, _ keypages.Provider, account string, keys []keypages.Key) (map[string]string, error) {
		b.mu.Lock()
		read := fetchRead{URL: url, Account: account}
		for _, key := range keys {
			read.Names = append(read.Names, key.Name)
		}
		b.reads = append(b.reads, read)
		reading, pageErr := b.reading, b.pageErr
		b.mu.Unlock()
		if reading != nil {
			<-reading
		}
		if pageErr != nil {
			return nil, pageErr
		}
		values := map[string]string{}
		for _, name := range read.Names {
			values[name] = b.page[name]
		}
		return values, nil
	}
	broker.signedIn = func(context.Context, string, keypages.Provider) (bool, string, error) {
		return true, "acct_fixture", nil
	}
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	t.Setenv(auth.StoreDirEnv, b.vault)
	b.service = &Service{Store: store, Instance: "test-instance", browsers: broker, Options: Options{
		Credentials:        auth.OpenStore,
		RefreshCredentials: func(context.Context, string) ([]string, error) { return []string{"pd-keys"}, nil },
	}}
	if len(names) == 0 {
		names = []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}
	}
	request, err := b.service.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Task: "pd-keys", Project: "PrecisionDocs-AI", Names: names, Why: "Charge test cards in the checkout tests", Link: "https://dashboard.stripe.com/test/apikeys"})
	if err != nil {
		t.Fatal(err)
	}
	b.request = request
	t.Cleanup(func() {
		if err := broker.closeAll(); err != nil {
			t.Error(err)
		}
		b.service.credentialWork.Wait()
	})
	return b
}

// signedIn records the project's Stripe login signed in on account.
func (b *fetchBoard) signedIn(account string) {
	b.t.Helper()
	now := time.Now()
	if err := browserlogin.Write(b.broker.state, []browserlogin.Login{{ID: fetchLogin, Provider: "stripe", Account: "PrecisionDocs-AI", AccountID: account, Projects: []string{"PrecisionDocs-AI"}, SignedInAt: &now}}); err != nil {
		b.t.Fatal(err)
	}
}

func (b *fetchBoard) fetch() (BrowserLease, error) {
	b.t.Helper()
	return b.service.fetchCredential(context.Background(), BrowserRequest{Action: "fetch", Task: "pd-keys", Request: b.request.ID})
}

func (b *fetchBoard) vaultValue(name string) (string, bool) {
	b.t.Helper()
	data, err := os.ReadFile(filepath.Join(b.vault, "PrecisionDocs-AI", name))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		b.t.Fatal(err)
	}
	return string(data), true
}

func (b *fetchBoard) stored() CredentialRequest {
	b.t.Helper()
	request, found := b.service.Store.credential(b.request.ID)
	if !found {
		b.t.Fatal("the board's request is gone")
	}
	return request
}

// leaked names every place outside the vault the page's keys reached: a
// file of the home, the fetch's answer, or a notice to the CFO.
func (b *fetchBoard) leaked(answers ...any) []string {
	b.t.Helper()
	var found []string
	for _, value := range b.page {
		found = append(found, filesHolding(b.t, b.home.Root, value)...)
		for _, answer := range answers {
			data, err := json.Marshal(answer)
			if err != nil {
				b.t.Fatal(err)
			}
			if strings.Contains(string(data), value) {
				found = append(found, "the fetch's answer")
			}
		}
		pending, err := wake.Pending(b.home.State)
		if err != nil {
			b.t.Fatal(err)
		}
		for _, record := range pending {
			if strings.Contains(record.Detail, value) {
				found = append(found, "a notice to the CFO")
			}
		}
	}
	return found
}

func TestAFetchStoresTheRequestsKeysInTheVaultAndAnswersOnlyTheirNames(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	b.signedIn("acct_fixture")

	// Act
	lease, err := b.fetch()
	b.service.credentialWork.Wait()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(lease.Fetched, []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}) || lease.URL != "" || lease.SignIn != "" || lease.Login != fetchLogin {
		t.Fatalf("lease = %+v, want the two names stored and no browser address", lease)
	}
	for name, value := range b.page {
		if got, found := b.vaultValue(name); !found || got != value {
			t.Fatalf("%s did not reach the project's vault", name)
		}
	}
	if request := b.stored(); request.State != "saved" || !slices.Equal(request.Fetched, []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}) {
		t.Fatalf("request = %+v, want it saved with both names fetched", request)
	}
	if len(b.chrome.launches) != 1 || !b.chrome.launches[0].Headless || !slices.Equal(b.chrome.launches[0].Hosts, b.broker.recipes.Providers[0].Hosts) {
		t.Fatalf("launches = %+v, want one headless Chrome held to Stripe's hosts", b.chrome.launches)
	}
	if len(b.reads) != 1 || b.reads[0].URL != "http://127.0.0.1:9001" || b.reads[0].Account != "acct_fixture" {
		t.Fatalf("reads = %+v, want one read in the started Chrome on the login's account", b.reads)
	}
	if !slices.Equal(b.chrome.endedPIDs(b.broker), []int{1}) || registered(t, b.broker)[0].Browser != nil {
		t.Fatal("the fetch's Chrome was not closed and recorded given back")
	}
	if leaks := b.leaked(lease); len(leaks) != 0 {
		t.Fatalf("a fetched key reached %v", leaks)
	}
	pending, err := wake.Pending(b.home.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].Detail, "STRIPE_TEST_PUBLISHABLE_KEY, STRIPE_TEST_SECRET_KEY fetched from the key page into PrecisionDocs-AI's vault") || !strings.Contains(pending[0].Detail, "told pd-keys to re-source auth.ps1") {
		t.Fatalf("wakes = %+v, want one notice naming the fetched names and the refresh", pending)
	}
}

func TestAFetchLeavesNamesTheRecipeDoesNotReadOrTheVaultHoldsForTheCard(t *testing.T) {
	// Arrange
	b := newFetchBoard(t, "STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
	b.signedIn("acct_fixture")
	if _, err := b.service.Store.updateCredential(b.request.ID, func(r *CredentialRequest) { r.Existing = []string{"STRIPE_TEST_PUBLISHABLE_KEY"} }); err != nil {
		t.Fatal(err)
	}

	// Act
	lease, err := b.fetch()
	b.service.credentialWork.Wait()

	// Assert
	if err != nil || !slices.Equal(lease.Fetched, []string{"STRIPE_TEST_SECRET_KEY"}) {
		t.Fatalf("lease = %+v, %v, want only the secret key fetched", lease, err)
	}
	if len(b.reads) != 1 || !slices.Equal(b.reads[0].Names, []string{"STRIPE_TEST_SECRET_KEY"}) {
		t.Fatalf("reads = %+v, want the page read for the one key still needed", b.reads)
	}
	if request := b.stored(); request.State != "open" {
		t.Fatalf("request = %+v, want it open for the webhook secret", request)
	}
}

func TestAFetchForALoginHeNeverSignedInToFilesHisSignInCardAndStartsNoBrowser(t *testing.T) {
	for _, test := range []struct {
		name   string
		logins []browserlogin.Login
	}{
		{"no login yet", nil},
		{"a login made but never signed in", []browserlogin.Login{{ID: fetchLogin, Provider: "stripe", Account: "PrecisionDocs-AI", Projects: []string{"PrecisionDocs-AI"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newFetchBoard(t)
			if err := browserlogin.Write(b.broker.state, test.logins); err != nil {
				t.Fatal(err)
			}

			// Act
			lease, err := b.fetch()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			cards := b.service.Store.Snapshot().BrowserSignIns
			if lease.SignIn == "" || len(cards) != 1 || cards[0].ID != lease.SignIn || cards[0].Fetch != b.request.ID || cards[0].Task != "pd-keys" || cards[0].State != "ready" {
				t.Fatalf("lease = %+v, cards = %+v, want his sign-in card carrying the fetch", lease, cards)
			}
			if len(b.chrome.launches) != 0 || len(b.reads) != 0 {
				t.Fatal("a fetch for a login never signed in started a browser")
			}
		})
	}
}

func TestAFetchMeetingASignInPageFilesHisCardAndClosesItsBrowser(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	b.signedIn("acct_fixture")
	b.pageErr = browserlogin.ErrSignedOut

	// Act
	lease, err := b.fetch()

	// Assert
	if err != nil || lease.SignIn == "" || len(lease.Fetched) != 0 {
		t.Fatalf("lease = %+v, %v, want his sign-in card and nothing fetched", lease, err)
	}
	if !slices.Equal(b.chrome.endedPIDs(b.broker), []int{1}) {
		t.Fatal("the fetch's Chrome was not closed")
	}
	if _, found := b.vaultValue("STRIPE_TEST_SECRET_KEY"); found {
		t.Fatal("a signed-out page stored a key")
	}
}

func TestAFetchCarriesOnByItselfOnceHeSignsIn(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	lease, err := b.fetch()
	if err != nil || lease.SignIn == "" {
		t.Fatalf("lease = %+v, %v, want his sign-in card", lease, err)
	}
	card := b.service.Store.Snapshot().BrowserSignIns[0]
	if err := b.service.openBrowserSignIn(card.ID, card.Generation); err != nil {
		t.Fatal(err)
	}
	if err := b.service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := b.service.processBrowserSignIns(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.service.credentialWork.Wait()

	// Assert
	logins := registered(t, b.broker)
	if len(logins) != 1 || logins[0].AccountID != "acct_fixture" || logins[0].SignedInAt == nil {
		t.Fatalf("logins = %+v, want his sign-in and its account recorded", logins)
	}
	if card := b.service.Store.Snapshot().BrowserSignIns[0]; card.State != "signed_in" {
		t.Fatalf("card = %+v, want it signed in", card)
	}
	if len(b.chrome.launches) != 2 || b.chrome.launches[0].Headless || !b.chrome.launches[1].Headless || len(b.reads) != 1 || b.reads[0].Account != "acct_fixture" {
		t.Fatalf("launches = %+v, reads = %+v, want his window, then a headless fetch on his account", b.chrome.launches, b.reads)
	}
	for name, value := range b.page {
		if got, found := b.vaultValue(name); !found || got != value {
			t.Fatalf("%s did not reach the vault after his sign-in", name)
		}
	}
	if leaks := b.leaked(); len(leaks) != 0 {
		t.Fatalf("a fetched key reached %v", leaks)
	}
}

func TestAFetchRefusesWhatItMustNotReadAndStoresNothing(t *testing.T) {
	for _, test := range []struct {
		name       string
		change     func(*fetchBoard)
		isLaunched bool
	}{
		{"a request another task asked for", func(b *fetchBoard) {
			brokerTask(t, b.broker, "pd-other", "PrecisionDocs-AI")
			b.request.ID = mustAccept(b, func(r *CredentialRequest) { r.Task = "pd-other" }).ID
		}, false},
		{"a request of another project", func(b *fetchBoard) {
			b.request.ID = mustAccept(b, func(r *CredentialRequest) { r.Task, r.Project = "", "Clock-In" }).ID
		}, false},
		{"a request no longer open", func(b *fetchBoard) {
			if _, err := b.service.Store.updateCredential(b.request.ID, func(r *CredentialRequest) { r.ExpiresAt = time.Now().Add(-time.Minute) }); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a page no recipe reads", func(b *fetchBoard) {
			b.request.ID = mustAccept(b, func(r *CredentialRequest) { r.Link = "https://dashboard.stripe.com/apikeys" }).ID
		}, false},
		{"a task without the provider's service", func(b *fetchBoard) {
			meta, err := state.ReadTaskMeta(b.broker.state, "pd-keys")
			if err != nil {
				t.Fatal(err)
			}
			meta.Credentials = []string{"github"}
			if err := state.WriteTaskMeta(b.broker.state, meta); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a task that left", func(b *fetchBoard) { b.gone["pd-keys"] = true }, false},
		{"a login whose sign-in named no account", func(b *fetchBoard) { b.signedIn("") }, false},
		{"a login lent to a goblin's browser", func(b *fetchBoard) {
			if _, err := b.broker.attach(context.Background(), "pd-keys", "stripe"); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a page on another account", func(b *fetchBoard) {
			b.pageErr = errors.New("the key page shows another account than the login's, so nothing was read")
		}, true},
		{"a masked read", func(b *fetchBoard) { b.page["STRIPE_TEST_SECRET_KEY"] = "sk_test_••••••••4abc" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newFetchBoard(t)
			b.signedIn("acct_fixture")
			test.change(b)
			launched := len(b.chrome.launches)

			// Act
			lease, err := b.fetch()
			b.service.credentialWork.Wait()

			// Assert
			if err == nil || len(lease.Fetched) != 0 || lease.SignIn != "" {
				t.Fatalf("lease = %+v, %v, want a refusal", lease, err)
			}
			for name := range b.page {
				if _, found := b.vaultValue(name); found {
					t.Fatalf("%s was stored by a refused fetch", name)
				}
			}
			if isLaunched := len(b.chrome.launches) > launched; isLaunched != test.isLaunched {
				t.Fatalf("launches = %d after %d, want a browser started only for a read", len(b.chrome.launches), launched)
			}
			if leaks := b.leaked(lease, err.Error()); len(leaks) != 0 {
				t.Fatalf("a key from the page reached %v", leaks)
			}
		})
	}
}

// mustAccept files a second request like the board's, as change makes it.
func mustAccept(b *fetchBoard, change func(*CredentialRequest)) CredentialRequest {
	b.t.Helper()
	request := CredentialRequest{ID: "cred-fedcba9876543210", Identity: strings.Repeat("d", 64), By: "cfo", Task: "pd-keys", Project: "PrecisionDocs-AI", Names: []string{"STRIPE_TEST_SECRET_KEY"}, Why: "Charge test cards in the checkout tests", Link: "https://dashboard.stripe.com/test/apikeys"}
	change(&request)
	accepted, err := b.service.acceptCredentialRequest(request)
	if err != nil {
		b.t.Fatal(err)
	}
	return accepted
}

func TestNoTaskBorrowsALoginWhileAFetchDrivesIt(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	b.signedIn("acct_fixture")
	b.reading = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := b.fetch()
		done <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		b.mu.Lock()
		isReading := len(b.reads) == 1
		b.mu.Unlock()
		if isReading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fetch never began reading")
		}
	}

	// Act
	_, attachErr := b.broker.attach(context.Background(), "pd-keys", "stripe")
	_, signInErr := b.service.requestBrowserSignIn(BrowserRequest{Action: "sign-in", Task: "pd-keys", Provider: "stripe"})
	_, fetchErr := b.fetch()
	close(b.reading)

	// Assert
	if attachErr == nil || signInErr == nil || fetchErr == nil || !strings.Contains(fetchErr.Error(), "a key fetch for pd-keys drives this login's browser now") {
		t.Fatalf("attach = %v, sign-in = %v, second fetch = %v, want each refused while the fetch reads", attachErr, signInErr, fetchErr)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(b.chrome.launches) != 1 {
		t.Fatalf("launches = %d, want only the fetch's", len(b.chrome.launches))
	}
}

func TestAGoblinWithNoBrowserSealCanStillFetch(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	b.signedIn("acct_fixture")
	meta, err := state.ReadTaskMeta(b.broker.state, "pd-keys")
	if err != nil {
		t.Fatal(err)
	}
	meta.Harness = "codex"
	if err := state.WriteTaskMeta(b.broker.state, meta); err != nil {
		t.Fatal(err)
	}

	// Act
	_, attachErr := b.broker.attach(context.Background(), "pd-keys", "stripe")
	lease, err := b.fetch()

	// Assert
	if attachErr == nil {
		t.Fatal("a Codex goblin was lent a login's browser to explore")
	}
	if err != nil || len(lease.Fetched) != 2 {
		t.Fatalf("lease = %+v, %v, want the Codex goblin's fetch stored", lease, err)
	}
}
