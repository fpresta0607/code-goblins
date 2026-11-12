package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/githubapi"
)

// billingTokenRunner is gh's persisted login: tokenA until consent replaces
// it with tokenB.
type billingTokenRunner struct {
	reads       int
	isConsented bool
}

func (runner *billingTokenRunner) Run(context.Context, execx.Request) (execx.Result, error) {
	runner.reads++
	if runner.isConsented {
		return execx.Result{Stdout: []byte("tokenB")}, nil
	}
	return execx.Result{Stdout: []byte("tokenA")}, nil
}

// billingServer answers the account, and billing only for the token consent
// granted the user scope.
func billingServer(requests *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"example-user"}`))
		} else if r.Header.Get("Authorization") == "Bearer tokenA" {
			w.Header().Set("X-OAuth-Scopes", "gist, read:org, repo, workflow")
			w.Header().Set("X-Accepted-OAuth-Scopes", "user")
			w.WriteHeader(404)
		} else {
			_, _ = w.Write([]byte(`{"usageItems":[]}`))
		}
	}))
}

func billingClient(server *httptest.Server, runner execx.Runner) *githubapi.Client {
	client := githubapi.New(runner)
	client.Getenv = func(string) string { return "" }
	client.BaseURL, client.HTTP = server.URL, server.Client()
	return client
}

func TestBillingConsentReusesPendingRunAndRefreshesThePersistedCLIAccount(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	runner := &billingTokenRunner{}
	requests := 0
	server := billingServer(&requests)
	defer server.Close()
	client := billingClient(server, runner)
	service := boardService(store)
	service.lifetime = context.Background()
	service.Options.GitHub, service.Options.ActionsSpend = client, (actionsbilling.Reader{GitHub: client}).Read
	asOverlordsBoard(service)
	month := time.Now().UTC().Format("2006-01")
	service.readActionsSpend(context.Background(), time.Now())

	// Act
	var runID string
	for range 2 {
		code, body := askTheBoard(t, service, "POST", "/api/actions-spend/repair", `{"month":"`+month+`","action":"allow"}`, nil)
		var reading actionsbilling.Reading
		if err := json.Unmarshal([]byte(body), &reading); err != nil {
			t.Fatal(err)
		}
		if code != 200 || reading.Recovery == nil || reading.Recovery.State != "ready" {
			t.Fatalf("HTTP %d: %s", code, body)
		}
		if runID != "" && runID != reading.Recovery.RunID {
			t.Fatal("duplicate consent run")
		}
		runID = reading.Recovery.RunID
	}

	// Assert
	runs := store.Snapshot().Runs
	if len(runs) != 1 || requests != 2 || runner.reads != 1 {
		t.Fatalf("runs %d requests %d token reads %d", len(runs), requests, runner.reads)
	}
	run := runs[0]
	// The persisted script is the native consent interface, not browser-supplied code.
	if run.Billing == nil || run.Billing.Account != "example-user" || !strings.Contains(run.Command, "auth refresh --hostname github.com --scopes user") || strings.Contains(run.Command, "tokenA") {
		t.Fatalf("unsafe consent: %+v", run)
	}
	runner.isConsented = true
	store.db.Runs[0].State, store.db.Runs[0].RunAction = "running", "consent-action"
	run = store.db.Runs[0]
	code := 0
	if err := service.completeRun(context.Background(), run, &code, ""); err != nil {
		t.Fatal(err)
	}
	service.credentialWork.Wait()
	reading := service.actionsSpendUsage(time.Now(), month, store.Snapshot().Runs)
	if reading.Status != "available" || reading.Total == nil || reading.Problem != nil || reading.Recovery != nil || runner.reads != 2 || requests != 4 {
		t.Fatalf("reading %+v; token reads %d requests %d", reading, runner.reads, requests)
	}
	if err := service.completeRun(context.Background(), run, &code, ""); err != nil {
		t.Fatal(err)
	}
	service.credentialWork.Wait()
	if requests != 4 {
		t.Fatal("consent completion refreshed twice")
	}
}

// A restarted supervisor reads the login gh persisted and asks no consent
// again, nor shows the earlier consent as recovery.
func TestBillingAfterRestartNeedsNoConsentAgain(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	finished := time.Now().UTC().Add(-time.Minute)
	store.db.Runs = []Run{{ID: "actions-billing-earlier", State: "succeeded", FinishedAt: &finished, Billing: &BillingRepair{Month: time.Now().UTC().Format("2006-01"), Account: "example-user"}}}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := billingServer(&requests)
	defer server.Close()
	runner := &billingTokenRunner{isConsented: true}
	client := billingClient(server, runner)
	restarted := boardService(store)
	restarted.lifetime = context.Background()
	restarted.Options.GitHub, restarted.Options.ActionsSpend = client, (actionsbilling.Reader{GitHub: client}).Read

	// Act
	restarted.readActionsSpend(context.Background(), time.Now())
	reading := restarted.actionsSpendUsage(time.Now(), time.Now().UTC().Format("2006-01"), store.Snapshot().Runs)

	// Assert
	if reading.Status != "available" || reading.Problem != nil || reading.Recovery != nil || len(store.Snapshot().Runs) != 1 || runner.reads != 1 {
		t.Fatalf("after restart: %+v; runs %+v; token reads %d", reading, store.Snapshot().Runs, runner.reads)
	}
}

// The reread after consent runs beside the cycle that ended the run, so a slow
// GitHub never holds the supervisor's loop.
func TestBillingConsentRefreshDoesNotHoldTheCycle(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	service.lifetime = context.Background()
	service.Options.GitHub = githubapi.New(nil)
	service.Options.GitHub.Getenv = func(string) string { return "" }
	reading, release := make(chan struct{}), make(chan struct{})
	service.Options.ActionsSpend = func(_ context.Context, now time.Time, month string) actionsbilling.Reading {
		close(reading)
		<-release
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: now, CheckedAt: now, Total: &actionsbilling.Usage{}}
	}
	month := time.Now().UTC().Format("2006-01")
	if err := service.prepareBillingRepair(month, actionsbilling.Problem{Action: "allow", Account: "example-user", Credential: "gh"}); err != nil {
		t.Fatal(err)
	}
	store.db.Runs[0].State, store.db.Runs[0].RunAction = "running", "run-billing"
	code := 0
	releaseRead := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseRead)

	// Act
	ended := make(chan error, 1)
	go func() { ended <- service.completeRun(context.Background(), store.db.Runs[0], &code, "") }()

	// Assert
	select {
	case <-reading:
	case <-time.After(60 * time.Second):
		t.Fatal("consent did not reread billing")
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("ending the consent run waited for GitHub's answer")
	}
	releaseRead()
	service.credentialWork.Wait()
	if got := service.actionsSpendUsage(time.Now(), month, store.Snapshot().Runs); got.Status != "available" {
		t.Fatalf("reading after consent = %+v", got)
	}
}

// Retry is a forced read, which every board may ask for as it may read; it
// starts no consent.
func TestBillingRetryRefreshesFailuresWithoutConsent(t *testing.T) {
	for _, problem := range []actionsbilling.Problem{{Kind: "transient", Action: "retry"}, {Kind: "authentication", Action: "renew", Credential: "gh"}} {
		t.Run(problem.Kind, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			service := boardService(store)
			service.lifetime = context.Background()
			reads := 0
			service.Options.ActionsSpend = func(_ context.Context, now time.Time, month string) actionsbilling.Reading {
				reads++
				return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: now, Problem: &problem}
			}
			month := time.Now().UTC().Format("2006-01")
			service.readActionsSpend(context.Background(), time.Now())

			// Act
			for _, change := range []func(*http.Request){nil, func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" }} {
				code, body := askTheBoard(t, service, "POST", "/api/actions-spend/repair", `{"month":"`+month+`","action":"retry"}`, change)
				if code != 200 {
					t.Fatalf("HTTP %d: %s", code, body)
				}
			}

			// Assert
			if reads != 2 || len(store.Snapshot().Runs) != 0 {
				t.Fatalf("reads %d; runs %+v", reads, store.Snapshot().Runs)
			}
		})
	}
}

// A Retry of a reading GitHub failed for another cause than a refused login
// sends the kept token again and starts no gh auth token.
func TestBillingRetryOfATransientFailureKeepsTheToken(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	runner := &billingTokenRunner{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"example-user"}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	client := billingClient(server, runner)
	service := boardService(store)
	service.lifetime = context.Background()
	service.Options.GitHub, service.Options.ActionsSpend = client, (actionsbilling.Reader{GitHub: client}).Read
	month := time.Now().UTC().Format("2006-01")
	service.readActionsSpend(context.Background(), time.Now())

	// Act
	code, body := askTheBoard(t, service, "POST", "/api/actions-spend/repair", `{"month":"`+month+`","action":"retry"}`, nil)

	// Assert
	var reading actionsbilling.Reading
	if err := json.Unmarshal([]byte(body), &reading); err != nil {
		t.Fatal(err)
	}
	if code != 200 || reading.Problem == nil || reading.Problem.Kind != "transient" || runner.reads != 1 {
		t.Fatalf("HTTP %d: %s; token reads %d", code, body, runner.reads)
	}
}

// A Retry after gh auth login reads the new login at once, though GitHub
// refused the old one less than a minute ago (#2003).
func TestBillingRetryAfterARefusedLoginReadsTheNewLoginAtOnce(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	runner := &billingTokenRunner{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tokenA" {
			w.WriteHeader(http.StatusUnauthorized)
		} else if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"example-user"}`))
		} else {
			_, _ = w.Write([]byte(`{"usageItems":[]}`))
		}
	}))
	defer server.Close()
	client := billingClient(server, runner)
	service := boardService(store)
	service.lifetime = context.Background()
	service.Options.GitHub, service.Options.ActionsSpend = client, (actionsbilling.Reader{GitHub: client}).Read
	month := time.Now().UTC().Format("2006-01")
	service.readActionsSpend(context.Background(), time.Now())
	if refused := service.actionsSpendUsage(time.Now(), month, nil); refused.Problem == nil || refused.Problem.Kind != "authentication" || runner.reads != 1 {
		t.Fatalf("refused reading %+v; token reads %d", refused, runner.reads)
	}
	runner.isConsented = true

	// Act
	code, body := askTheBoard(t, service, "POST", "/api/actions-spend/repair", `{"month":"`+month+`","action":"retry"}`, nil)

	// Assert
	var reading actionsbilling.Reading
	if err := json.Unmarshal([]byte(body), &reading); err != nil {
		t.Fatal(err)
	}
	if code != 200 || reading.Status != "available" || reading.Problem != nil || runner.reads != 2 {
		t.Fatalf("HTTP %d: %s; token reads %d", code, body, runner.reads)
	}
}

