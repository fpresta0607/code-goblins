package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/githubapi"
)

type billingTokenRunner struct{ reads int }

func (runner *billingTokenRunner) Run(context.Context, execx.Request) (execx.Result, error) {
	runner.reads++
	token := "tokenA"
	if runner.reads > 1 {
		token = "tokenB"
	}
	return execx.Result{Stdout: []byte(token)}, nil
}

func TestBillingConsentReusesPendingRunAndRefreshesThePersistedCLIAccount(t *testing.T) {
	store, _ := testStore(t)
	runner := &billingTokenRunner{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
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
	defer server.Close()
	client := githubapi.New(runner)
	client.Getenv = func(string) string { return "" }
	client.BaseURL, client.HTTP = server.URL, server.Client()
	service := boardService(store)
	service.lifetime = context.Background()
	service.Options.GitHub, service.Options.ActionsSpend = client, (actionsbilling.Reader{GitHub: client}).Read
	asOverlordsBoard(service)
	month := time.Now().UTC().Format("2006-01")
	service.readActionsSpend(context.Background(), time.Now())
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
	runs := store.Snapshot().Runs
	if len(runs) != 1 || requests != 2 || runner.reads != 1 {
		t.Fatalf("runs %d requests %d token reads %d", len(runs), requests, runner.reads)
	}
	run := runs[0]
	// The persisted script is the native consent interface, not browser-supplied code.
	if run.Billing == nil || run.Billing.Account != "example-user" || !strings.Contains(run.Command, "auth refresh --hostname github.com --scopes user") || strings.Contains(run.Command, "tokenA") {
		t.Fatalf("unsafe consent: %+v", run)
	}
	store.db.Runs[0].State, store.db.Runs[0].RunAction = "running", "consent-action"
	run = store.db.Runs[0]
	code := 0
	if err := service.completeRun(context.Background(), run, &code, ""); err != nil {
		t.Fatal(err)
	}
	reading := service.actionsSpendUsage(time.Now(), month)
	if reading.Status != "available" || reading.Total == nil || runner.reads != 2 || requests != 4 {
		t.Fatalf("reading %+v; token reads %d requests %d", reading, runner.reads, requests)
	}
	if err := service.completeRun(context.Background(), run, &code, ""); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatal("consent completion refreshed twice")
	}
	// A new process reads the same persisted authorized token without consent.
	restarted := githubapi.New(runner)
	restarted.Getenv = client.Getenv
	restarted.BaseURL, restarted.HTTP = server.URL, server.Client()
	if got := (actionsbilling.Reader{GitHub: restarted}).Read(context.Background(), time.Now(), month); got.Status != "available" {
		t.Fatalf("after restart: %+v", got)
	}
}

func TestBillingRetryRefreshesTransientFailuresWithoutConsent(t *testing.T) {
	store, _ := testStore(t)
	service := boardService(store)
	service.lifetime = context.Background()
	reads := 0
	service.Options.ActionsSpend = func(_ context.Context, now time.Time, month string) actionsbilling.Reading {
		reads++
		return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: now, Problem: &actionsbilling.Problem{Kind: "transient", Action: "retry"}}
	}
	asOverlordsBoard(service)
	month := time.Now().UTC().Format("2006-01")
	service.readActionsSpend(context.Background(), time.Now())
	for range 2 {
		code, body := askTheBoard(t, service, "POST", "/api/actions-spend/repair", `{"month":"`+month+`","action":"retry"}`, nil)
		if code != 200 {
			t.Fatalf("HTTP %d: %s", code, body)
		}
	}
	if reads != 2 || len(store.Snapshot().Runs) != 0 {
		t.Fatalf("reads %d; runs %+v", reads, store.Snapshot().Runs)
	}
}

func TestBillingRepairRejectsRemoteCSRFAndInjectedActions(t *testing.T) {
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
		{name: "csrf", body: `{"month":"` + month + `","action":"allow"}`, change: func(r *http.Request) { r.Header.Set("X-CFO-Token", "old") }, code: 403},
		{name: "arbitrary command", body: `{"month":"` + month + `","action":"allow","command":"evil"}`, code: 400},
		{name: "invalid month", body: `{"month":"` + month + `-01","action":"allow"}`, code: 400},
		{name: "arbitrary action", body: `{"month":"` + month + `","action":"evil"}`, code: 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, body := askTheBoard(t, service, "POST", "/api/actions-spend/repair", test.body, test.change)
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
	code, body := askTheBoard(t, service, "POST", "/api/actions", `{"id":"run-billing","kind":"run","run_id":"`+run.ID+`","generation":"`+run.Identity+`"}`, func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" })
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

func TestBillingRepairFailureAndRefusalDoNotRefreshOrLoop(t *testing.T) {
	for _, state := range []string{"failed", "stopped", "withdrawn"} {
		t.Run(state, func(t *testing.T) {
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
			if err := service.endRun(context.Background(), store.db.Runs[0], state, &code, ""); err != nil {
				t.Fatal(err)
			}
			reading := service.actionsSpendUsage(time.Now(), month)
			if reads != 0 || reading.Recovery == nil || reading.Recovery.State != state || reading.Problem.Action != "allow" {
				t.Fatalf("reads %d; reading %+v", reads, reading)
			}
		})
	}
}

func TestBillingConsentOutputStaysOnThePC(t *testing.T) {
	store, _ := testStore(t)
	service := boardService(store)
	store.db.Runs = []Run{{ID: "billing-consent-1", State: "succeeded", Output: "one-time-consent-code", Billing: &BillingRepair{Month: "2026-11"}}}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	code, body := askTheBoard(t, service, "GET", "/api/runs/billing-consent-1/output", "", func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" })
	if code != 403 || strings.Contains(body, "one-time-consent-code") {
		t.Fatalf("remote output HTTP %d: %s", code, body)
	}
	encoded, err := json.Marshal(boardItems(store.Snapshot()))
	if err != nil || strings.Contains(string(encoded), "one-time-consent-code") {
		t.Fatalf("board exposed consent output: %s %v", encoded, err)
	}
}

func TestBillingEnvironmentCredentialIsNeverReplacedByCLIConsent(t *testing.T) {
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
		err := service.prepareBillingRepair(time.Now().UTC().Format("2006-01"), actionsbilling.Problem{Action: "allow", Account: "example-user", Credential: source})
		if err == nil || len(store.Snapshot().Runs) != 0 {
			t.Fatalf("consent created for %s while GH_TOKEN is selected", source)
		}
	}
}
