package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures name keys of their own shape: a real provider key is never
// written into this repository, and text a scanner could take for one never
// reaches a push.
const (
	testKey  = "fixture-openrouter-key-one"
	otherKey = "fixture-openrouter-key-two"
	// secretText is shaped like a credential and never is one: it proves no
	// configuration text or provider phrase reaches a reading.
	secretText = "fixture-secret-text-that-must-not-surface"
)

// fixture is one Pi profile, the environment its session started with, and
// the answer OpenRouter gives for it.
type fixture struct {
	dir      string
	auth     string
	settings string
	env      map[string]string
	status   int
	body     string
	before   func(*http.Request)
}

// profile writes the fixture's Pi folder, with a key entry when neither auth
// nor settings says otherwise.
func profile(t *testing.T, f fixture) (string, map[string]string) {
	t.Helper()
	dir := f.dir
	if dir == "" {
		dir = t.TempDir()
	}
	auth := f.auth
	if auth == "" {
		auth = `{"` + Provider + `":{"type":"api_key","key":"` + testKey + `"}}`
	}
	writeFile(t, filepath.Join(dir, "auth.json"), auth)
	if f.settings != "" {
		writeFile(t, filepath.Join(dir, "settings.json"), f.settings)
	}
	return dir, f.env
}

// read answers the fixture's profile from one key document.
func read(t *testing.T, f fixture) Reading {
	t.Helper()
	dir, env := profile(t, f)
	body := f.body
	if body == "" {
		body = `{"data":{"usage":25.5,"usage_daily":1,"usage_weekly":9.04,"usage_monthly":20,"byok_usage":0,"byok_usage_daily":0,"byok_usage_weekly":0,"byok_usage_monthly":0,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly","include_byok_in_limit":false,"is_management_key":false,"is_free_tier":false,"budgets":[],"effective_budget":null}}`
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.before != nil {
			f.before(r)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer server.Close()
	return Reader{Source: Source{Dir: dir, Profile: "default", Env: env}, URL: server.URL, Now: func() time.Time {
		return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	}}.Read(context.Background())
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
			if reading.ReadAt.IsZero() || reading.ReadAt.Location() != time.UTC {
				t.Errorf("read_at = %v", reading.ReadAt)
			}
			if reading.Profile != "default" {
				t.Errorf("profile = %q", reading.Profile)
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
		{name: "a member budget", document: `{"usage_weekly":9.04,"usage":9.04,"limit":100,"limit_remaining":90.96,"limit_reset":"weekly","effective_budget":` + member + `,"budgets":[{"scope":"key_limit","limit_usd":100,"spend_usd":9.04,"remaining_usd":90.96,"reset_interval":"weekly","resets_at":"2026-10-19T00:00:00.000Z","include_byok":false},` + member + `]}`, want: "member"},
		{name: "the key's own limit", document: `{"usage_weekly":9.04,"usage":9.04,"limit":100,"limit_remaining":90.96,"limit_reset":"weekly","effective_budget":{"scope":"key_limit","limit_usd":100,"spend_usd":9.04,"remaining_usd":90.96,"reset_interval":"weekly","resets_at":null,"include_byok":false}}`, want: ""},
		{name: "nothing budgeted", document: `{"usage_weekly":9.04,"usage":9.04,"limit":null,"limit_remaining":null,"limit_reset":null,"effective_budget":null}`, want: ""},
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

// A period a key counts over rolls over on the documented UTC boundary, so a
// reading taken yesterday never reads as this period's, even when no budget
// layer states the reset time.
func TestReadingRollsEachPeriodOverOnItsDocumentedUTCBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		now    time.Time
		period string
		want   string
	}{
		{name: "a daily window", now: time.Date(2026, 10, 15, 23, 59, 0, 0, time.UTC), period: "daily", want: "2026-10-16T00:00:00Z"},
		{name: "a daily window at midnight", now: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), period: "daily", want: "2026-10-17T00:00:00Z"},
		{name: "a weekly window on a Thursday", now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), period: "weekly", want: "2026-10-19T00:00:00Z"},
		{name: "a weekly window on a Monday", now: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), period: "weekly", want: "2026-10-26T00:00:00Z"},
		{name: "a weekly window on a Sunday", now: time.Date(2026, 10, 18, 22, 0, 0, 0, time.UTC), period: "weekly", want: "2026-10-19T00:00:00Z"},
		{name: "a monthly window", now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), period: "monthly", want: "2026-11-01T00:00:00Z"},
		{name: "a monthly window at a month's end", now: time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), period: "monthly", want: "2027-01-01T00:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reading := Reader{Source: Source{Dir: t.TempDir()}, URL: "http://127.0.0.1:1/", Now: func() time.Time { return test.now }}.Read(context.Background())
			if reading.Status != StatusMissing {
				t.Fatalf("this fixture needs no server: status = %q", reading.Status)
			}
			reset := rollover(test.period, test.now)
			if got := reset.Format(time.RFC3339); got != test.want {
				t.Fatalf("%s rollover at %s = %s, want %s", test.period, test.now.Format(time.RFC3339), got, test.want)
			}
		})
	}
	// The reading itself carries the boundary, and a key's own budget states
	// one exactly.
	weekly := read(t, fixture{body: `{"data":{"usage_weekly":9.04,"usage":9.04,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly"}}`})
	if got := weekly.ResetsAt.Format(time.RFC3339); got != "2026-10-19T00:00:00Z" {
		t.Errorf("a weekly reading resets at %q, want the next Monday", got)
	}
	stated := read(t, fixture{body: `{"data":{"usage_weekly":9.04,"usage":9.04,"limit":50,"limit_remaining":40.96,"limit_reset":"weekly","budgets":[{"scope":"key_limit","limit_usd":50,"spend_usd":9.04,"remaining_usd":40.96,"reset_interval":"weekly","resets_at":"2026-10-21T09:00:00.000Z","include_byok":false}]}}`})
	if got := stated.ResetsAt.Format(time.RFC3339); got != "2026-10-21T09:00:00Z" {
		t.Errorf("a stated reset = %q, want the budget's own time", got)
	}
	lifetime := read(t, fixture{body: `{"data":{"usage":9.04,"limit":50,"limit_remaining":40.96,"limit_reset":null}}`})
	if !lifetime.ResetsAt.IsZero() {
		t.Errorf("a lifetime limit resets at %v, want no boundary", lifetime.ResetsAt)
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

// An answer with no key document, or one with no figures at all, is no
// reading: it is never an available spend of nothing.
func TestReadingRefusesAnAnswerWithNoKeyDocument(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"data":null}`,
		`{"data":{}}`,
		`{"data":{"usage":null,"usage_weekly":null,"limit":null}}`,
		`{"error":{"code":500}}`,
	} {
		reading := read(t, fixture{body: body})
		if reading.Status != StatusUnavailable {
			t.Fatalf("%s read as %q", body, reading.Status)
		}
		if reading.SpentUSD != nil || reading.LimitUSD != nil || reading.ByokUSD != nil {
			t.Fatalf("%s stated money: %+v", body, reading)
		}
		if !strings.Contains(reading.Detail, "no key reading") {
			t.Errorf("%s detail = %q", body, reading.Detail)
		}
	}
}

// No key is a state of its own, named as missing, and never a spend of zero.
func TestReadingSaysWhenTheProfileHasNoKey(t *testing.T) {
	for _, test := range []struct {
		name   string
		auth   string
		env    map[string]string
		want   string
		detail string
	}{
		{name: "no provider entry", auth: `{"deepseek":{"type":"api_key","key":"fixture-other"}}`, want: StatusMissing, detail: "Pi has no OpenRouter key"},
		{name: "an entry that names no key", auth: `{"openrouter":{"type":"api_key"}}`, want: StatusMissing, detail: "names no key"},
		{name: "an entry whose key is empty", auth: `{"openrouter":{"type":"api_key","key":""}}`, want: StatusMissing, detail: "names no key"},
		{name: "an entry that is not an API key", auth: `{"openrouter":{"type":"oauth","key":"fixture-other"}}`, want: StatusMissing, detail: "not an API key"},
		{name: "an entry with no type, which Pi itself refuses", auth: `{"openrouter":{"key":"fixture-other"}}`, want: StatusMissing, detail: "not an API key"},
		{name: "a key from the environment", auth: `{}`, env: map[string]string{EnvKey: testKey}, want: StatusAvailable},
		{name: "an environment key in another case", auth: `{}`, env: map[string]string{"openrouter_api_key": testKey}, want: StatusAvailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := fixture{auth: test.auth, env: test.env}
			if f.auth == "" {
				f.auth = `{"deepseek":{"type":"api_key","key":"fixture-other"}}`
			}

			// Act
			reading := read(t, f)

			// Assert
			if reading.Status != test.want {
				t.Fatalf("status = %q, detail = %q, want %q", reading.Status, reading.Detail, test.want)
			}
			if test.want == StatusAvailable {
				if reading.KeyID != fingerprint(testKey) || reading.SpentUSD == nil {
					t.Fatalf("an environment key was not read: %+v", reading)
				}
				if !reading.CredentialSince.IsZero() {
					t.Errorf("an environment key has no written time: %v", reading.CredentialSince)
				}
				return
			}
			if reading.SpentUSD != nil || reading.KeyID != "" {
				t.Errorf("a missing key states a spend: %+v", reading)
			}
			if !strings.Contains(reading.Detail, test.detail) {
				t.Errorf("detail = %q, want %q", reading.Detail, test.detail)
			}
		})
	}
}

// Pi's auth file is read before the environment, and an entry that names an
// environment variable is resolved the way Pi's own resolver resolves it.
func TestReadingResolvesTheKeyTheWayPiDoes(t *testing.T) {
	for _, test := range []struct {
		name     string
		auth     string
		env      map[string]string
		wantKey  string
		wantRefu string
	}{
		{name: "the auth file before the environment", env: map[string]string{EnvKey: otherKey}, wantKey: testKey},
		{name: "the entry's own environment first", auth: `{"openrouter":{"type":"api_key","key":"$MY_KEY","env":{"MY_KEY":"` + testKey + `"}}}`, env: map[string]string{"MY_KEY": otherKey}, wantKey: testKey},
		{name: "an empty entry value falls through to the session", auth: `{"openrouter":{"type":"api_key","key":"$MY_KEY","env":{"MY_KEY":""}}}`, env: map[string]string{"MY_KEY": testKey}, wantKey: testKey},
		{name: "a braced variable from the environment", auth: `{"openrouter":{"type":"api_key","key":"${PI_OPENROUTER}"}}`, env: map[string]string{"PI_OPENROUTER": testKey}, wantKey: testKey},
		{name: "a prefix around a variable", auth: `{"openrouter":{"type":"api_key","key":"fixture-${SUFFIX}"}}`, env: map[string]string{"SUFFIX": "abc"}, wantKey: "fixture-abc"},
		{name: "an underscore name", auth: `{"openrouter":{"type":"api_key","key":"$_KEY1"}}`, env: map[string]string{"_KEY1": testKey}, wantKey: testKey},
		{name: "a name that stops at a dash", auth: `{"openrouter":{"type":"api_key","key":"$A-B"}}`, env: map[string]string{"A": testKey}, wantKey: testKey + "-B"},
		{name: "an escaped dollar", auth: `{"openrouter":{"type":"api_key","key":"$$literal"}}`, wantKey: "$literal"},
		{name: "an escaped bang", auth: `{"openrouter":{"type":"api_key","key":"$!literal"}}`, wantKey: "!literal"},
		{name: "a name that is not an environment name stays as written", auth: `{"openrouter":{"type":"api_key","key":"${1BAD}"}}`, wantKey: "${1BAD}"},
		{name: "a digit after the dollar stays as written", auth: `{"openrouter":{"type":"api_key","key":"$1BAD"}}`, wantKey: "$1BAD"},
		{name: "an unclosed brace keeps its dollar", auth: `{"openrouter":{"type":"api_key","key":"${UNCLOSED"}}`, wantKey: "${UNCLOSED"},
		{name: "a variable nothing holds", auth: `{"openrouter":{"type":"api_key","key":"${NOT_SET_ANYWHERE}"}}`, wantRefu: "NOT_SET_ANYWHERE"},
		{name: "a key a command resolves", auth: `{"openrouter":{"type":"api_key","key":"!security find-generic-password -ws openrouter"}}`, wantRefu: "resolved by a command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := test.auth
			if auth == "" {
				auth = `{"` + Provider + `":{"type":"api_key","key":"` + testKey + `"}}`
			}

			reading := read(t, fixture{auth: auth, env: test.env})

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

// A key expression the board cannot resolve, and a provider's own words,
// never carry their text into a reading a browser could show.
func TestReadingKeepsConfigurationAndProviderTextOutOfItsWords(t *testing.T) {
	for _, test := range []struct {
		name, auth, want string
	}{
		// An invalid braced name is no environment reference at all: Pi keeps
		// the text, so it becomes the key itself, and the reading holds no
		// part of it.
		{name: "a malformed braced name carrying a secret", auth: `{"openrouter":{"type":"api_key","key":"${SECRET=` + secretText + `}"}}`, want: StatusAvailable},
		{name: "an unset variable named after a secret", auth: `{"openrouter":{"type":"api_key","key":"${` + secretText + `}"}}`, want: StatusAvailable},
		{name: "a valid variable that is not set", auth: `{"openrouter":{"type":"api_key","key":"${` + "NOT_SET" + `_ANYWHERE}"}}`, want: StatusMissing},
		{name: "a key a command would resolve", auth: `{"openrouter":{"type":"api_key","key":"!echo ` + secretText + `"}}`, want: StatusMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			reading := read(t, fixture{auth: test.auth})
			raised, err := json.Marshal(reading)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raised), secretText) || strings.Contains(reading.Detail, secretText) {
				t.Fatalf("a configuration's text reached the reading: %s", raised)
			}
			if reading.Status != test.want {
				t.Fatalf("status = %q, want %q", reading.Status, test.want)
			}
			if strings.Contains(test.auth, "NOT_SET_ANYWHERE") && !strings.Contains(reading.Detail, "NOT_SET_ANYWHERE") {
				t.Errorf("the refusal names no validated variable: %q", reading.Detail)
			}
		})
	}

	// A provider's reason phrase is its own untrusted words: the reading says
	// the status code and nothing the provider wrote.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("the fixture server cannot answer a raw response")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(conn, "HTTP/1.1 500 %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", secretText)
		conn.Close()
	}))
	defer server.Close()
	dir, env := profile(t, fixture{})
	reading := Reader{Source: Source{Dir: dir, Env: env}, URL: server.URL}.Read(context.Background())
	raised, err := json.Marshal(reading)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raised), secretText) || strings.Contains(reading.Detail, secretText) {
		t.Fatalf("a provider's reason phrase reached the reading: %s", raised)
	}
	if reading.Status != StatusUnavailable || !strings.Contains(reading.Detail, "HTTP 500") {
		t.Fatalf("reading = %+v", reading)
	}
}

// A reading made from the auth file says when that file was last written, so
// a session that started earlier is never claimed for a key written since.
func TestReadingDatesTheCredentialFileItRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	writeFile(t, path, `{"openrouter":{"type":"api_key","key":"`+testKey+`"}}`)
	written := time.Date(2026, 10, 10, 8, 30, 0, 0, time.UTC)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"usage":1,"usage_weekly":1,"limit":50,"limit_remaining":49,"limit_reset":"weekly"}}`))
	}))
	defer server.Close()
	reading := Reader{Source: Source{Dir: dir}, URL: server.URL}.Read(context.Background())
	if reading.Status != StatusAvailable {
		t.Fatalf("reading = %+v", reading)
	}
	if reading.CredentialSince.IsZero() || !reading.CredentialSince.Equal(written) {
		t.Fatalf("credential written at %v, want %v", reading.CredentialSince, written)
	}
	if raised, err := json.Marshal(reading); err != nil || strings.Contains(string(raised), written.Format(time.RFC3339)) {
		t.Errorf("the credential's file time reached the snapshot: %s", raised)
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
	dir, env := profile(t, fixture{})
	redirected := Reader{Source: Source{Dir: dir, Env: env}, URL: redirect.URL}.Read(context.Background())
	if redirected.Status != StatusUnavailable || !strings.Contains(redirected.Detail, "302") {
		t.Fatalf("a redirect read as: %+v", redirected)
	}
}

