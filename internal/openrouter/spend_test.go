package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// The fixtures name keys of their own shape: a real provider key is never
	// written into this repository, and a key the scanner could mistake for
	// one never reaches a push.
	testKey  = "fixture-openrouter-key-one"
	otherKey = "fixture-openrouter-key-two"
)

// fixture writes a Pi configuration folder with the files it names, and
// answers every read from a server that states what it was asked.
type fixture struct {
	dir     string
	env     map[string]string
	status  int
	body    string
	before  func(*http.Request)
	reading Reading
}

// read answers from one key document, which answers states.
func read(t *testing.T, options fixture) Reading {
	t.Helper()
	dir := options.dir
	if dir == "" {
		dir = t.TempDir()
		writeFile(t, filepath.Join(dir, "auth.json"), `{"`+Provider+`":{"type":"api_key","key":"`+testKey+`"}}`)
		writeFile(t, filepath.Join(dir, "settings.json"), `{"defaultProvider":"openrouter"}`)
	}
	status := options.status
	if status == 0 {
		status = http.StatusOK
	}
	body := options.body
	if body == "" {
		body = `{"data":{"usage":25.5,"usage_daily":1,"usage_weekly":9.04,"usage_monthly":20,"byok_usage":0,"byok_usage_daily":0,"byok_usage_weekly":0,"byok_usage_monthly":0,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly","include_byok_in_limit":false,"is_management_key":false,"is_free_tier":false,"budgets":[],"effective_budget":null}}`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if options.before != nil {
			options.before(r)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer server.Close()
	getenv := func(name string) string { return options.env[name] }
	if options.env == nil {
		getenv = func(string) string { return "" }
	}
	return Reader{Dir: dir, URL: server.URL, Env: getenv, Now: func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }}.Read(context.Background())
}

// The dial states the key's own spend in the window its own limit counts
// over, with that window named, and never a lifetime figure under a window
// limit or a window figure against a lifetime one.
func TestReadingNamesTheKeysOwnSpendInTheWindowItsLimitResetsOver(t *testing.T) {
	for _, test := range []struct {
		name, document, period string
		spent, limit           float64
	}{
		{"a weekly limit", `{"usage":25.5,"usage_daily":1,"usage_weekly":9.04,"usage_monthly":20,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly"}`, PeriodWeekly, 9.04, 50},
		{"a daily limit", `{"usage":25.5,"usage_daily":2.5,"usage_weekly":9.04,"usage_monthly":20,"limit":10,"limit_remaining":7.5,"limit_reset":"daily"}`, PeriodDaily, 2.5, 10},
		{"a monthly limit", `{"usage":25.5,"usage_daily":2.5,"usage_weekly":9.04,"usage_monthly":23.5,"limit":100,"limit_remaining":76.5,"limit_reset":"monthly"}`, PeriodMonthly, 23.5, 100},
		{"a limit that never resets", `{"usage":25.5,"usage_daily":2.5,"usage_weekly":9.04,"usage_monthly":23.5,"limit":100,"limit_remaining":74.5,"limit_reset":null}`, PeriodLifetime, 25.5, 100},
		{"no limit at all", `{"usage":25.5,"usage_daily":2.5,"usage_weekly":9.04,"usage_monthly":23.5,"limit":null,"limit_remaining":null,"limit_reset":null}`, PeriodLifetime, 25.5, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reading := read(t, fixture{body: `{"data":` + test.document + `}`})

			// Act
			spent, limit := reading.SpentUSD, reading.LimitUSD

			// Assert
			if reading.Status != StatusAvailable {
				t.Fatalf("status = %q, detail = %q", reading.Status, reading.Detail)
			}
			if reading.Period != test.period {
				t.Errorf("period = %q, want %q", reading.Period, test.period)
			}
			if spent == nil || *spent != test.spent {
				t.Errorf("spent = %v, want %v", spent, test.spent)
			}
			if test.limit == 0 {
				if limit != nil {
					t.Errorf("limit = %v, want none", *limit)
				}
			} else if limit == nil || *limit != test.limit {
				t.Errorf("limit = %v, want %v", limit, test.limit)
			}
			if !reading.Configured {
				t.Error("Pi's settings name openrouter, so the reading is configured")
			}
			if reading.ReadAt.IsZero() || reading.ReadAt.Location() != time.UTC {
				t.Errorf("read_at = %v", reading.ReadAt)
			}
		})
	}
}

