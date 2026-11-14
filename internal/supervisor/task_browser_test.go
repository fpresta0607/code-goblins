package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fakeBrowser stands in for a goblin's Chrome: it answers the target
// commands the supervisor's own clients send from its pages, records every
// command, and raises the events a test hands it.
type fakeBrowser struct {
	mu     sync.Mutex
	pages  []targetInfo
	calls  []fakeCall
	events chan browserlogin.Event
	// hear is what the goblin's last DevTools connection was served with.
	hear                   func(browserlogin.Command) string
	clients, closedClients int
	hasExited              bool
}

type fakeCall struct {
	session, method string
	params          json.RawMessage
}

func newFakeBrowser(pages ...targetInfo) *fakeBrowser {
	return &fakeBrowser{pages: pages, events: make(chan browserlogin.Event, 16)}
}

func (f *fakeBrowser) ServeClient(w http.ResponseWriter, _ *http.Request, hear func(browserlogin.Command) string) {
	f.mu.Lock()
	f.hear = hear
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fakeBrowser) Open(context.Context) (screenClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients++
	return fakeClient{f}, nil
}

func (f *fakeBrowser) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.hasExited
}

// raise hands the supervisor's clients one of the browser's events.
func (f *fakeBrowser) raise(method, session, params string) {
	f.events <- browserlogin.Event{Session: session, Method: method, Params: json.RawMessage(params)}
}

// named is every command named method the browser got, in order.
func (f *fakeBrowser) named(method string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(f.calls), func(call fakeCall) bool { return call.method != method })
}

// waitFor is the first command named method the browser got, once it got it.
func (f *fakeBrowser) waitFor(t *testing.T, method string) fakeCall {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if calls := f.named(method); len(calls) > 0 {
			return calls[0]
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("the browser never got %s; it got %+v", method, f.calls)
	return fakeCall{}
}

func (f *fakeBrowser) closed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closedClients
}

// fakeClient is a client of the supervisor's own on a fakeBrowser.
type fakeClient struct{ browser *fakeBrowser }

func (c fakeClient) Call(_ context.Context, session, method string, params, result any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	f := c.browser
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{session, method, data})
	var answer any
	switch method {
	case "Target.getTargets":
		answer = map[string]any{"targetInfos": slices.Clone(f.pages)}
	case "Target.attachToTarget":
		var target struct {
			ID string `json:"targetId"`
		}
		err = json.Unmarshal(data, &target)
		answer = map[string]string{"sessionId": "session-" + target.ID}
	case "Target.createTarget":
		var target struct {
			URL string `json:"url"`
		}
		err = json.Unmarshal(data, &target)
		id := fmt.Sprintf("page-%d", len(f.pages)+1)
		f.pages = append(f.pages, targetInfo{ID: id, Type: "page", URL: target.URL})
		answer = map[string]string{"targetId": id}
	}
	f.mu.Unlock()
	if err != nil || result == nil || answer == nil {
		return err
	}
	data, err = json.Marshal(answer)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func (c fakeClient) Events() <-chan browserlogin.Event { return c.browser.events }

func (c fakeClient) Close() {
	c.browser.mu.Lock()
	c.browser.closedClients++
	c.browser.mu.Unlock()
}

// fakeGoblinChrome starts browser as each goblin's, in place of Chrome, counting
// its starts and ends; failure, when set, is each start's error, and hold,
// when set, keeps each start waiting until it closes.
type fakeGoblinChrome struct {
	browser        *fakeBrowser
	mu             sync.Mutex
	started, ended int
	failure        error
	hold           chan struct{}
}

func (c *fakeGoblinChrome) counts() (started, ended int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started, c.ended
}

