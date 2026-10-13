package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fakeChrome stands in for Chrome: it records each launch and each end, and
// a process stays alive until it is ended or killed. An end waits for
// closing while it is set.
type fakeChrome struct {
	mu       sync.Mutex
	launches []browserlogin.Launch
	ended    []int
	alive    map[int]bool
	next     int
	closing  chan struct{}
}

func (f *fakeChrome) start(_ context.Context, launch browserlogin.Launch) (browserlogin.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches = append(f.launches, launch)
	f.next++
	f.alive[f.next] = true
	return browserlogin.Process{PID: f.next, Started: time.Unix(int64(f.next), 0), URL: fmt.Sprintf("http://127.0.0.1:%d", 9000+f.next)}, nil
}

func (f *fakeChrome) end(_ context.Context, process browserlogin.Process) error {
	if f.closing != nil {
		<-f.closing
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, process.PID)
	f.alive[process.PID] = false
	return nil
}

// endedPIDs are the Chromes ended once every close the broker began is
// over.
func (f *fakeChrome) endedPIDs(broker *browserBroker) []int {
	broker.work.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ended)
}

// testBroker is a broker on a scratch state folder with Stripe's recipe, a
// machine with free memory, and tasks that are live unless gone names them.
func testBroker(t *testing.T) (*browserBroker, *fakeChrome, map[string]bool, *time.Time) {
	t.Helper()
	chrome := &fakeChrome{alive: map[int]bool{}}
	gone := map[string]bool{}
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	broker := &browserBroker{
		state:   t.TempDir(),
		data:    t.TempDir(),
		recipes: keypages.Recipes{Providers: []keypages.Provider{{ID: "stripe", Name: "Stripe", Hosts: []string{"stripe.com", "*.stripe.com"}}}},
		memory:  func() (Memory, error) { return Memory{Available: 8 << 30, CommitAvailable: 8 << 30}, nil },
		live: func(task string) error {
			if gone[task] {
				return errors.New(task + " has left")
			}
			return nil
		},
		executable: func() (string, error) { return `C:\chrome\chrome.exe`, nil },
		start:      chrome.start,
		end:        chrome.end,
		alive: func(process browserlogin.Process) bool {
			chrome.mu.Lock()
			defer chrome.mu.Unlock()
			return chrome.alive[process.PID]
		},
		now:       func() time.Time { return now },
		report:    func(err error) { t.Errorf("a browser was not closed: %v", err) },
		processes: map[string]browserlogin.Process{},
		closing:   map[string]bool{},
		signing:   map[string]string{},
	}
	return broker, chrome, gone, &now
}

// brokerTask records a live goblin task of project.
func brokerTask(t *testing.T, broker *browserBroker, id, project string) {
	t.Helper()
	meta := state.TaskMeta{ID: id, Project: filepath.Join(`C:\dev`, project), Backend: "native", Window: "native", Harness: "claude", Kind: "ship", Worktree: filepath.Join(`C:\cfo\worktrees`, id), Credentials: []string{"stripe"}, HasCredentials: true}
	if err := state.WriteTaskMeta(broker.state, meta); err != nil {
		t.Fatal(err)
	}
	brokerManifest(t, broker, project, auth.Service{Name: "stripe", Method: auth.MethodEnv, Env: []string{"STRIPE_TEST_SECRET_KEY"}})
}

