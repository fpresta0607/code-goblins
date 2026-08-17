package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// scriptedRunner replays one result per call in order, which is how a
// browser session's open/snapshot/click sequence is exercised.
type scriptedRunner struct {
	results []execx.Result
	calls   []execx.Request
}

func (r *scriptedRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	index := len(r.calls)
	r.calls = append(r.calls, req)
	if index < len(r.results) {
		return r.results[index], nil
	}
	return execx.Result{}, nil
}

const oauthSnapshot = `page:
  title: Authorize cfo
uid=g1:2_0 RootWebArea "Authorize cfo"
  uid=g1:2_3 link "Cancel"
  uid=g1:2_7 button "Authorize cfo-fleet"
`

func TestBrowserClicksTheApprovalControlAnExistingSessionPutOnScreen(t *testing.T) {
	runner := &scriptedRunner{results: []execx.Result{
		{ExitCode: 0, Stdout: []byte(oauthSnapshot)},
		{ExitCode: 0},
	}}

	note, err := ChromeBrowser{Runner: runner}.Confirm(context.Background(), "https://vercel.com/oauth", []string{"Authorize", "Continue"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !strings.Contains(note, "Authorize cfo-fleet") {
		t.Errorf("note = %q, want it to name the control clicked", note)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want open then click with no extra snapshot", len(runner.calls))
	}
	if got := runner.calls[1].Args; len(got) != 2 || got[0] != "click" || got[1] != "@g1:2_7" {
		t.Errorf("click args = %v, want the ref from the snapshot", got)
	}
}

func TestBrowserRanksLabelsInTheManifestsOrder(t *testing.T) {
	snapshot := `uid=a1 button "Continue"
uid=a2 button "Authorize"
`
	runner := &scriptedRunner{results: []execx.Result{{ExitCode: 0, Stdout: []byte(snapshot)}, {ExitCode: 0}}}

	if _, err := (ChromeBrowser{Runner: runner}).Confirm(context.Background(), "https://x.test", []string{"Authorize", "Continue"}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// "Authorize" is listed first, so it wins even though "Continue" appears
	// earlier on the page.
	if got := runner.calls[1].Args[1]; got != "@a2" {
		t.Errorf("clicked %q, want the manifest's first-ranked label", got)
	}
}

func TestBrowserReportsThatAHumanIsNeededWhenThereIsNothingToClick(t *testing.T) {
	runner := &scriptedRunner{results: []execx.Result{
		{ExitCode: 0, Stdout: []byte(`uid=b1 textbox "Email"`)},
		{ExitCode: 0, Stdout: []byte(`uid=b1 textbox "Email"`)},
	}}

	note, err := ChromeBrowser{Runner: runner}.Confirm(context.Background(), "https://x.test", []string{"Authorize"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !strings.Contains(note, "human") {
		t.Errorf("note = %q, want it to say a human sign-in is required", note)
	}
	// A page with only a login form must never be typed into.
	for _, call := range runner.calls {
		if len(call.Args) > 0 && call.Args[0] == "click" {
			t.Errorf("clicked something on a page with no approval control: %v", call.Args)
		}
	}
}

func TestBrowserRetriesTheSnapshotWhenTheOpenOutputWasStillSettling(t *testing.T) {
	runner := &scriptedRunner{results: []execx.Result{
		{ExitCode: 0, Stdout: []byte("page:\n  title: loading\n")},
		{ExitCode: 0, Stdout: []byte(oauthSnapshot)},
		{ExitCode: 0},
	}}

	if _, err := (ChromeBrowser{Runner: runner}).Confirm(context.Background(), "https://x.test", []string{"Authorize"}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(runner.calls) != 3 || runner.calls[1].Args[0] != "snapshot" {
		t.Fatalf("calls = %d, want open, snapshot, click", len(runner.calls))
	}
}

func TestBrowserDoesNothingWithoutDeclaredLabels(t *testing.T) {
	runner := &scriptedRunner{}
	note, err := ChromeBrowser{Runner: runner}.Confirm(context.Background(), "https://x.test", nil)
	if err != nil || note != "" {
		t.Fatalf("Confirm = (%q, %v), want a silent no-op", note, err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("opened a browser with no approval control declared: %v", runner.calls)
	}
}
