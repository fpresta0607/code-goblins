package supervisor

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBrowserPipeRefusesAnotherTasksCallerBeforeLendingOrReleasing(t *testing.T) {
	for _, action := range []string{"attach", "release", "sign-in"} {
		t.Run(action, func(t *testing.T) {
			store, h := testStore(t)
			broker, chrome, _, _ := testBroker(t)
			broker.state = h.State
			brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
			hostTerminal(t, h.State, "pd-keys")
			if action == "release" {
				if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
					t.Fatal(err)
				}
			}
			runPipe(t, &Service{Store: store, browsers: broker})

			_, err := askPipe(h.State, runPipeRequest{Kind: "browser", Browser: &BrowserRequest{Action: action, Task: "pd-keys", Provider: "stripe"}})

			if err == nil || !strings.Contains(err.Error(), "browser caller") {
				t.Fatalf("unproven caller's %s = %v, want refusal before the broker acts", action, err)
			}
			if action != "release" && len(chrome.launches) != 0 {
				t.Fatal("a refused caller started a browser")
			}
			if len(store.Snapshot().BrowserSignIns) != 0 {
				t.Fatal("a refused caller filed a sign-in card")
			}
			if action == "release" && len(chrome.endedPIDs(broker)) != 0 {
				t.Fatal("a refused caller ended another task's browser")
			}
		})
	}
}

func TestGoblinPipeCallerRejectsAConnectionFromAnEarlierProcessLifetime(t *testing.T) {
	stateDir := t.TempDir()
	writeNativeTask(t, stateDir, "pd-keys")
	terminal := hostTerminal(t, stateDir, "pd-keys")
	terminal.standIn(t)
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("cannot read this process's start")
	}

	err := goblinPipeCaller(stateDir, "pd-keys", os.Getpid(), started.Add(-time.Second))

	if err == nil || !strings.Contains(err.Error(), "identity cannot be proven") {
		t.Fatalf("earlier connection = %v, want a reused PID refused", err)
	}
	if err := goblinPipeCaller(stateDir, "pd-keys", os.Getpid(), time.Now()); err != nil {
		t.Fatalf("current caller = %v, want the task's own program accepted", err)
	}
}

func TestHelperPipeRefusesAnotherTasksCaller(t *testing.T) {
	store, h := testStore(t)
	writeNativeTask(t, h.State, "pd-keys")
	runPipe(t, &Service{Store: store})

	_, err := askPipe(h.State, runPipeRequest{Kind: "helper", Helper: &HelperRequest{Parent: "pd-keys", Brief: "Check the browser"}})

	if err == nil || !strings.Contains(err.Error(), "helper caller") {
		t.Fatalf("unproven helper caller = %v, want refusal before dispatch", err)
	}
}

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
	t.Cleanup(func() {
		if err := broker.closeAll(); err != nil {
			t.Error(err)
		}
	})
	terminal := hostTerminal(t, h.State, "pd-keys")
	terminal.standIn(t)
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
