package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// loginsBoard is the fetch board whose goblin pd-keys, of generation g1,
// has its connections shown on the board on this PC, with logins of its
// project, of another project, and one it shares.
func loginsBoard(t *testing.T) (*fetchBoard, *HTTP) {
	t.Helper()
	b := newFetchBoard(t)
	meta, err := state.ReadTaskMeta(b.broker.state, "pd-keys")
	if err != nil {
		t.Fatal(err)
	}
	meta.SpawnGen = "g1"
	if err := state.WriteTaskMeta(b.broker.state, meta); err != nil {
		t.Fatal(err)
	}
	declareLogin(t, b.broker, "PrecisionDocs-AI", auth.BrowserLogin{Provider: "stripe", Account: "SIQstack", SignsOutAfterFetch: true})
	signedIn, used := time.Now().Add(-2*time.Hour), time.Now().Add(-5*time.Minute)
	if err := browserlogin.Write(b.broker.state, []browserlogin.Login{
		{ID: "stripe-precisiondocs-ai", Provider: "stripe", Account: "PrecisionDocs-AI", Projects: []string{"PrecisionDocs-AI"}, AccountID: "acct_fixture", SignedInAt: &signedIn, UsedAt: &used},
		{ID: "stripe-siqstack", Provider: "stripe", Account: "SIQstack", Projects: []string{"SIQshift", "PrecisionDocs-AI"}, IsShared: true, UsedAt: &used},
		{ID: "stripe-clock-in", Provider: "stripe", Account: "Clock-In", Projects: []string{"Clock-In"}, SignedInAt: &signedIn},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stripe-precisiondocs-ai", "stripe-siqstack", "stripe-clock-in"} {
		profile, err := browserlogin.MakeProfile(b.broker.state, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profile, "Cookies"), []byte("a synthetic session"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checks := connections.NewCache(time.Minute, time.Second, func(context.Context, string) connections.Snapshot {
		return connections.Snapshot{Entries: []connections.Entry{}}
	})
	t.Cleanup(checks.Close)
	b.service.connectionChecks = checks
	return b, NewHTTP(b.service, credentialBoardHost, nil)
}

// boardLogins sends a request the board's page on this PC sends, after
// change, and decodes the logins it answers with.
func boardLogins(t *testing.T, handler *HTTP, method, path, body string, change func(*http.Request)) (int, []browserLoginEntry) {
	t.Helper()
	request := httptest.NewRequest(method, "http://"+credentialBoardHost+path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://"+credentialBoardHost)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Header.Set("Content-Type", "application/json")
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var answer struct {
		BrowserLogins []browserLoginEntry `json:"browser_logins"`
	}
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
	}
	return response.Code, answer.BrowserLogins
}

func TestAGoblinsConnectionsListTheLoginsItsProjectMayUse(t *testing.T) {
	// Arrange
	b, handler := loginsBoard(t)

	// Act
	code, logins := boardLogins(t, handler, http.MethodGet, "/api/connections?task=pd-keys&generation=g1", "", nil)

	// Assert
	if code != http.StatusOK || len(logins) != 2 {
		t.Fatalf("connections = %d, %+v, want the project's two logins", code, logins)
	}
	own, shared := logins[0], logins[1]
	if own.ID != "stripe-precisiondocs-ai" || own.Provider != "Stripe" || !own.IsSignedIn || own.SignedInAt == nil || own.UsedAt == nil || own.SignsOutAfterFetch || own.IsInUse {
		t.Fatalf("own login = %+v, want it signed in, used and idle", own)
	}
	if shared.ID != "stripe-siqstack" || shared.IsSignedIn || !shared.SignsOutAfterFetch || strings.Join(shared.Projects, ",") != "SIQshift,PrecisionDocs-AI" {
		t.Fatalf("shared login = %+v, want it signed out, shared and signing out after each fetch", shared)
	}
	if _, err := b.broker.attach(context.Background(), "pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	if _, logins := boardLogins(t, handler, http.MethodGet, "/api/connections?task=pd-keys&generation=g1", "", nil); logins[0].IsInUse || !logins[1].IsInUse {
		t.Fatalf("logins = %+v, want the shared login it names lent and shown in use", logins)
	}
}

func TestSignOutEndsTheSessionAndKeepsTheLoginWhileForgetDropsIt(t *testing.T) {
	// Arrange
	b, handler := loginsBoard(t)
	profile := browserlogin.ProfileDir(b.broker.state, "stripe-precisiondocs-ai")

	// Act
	signedOut, afterSignOut := boardLogins(t, handler, http.MethodPost, "/api/browser-logins/sign-out", `{"task":"pd-keys","generation":"g1","login":"stripe-precisiondocs-ai"}`, nil)
	_, profileErr := os.Stat(profile)
	forgot, afterForget := boardLogins(t, handler, http.MethodPost, "/api/browser-logins/forget", `{"task":"pd-keys","generation":"g1","login":"stripe-siqstack"}`, nil)

	// Assert
	if signedOut != http.StatusOK || !os.IsNotExist(profileErr) || len(afterSignOut) != 2 || afterSignOut[0].IsSignedIn {
		t.Fatalf("sign out = %d, %+v, profile %v, want the session gone and the login kept", signedOut, afterSignOut, profileErr)
	}
	if forgot != http.StatusOK || len(afterForget) != 1 || afterForget[0].ID != "stripe-precisiondocs-ai" {
		t.Fatalf("forget = %d, %+v, want the shared login gone", forgot, afterForget)
	}
	if _, err := os.Stat(browserlogin.ProfileDir(b.broker.state, "stripe-siqstack")); !os.IsNotExist(err) {
		t.Fatal("the forgotten login's profile is still there")
	}
	if _, err := os.Stat(filepath.Join(browserlogin.ProfileDir(b.broker.state, "stripe-clock-in"), "Cookies")); err != nil {
		t.Fatalf("another project's session was touched: %v", err)
	}
}

func TestSignOutAndForgetAreRefusedWhereTheyMustNotAct(t *testing.T) {
	for _, test := range []struct {
		name, path, body string
		arrange          func(*fetchBoard)
		change           func(*http.Request)
		code             int
	}{
		{"another PC", "/api/browser-logins/forget", `{"task":"pd-keys","generation":"g1","login":"stripe-precisiondocs-ai"}`, nil, func(r *http.Request) { r.RemoteAddr = "100.101.102.103:50000" }, http.StatusForbidden},
		{"a proxy", "/api/browser-logins/sign-out", `{"task":"pd-keys","generation":"g1","login":"stripe-precisiondocs-ai"}`, nil, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "100.101.102.103") }, http.StatusForbidden},
		{"a restarted goblin", "/api/browser-logins/forget", `{"task":"pd-keys","generation":"g0","login":"stripe-precisiondocs-ai"}`, nil, nil, http.StatusConflict},
		{"another project's login", "/api/browser-logins/forget", `{"task":"pd-keys","generation":"g1","login":"stripe-clock-in"}`, nil, nil, http.StatusConflict},
		{"a login in use", "/api/browser-logins/sign-out", `{"task":"pd-keys","generation":"g1","login":"stripe-siqstack"}`, func(b *fetchBoard) {
			if _, err := b.broker.attach(context.Background(), "pd-keys", "stripe"); err != nil {
				b.t.Fatal(err)
			}
		}, nil, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b, handler := loginsBoard(t)
			if test.arrange != nil {
				test.arrange(b)
			}

			// Act
			code, _ := boardLogins(t, handler, http.MethodPost, test.path, test.body, test.change)

			// Assert
			if code != test.code {
				t.Fatalf("code = %d, want %d", code, test.code)
			}
			for _, id := range []string{"stripe-precisiondocs-ai", "stripe-siqstack", "stripe-clock-in"} {
				if _, err := os.Stat(filepath.Join(browserlogin.ProfileDir(b.broker.state, id), "Cookies")); err != nil {
					t.Fatalf("%s's session was ended by a refused request: %v", id, err)
				}
			}
		})
	}
}