func brokerManifest(t *testing.T, broker *browserBroker, project string, services ...auth.Service) {
	t.Helper()
	path := auth.ManifestPath(broker.data, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(auth.Manifest{Project: auth.ProjectName(project), Services: services})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func registered(t *testing.T, broker *browserBroker) []browserlogin.Login {
	t.Helper()
	logins, err := browserlogin.Read(broker.state)
	if err != nil {
		t.Fatal(err)
	}
	return logins
}

func TestABrowserIsLentOnTheProjectsOwnLoginHeadlessAndReachingOnlyItsProvidersHosts(t *testing.T) {
	broker, chrome, _, now := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")

	lease, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"})
	if err != nil {
		t.Fatal(err)
	}

	if lease.Login != "stripe-precisiondocs-ai" || lease.URL != "http://127.0.0.1:9001" || lease.Provider != "Stripe" || lease.Account != "PrecisionDocs-AI" {
		t.Fatalf("lease = %+v, want the project's own Stripe login at the started Chrome's address", lease)
	}
	if len(chrome.launches) != 1 {
		t.Fatalf("launches = %d, want 1", len(chrome.launches))
	}
	launch := chrome.launches[0]
	if !launch.Headless || !slices.Equal(launch.Hosts, []string{"stripe.com", "*.stripe.com"}) || launch.Profile != browserlogin.ProfileDir(broker.state, "stripe-precisiondocs-ai") {
		t.Fatalf("launch = %+v, want a headless Chrome on the login's profile reaching only Stripe's hosts", launch)
	}
	logins := registered(t, broker)
	if len(logins) != 1 || !slices.Equal(logins[0].Projects, []string{"PrecisionDocs-AI"}) || logins[0].Browser == nil || logins[0].Browser.Holder != "pd-keys" || !logins[0].Browser.Since.Equal(*now) || logins[0].Browser.PID != 1 {
		t.Fatalf("registry = %+v, want the login recorded with its browser lent to pd-keys", logins)
	}
}

func TestALoginLentToOneTaskIsRefusedToAnotherUntilItIsGivenBack(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	brokerTask(t, broker, "pd-other", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}

	_, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-other", Provider: "stripe"})
	if err == nil || !strings.Contains(err.Error(), "lent to pd-keys") || !strings.Contains(err.Error(), "Ask again") {
		t.Fatalf("second task's attach = %v, want it refused naming the holder and when to ask again", err)
	}
	if len(chrome.launches) != 1 {
		t.Fatalf("launches = %d, want no second Chrome on the profile", len(chrome.launches))
	}

	if _, err := broker.take(context.Background(), BrowserRequest{Action: "release", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	chrome.endedPIDs(broker)
	lease, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-other", Provider: "stripe"})
	if err != nil || lease.Login != "stripe-precisiondocs-ai" {
		t.Fatalf("attach after release = (%+v, %v), want the same login lent to the next task", lease, err)
	}
}

func TestALoginWhoseBrowserIsStillClosingIsLentOnlyOnceItIsGone(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	chrome.closing = make(chan struct{})

	if _, err := broker.take(context.Background(), BrowserRequest{Action: "release", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	_, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"})
	if err == nil || !strings.Contains(err.Error(), "still closing") {
		t.Fatalf("attach while closing = %v, want it refused until the browser is gone", err)
	}

	close(chrome.closing)
	chrome.endedPIDs(broker)
	if lease, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil || lease.URL != "http://127.0.0.1:9002" {
		t.Fatalf("attach once closed = (%+v, %v), want a new browser", lease, err)
	}
}

func TestALoginWhoseChromeExitedIsClosedBeforeItIsLentAgain(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	chrome.mu.Lock()
	chrome.alive[1] = false
	chrome.mu.Unlock()

	_, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"})

	if err == nil || !strings.Contains(err.Error(), "still closing") {
		t.Fatalf("attach over an exited Chrome = %v, want it refused while what is left of it is ended", err)
	}
	if ended := chrome.endedPIDs(broker); !slices.Equal(ended, []int{1}) {
		t.Fatalf("ended = %v, want the exited Chrome's job ended", ended)
	}
	if lease, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil || lease.URL != "http://127.0.0.1:9002" {
		t.Fatalf("attach once it is ended = (%+v, %v), want a new browser", lease, err)
	}
}

func TestTheSameTaskAskingAgainKeepsItsBrowserAndRenewsItsLease(t *testing.T) {
	broker, chrome, _, now := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	first, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(20 * time.Minute)

	again, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"})
	if err != nil {
		t.Fatal(err)
	}

	if again.URL != first.URL || len(chrome.launches) != 1 {
		t.Fatalf("second attach = %+v after %d launches, want the same browser", again, len(chrome.launches))
	}
	if since := registered(t, broker)[0].Browser.Since; !since.Equal(*now) {
		t.Fatalf("lease since %s, want renewed at %s", since, *now)
	}
}

func TestEachProjectGetsALoginOfItsOwn(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	brokerTask(t, broker, "shift-keys", "SIQshift")

	pd, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"})
	if err != nil {
		t.Fatal(err)
	}
	shift, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "shift-keys", Provider: "stripe"})
	if err != nil {
		t.Fatal(err)
	}

	if pd.Login == shift.Login || shift.Login != "stripe-siqshift" || len(chrome.launches) != 2 || chrome.launches[0].Profile == chrome.launches[1].Profile {
		t.Fatalf("logins %s and %s on %d launches, want one login and profile per project", pd.Login, shift.Login, len(chrome.launches))
	}
}

func TestOnlyAHarnessWithTheBrowserSealHookCanExploreALogin(t *testing.T) {
	for _, harness := range []string{"codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			broker, chrome, _, _ := testBroker(t)
			brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
			meta, err := state.ReadTaskMeta(broker.state, "pd-keys")
			if err != nil {
				t.Fatal(err)
			}
			meta.Harness = harness
			if err := state.WriteTaskMeta(broker.state, meta); err != nil {
				t.Fatal(err)
			}

			_, err = broker.attach(context.Background(), "pd-keys", "stripe")

			if err == nil || !strings.Contains(err.Error(), "PreToolUse") || len(chrome.launches) != 0 {
				t.Fatalf("%s attach = %v, launches %d, want refusal before a browser starts", harness, err, len(chrome.launches))
			}
		})
	}
}

func TestALoginIDAnotherProjectHoldsIsNeverShared(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	if err := browserlogin.Write(broker.state, []browserlogin.Login{{ID: "stripe-acme-ai", Provider: "stripe", Account: "Acme.AI", Projects: []string{"Acme.AI"}}}); err != nil {
		t.Fatal(err)
	}
	brokerTask(t, broker, "acme-keys", "acme-ai")

	_, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "acme-keys", Provider: "stripe"})

	if err == nil || !strings.Contains(err.Error(), "belongs to another project") || len(chrome.launches) != 0 {
		t.Fatalf("attach = %v after %d launches, want it refused with nothing started", err, len(chrome.launches))
	}
}