// newFakeBrowsers is a supervisor's goblin browsers, each one chrome's,
// that never close for being idle or hand control back on their own.
func newFakeBrowsers(t *testing.T, chrome *fakeGoblinChrome) *taskBrowsers {
	t.Helper()
	browsers := &taskBrowsers{
		running: map[string]*taskBrowser{},
		launch: func(context.Context, state.TaskMeta) (screenSource, func() error, error) {
			if chrome.hold != nil {
				<-chrome.hold
			}
			chrome.mu.Lock()
			defer chrome.mu.Unlock()
			if chrome.failure != nil {
				return nil, nil, chrome.failure
			}
			chrome.started++
			return chrome.browser, func() error {
				chrome.mu.Lock()
				chrome.ended++
				chrome.mu.Unlock()
				return nil
			}, nil
		},
		live:      func(string) error { return nil },
		notify:    func() {},
		ended:     func(string, string) {},
		report:    func(err error) { t.Errorf("a browser reported %v", err) },
		idle:      time.Hour,
		unused:    time.Hour,
		inUse:     time.Hour,
		unwatched: time.Hour,
	}
	t.Cleanup(browsers.closeAll)
	return browsers
}

// goblinCommands opens task-1's DevTools route for its current session as its
// goblin's tool does and returns what hears each command it sends.
func goblinCommands(t *testing.T, service *Service, chrome *fakeGoblinChrome) func(browserlogin.Command) string {
	t.Helper()
	handler := NewHTTP(service, credentialBoardHost, nil)
	handler.devToolsGuard = func(next http.Handler) http.Handler { return next }
	request := httptest.NewRequest("GET", "http://"+credentialBoardHost+"/api/browser/devtools?task=task-1&generation=g1", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("devtools = %d %s, want it served", response.Code, response.Body)
	}
	chrome.browser.mu.Lock()
	defer chrome.browser.mu.Unlock()
	return chrome.browser.hear
}

// eventually fails the test unless isTrue holds within thirty seconds.
func eventually(t *testing.T, what string, isTrue func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if isTrue() {
			return
		}
	}
	t.Fatalf("%s never happened", what)
}

func TestAGoblinsBrowserCommandIsRefusedOnlyWhileAPersonDrivesIt(t *testing.T) {
	for _, test := range []struct {
		method      string
		isDriven    bool
		isRefused   bool
		description string
	}{
		{"Input.dispatchMouseEvent", false, false, "a click while the goblin drives"},
		{"Input.dispatchMouseEvent", true, true, "a click while a person drives"},
		{"Input.insertText", true, true, "typing while a person drives"},
		{"Page.navigate", true, true, "a navigation while a person drives"},
		{"Runtime.evaluate", true, true, "a script while a person drives"},
		{"Page.enable", true, false, "a domain's enable, to set up a page"},
		{"Network.disable", true, false, "a domain's disable"},
		{"Target.setAutoAttach", true, false, "attaching to a page a sign-in opened"},
		{"Runtime.runIfWaitingForDebugger", true, false, "letting that page run"},
	} {
		t.Run(test.description, func(t *testing.T) {
			// Act
			refusal := refusedWhileDriven(test.isDriven, test.method)

			// Assert
			if (refusal != "") != test.isRefused {
				t.Fatalf("refusal = %q, want refused %v", refusal, test.isRefused)
			}
		})
	}
}