func TestALoginThatSignsOutAfterEachFetchKeepsNoSession(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	declareLogin(t, b.broker, "PrecisionDocs-AI", auth.BrowserLogin{Provider: "stripe", Account: "PrecisionDocs", AccountID: "acct_fixture", SignsOutAfterFetch: true})
	now := time.Now()
	if err := browserlogin.Write(b.broker.state, []browserlogin.Login{{ID: "stripe-precisiondocs", Provider: "stripe", Account: "PrecisionDocs", Projects: []string{"PrecisionDocs-AI"}, IsShared: true, SignedInAt: &now}}); err != nil {
		t.Fatal(err)
	}

	// Act
	lease, err := b.fetch()
	b.service.credentialWork.Wait()
	b.broker.work.Wait()

	// Assert
	if err != nil || len(lease.Fetched) != 2 {
		t.Fatalf("fetch = %+v, %v, want both keys stored", lease, err)
	}
	if _, err := os.Stat(browserlogin.ProfileDir(b.broker.state, "stripe-precisiondocs")); !os.IsNotExist(err) {
		t.Fatalf("the profile = %v, want it gone after the fetch's browser closed", err)
	}
	if logins := registered(t, b.broker); logins[0].SignedInAt != nil {
		t.Fatalf("registry = %+v, want the login recorded signed out", logins)
	}
}