// The external BYOK spend is reported beside the key's own usage and never
// added to it, and the limit says whether it counts BYOK toward itself.
func TestReadingKeepsByokSpendApartFromTheKeysOwnUsage(t *testing.T) {
	for _, test := range []struct {
		name        string
		document    string
		includes    bool
		byok        float64
		remaining   float64
		wantRemains bool
	}{
		{name: "counted separately", document: `{"usage_weekly":9.04,"usage":9.04,"byok_usage_weekly":1.2,"byok_usage":1.2,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly","include_byok_in_limit":false}`, byok: 1.2, remaining: 40.96, wantRemains: true},
		{name: "counted toward the limit", document: `{"usage_weekly":9.04,"usage":9.04,"byok_usage_weekly":1.2,"byok_usage":1.2,"limit":50,"limit_remaining":39.76,"limit_reset":"weekly","include_byok_in_limit":true}`, includes: true, byok: 1.2, remaining: 39.76, wantRemains: true},
		{name: "a key with no limit at all", document: `{"usage":9.04,"byok_usage":1.2,"limit":null,"limit_remaining":null,"limit_reset":null,"include_byok_in_limit":false}`, byok: 1.2},
	} {
		t.Run(test.name, func(t *testing.T) {
			reading := read(t, fixture{body: `{"data":` + test.document + `}`})
			if reading.SpentUSD == nil || *reading.SpentUSD != 9.04 {
				t.Fatalf("the key's own usage was changed by BYOK: %v", reading.SpentUSD)
			}
			if reading.ByokUSD == nil || *reading.ByokUSD != test.byok {
				t.Errorf("byok = %v, want %v", reading.ByokUSD, test.byok)
			}
			if reading.IncludesByok != test.includes {
				t.Errorf("includes_byok = %v, want %v", reading.IncludesByok, test.includes)
			}
			if test.wantRemains && (reading.RemainingUSD == nil || *reading.RemainingUSD != test.remaining) {
				t.Errorf("remaining = %v, want %v", reading.RemainingUSD, test.remaining)
			}
			if !test.wantRemains && reading.RemainingUSD != nil {
				t.Errorf("remaining = %v, want none against no limit", *reading.RemainingUSD)
			}
			if raised, err := json.Marshal(reading); err != nil || strings.Contains(string(raised), testKey) {
				t.Error("the reading holds the key it was made with")
			}
		})
	}
}

// A budget layer that is not this key's own is named by its scope, with its
// own spend, and the key's own dollars stay the key's.
func TestReadingNamesABudgetThatBlocksBeforeTheKeysOwnLimit(t *testing.T) {
	member := `{"scope":"member","guardrail_id":"g","budget_id":null,"limit_usd":50,"spend_usd":32.5,"remaining_usd":17.5,"reset_interval":"weekly","resets_at":"2026-10-19T00:00:00.000Z","include_byok":false}`
	for _, test := range []struct {
		name     string
		document string
		want     string
	}{
		{name: "a member budget", document: `{"usage_weekly":9.04,"limit":100,"limit_remaining":90.96,"limit_reset":"weekly","effective_budget":` + member + `,"budgets":[{"scope":"key_limit","limit_usd":100,"spend_usd":9.04,"remaining_usd":90.96,"reset_interval":"weekly","resets_at":"2026-10-19T00:00:00.000Z","include_byok":false},` + member + `]}`, want: "member"},
		{name: "the key's own limit", document: `{"usage_weekly":9.04,"limit":100,"limit_remaining":90.96,"limit_reset":"weekly","effective_budget":{"scope":"key_limit","limit_usd":100,"spend_usd":9.04,"remaining_usd":90.96,"reset_interval":"weekly","resets_at":null,"include_byok":false}}`, want: ""},
		{name: "nothing budgeted", document: `{"usage_weekly":9.04,"limit":null,"limit_remaining":null,"limit_reset":null,"effective_budget":null}`, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			reading := read(t, fixture{body: `{"data":` + test.document + `}`})
			scope := ""
			if reading.Blocking != nil {
				scope = reading.Blocking.Scope
			}
			if scope != test.want {
				t.Fatalf("blocking budget = %q, want %q", scope, test.want)
			}
			if reading.Blocking != nil {
				if reading.Blocking.SpentUSD != 32.5 || reading.Blocking.LimitUSD != 50 || reading.Blocking.Period != PeriodWeekly {
					t.Errorf("blocking budget = %+v", *reading.Blocking)
				}
				if *reading.SpentUSD != 9.04 {
					t.Errorf("the key's own spend took the member budget's: %v", *reading.SpentUSD)
				}
			}
		})
	}
}

