package supervisor

import (
	"net/http"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin's ask over the supervisor's pipe starts a real Chrome on the
// login's own profile, headless and held to its provider's hosts, and giving
// it back closes it until nothing of it holds the profile.
func TestABrowserLentOverThePipeIsAHeadlessChromeOnTheLoginsProfileThatCloses(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	broker := newBrowserBroker(h.State, Options{
		KeyPages: keypages.Recipes{Providers: []keypages.Provider{{ID: "stripe", Name: "Stripe", Hosts: []string{"stripe.com", "*.stripe.com"}}}},
		Dispatch: &Dispatch{Memory: func() (Memory, error) { return Memory{Available: 8 << 30, CommitAvailable: 8 << 30}, nil }},
	}, func(err error) { t.Errorf("a browser was not closed: %v", err) })
	// The stand-in task runs in no terminal of its own.
	broker.live = func(string) error { return nil }
	runPipe(t, &Service{Store: store, browsers: broker})
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "pd-keys", Project: `C:\dev\PrecisionDocs-AI`, Backend: "native", Window: "native", Harness: "claude", Kind: "ship", Worktree: `C:\cfo\worktrees\pd-keys`}); err != nil {
		t.Fatal(err)
	}

	// Act
	reply, err := askPipe(h.State, runPipeRequest{Kind: "browser", Browser: &BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if reply.Browser == nil || reply.Browser.Login != "stripe-precisiondocs-ai" {
		t.Fatalf("reply = %+v, want the project's Stripe login lent", reply)
	}
	response, err := http.Get(reply.Browser.URL + "/json/version")
	if err != nil {
		t.Fatalf("the lent browser does not answer at %s: %v", reply.Browser.URL, err)
	}
	response.Body.Close()
	logins, err := browserlogin.Read(h.State)
	if err != nil || len(logins) != 1 || logins[0].Browser == nil {
		t.Fatalf("registry = (%+v, %v), want the lent browser recorded", logins, err)
	}
	lent := browserlogin.Process{PID: logins[0].Browser.PID, Started: logins[0].Browser.Started}
	arguments, err := proc.Arguments(lent.PID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--headless", "--user-data-dir=" + browserlogin.ProfileDir(h.State, "stripe-precisiondocs-ai"), "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE stripe.com, EXCLUDE *.stripe.com"} {
		if !slices.Contains(arguments, want) {
			t.Errorf("chrome %d's arguments %q lack %q", lent.PID, arguments, want)
		}
	}

	if _, err := askPipe(h.State, runPipeRequest{Kind: "browser", Browser: &BrowserRequest{Action: "release", Task: "pd-keys", Provider: "stripe"}}); err != nil {
		t.Fatal(err)
	}
	broker.work.Wait()
	if browserlogin.Running(lent) {
		t.Fatal("the browser still runs after it was given back")
	}
	if logins, err := browserlogin.Read(h.State); err != nil || logins[0].Browser != nil || logins[0].UsedAt == nil {
		t.Fatalf("registry = (%+v, %v), want the login given back and used", logins, err)
	}
}