// A goblin's browser starts once for its generation, and a session of a
// newer generation closes the older one's browser and starts its own.
func TestAGoblinsBrowserStartsOncePerGeneration(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	browsers := newFakeBrowsers(t, chrome)
	first := state.TaskMeta{ID: "task-1", SpawnGen: "g1"}
	second := state.TaskMeta{ID: "task-1", SpawnGen: "g2"}

	// Act
	opened, err := browsers.ensure(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	again, againErr := browsers.ensure(context.Background(), first)
	startedOnce, _ := chrome.counts()
	replaced, replacedErr := browsers.ensure(context.Background(), second)

	// Assert
	if againErr != nil || again != opened || startedOnce != 1 {
		t.Fatalf("second ensure = %p %v after %d starts, want the same browser started once", again, againErr, startedOnce)
	}
	if replacedErr != nil || replaced == opened {
		t.Fatalf("a newer generation got %p %v, want a browser of its own", replaced, replacedErr)
	}
	eventually(t, "the older browser's end", func() bool { _, ended := chrome.counts(); return ended == 1 })
	if browsers.isRunning("task-1", "g1") || !browsers.isRunning("task-1", "g2") {
		t.Fatal("the older generation's browser still shows")
	}
}

// A browser that could not start is no browser: the error reaches the
// session that asked, and the next session's ask starts it again.
func TestAGoblinsBrowserThatFailedToStartIsTriedAgain(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(), failure: errors.New("chrome did not start")}
	browsers := newFakeBrowsers(t, chrome)
	meta := state.TaskMeta{ID: "task-1", SpawnGen: "g1"}

	// Act
	_, failed := browsers.ensure(context.Background(), meta)
	chrome.mu.Lock()
	chrome.failure = nil
	chrome.mu.Unlock()
	opened, err := browsers.ensure(context.Background(), meta)

	// Assert
	if failed == nil || failed.Error() != "chrome did not start" {
		t.Fatalf("first ensure = %v, want Chrome's failure", failed)
	}
	if err != nil || opened == nil || !browsers.isRunning("task-1", "g1") {
		t.Fatalf("second ensure = %v, want the browser running", err)
	}
}

// A supervisor built without Start runs no browsers: its snapshot shows none,
// an ask for one is refused, and its sweep and stop have none to close, none
// of it a panic.
func TestASupervisorThatWasNeverStartedRunsNoBrowsers(t *testing.T) {
	// Arrange
	var browsers *taskBrowsers

	// Act
	_, err := browsers.ensure(context.Background(), state.TaskMeta{ID: "task-1", SpawnGen: "g1"})
	browsers.handBack("task-1")
	browsers.sweep(t.TempDir())
	browsers.closeAll()

	// Assert
	if err == nil || browsers.isRunning("task-1", "g1") {
		t.Fatalf("ensure = %v, want it refused", err)
	}
}

