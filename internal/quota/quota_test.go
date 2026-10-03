package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// snapshotTime is a minute after the fixtures' generatedAt, so they read as
// fresh unless a test moves the clock.
var snapshotTime = time.Date(2026, 9, 17, 12, 31, 0, 0, time.UTC)

func TestWeeklyReadingSelectsTheProviderAccountWeek(t *testing.T) {
	report, err := Parse(fixture(t, "projected"), snapshotTime)
	if err != nil {
		t.Fatal(err)
	}
	for provider, remaining := range map[string]float64{"claude": 99, "codex": 60} {
		t.Run(provider, func(t *testing.T) {
			reading := report.Weekly(provider, snapshotTime)
			if reading.Status != "available" || reading.PercentRemaining == nil || *reading.PercentRemaining != remaining || reading.ResetsAt.IsZero() || reading.ReadAt.IsZero() || reading.Source != "oauth" {
				t.Fatalf("weekly reading = %+v, want %v%% account weekly remaining with provenance", reading, remaining)
			}
		})
	}
}

func TestWeeklyReadingNeverReplacesSuppliedUnknownRefreshTime(t *testing.T) {
	now := time.Date(2026, 10, 3, 13, 7, 0, 0, time.UTC)
	reset := time.Date(2026, 10, 9, 22, 27, 42, 0, time.UTC)
	for _, test := range []struct {
		name   string
		field  string
		status string
		readAt time.Time
	}{
		{"absent", "", "available", now},
		{"fresh", `,"refreshedAt":"2026-10-03T13:06:00Z"`, "available", now.Add(-time.Minute)},
		{"null", `,"refreshedAt":null`, "unavailable", time.Time{}},
		{"malformed", `,"refreshedAt":"not-a-date"`, "unavailable", time.Time{}},
		{"zero", `,"refreshedAt":"0001-01-01T00:00:00Z"`, "unavailable", time.Time{}},
		{"empty", `,"refreshedAt":""`, "unavailable", time.Time{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(`{"generatedAt":"2026-10-03T13:07:00Z","providers":[{"provider":"codex","source":"oauth","state":{"status":"fresh"%s},"quotaSemantics":{"status":"known","effectiveAvailability":[{"scope":"all_models","status":"known","effectivePercentRemaining":75,"runway":{"status":"through_reset","limitingWindowId":"weekly"}}]},"windows":[{"id":"weekly","percentUsed":25,"resetsAt":"2026-10-09T22:27:42Z"}]}]}`, test.field)
			report, skipped := read(t, &fakeRunner{result: execx.Result{Stdout: []byte(data)}}, now)
			if skipped != "" {
				t.Fatalf("refresh metadata changed reader error behavior: %s", skipped)
			}
			reading := report.Weekly("codex", now)
			projection, err := json.Marshal(struct {
				Provider string `json:"provider"`
				WeeklyReading
			}{Provider: "codex", WeeklyReading: reading})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("weekly presentation %s %s", test.name, projection)
			if reading.Status != test.status || !reading.ReadAt.Equal(test.readAt) {
				t.Errorf("reading = %+v, want %s with read time %s", reading, test.status, test.readAt)
			}
			if test.status == "available" {
				if reading.PercentRemaining == nil || *reading.PercentRemaining != 75 {
					t.Errorf("valid reading = %+v, want 75%%", reading)
				}
			} else if reading.PercentRemaining != nil {
				t.Errorf("supplied unknown refresh time invented remaining percentage: %+v", reading)
			}
			if !reading.ResetsAt.Equal(reset) {
				t.Errorf("reset lost with refresh metadata: %+v", reading)
			}
			if headroom := report.Headroom("codex", ""); headroom != (Headroom{Provider: "codex", Scope: "all_models", Known: true, PercentRemaining: 75, Runway: "through_reset", ResetsAt: reset}) {
				t.Errorf("refresh metadata changed routing evidence: %+v", headroom)
			}
		})
	}
}

func TestReadKeepsRefreshMetadataTypeErrors(t *testing.T) {
	for _, test := range []struct{ value, kind string }{
		{"25", "number"}, {"true", "bool"}, {"{}", "object"}, {"[]", "array"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			data := fmt.Sprintf(`{"generatedAt":"2026-09-17T12:30:00Z","providers":[{"provider":"codex","state":{"refreshedAt":%s}}]}`, test.value)
			parsedReport, err := Parse([]byte(data), snapshotTime)
			var typeError *json.UnmarshalTypeError
			if !errors.As(err, &typeError) || typeError.Value != test.kind || typeError.Type.String() != "string" {
				t.Fatalf("parse error = %v, want a typed JSON failure for %s into string", err, test.kind)
			}
			report, skipped := read(t, &fakeRunner{result: execx.Result{Stdout: []byte(data)}}, snapshotTime)
			t.Logf("typed JSON failure %s: %v; Reader rejection: %s", test.kind, err, skipped)
			if !strings.HasPrefix(skipped, "quota-axi: unparseable snapshot: ") || !strings.Contains(skipped, typeError.Error()) || parsedReport.Headroom("codex", "") != (Headroom{Provider: "codex"}) || report.Headroom("codex", "") != (Headroom{Provider: "codex"}) {
				t.Fatalf("metadata type failure = %q, parsed %+v, read %+v; want unparseable snapshot with JSON error details and no routing evidence", skipped, parsedReport, report)
			}
		})
	}
}