// Every way a read can fail has its own honest state, and none of them is a
// spend of zero or a word taken from the provider's answer.
func TestReadingSaysWhyItCouldNotBeTaken(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
		detail string
	}{
		{name: "a refused key", status: http.StatusUnauthorized, want: StatusAuthRequired, detail: "refused"},
		{name: "a forbidden key", status: http.StatusForbidden, want: StatusAuthRequired, detail: "refused"},
		{name: "a rate limit", status: http.StatusTooManyRequests, want: StatusRateLimited, detail: "rate limited"},
		{name: "a server error", status: http.StatusInternalServerError, want: StatusUnavailable, detail: "HTTP 500"},
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
	dir, env := profile(t, fixture{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	cancel()
	reading := Reader{Source: Source{Dir: dir, Env: env}, URL: server.URL}.Read(ctx)
	if reading.Status != StatusUnavailable || reading.SpentUSD != nil {
		t.Fatalf("a cancelled read = %+v", reading)
	}
}

// A reading carries no part of the key it was made with, in its JSON or in
// its words.
func TestReadingHoldsNoPartOfTheKey(t *testing.T) {
	reading := read(t, fixture{})
	raised, err := json.Marshal(reading)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{testKey, testKey[8:24], testKey[len(testKey)-12:], "Bearer"} {
		if strings.Contains(string(raised), fragment) || strings.Contains(reading.Detail, fragment) {
			t.Fatalf("the reading holds %q: %s", fragment, raised)
		}
	}
	if reading.KeyID == "" || strings.Contains(testKey, reading.KeyID) {
		t.Fatalf("the fingerprint is no handle on the key, or is part of it: %q", reading.KeyID)
	}
}

// DefaultProvider reads the provider Pi's own settings start with, and names
// none when the settings cannot be read.
func TestDefaultProviderReadsPisOwnSettings(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		want     string
	}{
		{name: "openrouter", settings: `{"defaultProvider":"openrouter"}`, want: "openrouter"},
		{name: "another provider", settings: `{"defaultProvider":"deepseek"}`, want: "deepseek"},
		{name: "no settings at all", want: ""},
		{name: "settings that name none", settings: `{}`, want: ""},
		{name: "settings that cannot be read", settings: `{`, want: ""},
		{name: "no folder", settings: "openrouter", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.name == "no folder" {
				dir = ""
			}
			if test.settings != "openrouter" && test.settings != "" {
				writeFile(t, filepath.Join(dir, "settings.json"), test.settings)
			}
			if got := DefaultProvider(dir); got != test.want {
				t.Fatalf("DefaultProvider = %q, want %q", got, test.want)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
