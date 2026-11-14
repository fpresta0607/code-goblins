package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin's browser is the supervisor's own headless Chrome, its window 1280
// by 800, on a profile in its task's scratch folder; it closes once its
// goblin's terminal no longer answers, after two minutes with nothing holding
// it or ten minutes with no command from its goblin's session, reads as in
// use for a minute after such a command, and hands back control nobody
// watches after 30 minutes, as the board's docs say.
func TestAGoblinsBrowserIsAHeadlessChromeOf1280By800OnItsTasksProfile(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	taskTmp := t.TempDir()
	browsers := newTaskBrowsers(store.Home.State, func() {}, func(string, string) {}, func(err error) { t.Errorf("a browser reported %v", err) })
	t.Cleanup(browsers.closeAll)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Act
	b, err := browsers.ensure(ctx, state.TaskMeta{ID: "task-1", SpawnGen: "g1", TaskTmp: taskTmp})
	if err != nil {
		t.Fatal(err)
	}
	client, err := b.source.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version struct {
		UserAgent string `json:"userAgent"`
	}
	versionErr := client.Call(ctx, "", "Browser.getVersion", nil, &version)
	var targets struct {
		Infos []targetInfo `json:"targetInfos"`
	}
	targetsErr := client.Call(ctx, "", "Target.getTargets", nil, &targets)
	page := ""
	for _, info := range targets.Infos {
		if info.Type == "page" {
			page = info.ID
		}
	}
	var window struct {
		Bounds struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"bounds"`
	}
	windowErr := client.Call(ctx, "", "Browser.getWindowForTarget", map[string]string{"targetId": page}, &window)
	client.Close()
	browsers.sweep(store.Home.State)
	isRunningAfterSweep := browsers.isRunning("task-1", "g1")
	browsers.closeAll()

	// Assert
	if err := errors.Join(versionErr, targetsErr, windowErr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.UserAgent, "HeadlessChrome") {
		t.Fatalf("the browser is %q, want a headless Chrome", version.UserAgent)
	}
	if window.Bounds.Width != 1280 || window.Bounds.Height != 800 {
		t.Fatalf("its window is %dx%d, want 1280x800", window.Bounds.Width, window.Bounds.Height)
	}
	if entries, err := os.ReadDir(filepath.Join(taskTmp, "browser-profile")); err != nil || len(entries) == 0 {
		t.Fatalf("the task's profile holds %d entries, %v; want Chrome's profile there", len(entries), err)
	}
	if isRunningAfterSweep {
		t.Fatal("the browser of a goblin whose terminal does not answer still shows after the sweep")
	}
	if b.source.IsRunning() {
		t.Fatal("Chrome still runs after the supervisor closed it")
	}
	if browsers.idle != 2*time.Minute || browsers.unused != 10*time.Minute || browsers.inUse != time.Minute || browsers.unwatched != 30*time.Minute {
		t.Fatalf("the browser closes after %v let go or %v unused, reads in use for %v and hands back after %v unwatched, want 2m, 10m, 1m and 30m", browsers.idle, browsers.unused, browsers.inUse, browsers.unwatched)
	}
}

// A click the goblin sends through its own browser session, the board's
// DevTools route that its chrome-devtools-axi reaches, shows in its Screen
// view as a pointer at that place on the page it clicked, and reads as use
// of its browser, whether the session attached to that page itself or, as
// chrome-devtools-axi's puppeteer does, by auto-attach (#2537).
func TestAGoblinsClickThroughItsOwnSessionShowsItsPointerInTheScreenView(t *testing.T) {
	for _, test := range []struct {
		name         string
		isAutoAttach bool
	}{
		{"an attach of its own", false},
		{"an auto-attach", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			clickShowsItsPointer(t, test.isAutoAttach)
		})
	}
}

func clickShowsItsPointer(t *testing.T, isAutoAttach bool) {
	// Arrange
	store, _ := testStore(t)
	meta, err := state.ReadTaskMeta(store.Home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.TaskTmp = t.TempDir()
	if err := state.WriteTaskMeta(store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Instance: "test-instance"}
	service.screens = newTaskBrowsers(store.Home.State, func() {}, func(string, string) {}, func(err error) { t.Errorf("a browser reported %v", err) })
	t.Cleanup(service.screens.closeAll)
	handler := NewHTTP(service, "", nil)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	handler.Host = server.Listener.Addr().String()
	socket := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	goblin, _, err := websocket.Dial(ctx, socket+"/api/browser/devtools?task=task-1&generation=g1", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { goblin.CloseNow() })
	sent := int64(0)
	autoAttached := ""
	type devToolsReply struct {
		ID     int64           `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	read := func() devToolsReply {
		t.Helper()
		var message struct {
			devToolsReply
			Method string `json:"method"`
			Params struct {
				Session string     `json:"sessionId"`
				Info    targetInfo `json:"targetInfo"`
			} `json:"params"`
		}
		if err := wsjson.Read(ctx, goblin, &message); err != nil {
			t.Fatal(err)
		}
		if message.Method == "Target.attachedToTarget" && message.Params.Info.Type == "page" {
			autoAttached = message.Params.Session
		}
		return message.devToolsReply
	}
	call := func(session, method string, params, result any) {
		t.Helper()
		sent++
		command := map[string]any{"id": sent, "method": method, "params": params}
		if session != "" {
			command["sessionId"] = session
		}
		if err := wsjson.Write(ctx, goblin, command); err != nil {
			t.Fatal(err)
		}
		for {
			reply := read()
			if reply.ID != sent {
				continue
			}
			if len(reply.Error) != 0 {
				t.Fatalf("%s = %s", method, reply.Error)
			}
			if result != nil {
				if err := json.Unmarshal(reply.Result, result); err != nil {
					t.Fatal(err)
				}
			}
			return
		}
	}
	var attached struct {
		Session string `json:"sessionId"`
	}
	if isAutoAttach {
		call("", "Target.setAutoAttach", map[string]any{"autoAttach": true, "flatten": true, "waitForDebuggerOnStart": false}, nil)
		for autoAttached == "" {
			read()
		}
		attached.Session = autoAttached
	} else {
		var targets struct {
			Infos []targetInfo `json:"targetInfos"`
		}
		call("", "Target.getTargets", nil, &targets)
		page := ""
		for _, info := range targets.Infos {
			if info.Type == "page" {
				page = info.ID
			}
		}
		call("", "Target.attachToTarget", map[string]any{"targetId": page, "flatten": true}, &attached)
	}
	// The goblin clicks on the page its browser opened with, as no navigation
	// may be under way while the view attaches to it: a view that opens
	// while a page's navigation swaps its renderer is told the browser did
	// not answer.
	view, _, err := websocket.Dial(ctx, socket+"/api/screen?task=task-1&generation=g1&token=test-instance", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { view.CloseNow() })
	told := func(isWanted func(screenSaid) bool) screenSaid {
		t.Helper()
		last := "nothing"
		for {
			_, data, err := view.Read(ctx)
			if err != nil {
				t.Fatalf("the view was never told what it waits for, its last state %s: %v", last, err)
			}
			var said screenSaid
			if err := json.Unmarshal(data, &said); err != nil {
				t.Fatal(err)
			}
			said.raw = string(data)
			if said.Type != "state" {
				continue
			}
			if isWanted(said) {
				return said
			}
			last = said.raw
		}
	}
	opened := told(func(said screenSaid) bool { return said.HasPage })

	// Act
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		call(attached.Session, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": 40, "y": 30, "button": "left", "clickCount": 1}, nil)
	}
	clicked := told(func(said screenSaid) bool { return said.Pointer != nil })

	// Assert
	if opened.Pointer != nil {
		t.Fatalf("the view opened told %s, want no pointer before a click", opened.raw)
	}
	if *clicked.Pointer != (screenPlace{X: 40, Y: 30}) {
		t.Fatalf("told %s after the goblin's click at 40,30", clicked.raw)
	}
	if !service.screens.isInUse("task-1", "g1") {
		t.Fatal("the browser the goblin just clicked in does not read as in use")
	}
}