func TestWeeklyReadingPreservesBoundsAndRejectsInvalidMeasurements(t *testing.T) {
	for _, test := range []struct {
		used      string
		remaining float64
		status    string
	}{
		{"0", 100, "available"}, {"100", 0, "available"}, {"95", 5, "available"}, {"94.9", 5.1, "available"},
		{"-1", 0, "unavailable"}, {"101", 0, "unavailable"}, {"null", 0, "unavailable"}, {`"unknown"`, 0, "unavailable"}, {"1e999", 0, "unavailable"},
	} {
		t.Run(test.used, func(t *testing.T) {
			data := fmt.Sprintf(`{"generatedAt":"2026-09-17T12:30:00Z","providers":[{"provider":"codex","source":"oauth","state":{"status":"fresh"},"quotaSemantics":{"status":"known"},"windows":[{"id":"weekly","percentUsed":%s,"resetsAt":"2026-09-20T12:00:00Z"}]}]}`, test.used)
			report, err := Parse([]byte(data), snapshotTime)
			if err != nil {
				t.Fatal(err)
			}
			reading := report.Weekly("codex", snapshotTime)
			if reading.Status != test.status || (test.status == "available" && (reading.PercentRemaining == nil || math.Abs(*reading.PercentRemaining-test.remaining) > 0.000001)) || (test.status != "available" && reading.PercentRemaining != nil) {
				t.Fatalf("reading = %+v, want %s %v", reading, test.status, test.remaining)
			}
			if !reading.ResetsAt.Equal(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)) {
				t.Fatalf("known reset lost with percentage %s: %+v", test.used, reading)
			}
		})
	}
}

func TestWeeklyReadingNamesStaleAndSignInStatesWithoutNumbers(t *testing.T) {
	for _, test := range []struct {
		state  string
		now    time.Time
		status string
	}{
		{`"status":"auth_required","error":"private token payload"`, snapshotTime, "auth_required"},
		{`"status":"fresh","stale":true`, snapshotTime, "stale"},
		{`"status":"fresh","refreshedAt":"2026-09-17T10:00:00Z"`, snapshotTime, "stale"},
		{`"status":"fresh"`, snapshotTime.Add(2 * time.Hour), "stale"},
		{`"status":"fresh"`, snapshotTime.Add(-2 * time.Hour), "unavailable"},
		{`"status":"failed"`, snapshotTime, "unavailable"},
	} {
		t.Run(test.status+test.state, func(t *testing.T) {
			data := fmt.Sprintf(`{"generatedAt":"2026-09-17T12:30:00Z","providers":[{"provider":"claude","source":"oauth","state":{%s},"quotaSemantics":{"status":"known"},"windows":[{"id":"seven_day","percentUsed":20}]}]}`, test.state)
			report, _ := Parse([]byte(data), test.now)
			reading := report.Weekly("claude", test.now)
			if reading.Status != test.status || reading.PercentRemaining != nil {
				t.Fatalf("reading = %+v, want %s without a percentage", reading, test.status)
			}
		})
	}
}

func TestWeeklyReadingNeverUsesAPICreditsOrModelWindows(t *testing.T) {
	for _, test := range []struct{ source, window string }{
		{"api", "weekly"}, {"oauth", "model:codex_bengalfox:5h"}, {"oauth", "five_hour"},
	} {
		t.Run(test.source+test.window, func(t *testing.T) {
			data := fmt.Sprintf(`{"generatedAt":"2026-09-17T12:30:00Z","providers":[{"provider":"codex","source":%q,"state":{"status":"fresh"},"quotaSemantics":{"status":"known"},"credits":{"remaining":500},"windows":[{"id":%q,"percentUsed":0}]}]}`, test.source, test.window)
			report, err := Parse([]byte(data), snapshotTime)
			if err != nil {
				t.Fatal(err)
			}
			reading := report.Weekly("codex", snapshotTime)
			if reading.Status != "unavailable" || reading.PercentRemaining != nil {
				t.Fatalf("non-subscription reading = %+v", reading)
			}
		})
	}
}