// The window's reset time comes from the key-scoped budget for that window,
// and no budget for another window names one.
func TestReadingTakesTheResetTimeOfTheKeysOwnWindow(t *testing.T) {
	for _, test := range []struct {
		name     string
		document string
		want     string
	}{
		{name: "its own weekly budget", document: `{"usage_weekly":9.04,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly","budgets":[{"scope":"workspace_default_key","limit_usd":50,"spend_usd":9.04,"remaining_usd":40.96,"reset_interval":"weekly","resets_at":"2026-10-19T00:00:00.000Z","include_byok":false}]}`, want: "2026-10-19T00:00:00Z"},
		{name: "a budget for another window", document: `{"usage_weekly":9.04,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly","budgets":[{"scope":"key_limit","limit_usd":50,"spend_usd":9.04,"remaining_usd":40.96,"reset_interval":"monthly","resets_at":"2026-11-01T00:00:00.000Z","include_byok":false}]}`, want: ""},
		{name: "no budget at all", document: `{"usage_weekly":9.04,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly"}`, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			reading := read(t, fixture{body: `{"data":` + test.document + `}`})
			got := ""
			if !reading.ResetsAt.IsZero() {
				got = reading.ResetsAt.Format(time.RFC3339)
			}
			if got != test.want {
				t.Fatalf("resets_at = %q, want %q", got, test.want)
			}
		})
	}
}

// A window this build cannot count shows the limit without a spend, and says
// so, rather than counting the wrong counter.
func TestReadingSaysWhenItCannotCountTheKeysWindow(t *testing.T) {
	reading := read(t, fixture{body: `{"data":{"usage":25.5,"usage_daily":2.5,"usage_weekly":9.04,"usage_monthly":23.5,"limit":50,"limit_remaining":40.96,"limit_reset":"yearly"}}`})
	if reading.Status != StatusAvailable || reading.SpentUSD != nil || reading.LimitUSD == nil {
		t.Fatalf("reading = %+v", reading)
	}
	if reading.Period != "" || !strings.Contains(reading.Detail, "window") {
		t.Fatalf("period = %q, detail = %q", reading.Period, reading.Detail)
	}
}

// A figure OpenRouter left out is no zero: the reading says what is missing.
func TestReadingSaysWhenTheWindowFigureIsMissing(t *testing.T) {
	reading := read(t, fixture{body: `{"data":{"usage":25.5,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly"}}`})
	if reading.Status != StatusAvailable || reading.SpentUSD != nil {
		t.Fatalf("reading = %+v", reading)
	}
	if !strings.Contains(reading.Detail, "weekly") {
		t.Fatalf("detail = %q", reading.Detail)
	}
}