// A Retry its 10 s limit holds back reads nothing, so it keeps the wait after
// a refused login: gh auth token starts at most once per read, never once per
// click (#2003).
func TestBillingRetryWithinItsLimitKeepsTheRefusedTokenWait(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	runner := &billingTokenRunner{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := billingClient(server, runner)
	service := boardService(store)
	service.lifetime = context.Background()
	service.Options.GitHub, service.Options.ActionsSpend = client, (actionsbilling.Reader{GitHub: client}).Read
	month := time.Now().UTC().Format("2006-01")
	service.readActionsSpend(context.Background(), time.Now())

	// Act
	var codes []int
	for range 3 {
		code, _ := askTheBoard(t, service, "POST", "/api/actions-spend/repair", `{"month":"`+month+`","action":"retry"}`, nil)
		codes = append(codes, code)
	}
	held := client.Get(context.Background(), "user", &struct{}{})

	// Assert
	var credential *githubapi.CredentialError
	if fmt.Sprint(codes) != "[200 200 200]" || runner.reads != 2 || !errors.As(held, &credential) {
		t.Fatalf("HTTP %v; token reads %d; then %v: want one Retry read, the wait kept, and gh read once by it", codes, runner.reads, held)
	}
}

func TestBillingRepairRejectsRemoteCSRFAndInjectedActions(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	asOverlordsBoard(service)
	month := time.Now().UTC().Format("2006-01")
	for _, test := range []struct {
		name, body string
		change     func(*http.Request)
		code       int
	}{
		{name: "remote", body: `{"month":"` + month + `","action":"allow"}`, change: func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" }, code: 403},
		{name: "csrf", body: `{"month":"` + month + `","action":"allow"}`, change: func(r *http.Request) { r.Header.Set("X-CEO-Token", "old") }, code: 403},
		{name: "csrf retry", body: `{"month":"` + month + `","action":"retry"}`, change: func(r *http.Request) { r.Header.Set("X-CEO-Token", "old") }, code: 403},
		{name: "arbitrary command", body: `{"month":"` + month + `","action":"allow","command":"evil"}`, code: 400},
		{name: "invalid month", body: `{"month":"` + month + `-01","action":"allow"}`, code: 400},
		{name: "arbitrary action", body: `{"month":"` + month + `","action":"evil"}`, code: 400},
		{name: "login renewal is guidance only", body: `{"month":"` + month + `","action":"renew"}`, code: 400},
		{name: "retry without a problem", body: `{"month":"` + month + `","action":"retry"}`, code: 409},
		{name: "allow without a permission problem", body: `{"month":"` + month + `","action":"allow"}`, code: 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			code, body := askTheBoard(t, service, "POST", "/api/actions-spend/repair", test.body, test.change)

			// Assert
			if code != test.code {
				t.Fatalf("HTTP %d: %s", code, body)
			}
		})
	}
	if len(store.Snapshot().Runs) != 0 {
		t.Fatal("rejected request created a run")
	}
}

func TestBillingConsentTerminalAndRunRequireTheLocalOwner(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	asOverlordsBoard(service)
	service.Options.GitHub = githubapi.New(nil)
	service.Options.GitHub.Getenv = func(string) string { return "" }
	month := time.Now().UTC().Format("2006-01")
	if err := service.prepareBillingRepair(month, actionsbilling.Problem{Action: "allow", Account: "example-user", Credential: "gh"}); err != nil {
		t.Fatal(err)
	}
	run := store.Snapshot().Runs[0]

	// Act
	code, body := askTheBoard(t, service, "POST", "/api/actions", `{"id":"run-billing","kind":"run","run_id":"`+run.ID+`","generation":"`+run.Identity+`"}`, func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" })

	// Assert
	if code != 403 {
		t.Fatalf("remote run HTTP %d: %s", code, body)
	}
	store.db.Runs[0].State, store.db.Runs[0].RunAction = "running", "run-billing"
	store.db.Runs[0].Terminal = true
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	binding, err := service.nativeBinding(url.Values{"run": {run.ID}})
	if err != nil || binding.localOnly == "" {
		t.Fatalf("billing terminal lacks PC restriction: %+v %v", binding, err)
	}
}

// Consent is the board's own item, made at his click: the CEO cannot
// withdraw it, Skip and answers in words do not take it, and AFK mode does
// not hold it as an item that waited on him.
func TestBillingConsentIsTheBoardsOwn(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	service.Options.GitHub = githubapi.New(nil)
	service.Options.GitHub.Getenv = func(string) string { return "" }
	if err := service.prepareBillingRepair(time.Now().UTC().Format("2006-01"), actionsbilling.Problem{Action: "allow", Account: "example-user", Credential: "gh"}); err != nil {
		t.Fatal(err)
	}
	run := store.Snapshot().Runs[0]

	// Act
	withdrawn := store.withdrawRun(run.ID, "the CEO no longer needs it")
	_, skipped := store.Queue(Action{ID: "skip-billing", Kind: "run_skip", RunID: run.ID, Generation: run.Identity})
	_, answered := store.Queue(Action{ID: "answer-billing", Kind: "run_answer", Text: "not now", RunID: run.ID, Generation: run.Identity})

	// Assert
	if withdrawn == nil || !strings.Contains(withdrawn.Error(), "not the CEO's to withdraw") {
		t.Errorf("withdrawal = %v, want it refused", withdrawn)
	}
	for _, err := range []error{skipped, answered} {
		if err == nil || !strings.Contains(err.Error(), "the board's own") {
			t.Errorf("press = %v, want it refused as the board's own", err)
		}
	}
	if slices.ContainsFunc(waitingOnOverlord(store.Snapshot()), func(item heldItem) bool { return item.key() == "run:"+run.ID }) {
		t.Error("AFK mode holds the board's own consent")
	}
	if got := store.Snapshot().Runs[0].State; got != "ready" {
		t.Errorf("consent state = %s, want it still ready", got)
	}
}

func TestBillingRepairFailureAndStopDoNotRefreshOrLoop(t *testing.T) {
	for _, state := range []string{"failed", "stopped"} {
		t.Run(state, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			service := boardService(store)
			reads := 0
			service.Options.ActionsSpend = func(context.Context, time.Time, string) actionsbilling.Reading {
				reads++
				return actionsbilling.Reading{}
			}
			month := time.Now().UTC().Format("2006-01")
			service.actionsSpendReadings = map[string]*actionsSpendEntry{month: {reading: actionsbilling.Reading{Month: month, Status: "unavailable", Problem: &actionsbilling.Problem{Kind: "permission", Action: "allow", Account: "example-user", Credential: "gh"}}}}
			service.Options.GitHub = githubapi.New(nil)
			service.Options.GitHub.Getenv = func(string) string { return "" }
			if err := service.prepareBillingRepair(month, *service.actionsSpendReadings[month].reading.Problem); err != nil {
				t.Fatal(err)
			}
			store.db.Runs[0].State, store.db.Runs[0].RunAction = "running", "run-billing"
			code := 1

			// Act
			err := service.endRun(context.Background(), store.db.Runs[0], state, &code, "")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			service.credentialWork.Wait()
			reading := service.actionsSpendUsage(time.Now(), month, store.Snapshot().Runs)
			if reads != 0 || reading.Recovery == nil || reading.Recovery.State != state || reading.Problem.Action != "allow" {
				t.Fatalf("reads %d; reading %+v", reads, reading)
			}
		})
	}
}

// A reading shows the consent that explains its problem: a pending one, or
// one for its account and month that ended since GitHub last answered. An
// earlier consent never relabels a later, unrelated failure.
func TestBillingRecoveryNamesOnlyTheConsentThatExplainsTheReading(t *testing.T) {
	now := time.Now().UTC()
	month := now.Format("2006-01")
	earlier, later := now.Add(-48*time.Hour), now.Add(-time.Minute)
	permission := &actionsbilling.Problem{Kind: "permission", Action: "allow", Account: "example-user", Credential: "gh"}
	transient := &actionsbilling.Problem{Kind: "transient", Action: "retry", Account: "example-user", Credential: "gh"}
	for _, test := range []struct {
		name       string
		problem    *actionsbilling.Problem
		run        Run
		isAttached bool
	}{
		{name: "pending consent", problem: permission, run: Run{State: "ready", Billing: &BillingRepair{Month: month, Account: "example-user"}}, isAttached: true},
		{name: "consent that failed since the last reading", problem: permission, run: Run{State: "failed", FinishedAt: &later, Billing: &BillingRepair{Month: month, Account: "example-user"}}, isAttached: true},
		{name: "consent before the last reading", problem: permission, run: Run{State: "succeeded", FinishedAt: &earlier, Billing: &BillingRepair{Month: month, Account: "example-user"}}},
		{name: "earlier consent and a transient failure", problem: transient, run: Run{State: "succeeded", FinishedAt: &later, Billing: &BillingRepair{Month: month, Account: "example-user"}}},
		{name: "another month's consent", problem: permission, run: Run{State: "failed", FinishedAt: &later, Billing: &BillingRepair{Month: "2001-01", Account: "example-user"}}},
		{name: "another account's consent", problem: permission, run: Run{State: "failed", FinishedAt: &later, Billing: &BillingRepair{Month: month, Account: "other-user"}}},
		{name: "available reading with a pending consent", run: Run{State: "ready", Billing: &BillingRepair{Month: month, Account: "example-user"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			test.run.ID = "actions-billing-1"
			service := &Service{actionsSpendReadings: map[string]*actionsSpendEntry{month: {reading: actionsbilling.Reading{Status: "stale", Month: month, ReadAt: now.Add(-24 * time.Hour), CheckedAt: now, Total: &actionsbilling.Usage{}, Problem: test.problem}}}}

			// Act
			reading := service.actionsSpendUsage(now, month, []Run{test.run})

			// Assert
			if (reading.Recovery != nil) != test.isAttached {
				t.Fatalf("recovery = %+v, want attached %t", reading.Recovery, test.isAttached)
			}
		})
	}
}

func TestBillingConsentOutputStaysOnThePC(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	store.db.Runs = []Run{{ID: "billing-consent-1", State: "succeeded", Output: "one-time-consent-code", Billing: &BillingRepair{Month: "2026-11"}}}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}

	// Act
	code, body := askTheBoard(t, service, "GET", "/api/runs/billing-consent-1/output", "", func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" })

	// Assert
	if code != 403 || strings.Contains(body, "one-time-consent-code") {
		t.Fatalf("remote output HTTP %d: %s", code, body)
	}
	encoded, err := json.Marshal(boardItems(store.Snapshot()))
	if err != nil || strings.Contains(string(encoded), "one-time-consent-code") {
		t.Fatalf("board exposed consent output: %s %v", encoded, err)
	}
}

func TestBillingEnvironmentCredentialIsNeverReplacedByCLIConsent(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	client := githubapi.New(nil)
	client.Getenv = func(name string) string {
		if name == "GH_TOKEN" {
			return "selected-environment"
		}
		return ""
	}
	service.Options.GitHub = client
	for _, source := range []string{"gh", "GH_TOKEN", "GITHUB_TOKEN"} {
		// Act
		err := service.prepareBillingRepair(time.Now().UTC().Format("2006-01"), actionsbilling.Problem{Action: "allow", Account: "example-user", Credential: source})

		// Assert
		if err == nil || len(store.Snapshot().Runs) != 0 {
			t.Fatalf("consent created for %s while GH_TOKEN is selected", source)
		}
	}
}

// The board's streamed reading names the pending consent it is waiting on,
// and drops it once that consent is gone.
func TestBillingSnapshotNamesThePendingConsent(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	service.Options.ActionsSpend = func(context.Context, time.Time, string) actionsbilling.Reading {
		t.Fatal("a snapshot read GitHub")
		return actionsbilling.Reading{}
	}
	month := time.Now().UTC().Format("2006-01")
	service.actionsSpendReadings = map[string]*actionsSpendEntry{month: {started: time.Now(), reading: actionsbilling.Reading{Month: month, Status: "unavailable", Problem: &actionsbilling.Problem{Kind: "permission", Action: "allow", Account: "example-user", Credential: "gh"}}}}
	store.db.Runs = []Run{{ID: "actions-billing-pending", State: "ready", Billing: &BillingRepair{Month: month, Account: "example-user"}}}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}

	// Act
	pending, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	store.db.Runs = nil
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	ended, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if pending.ActionsSpend == nil || pending.ActionsSpend.Recovery == nil || pending.ActionsSpend.Recovery.RunID != "actions-billing-pending" {
		t.Fatalf("snapshot with a pending consent = %+v", pending.ActionsSpend)
	}
	if ended.ActionsSpend == nil || ended.ActionsSpend.Recovery != nil {
		t.Fatalf("snapshot without a consent = %+v", ended.ActionsSpend)
	}
}

// Successful consent makes every cached month stale, not only the consent's
// own, so another month shown on a device is read again with the new access.
func TestBillingConsentRereadsEveryCachedMonth(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := boardService(store)
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	service.lifetime = ended
	service.Options.GitHub = githubapi.New(nil)
	service.Options.GitHub.Getenv = func(string) string { return "" }
	reads := map[string]int{}
	service.Options.ActionsSpend = func(_ context.Context, now time.Time, month string) actionsbilling.Reading {
		reads[month]++
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: now, CheckedAt: now, Total: &actionsbilling.Usage{}}
	}
	now := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	permission := &actionsbilling.Problem{Kind: "permission", Action: "allow", Account: "example-user", Credential: "gh"}
	service.actionsSpendReadings = map[string]*actionsSpendEntry{
		"2026-10": {started: now, reading: actionsbilling.Reading{Month: "2026-10", Status: "unavailable", Problem: permission}},
		"2026-11": {started: now, done: make(chan struct{}), reading: actionsbilling.Reading{Month: "2026-11", Status: "unavailable", Problem: permission}},
	}
	run := Run{ID: "actions-billing-1", State: "succeeded", Billing: &BillingRepair{Month: "2026-11", Account: "example-user"}}

	// Act
	service.finishBillingRepair(run, "succeeded")
	service.credentialWork.Wait()
	reading := service.readActionsSpendMonth(context.Background(), context.Background(), now, "2026-10")

	// Assert
	if reads["2026-10"] != 1 || reading.Status != "available" || reading.Problem != nil {
		t.Fatalf("reads %v; other month %+v", reads, reading)
	}
}