func TestParseKeepsTheMeasuredFractionForAllowanceFloors(t *testing.T) {
	data := strings.ReplaceAll(string(fixture(t, "projected")), `"effectivePercentRemaining": 12`, `"effectivePercentRemaining": 3.1`)

	report, err := Parse([]byte(data), snapshotTime)

	if err != nil || float64(report.Providers["claude"].Scopes["model:fable"].PercentRemaining) != 3.1 {
		t.Fatalf("fraction lost from allowance reading: %+v, %v", report.Providers["claude"].Scopes, err)
	}
}

type fakeRunner struct {
	result   execx.Result
	err      error
	requests []execx.Request
}

func (r *fakeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	return r.result, r.err
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func read(t *testing.T, runner *fakeRunner, now time.Time) (Report, string) {
	t.Helper()
	report, skipped := Reader{Commands: runner, Now: func() time.Time { return now }}.Read(context.Background())
	if len(runner.requests) != 1 || runner.requests[0].Name != "quota-axi" || strings.Join(runner.requests[0].Args, " ") != "--json" {
		t.Fatalf("requests = %+v, want one quota-axi --json", runner.requests)
	}
	return report, skipped
}

func TestReadInterpretsAnExhaustedProvider(t *testing.T) {
	// The machine on 2026-09-17: codex weekly at 0% until the 20th, claude
	// fresh with a model-scoped fable window.
	report, skipped := read(t, &fakeRunner{result: execx.Result{Stdout: fixture(t, "exhausted")}}, snapshotTime)
	if skipped != "" {
		t.Fatalf("skipped = %q, want the check to run", skipped)
	}
	codex := report.Headroom("codex", "gpt-5.5")
	if !codex.Known || !codex.Exhausted || codex.Scope != "all_models" || codex.Runway != "exhausted_now" {
		t.Errorf("codex = %+v, want exhausted now on the all-models scope", codex)
	}
	if got, want := codex.String(), "codex all_models exhausted_now, resets 2026-09-20T12:17:37Z"; got != want {
		t.Errorf("codex.String() = %q, want %q", got, want)
	}
	fable := report.Headroom("claude", "fable")
	if !fable.Known || fable.Exhausted || fable.Scope != "model:fable" || fable.PercentRemaining != 97 {
		t.Errorf("fable = %+v, want the model-scoped window with 97%% left", fable)
	}
	if got, want := fable.String(), "claude model:fable 97% remaining, runway through_reset"; got != want {
		t.Errorf("fable.String() = %q, want %q", got, want)
	}
	if opus := report.Headroom("claude", "opus"); opus.Scope != "all_models" || opus.Exhausted {
		t.Errorf("opus = %+v, want the all-models scope when no model window exists", opus)
	}
}

func TestReadInterpretsAProjectedExhaustion(t *testing.T) {
	report, skipped := read(t, &fakeRunner{result: execx.Result{Stdout: fixture(t, "projected")}}, snapshotTime)
	if skipped != "" {
		t.Fatalf("skipped = %q", skipped)
	}
	fable := report.Headroom("claude", "fable")
	if !fable.Known || fable.Exhausted || fable.Runway != "projected_exhaustion" || fable.PercentRemaining != 12 {
		t.Errorf("fable = %+v, want usable but projected to exhaust", fable)
	}
	if got, want := fable.String(), "claude model:fable 12% remaining, runway projected_exhaustion, projected exhausted 2026-09-17T14:05:00Z"; got != want {
		t.Errorf("fable.String() = %q, want %q", got, want)
	}
	if codex := report.Headroom("codex", ""); codex.Exhausted || codex.PercentRemaining != 60 {
		t.Errorf("codex = %+v, want 60%% through reset", codex)
	}
}

func TestReadSkipsWhenQuotaAxiIsMissing(t *testing.T) {
	runner := &fakeRunner{err: errors.New(`exec: "quota-axi": executable file not found in %PATH%`)}
	report, skipped := read(t, runner, snapshotTime)
	if !strings.Contains(skipped, "quota-axi --json") || !strings.Contains(skipped, "executable file not found") {
		t.Errorf("skipped = %q, want the missing tool named", skipped)
	}
	if h := report.Headroom("claude", "fable"); h.Known || h.Exhausted || h.String() != "claude: no quota evidence" {
		t.Errorf("headroom = %+v, want no evidence and no block", h)
	}
}

func TestReadSkipsAFailingUnparseableOrStaleSnapshot(t *testing.T) {
	cases := []struct {
		name   string
		runner *fakeRunner
		now    time.Time
		want   string
	}{
		{"nonzero exit", &fakeRunner{result: execx.Result{ExitCode: 1, Stderr: []byte("boom")}}, snapshotTime, "exited with code 1"},
		{"not json", &fakeRunner{result: execx.Result{Stdout: []byte("not json")}}, snapshotTime, "unparseable"},
		{"no generation time", &fakeRunner{result: execx.Result{Stdout: []byte(`{"providers":[]}`)}}, snapshotTime, "no generation time"},
		{"stale", &fakeRunner{result: execx.Result{Stdout: fixture(t, "exhausted")}}, snapshotTime.Add(2 * time.Hour), "stale (generated 2026-09-17T12:30:54Z)"},
	}
	for _, test := range cases {
		report, skipped := read(t, test.runner, test.now)
		if !strings.Contains(skipped, test.want) {
			t.Errorf("%s: skipped = %q, want %q", test.name, skipped, test.want)
		}
		if h := report.Headroom("codex", ""); h.Known || h.Exhausted {
			t.Errorf("%s: headroom = %+v, want no evidence", test.name, h)
		}
	}
}

func TestReadCollapsesAMultiLineFailureOntoOneLine(t *testing.T) {
	stderr := "node:internal/modules/cjs/loader:1228\n  throw err;\n  ^\n\nError: Cannot find module 'quota-axi'\n    at Module._resolveFilename (node:internal/modules/cjs/loader:1225:15)\r\n"
	_, skipped := read(t, &fakeRunner{result: execx.Result{ExitCode: 1, Stderr: []byte(stderr)}}, snapshotTime)
	if strings.ContainsAny(skipped, "\r\n") {
		t.Errorf("skipped = %q, want one line", skipped)
	}
	if want := "exited with code 1: node:internal/modules/cjs/loader:1228 throw err; ^ Error: Cannot find module 'quota-axi' at Module._resolveFilename (node:internal/modules/cjs/loader:1225:15)"; !strings.HasSuffix(skipped, want) {
		t.Errorf("skipped = %q, want it to end %q", skipped, want)
	}
}

func TestHeadroomTreatsWhatQuotaAxiCannotMeasureAsNoEvidence(t *testing.T) {
	report, err := Parse(fixture(t, "exhausted"), snapshotTime)
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"cursor", "kimi", "grok", "pi"} {
		if h := report.Headroom(harness, "anything"); h.Known || h.Exhausted {
			t.Errorf("%s = %+v, want no evidence: unknown is not zero", harness, h)
		}
	}
}