// No key is a state of its own, named as missing, and never a spend of zero.
func TestReadingSaysWhenPiHasNoKey(t *testing.T) {
	for _, test := range []struct {
		name       string
		settings   string
		auth       string
		env        map[string]string
		configured bool
		detail     string
	}{
		{name: "no configuration at all", detail: "Pi has no OpenRouter key"},
		{name: "Pi uses openrouter with no key", settings: `{"defaultProvider":"openrouter"}`, configured: true, detail: "Pi has no OpenRouter key"},
		{name: "Pi uses another provider", settings: `{"defaultProvider":"deepseek"}`, detail: "Pi has no OpenRouter key"},
		{name: "an auth file without the provider", auth: `{"deepseek":{"type":"api_key","key":"fixture-other-provider"}}`, detail: "Pi has no OpenRouter key"},
		{name: "an entry that names no key", auth: `{"openrouter":{"type":"api_key"}}`, detail: "Pi's OpenRouter entry names no key"},
		{name: "an entry that is not an API key", auth: `{"openrouter":{"type":"oauth","key":"x"}}`, detail: "not an API key"},
		{name: "a key from the environment", env: map[string]string{EnvKey: testKey}, detail: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if test.settings != "" {
				writeFile(t, filepath.Join(dir, "settings.json"), test.settings)
			}
			if test.auth != "" {
				writeFile(t, filepath.Join(dir, "auth.json"), test.auth)
			}

			// Act
			reading := read(t, fixture{dir: dir, env: test.env})

			// Assert
			if test.detail == "" {
				if reading.Status != StatusAvailable || reading.KeyID != fingerprint(testKey) {
					t.Fatalf("an environment key was not read: %+v", reading)
				}
				return
			}
			if reading.Status != StatusMissing {
				t.Fatalf("status = %q, want missing", reading.Status)
			}
			if !strings.Contains(reading.Detail, test.detail) {
				t.Errorf("detail = %q, want %q", reading.Detail, test.detail)
			}
			if reading.SpentUSD != nil || reading.KeyID != "" {
				t.Errorf("a missing key states a spend: %+v", reading)
			}
			if reading.Configured != test.configured {
				t.Errorf("configured = %v, want %v", reading.Configured, test.configured)
			}
		})
	}
}

// Pi's auth file is read before the environment, and an entry that names an
// environment variable is resolved the way Pi resolves it.
func TestReadingResolvesTheKeyTheWayPiDoes(t *testing.T) {
	for _, test := range []struct {
		name     string
		auth     string
		env      map[string]string
		wantKey  string
		wantRefu string
	}{
		{name: "the auth file before the environment", auth: `{"openrouter":{"type":"api_key","key":"` + testKey + `"}}`, env: map[string]string{EnvKey: otherKey}, wantKey: testKey},
		{name: "the entry's own environment first", auth: `{"openrouter":{"type":"api_key","key":"$MY_KEY","env":{"MY_KEY":"` + testKey + `"}}}`, env: map[string]string{"MY_KEY": otherKey}, wantKey: testKey},
		{name: "a braced variable from the environment", auth: `{"openrouter":{"type":"api_key","key":"${PI_OPENROUTER}"}}`, env: map[string]string{"PI_OPENROUTER": testKey}, wantKey: testKey},
		{name: "a prefix around a variable", auth: `{"openrouter":{"type":"api_key","key":"fixture-${SUFFIX}"}}`, env: map[string]string{"SUFFIX": "abc"}, wantKey: "fixture-abc"},
		{name: "an escaped dollar", auth: `{"openrouter":{"type":"api_key","key":"$$literal"}}`, wantKey: "$literal"},
		{name: "a variable nothing holds", auth: `{"openrouter":{"type":"api_key","key":"${NOT_SET_ANYWHERE}"}}`, wantRefu: "NOT_SET_ANYWHERE"},
		{name: "a key a command resolves", auth: `{"openrouter":{"type":"api_key","key":"!security find-generic-password -ws openrouter"}}`, wantRefu: "resolved by a command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "auth.json"), test.auth)

			reading := read(t, fixture{dir: dir, env: test.env})

			if test.wantRefu != "" {
				if reading.Status != StatusMissing || !strings.Contains(reading.Detail, test.wantRefu) {
					t.Fatalf("reading = %+v", reading)
				}
				if strings.Contains(reading.Detail, "find-generic-password") {
					t.Error("the refusal repeats the command a configuration names")
				}
				return
			}
			if reading.Status != StatusAvailable || reading.KeyID != fingerprint(test.wantKey) {
				t.Fatalf("reading = %+v, want the key %q", reading, test.wantKey)
			}
		})
	}
}