func TestABrowserRequestIsRefusedWithNothingStarted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*browserBroker, map[string]bool)
		request BrowserRequest
		want    string
	}{
		{"a provider with no recipe", nil, BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "resend"}, `no key page recipe for "resend"`},
		{"a task with no record", nil, BrowserRequest{Action: "attach", Task: "pd-nobody", Provider: "stripe"}, "has no live record"},
		{"a task that has left", func(_ *browserBroker, gone map[string]bool) { gone["pd-keys"] = true }, BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}, "pd-keys has left"},
		{"free memory under the floor", func(broker *browserBroker, _ map[string]bool) {
			broker.memory = func() (Memory, error) { return Memory{Available: 3 << 30, CommitAvailable: 9 << 30}, nil }
		}, BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}, "under the 4 GB a browser waits for"},
		{"an unknown action", nil, BrowserRequest{Action: "borrow", Task: "pd-keys", Provider: "stripe"}, "attaches or releases"},
		{"a value shaped like no task", nil, BrowserRequest{Action: "attach", Task: "../x", Provider: "stripe"}, "task"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker, chrome, gone, _ := testBroker(t)
			brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
			if tc.prepare != nil {
				tc.prepare(broker, gone)
			}

			_, err := broker.take(context.Background(), tc.request)

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("take = %v, want a refusal saying %q", err, tc.want)
			}
			if len(chrome.launches) != 0 {
				t.Fatalf("started %d Chromes on a refused request", len(chrome.launches))
			}
		})
	}
}