func TestALoginThatSignsOutAfterEachFetchEndsTheSessionWhenTheLentBrowserItReadInCloses(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	declareLogin(t, b.broker, "PrecisionDocs-AI", auth.BrowserLogin{Provider: "stripe", Account: "PrecisionDocs-AI", AccountID: "acct_fixture", SignsOutAfterFetch: true})
	lendToTheGoblin(t, b)
	if lease, err := b.fetch(); err != nil || len(lease.Fetched) != 2 {
		t.Fatalf("fetch = %+v, %v, want both keys stored", lease, err)
	}
	b.service.credentialWork.Wait()
	if _, err := os.Stat(browserlogin.ProfileDir(b.broker.state, fetchLogin)); err != nil {
		t.Fatalf("the profile = %v, want it kept while the goblin's browser runs on it", err)
	}

	// Act
	err := b.broker.release("pd-keys", "stripe")
	b.broker.work.Wait()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(browserlogin.ProfileDir(b.broker.state, fetchLogin)); !os.IsNotExist(err) {
		t.Fatalf("the profile = %v, want it gone once the browser the fetch read in closed", err)
	}
	if logins := registered(t, b.broker); logins[0].SignedInAt != nil {
		t.Fatalf("registry = %+v, want the login recorded signed out", logins)
	}
}

func TestALoginThatSignsOutAfterEachFetchEndsTheSessionOnceAnUnconfirmedCloseIsGone(t *testing.T) {
	for _, test := range []struct {
		name          string
		isGone        bool
		isProfileKept bool
	}{
		{"its chrome still there", false, true},
		{"its chrome gone since", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newFetchBoard(t)
			declareLogin(t, b.broker, "PrecisionDocs-AI", auth.BrowserLogin{Provider: "stripe", Account: "PrecisionDocs-AI", AccountID: "acct_fixture", SignsOutAfterFetch: true})
			lendToTheGoblin(t, b)
			if lease, err := b.fetch(); err != nil || len(lease.Fetched) != 2 {
				t.Fatalf("fetch = %+v, %v, want both keys stored", lease, err)
			}
			b.service.credentialWork.Wait()
			b.broker.end = func(context.Context, browserlogin.Process) error {
				return errors.New("chrome 1's processes were not gone 1m0s after its job ended")
			}
			b.broker.report = func(error) {}
			isGone := test.isGone
			b.broker.gone = func(browserlogin.Process) bool { return isGone }
			// The Chrome goes in the end, since the board's own cleanup wants
			// every browser it lent closed.
			t.Cleanup(func() {
				isGone = true
				if err := b.broker.sweep(); err != nil {
					t.Error(err)
				}
			})
			if err := b.broker.release("pd-keys", "stripe"); err != nil {
				t.Fatal(err)
			}
			b.broker.work.Wait()

			// Act
			err := b.broker.sweep()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(browserlogin.ProfileDir(b.broker.state, fetchLogin)); (err == nil) != test.isProfileKept {
				t.Fatalf("the profile = %v, want kept %t", err, test.isProfileKept)
			}
		})
	}
}

func TestAFetchMeetingASignInPageRecordsItsLoginSignedOut(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	b.signedIn("acct_fixture")
	b.pageErr = browserlogin.ErrSignedOut

	// Act
	lease, err := b.fetch()

	// Assert
	if err != nil || lease.SignIn == "" {
		t.Fatalf("fetch = %+v, %v, want his sign-in card", lease, err)
	}
	if logins := registered(t, b.broker); logins[0].SignedInAt != nil {
		t.Fatalf("registry = %+v, want the login recorded signed out", logins)
	}
}