// A goblin's browser keeps its profile in its task's scratch folder, so a
// task with none starts no browser, and says why.
func TestAGoblinsBrowserNeedsItsTasksScratchFolder(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	browsers := newTaskBrowsers(store.Home.State, func() {}, func(string, string) {}, func(err error) { t.Errorf("a browser reported %v", err) })
	t.Cleanup(browsers.closeAll)

	// Act
	_, err := browsers.ensure(context.Background(), state.TaskMeta{ID: "task-1", SpawnGen: "g1"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "no scratch folder") || browsers.isRunning("task-1", "g1") {
		t.Fatalf("ensure = %v, want it refused for want of a scratch folder", err)
	}
}

// A browser nobody uses closes, but never while a person drives it.
func TestAGoblinsBrowserClosesOnceNobodyUsesIt(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	browsers := newFakeBrowsers(t, chrome)
	browsers.idle = 10 * time.Millisecond
	b, err := browsers.ensure(context.Background(), state.TaskMeta{ID: "task-1", SpawnGen: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	release := browsers.connect(b)
	browsers.drive(b, true)
	release()

	// Act
	time.Sleep(20 * browsers.idle)
	_, endedWhileDriven := chrome.counts()
	browsers.drive(b, false)

	// Assert
	if endedWhileDriven != 0 {
		t.Fatal("the browser closed while a person drove it")
	}
	eventually(t, "the idle browser's end", func() bool { _, ended := chrome.counts(); return ended == 1 })
	if browsers.isRunning("task-1", "g1") {
		t.Fatal("the closed browser still shows")
	}
}

// A browser its goblin's session holds but sends no command closes once it
// was unused for its while, since a goblin's chrome-devtools-axi bridge holds
// its connection for as long as it runs, but never while its goblin uses it,
// and a page's setup is no use (#2537).
func TestAGoblinsBrowserItsSessionHoldsClosesOnceUnused(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	browsers := newFakeBrowsers(t, chrome)
	browsers.unused = time.Second
	b, err := browsers.ensure(context.Background(), state.TaskMeta{ID: "task-1", SpawnGen: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	release := browsers.connect(b)
	defer release()

	// Act
	for deadline := time.Now().Add(3 * browsers.unused); time.Now().Before(deadline); time.Sleep(browsers.unused / 10) {
		browsers.use(b, browserlogin.Command{Method: "Page.navigate", Target: "shop", Params: json.RawMessage(`{"url":"https://shop.example.com/"}`)})
	}
	_, endedWhileUsed := chrome.counts()

	// Assert
	if endedWhileUsed != 0 {
		t.Fatal("the browser closed while its goblin used it")
	}
	eventually(t, "the unused browser's end", func() bool {
		browsers.use(b, browserlogin.Command{Method: "Page.enable", Target: "shop"})
		_, ended := chrome.counts()
		return ended == 1
	})
	if browsers.isRunning("task-1", "g1") {
		t.Fatal("the closed browser still shows")
	}
}

// A browser its goblin's session holds stays open its unused while after a
// view closes, a person hands it back or its session connects again, however
// long ago its goblin's last command was, so a sign-in he finished on its
// screen is still there for the goblin (#2537).
func TestAHeldBrowserStaysOpenAWhileAfterItWasLastWatchedDrivenOrConnected(t *testing.T) {
	for _, test := range []struct {
		name string
		// hold keeps the browser from closing until its returned let go.
		hold func(*taskBrowsers, *taskBrowser) (letGo func())
	}{
		{"a view closes", func(browsers *taskBrowsers, b *taskBrowser) func() {
			browsers.connect(b)
			_, release := browsers.watch(b)
			return release
		}},
		{"he hands it back", func(browsers *taskBrowsers, b *taskBrowser) func() {
			browsers.connect(b)
			browsers.drive(b, true)
			return func() { browsers.drive(b, false) }
		}},
		{"its session connects again", func(browsers *taskBrowsers, b *taskBrowser) func() {
			browsers.connect(b)()
			return func() { browsers.connect(b) }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
			browsers := newFakeBrowsers(t, chrome)
			browsers.unused = 300 * time.Millisecond
			b, err := browsers.ensure(context.Background(), state.TaskMeta{ID: "task-1", SpawnGen: "g1"})
			if err != nil {
				t.Fatal(err)
			}
			browsers.use(b, browserlogin.Command{Method: "Page.navigate", Target: "shop", Params: json.RawMessage(`{"url":"https://shop.example.com/"}`)})
			letGo := test.hold(browsers, b)
			time.Sleep(3 * browsers.unused)

			// Act
			letGo()
			time.Sleep(browsers.unused / 3)
			_, endedSoonAfter := chrome.counts()

			// Assert
			if endedSoonAfter != 0 {
				t.Fatal("the browser closed as soon as nothing watched or drove it")
			}
			eventually(t, "the unused browser's end", func() bool { _, ended := chrome.counts(); return ended == 1 })
		})
	}
}

// A goblin's browser reads as in use, which its card shows as Browser
// active, after its goblin's navigation, click or typed text through its
// DevTools route, never after a page's setup alone nor for its start, and
// stops a while after its goblin's last command; the board hears of each
// change (#2537).
func TestAGoblinsBrowserReadsInUseOnlyAfterItsGoblinUsesIt(t *testing.T) {
	for _, test := range []struct {
		name    string
		command browserlogin.Command
		isUse   bool
	}{
		{"a navigation", browserlogin.Command{Method: "Page.navigate", Target: "shop", Params: json.RawMessage(`{"url":"https://shop.example.com/"}`)}, true},
		{"a click", browserlogin.Command{Method: "Input.dispatchMouseEvent", Target: "shop", Params: json.RawMessage(`{"type":"mousePressed","x":40,"y":30,"button":"left","clickCount":1}`)}, true},
		{"typed text", browserlogin.Command{Method: "Input.insertText", Target: "shop", Params: json.RawMessage(`{"text":"made-up words"}`)}, true},
		{"a page's setup", browserlogin.Command{Method: "Page.enable", Target: "shop"}, false},
		{"a look at its pages", browserlogin.Command{Method: "Target.getTargets"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
			browsers := newFakeBrowsers(t, chrome)
			browsers.inUse = 500 * time.Millisecond
			var told atomic.Int32
			browsers.notify = func() { told.Add(1) }
			service := &Service{Store: store, Instance: "test-instance", screens: browsers}
			hear := goblinCommands(t, service, chrome)
			eventually(t, "the board's word of the browser's start", func() bool { return told.Load() == 1 })
			atStart := snapshotTask(t, service, "task-1")

			// Act
			refusal := hear(test.command)
			afterCommand, toldOfUse := snapshotTask(t, service, "task-1"), told.Load()

			// Assert
			if refusal != "" || !atStart.Browser || atStart.BrowserInUse || !afterCommand.Browser || afterCommand.BrowserInUse != test.isUse || (toldOfUse == 2) != test.isUse {
				t.Fatalf("refusal %q, the task at start %+v and after %+v, the board told %d times", refusal, atStart, afterCommand, toldOfUse)
			}
			encoded, err := json.Marshal(afterCommand)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), `"browser_in_use":true`) != test.isUse {
				t.Fatalf("the snapshot's task reads %s, want browser_in_use %v", encoded, test.isUse)
			}
			if !test.isUse {
				return
			}
			eventually(t, "the use's lapse", func() bool { return told.Load() == 3 })
			if lapsed := snapshotTask(t, service, "task-1"); !lapsed.Browser || lapsed.BrowserInUse {
				t.Fatalf("after the lapse the task reads %+v, want its browser running and not in use", lapsed)
			}
			hear(test.command)
			if again := snapshotTask(t, service, "task-1"); !again.BrowserInUse || told.Load() != 4 {
				t.Fatalf("after the next use the task reads %+v, the board told %d times", again, told.Load())
			}
		})
	}
}

// Control nobody watches goes back to the goblin, so a tab closed mid-step
// never holds its browser for good, and every view hears of it.
func TestControlNobodyWatchesGoesBackToTheGoblin(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	browsers := newFakeBrowsers(t, chrome)
	browsers.unwatched = 10 * time.Millisecond
	b, err := browsers.ensure(context.Background(), state.TaskMeta{ID: "task-1", SpawnGen: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	wake, release := browsers.watch(b)
	browsers.drive(b, true)
	<-wake

	// Act
	time.Sleep(20 * browsers.unwatched)
	isDrivenWhileWatched := browsers.isDriven(b)
	release()

	// Assert
	if !isDrivenWhileWatched {
		t.Fatal("control went back while a view showed the browser")
	}
	eventually(t, "control's return", func() bool { return !browsers.isDriven(b) })
}

// The supervisor's sweep closes the browser of a goblin that stopped or was
// started again, whose task's store says its session is g1, and a browser
// that exited, which the goblin's next use starts again.
func TestTheSweepClosesTheBrowserOfAGoblinThatStoppedOrABrowserThatExited(t *testing.T) {
	for _, test := range []struct {
		name       string
		generation string
		live       error
		hasExited  bool
	}{
		{"a goblin that stopped", "g1", errors.New("its terminal ended"), false},
		{"a goblin started again", "g0", nil, false},
		{"a browser that exited", "g1", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
			browsers := newFakeBrowsers(t, chrome)
			browsers.live = func(string) error { return test.live }
			meta := state.TaskMeta{ID: "task-1", SpawnGen: test.generation}
			if _, err := browsers.ensure(context.Background(), meta); err != nil {
				t.Fatal(err)
			}
			chrome.browser.mu.Lock()
			chrome.browser.hasExited = test.hasExited
			chrome.browser.mu.Unlock()

			// Act
			browsers.sweep(store.Home.State)

			// Assert
			eventually(t, "the browser's end", func() bool { _, ended := chrome.counts(); return ended == 1 })
			if browsers.isRunning("task-1", test.generation) {
				t.Fatal("the closed browser still shows")
			}
			if test.hasExited {
				chrome.browser.mu.Lock()
				chrome.browser.hasExited = false
				chrome.browser.mu.Unlock()
				if _, err := browsers.ensure(context.Background(), meta); err != nil || !browsers.isRunning("task-1", "g1") {
					t.Fatalf("the next use = %v, want a browser started again", err)
				}
			}
		})
	}
}

// A goblin's DevTools session reaches its browser only from this PC, never
// from a web page, and only for its task's current session; once there, its
// commands are refused while a person drives.
func TestAGoblinsDevToolsReachItsBrowserOnlyFromItsOwnSession(t *testing.T) {
	for _, test := range []struct {
		name   string
		query  string
		change func(*http.Request)
		want   int
	}{
		{"its current session", "task=task-1&generation=g1", nil, http.StatusOK},
		{"a web page", "task=task-1&generation=g1", func(r *http.Request) { r.Header.Set("Origin", "http://"+credentialBoardHost) }, http.StatusForbidden},
		{"another machine", "task=task-1&generation=g1", func(r *http.Request) { r.RemoteAddr = "100.64.0.7:50000" }, http.StatusForbidden},
		{"an earlier session", "task=task-1&generation=g0", nil, http.StatusConflict},
		{"no task", "generation=g1", nil, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
			service := &Service{Store: store, Instance: "test-instance", screens: newFakeBrowsers(t, chrome)}
			handler := NewHTTP(service, credentialBoardHost, nil)
			handler.devToolsGuard = func(next http.Handler) http.Handler { return next }
			request := httptest.NewRequest("GET", "http://"+credentialBoardHost+"/api/browser/devtools?"+test.query, nil)
			request.RemoteAddr = "127.0.0.1:50000"
			if test.change != nil {
				test.change(request)
			}
			response := httptest.NewRecorder()

			// Act
			handler.ServeHTTP(response, request)

			// Assert
			if response.Code != test.want {
				t.Fatalf("devtools = %d %s, want %d", response.Code, response.Body, test.want)
			}
			started, _ := chrome.counts()
			if (started == 1) != (test.want == http.StatusOK) {
				t.Fatalf("%d browsers started for a %d", started, response.Code)
			}
			if test.want != http.StatusOK {
				return
			}
			b := service.screens.shown("task-1", "g1")
			chrome.browser.mu.Lock()
			hear := chrome.browser.hear
			chrome.browser.mu.Unlock()
			refuse := func(method string) string { return hear(browserlogin.Command{Method: method}) }
			beforeDriving := refuse("Page.navigate")
			service.screens.drive(b, true)
			if beforeDriving != "" || refuse("Page.navigate") != drivingRefusal {
				t.Fatalf("navigation refused %q before and %q while driven", beforeDriving, refuse("Page.navigate"))
			}
		})
	}
}

// The board's DevTools route for goblins answers a process of this PC's own
// user over its socket, as a login's bridge does, and nothing whose socket
// it cannot trace to one.
func TestTheBoardsDevToolsRouteAnswersOnlyTheOwnersProcesses(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	service := &Service{Store: store, Instance: "test-instance", screens: newFakeBrowsers(t, chrome)}
	handler := NewHTTP(service, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = server.Listener.Addr().String()
	untraced := httptest.NewRequest("GET", server.URL+"/api/browser/devtools?task=task-1&generation=g1", nil)
	untraced.RemoteAddr = "127.0.0.1:50000"
	refused := httptest.NewRecorder()

	// Act
	owned, err := server.Client().Get(server.URL + "/api/browser/devtools?task=task-1&generation=g1")
	if err != nil {
		t.Fatal(err)
	}
	owned.Body.Close()
	handler.ServeHTTP(refused, untraced)

	// Assert
	if owned.StatusCode != http.StatusOK {
		t.Fatalf("this test's own process = %d, want it served", owned.StatusCode)
	}
	if refused.Code != http.StatusForbidden {
		t.Fatalf("a request no socket carries = %d %s, want it refused", refused.Code, refused.Body)
	}
}