func TestGivingABrowserBackClosesItAndRecordsTheLoginUsed(t *testing.T) {
	broker, chrome, _, now := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Minute)

	if _, err := broker.take(context.Background(), BrowserRequest{Action: "release", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}

	if ended := chrome.endedPIDs(broker); !slices.Equal(ended, []int{1}) {
		t.Fatalf("ended = %v, want the lent Chrome", ended)
	}
	login := registered(t, broker)[0]
	if login.Browser != nil || login.UsedAt == nil || !login.UsedAt.Equal(*now) {
		t.Fatalf("login = %+v, want no browser and used now", login)
	}
}

func TestTheSweepClosesABrowserPastItsLeaseOrWhoseTaskLeft(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(*fakeChrome, map[string]bool, *time.Time)
	}{
		{"past its lease", func(_ *fakeChrome, _ map[string]bool, now *time.Time) { *now = now.Add(browserLease + time.Second) }},
		{"its task left", func(_ *fakeChrome, gone map[string]bool, _ *time.Time) { gone["pd-keys"] = true }},
		{"its Chrome exited", func(chrome *fakeChrome, _ map[string]bool, _ *time.Time) { chrome.alive[1] = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker, chrome, gone, now := testBroker(t)
			brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
			if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
				t.Fatal(err)
			}
			tc.after(chrome, gone, now)

			if err := broker.sweep(); err != nil {
				t.Fatal(err)
			}

			if ended := chrome.endedPIDs(broker); !slices.Equal(ended, []int{1}) || registered(t, broker)[0].Browser != nil {
				t.Fatalf("ended %v, registry %+v, want the browser closed and given back", ended, registered(t, broker))
			}
		})
	}
}

func TestTheSweepKeepsABrowserInUseAndForgetsOneAStoppedSupervisorLeft(t *testing.T) {
	broker, chrome, _, now := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(browserLease - time.Minute)
	if err := broker.sweep(); err != nil {
		t.Fatal(err)
	}
	if len(chrome.endedPIDs(broker)) != 0 || registered(t, broker)[0].Browser == nil {
		t.Fatal("the sweep closed a browser in use within its lease")
	}

	// A new supervisor's broker never started the recorded Chrome, which
	// ended with the supervisor that held its job.
	restarted, _, _, _ := testBroker(t)
	restarted.state = broker.state
	if err := restarted.sweep(); err != nil {
		t.Fatal(err)
	}
	if registered(t, restarted)[0].Browser != nil {
		t.Fatal("a new supervisor kept the record of a browser it never started")
	}
}

func TestAStoppingSupervisorClosesItsBrowsersAndLendsNoMore(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}

	if err := broker.closeAll(); err != nil {
		t.Fatal(err)
	}

	if ended := chrome.endedPIDs(broker); !slices.Equal(ended, []int{1}) {
		t.Fatalf("ended = %v, want the lent Chrome closed", ended)
	}
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err == nil || !strings.Contains(err.Error(), "stopping") {
		t.Fatalf("attach after closeAll = %v, want it refused", err)
	}
}

func TestALoginCannotBeLentAgainWhenItsBrowserDidNotClose(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	broker.end = func(context.Context, browserlogin.Process) error { return errors.New("still running") }
	var reported error
	broker.report = func(err error) { reported = err }
	if err := broker.release("pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	broker.work.Wait()

	_, err := broker.attach(context.Background(), "pd-keys", "stripe")

	if err == nil || !strings.Contains(err.Error(), "still closing") || len(chrome.launches) != 1 || reported == nil {
		t.Fatalf("attach after failed close = %v, launches %d, report %v, want blocked and reported", err, len(chrome.launches), reported)
	}
}

func TestAStoppingBrokerEndsItsProcessesEvenWhenTheRegistryCannotBeRead(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	if _, err := broker.take(context.Background(), BrowserRequest{Action: "attach", Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(browserlogin.RegistryPath(broker.state), []byte("invalid registry"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := broker.closeAll()

	if ended := chrome.endedPIDs(broker); err == nil || !slices.Equal(ended, []int{1}) {
		t.Fatalf("closeAll = %v, ended %v, want the registry error and every owned process ended", err, ended)
	}
}
