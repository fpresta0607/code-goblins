package quota

import (
	"context"
	"errors"
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