// The request is one bounded GET of the current-key endpoint, carrying the
// key in its Authorization header and nothing in a query, and it follows no
// redirect, so the key never reaches a host the reading did not name.
func TestReadingAsksTheCurrentKeyEndpointWithoutFollowingRedirects(t *testing.T) {
	var method, path, authorization, query string
	reading := read(t, fixture{before: func(r *http.Request) {
		method, path, query, authorization = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
	}})
	if reading.Status != StatusAvailable {
		t.Fatalf("reading = %+v", reading)
	}
	if method != http.MethodGet || path != "/" || query != "" {
		t.Errorf("request = %s %s?%s", method, path, query)
	}
	if authorization != "Bearer "+testKey {
		t.Errorf("authorization = %q", authorization)
	}

	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a redirect was followed to another host")
	}))
	defer elsewhere.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer redirect.Close()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth.json"), `{"openrouter":{"type":"api_key","key":"`+testKey+`"}}`)
	redirected := Reader{Dir: dir, URL: redirect.URL, Env: func(string) string { return "" }}.Read(context.Background())
	if redirected.Status != StatusUnavailable || !strings.Contains(redirected.Detail, "302") {
		t.Fatalf("a redirect read as: %+v", redirected)
	}
}

// Every way a read can fail has its own honest state, and none of them is a
// spend of zero or a word taken from the provider's answer.
func TestReadingSaysWhyItCouldNotBeTaken(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		body    string
		want    string
		detail  string
		keyless bool
	}{
		{name: "a refused key", status: http.StatusUnauthorized, want: StatusAuthRequired, detail: "refused"},
		{name: "a forbidden key", status: http.StatusForbidden, want: StatusAuthRequired, detail: "refused"},
		{name: "a rate limit", status: http.StatusTooManyRequests, want: StatusRateLimited, detail: "rate limited"},
		{name: "a server error", status: http.StatusInternalServerError, want: StatusUnavailable, detail: "500"},
		{name: "an answer that is not JSON", body: `<html>nope</html>`, want: StatusUnavailable, detail: "no key reading"},
		{name: "an answer cut short", body: `{"data":{"usage":1,` + strings.Repeat(" ", maxAnswer), want: StatusUnavailable, detail: "no key reading"},
		{name: "a management key", body: `{"data":{"usage":0,"limit":null,"limit_reset":null,"is_management_key":true}}`, want: StatusUnavailable, detail: "management key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := test.status
			if status == 0 {
				status = http.StatusOK
			}
			reading := read(t, fixture{status: status, body: test.body})
			if reading.Status != test.want {
				t.Fatalf("status = %q, want %q", reading.Status, test.want)
			}
			if !strings.Contains(strings.ToLower(reading.Detail), strings.ToLower(test.detail)) {
				t.Errorf("detail = %q, want %q", reading.Detail, test.detail)
			}
			if strings.Contains(reading.Detail, testKey) || strings.Contains(reading.Detail, "Bearer") {
				t.Errorf("the detail carries the key or its header: %q", reading.Detail)
			}
			if reading.SpentUSD != nil || reading.LimitUSD != nil {
				t.Errorf("a failed read states money: %+v", reading)
			}
		})
	}
}

// A read is bounded by its context, so a provider that does not answer leaves
// nothing of the fleet waiting on it.
func TestReadingEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth.json"), `{"openrouter":{"type":"api_key","key":"`+testKey+`"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	cancel()
	reading := Reader{Dir: dir, URL: server.URL, Env: func(string) string { return "" }}.Read(ctx)
	if reading.Status != StatusUnavailable || reading.SpentUSD != nil {
		t.Fatalf("a cancelled read = %+v", reading)
	}
}

// A reading carries no part of the key it was made with, in its JSON or in
// its words.
func TestReadingHoldsNoPartOfTheKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth.json"), `{"openrouter":{"type":"api_key","key":"`+testKey+`"}}`)
	reading := read(t, fixture{dir: dir})
	raised, err := json.Marshal(reading)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{testKey, testKey[10:26], testKey[len(testKey)-16:], "Bearer"} {
		if strings.Contains(string(raised), fragment) || strings.Contains(reading.Detail, fragment) {
			t.Fatalf("the reading holds %q: %s", fragment, raised)
		}
	}
	if reading.KeyID == "" || strings.Contains(testKey, reading.KeyID) {
		t.Fatalf("the fingerprint is no handle on the key, or is part of it: %q", reading.KeyID)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