func TestHeadroomReadsAMeasuredZeroAndAStaleProviderRight(t *testing.T) {
	zero := Report{Providers: map[string]Provider{"claude": {Name: "claude", Known: true, Scopes: map[string]Scope{"all_models": {Name: "all_models", Known: true, PercentRemaining: 0, Runway: "through_reset"}}}}}
	if h := zero.Headroom("claude", "opus"); !h.Exhausted {
		t.Errorf("headroom = %+v, want a measured zero to read as exhausted", h)
	}
	stale := Report{Providers: map[string]Provider{"claude": {Name: "claude", Known: true, Stale: true, Scopes: map[string]Scope{"all_models": {Name: "all_models", Known: true, Runway: "exhausted_now"}}}}}
	if h := stale.Headroom("claude", "opus"); h.Known || h.Exhausted {
		t.Errorf("headroom = %+v, want a stale provider to be no evidence", h)
	}
}

func TestReadRequiresARunner(t *testing.T) {
	if _, skipped := (Reader{}).Read(context.Background()); !strings.Contains(skipped, "command runner is required") {
		t.Errorf("skipped = %q, want the missing runner named", skipped)
	}
}

// AFK mode's report says what was spent while the Overlord was away, so the
// snapshot keeps each window's use and a credit balance, which the headroom
// scopes do not carry.
func TestReadKeepsEachWindowsUseAndACreditBalance(t *testing.T) {
	report, skipped := read(t, &fakeRunner{result: execx.Result{Stdout: fixture(t, "projected")}}, snapshotTime)
	if skipped != "" {
		t.Fatalf("skipped = %q, want the check to run", skipped)
	}

	claude := report.Providers["claude"]
	if len(claude.Windows) != 3 || claude.Windows[0] != (Window{ID: "five_hour", Label: "session", PercentUsed: 88, ResetsAt: time.Date(2026, 9, 17, 17, 20, 0, 570646000, time.UTC)}) || claude.Windows[1].Label != "week" || claude.Windows[1].PercentUsed != 1 {
		t.Errorf("claude windows = %+v, want its session, week and model windows with what each has used", claude.Windows)
	}
	if claude.Credits != nil {
		t.Errorf("claude credits = %+v, want none: quota-axi reported no balance", claude.Credits)
	}
	codex := report.Providers["codex"]
	if codex.Credits == nil || *codex.Credits != (Credits{Remaining: 0, Unit: "credits"}) {
		t.Errorf("codex credits = %+v, want the zero balance quota-axi reported", codex.Credits)
	}
	if len(codex.Windows) != 3 || codex.Windows[0].PercentUsed != 40 {
		t.Errorf("codex windows = %+v, want three with the week at 40%% used", codex.Windows)
	}
}
