package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// screenBoard is a board whose goblin task-1 runs a fake browser, served on
// a real socket so its Screen view is a real WebSocket.
type screenBoard struct {
	service *Service
	handler *HTTP
	server  *httptest.Server
	chrome  *fakeGoblinChrome
	browser *taskBrowser
}

// newScreenBoard serves service's Screen views, each from another machine
// when isRemote, with task-1's browser already running.
func newScreenBoard(t *testing.T, service *Service, chrome *fakeGoblinChrome, isRemote bool) *screenBoard {
	t.Helper()
	if service.screens == nil {
		service.screens = newFakeBrowsers(t, chrome)
		service.screens.notify = service.notify
	}
	handler := NewHTTP(service, "", nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRemote {
			r.RemoteAddr = "100.64.0.7:50000"
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	handler.Host = server.Listener.Addr().String()
	b, err := service.screens.ensure(context.Background(), state.TaskMeta{ID: "task-1", SpawnGen: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	return &screenBoard{service: service, handler: handler, server: server, chrome: chrome, browser: b}
}

// dial opens a Screen view of task-1 with token, from origin unless it is
// empty, and answers the handshake's response.
func (board *screenBoard) dial(t *testing.T, token, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	query := url.Values{"task": {"task-1"}, "generation": {"g1"}, "token": {token}}
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(board.server.URL, "http")+"/api/screen?"+query.Encode(), &websocket.DialOptions{HTTPHeader: header})
	if conn != nil {
		t.Cleanup(func() { conn.CloseNow() })
	}
	return conn, response, err
}

// screenView is a Screen view the test holds open.
type testScreenView struct {
	conn *websocket.Conn
}

// screenSaid is one thing a view is told: the browser's state or a frame.
type screenSaid struct {
	browserScreenState
	Data  string  `json:"data"`
	Width float64 `json:"width"`
	raw   string
}

func (board *screenBoard) open(t *testing.T) testScreenView {
	t.Helper()
	conn, _, err := board.dial(t, board.service.Instance, board.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return testScreenView{conn}
}

// next is what the view is told next.
func (v testScreenView) next(t *testing.T) screenSaid {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, data, err := v.conn.Read(ctx)
	if err != nil {
		t.Fatalf("the view was told nothing: %v", err)
	}
	var said screenSaid
	if err := json.Unmarshal(data, &said); err != nil {
		t.Fatal(err)
	}
	said.raw = string(data)
	return said
}

// send sends message as the board's page does.
func (v testScreenView) send(t *testing.T, message string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := v.conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
		t.Fatal(err)
	}
}

func screenService(t *testing.T) *Service {
	t.Helper()
	store, _ := testStore(t)
	return &Service{Store: store, Instance: "test-instance"}
}

// A view shows the newest page by its host alone, gets each frame only once
// it drew the one before, and stops the browser's screencast as it closes,
// so a view nobody shows costs the browser nothing.
func TestScreenViewSendsEachFrameOnceTheLastIsDrawnAndStopsWhenItCloses(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(
		targetInfo{ID: "old", Type: "page", Title: "Shop", URL: "https://example.com/"},
		targetInfo{ID: "login", Type: "page", Title: "Sign in", URL: "https://login.example.com/callback?code=one-time-code"},
	)}
	board := newScreenBoard(t, screenService(t), chrome, false)
	view := board.open(t)

	// Act
	told := view.next(t)
	start := chrome.browser.waitFor(t, "Page.startScreencast")
	chrome.browser.raise("Page.screencastFrame", start.session, `{"data":"ZnJhbWU=","sessionId":7,"metadata":{"deviceWidth":1280,"deviceHeight":800}}`)
	frame := view.next(t)
	acksBeforeDrawn := len(chrome.browser.named("Page.screencastFrameAck"))
	view.send(t, `{"type":"ack"}`)
	ack := chrome.browser.waitFor(t, "Page.screencastFrameAck")
	view.conn.Close(websocket.StatusNormalClosure, "")
	stop := chrome.browser.waitFor(t, "Page.stopScreencast")

	// Assert
	if !told.HasPage || told.Title != "Sign in" || told.Host != "login.example.com" || strings.Contains(told.raw, "one-time-code") {
		t.Fatalf("the view was told %s, want the newest page by its host alone", told.raw)
	}
	if start.session != "session-login" {
		t.Fatalf("the screencast ran on %q, want the newest page's", start.session)
	}
	if frame.Type != "frame" || frame.Data != "ZnJhbWU=" || frame.Width != 1280 {
		t.Fatalf("the view got %s, want the frame", frame.raw)
	}
	if acksBeforeDrawn != 0 || string(ack.params) != `{"sessionId":7}` || ack.session != start.session {
		t.Fatalf("acked %d before the view drew, then %s on %q", acksBeforeDrawn, ack.params, ack.session)
	}
	if stop.session != start.session {
		t.Fatalf("the screencast stopped on %q, want %q", stop.session, start.session)
	}
	eventually(t, "the view's client's close", func() bool { return chrome.browser.closed() == 1 })
}

// A page the goblin opens takes the screen, and the one it left stops
// sending frames.
func TestScreenViewFollowsTheNewestPage(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "shop", Type: "page", Title: "Shop", URL: "https://example.com/"})}
	board := newScreenBoard(t, screenService(t), chrome, false)
	view := board.open(t)
	view.next(t)

	// Act
	chrome.browser.raise("Target.targetCreated", "", `{"targetInfo":{"targetId":"popup","type":"page","title":"Sign in","url":"https://login.example.com/"}}`)
	told := view.next(t)

	// Assert
	stops := chrome.browser.named("Page.stopScreencast")
	starts := chrome.browser.named("Page.startScreencast")
	if told.Title != "Sign in" || len(stops) != 1 || stops[0].session != "session-shop" || len(starts) != 2 || starts[1].session != "session-popup" {
		t.Fatalf("told %s after stops %+v and starts %+v", told.raw, stops, starts)
	}
}

// A view that opens on a running browser cannot tell which of its pages came
// last, since Chrome lists them in no set order, so it shows a page with
// something on it over a blank one, such as the tab a new browser opens with,
// wherever the list puts each.
func TestScreenViewShowsAPageWithSomethingOnItOverABlankOne(t *testing.T) {
	blank := targetInfo{ID: "blank", Type: "page", URL: "about:blank"}
	shop := targetInfo{ID: "shop", Type: "page", Title: "Shop", URL: "https://example.com/"}
	for _, test := range []struct {
		name  string
		pages []targetInfo
	}{
		{"the blank page listed first", []targetInfo{blank, shop}},
		{"the blank page listed last", []targetInfo{shop, blank}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			chrome := &fakeGoblinChrome{browser: newFakeBrowser(test.pages...)}
			board := newScreenBoard(t, screenService(t), chrome, false)

			// Act
			told := board.open(t).next(t)

			// Assert
			starts := chrome.browser.named("Page.startScreencast")
			if told.Title != "Shop" || len(starts) != 1 || starts[0].session != "session-shop" {
				t.Fatalf("told %s after starts %+v", told.raw, starts)
			}
		})
	}
}

// The page a goblin sends somewhere is the one it works on, so the view moves
// to it; a page that only changed its title keeps its place.
func TestScreenViewFollowsThePageTheGoblinNavigated(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "docs", Type: "page", Title: "Docs", URL: "https://docs.example.com/"}, targetInfo{ID: "shop", Type: "page", Title: "Shop", URL: "https://example.com/"})}
	board := newScreenBoard(t, screenService(t), chrome, false)
	view := board.open(t)
	view.next(t)

	// Act
	chrome.browser.raise("Target.targetInfoChanged", "", `{"targetInfo":{"targetId":"docs","type":"page","title":"Sign in","url":"https://login.example.com/"}}`)
	navigated := view.next(t)
	chrome.browser.raise("Target.targetInfoChanged", "", `{"targetInfo":{"targetId":"shop","type":"page","title":"Shop, 2 items","url":"https://example.com/"}}`)
	retitled := view.next(t)

	// Assert
	starts := chrome.browser.named("Page.startScreencast")
	if navigated.Title != "Sign in" || retitled.Title != "Sign in" || len(starts) != 2 || starts[0].session != "session-shop" || starts[1].session != "session-docs" {
		t.Fatalf("told %s then %s after starts %+v", navigated.raw, retitled.raw, starts)
	}
}

// A press of the goblin's mouse through its DevTools route on the page a view
// shows reaches that view as the place it pressed and nothing else, as each
// later word of the browser's state does; a press on another page, its
// release and its typed text do not (#2537).
func TestScreenViewShowsWhereTheGoblinPressedOnThePageItShows(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "docs", Type: "page", Title: "Docs", URL: "https://docs.example.com/"}, targetInfo{ID: "shop", Type: "page", Title: "Shop", URL: "https://example.com/"})}
	board := newScreenBoard(t, screenService(t), chrome, false)
	view := board.open(t)
	opened := view.next(t)
	hear := goblinCommands(t, board.service, chrome)
	press := func(target string, kind string, x, y float64) {
		hear(browserlogin.Command{Method: "Input.dispatchMouseEvent", Target: target, Params: json.RawMessage(fmt.Sprintf(`{"type":%q,"x":%v,"y":%v,"button":"left","clickCount":1}`, kind, x, y))})
	}

	// Act
	press("docs", "mousePressed", 5, 6)
	elsewhere := view.next(t)
	press("shop", "mouseReleased", 7, 8)
	hear(browserlogin.Command{Method: "Input.insertText", Target: "shop", Params: json.RawMessage(`{"text":"made-up words"}`)})
	press("shop", "mousePressed", 120.5, 64)
	pressed := view.next(t)
	board.service.screens.drive(board.browser, true)
	driven := view.next(t)

	// Assert
	if opened.Pointer != nil || elsewhere.Pointer != nil {
		t.Fatalf("told %s as it opened and %s after a press on another page, want no pointer", opened.raw, elsewhere.raw)
	}
	for _, told := range []screenSaid{pressed, driven} {
		if told.Pointer == nil || *told.Pointer != (screenPlace{X: 120.5, Y: 64}) || !strings.Contains(told.raw, `"pointer":{"x":120.5,"y":64}`) || strings.Contains(told.raw, "made-up words") {
			t.Fatalf("told %s, want the pointer at 120.5,64 and nothing typed", told.raw)
		}
	}
}

// Chrome titles a page with no title of its own by its address, as it titles
// each page it navigates until the page's title is drawn, and an address can
// carry a sign-in's code, so a view shows such a page as untitled, by its
// host alone, whether it was open as the view opened or went there after.
// Chrome shows an internationalized host in Unicode, so bücher.example, whose
// address is xn--bcher-kva.example (RFC 3492's own example), is titled so.
func TestScreenViewNeverShowsAnAddressChromeTitledAPageWith(t *testing.T) {
	for _, test := range []struct {
		name, address, title, host string
	}{
		{"an https address, which Chrome titles whole", "https://login.example.com/callback?code=CODE", "https://login.example.com/callback?code=CODE", "login.example.com"},
		{"an http address, which Chrome titles without its scheme", "http://login.example.com/callback?code=CODE", "login.example.com/callback?code=CODE", "login.example.com"},
		{"an internationalized address, its host titled in Unicode", "https://xn--bcher-kva.example/callback?code=CODE", "https://bücher.example/callback?code=CODE", "xn--bcher-kva.example"},
		{"an internationalized address with its token in its fragment", "https://xn--bcher-kva.example/callback#access_token=CODE", "https://bücher.example/callback#access_token=CODE", "xn--bcher-kva.example"},
		{"an internationalized address whose query Chrome shows unescaped", "https://xn--bcher-kva.example/callback?code=CODE&state=a%3Ab", "https://bücher.example/callback?code=CODE&state=a:b", "xn--bcher-kva.example"},
		{"an internationalized address Chrome titles without its scheme, its query unescaped", "http://xn--bcher-kva.example/callback?code=CODE&state=a%3Ab", "bücher.example/callback?code=CODE&state=a:b", "xn--bcher-kva.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			titled := func(code string) targetInfo {
				return targetInfo{ID: "login", Type: "page", Title: strings.ReplaceAll(test.title, "CODE", code), URL: strings.ReplaceAll(test.address, "CODE", code)}
			}
			chrome := &fakeGoblinChrome{browser: newFakeBrowser(titled("first-code-7f3a"))}
			board := newScreenBoard(t, screenService(t), chrome, false)
			view := board.open(t)
			navigated, err := json.Marshal(map[string]targetInfo{"targetInfo": titled("second-code-9b1c")})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			opened := view.next(t)
			chrome.browser.raise("Target.targetInfoChanged", "", string(navigated))
			went := view.next(t)

			// Assert
			for _, told := range []screenSaid{opened, went} {
				if strings.Contains(told.raw, "-code-") || told.Title != "" || told.Host != test.host || !told.HasPage {
					t.Fatalf("the view was told %s, want an untitled page by its host alone", told.raw)
				}
			}
		})
	}
}

// Only the board's own page, with the board's token, opens a Screen view: a
// request without the token or with another, or with no origin, as any
// program on this PC can send, is refused before a DevTools client of the
// supervisor's reaches the goblin's browser.
func TestScreenViewOpensOnlyFromTheBoardsOwnPageWithItsToken(t *testing.T) {
	for _, test := range []struct {
		name, token string
		hasOrigin   bool
	}{
		{"without the token", "", true},
		{"with another token", "another-instance", true},
		{"with no origin", "test-instance", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "login", Type: "page", Title: "Sign in", URL: "https://login.example.com/"})}
			board := newScreenBoard(t, screenService(t), chrome, false)
			origin := ""
			if test.hasOrigin {
				origin = board.server.URL
			}

			// Act
			_, response, err := board.dial(t, test.token, origin)

			// Assert
			if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("the view opened with %v, want its handshake refused", err)
			}
			chrome.browser.mu.Lock()
			clients := chrome.browser.clients
			chrome.browser.mu.Unlock()
			if clients != 0 {
				t.Fatalf("%d DevTools clients reached the browser", clients)
			}
		})
	}
}

// At most 16 Screen views are open at once, as the board's docs say: one more
// is turned away, saying so, before it reaches the browser.
func TestScreenViewsPastSixteenAreTurnedAway(t *testing.T) {
	// Arrange
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "login", Type: "page", Title: "Sign in", URL: "https://login.example.com/"})}
	board := newScreenBoard(t, screenService(t), chrome, false)
	for range 16 {
		board.open(t).next(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	_, _, err := board.open(t).conn.Read(ctx)

	// Assert
	var closed websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.StatusTryAgainLater || closed.Reason != "Too many screens are open." {
		t.Fatalf("the seventeenth view read %v, want it turned away", err)
	}
	chrome.browser.mu.Lock()
	clients := chrome.browser.clients
	chrome.browser.mu.Unlock()
	if clients != 16 {
		t.Fatalf("%d DevTools clients reached the browser, want one for each of 16 views", clients)
	}
}

// A driving view's mouse, keys and pastes reach the page only as DevTools'
// Input domain takes them: an event it names, a click of at most three on a
// point near the page, a key of a byte's code typing at most a character,
// and a paste of at most 4000 characters, counted as the board counts them,
// whatever bytes each takes.
func TestScreenViewPassesOnlyInputTheInputDomainTakes(t *testing.T) {
	paste := func(text string) string {
		data, err := json.Marshal(map[string]string{"type": "text", "text": text})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, test := range []struct {
		name, message string
		isPassed      bool
	}{
		{"a click", `{"type":"mouse","event":"mousePressed","x":10,"y":20,"button":"left","buttons":1,"click_count":1}`, true},
		{"a click far off any page", `{"type":"mouse","event":"mousePressed","x":1e9,"y":20,"button":"left","buttons":1,"click_count":1}`, false},
		{"a fourth click in a row", `{"type":"mouse","event":"mousePressed","x":10,"y":20,"button":"left","buttons":1,"click_count":4}`, false},
		{"a mouse event the Input domain has no name for", `{"type":"mouse","event":"mouseDragged","x":10,"y":20,"button":"left","buttons":1}`, false},
		{"a button no mouse has", `{"type":"mouse","event":"mousePressed","x":10,"y":20,"button":"thumb","buttons":1,"click_count":1}`, false},
		{"a key", `{"type":"key","event":"keyDown","key":"a","code":"KeyA","key_code":65,"text":"a"}`, true},
		{"a key code past a byte", `{"type":"key","event":"keyDown","key":"a","code":"KeyA","key_code":256,"text":"a"}`, false},
		{"a key that types more than a character", `{"type":"key","event":"keyDown","key":"a","code":"KeyA","key_code":65,"text":"abcdefghi"}`, false},
		{"a key event the Input domain has no name for", `{"type":"key","event":"char","key":"a","code":"KeyA","key_code":65,"text":"a"}`, false},
		{"a paste of 4000 characters of three bytes each", paste(strings.Repeat("語", 4000)), true},
		{"a paste past 4000 characters", paste(strings.Repeat("語", 4001)), false},
		{"an empty paste", paste(""), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "login", Type: "page", Title: "Sign in", URL: "https://login.example.com/"})}
			board := newScreenBoard(t, screenService(t), chrome, false)
			view := board.open(t)
			view.next(t)
			view.send(t, `{"type":"take"}`)
			view.next(t)
			inputs := func() []fakeCall {
				chrome.browser.mu.Lock()
				defer chrome.browser.mu.Unlock()
				return slices.DeleteFunc(slices.Clone(chrome.browser.calls), func(call fakeCall) bool { return !strings.HasPrefix(call.method, "Input.") })
			}
			isMove := func(call fakeCall) bool { return strings.Contains(string(call.params), `"type":"mouseMoved"`) }

			// Act
			view.send(t, test.message)
			view.send(t, `{"type":"mouse","event":"mouseMoved","x":1,"y":2}`)
			eventually(t, "the move sent after it", func() bool { return slices.ContainsFunc(inputs(), isMove) })

			// Assert
			passed := slices.DeleteFunc(inputs(), isMove)
			if test.isPassed != (len(passed) == 1) || len(passed) > 1 {
				t.Fatalf("the page got %+v, want it passed %v", passed, test.isPassed)
			}
		})
	}
}

// Only a view on this PC takes control, and only once taken do its mouse
// and keys reach the page. What a person types reaches the page alone: no
// file in the home and no error the board shows holds it.
func TestScreenViewDrivesTheBrowserOnlyFromThisPCAndOnlyOnceTaken(t *testing.T) {
	for _, test := range []struct {
		name     string
		isRemote bool
	}{
		{"from this PC", false},
		{"from another machine", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "login", Type: "page", Title: "Sign in", URL: "https://login.example.com/"})}
			service := screenService(t)
			board := newScreenBoard(t, service, chrome, test.isRemote)
			view := board.open(t)
			first := view.next(t)
			canary := newCanary(t)
			typed, _ := json.Marshal(map[string]string{"type": "text", "text": canary})

			// Act
			view.send(t, `{"type":"key","event":"keyDown","key":"a","code":"KeyA","key_code":65,"text":"a"}`)
			view.send(t, `{"type":"take"}`)
			afterTake := view.next(t)
			view.send(t, string(typed))
			view.send(t, `{"type":"take"}`)
			if test.isRemote {
				view.next(t)
			}
			view.send(t, `{"type":"mouse","event":"mousePressed","x":10,"y":20,"button":"left","buttons":1,"click_count":1}`)
			view.send(t, `{"type":"take"}`)
			if test.isRemote {
				view.next(t)
			}

			// Assert
			if first.CanDrive == test.isRemote || first.IsDriven {
				t.Fatalf("first told %s", first.raw)
			}
			if afterTake.IsDriven == test.isRemote || board.service.screens.isDriven(board.browser) == test.isRemote {
				t.Fatalf("after take told %s", afterTake.raw)
			}
			if !test.isRemote {
				chrome.browser.waitFor(t, "Input.dispatchMouseEvent")
			}
			keys := chrome.browser.named("Input.dispatchKeyEvent")
			texts := chrome.browser.named("Input.insertText")
			if len(keys) != 0 {
				t.Fatalf("a key before take reached the page: %+v", keys)
			}
			if test.isRemote {
				if len(texts) != 0 || len(chrome.browser.named("Input.dispatchMouseEvent")) != 0 {
					t.Fatal("input from another machine reached the page")
				}
			} else if len(texts) != 1 || !strings.Contains(string(texts[0].params), canary) {
				t.Fatalf("typing reached the page as %+v", texts)
			}
			if found := filesHolding(t, service.Store.Home.Root, canary); len(found) != 0 {
				t.Fatalf("what was typed is in %v", found)
			}
			service.mu.Lock()
			lastError := service.lastError
			service.mu.Unlock()
			if strings.Contains(lastError, canary) {
				t.Fatal("what was typed is in the board's error")
			}
		})
	}
}

// End to end on the board: his Take control on a goblin's card opens the
// card's link in the goblin's own browser, on the page it was on or a new
// one, and gives him control there; his Hand back closes the card as done
// with his words and gives the browser back to the goblin.
func TestStepCardTakeControlOpensItsLinkAndHandBackClosesItWithHisWords(t *testing.T) {
	for _, test := range []struct {
		name  string
		pages []targetInfo
	}{
		{"on the page it was on", []targetInfo{{ID: "shop", Type: "page", Title: "Shop", URL: "https://example.com/"}}},
		{"with no page open", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, func(r *CredentialRequest) { r.Window, r.Link = true, "https://example.com/login" })
			chrome := &fakeGoblinChrome{browser: newFakeBrowser(test.pages...)}
			b.service.screens = newFakeBrowsers(t, chrome)
			b.service.screens.notify = b.service.notify

			// Act
			response := b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
			b.service.credentialWork.Wait()
			taken := b.stored()
			board := newScreenBoard(t, b.service, chrome, false)
			isDrivenOnceTaken := board.service.screens.isDriven(board.browser)
			view := board.open(t)
			told := view.next(t)
			view.send(t, `{"type":"hand-back","reason":"  signed in,\n the second factor is on "}`)
			eventually(t, "the card's close", func() bool { return b.stored().State == "done" })
			afterHandBack := view.next(t)

			// Assert
			if response.Code != http.StatusOK || taken.WindowState != "open" || !isDrivenOnceTaken {
				t.Fatalf("take control = %d %s, card %+v, driven %v", response.Code, response.Body, taken, isDrivenOnceTaken)
			}
			if test.pages == nil {
				if created := chrome.browser.named("Target.createTarget"); len(created) != 1 || string(created[0].params) != `{"url":"https://example.com/login"}` {
					t.Fatalf("created %+v, want a page at the link", created)
				}
			} else if navigated := chrome.browser.named("Page.navigate"); len(navigated) != 1 || navigated[0].session != "session-shop" || string(navigated[0].params) != `{"url":"https://example.com/login"}` {
				t.Fatalf("navigated %+v, want the goblin's page at the link", navigated)
			}
			if !told.IsDriven {
				t.Fatalf("the view was told %s, want him driving", told.raw)
			}
			if closed := b.stored(); closed.Reason != "Handed back from the board: signed in, the second factor is on" {
				t.Fatalf("the card closed saying %q", closed.Reason)
			}
			if afterHandBack.IsDriven || board.service.screens.isDriven(board.browser) || refusedWhileDriven(board.service.screens.isDriven(board.browser), "Page.navigate") != "" {
				t.Fatalf("after hand back the view was told %s and the goblin is still refused", afterHandBack.raw)
			}
		})
	}
}

// A goblin's browser that cannot open offers his Take control again, saying
// why, and leaves the goblin's browser its own.
func TestStepCardTakeControlOffersItselfAgainWhenTheBrowserCannotOpen(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.Window = true })
	b.service.screens = newFakeBrowsers(t, &fakeGoblinChrome{browser: newFakeBrowser(), failure: errors.New("Chrome did not start")})

	// Act
	response := b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
	b.service.credentialWork.Wait()

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("take control = %d %s", response.Code, response.Body)
	}
	if card := b.stored(); card.State != "open" || card.WindowState != "" || card.Reason != "The goblin's browser could not open: Chrome did not start" {
		t.Fatalf("card = %+v, want the button offered again with why", card)
	}
}

// Whatever closes a card he holds a goblin's browser for gives the browser
// back, so its goblin is never left refused.
func TestClosingACardGivesTheGoblinItsBrowserBack(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.Window = true })
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	b.service.screens = newFakeBrowsers(t, chrome)
	b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
	b.service.credentialWork.Wait()
	browser := b.service.screens.shown("task-1", "g1")
	isDrivenBeforeClose := b.service.screens.isDriven(browser)

	// Act
	response := b.send(b.handler, "/api/credentials/close", b.closeBody("dismiss"), nil)

	// Assert
	if !isDrivenBeforeClose || response.Code != http.StatusOK {
		t.Fatalf("driven %v before a dismissal that answered %d %s", isDrivenBeforeClose, response.Code, response.Body)
	}
	if b.service.screens.isDriven(browser) {
		t.Fatal("the goblin's browser is still his after the card closed")
	}
}

// His Hand back closes the card with his words on one line, up to 300
// characters however many bytes each takes, as the board counts them, and
// never with a word shaped like a key, which would reach the goblin's
// conversation, as his answer on a card never does: the card then says only
// that the step is taken, and no file of the home holds the key.
func TestHandBackClosesTheCardWithHisWordsUpTo300CharactersAndNeverAKey(t *testing.T) {
	canary := newCanary(t)
	for _, test := range []struct {
		name, words, reason string
	}{
		{"300 characters of two bytes each", strings.Repeat("é", 300), strings.Repeat("é", 300)},
		{"301 characters", strings.Repeat("é", 301), strings.Repeat("é", 300)},
		{"a key among his words", "signed in, the token is " + canary, "the step is taken"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, func(r *CredentialRequest) { r.Window, r.Link = true, "https://example.com/login" })
			chrome := &fakeGoblinChrome{browser: newFakeBrowser(targetInfo{ID: "shop", Type: "page", Title: "Shop", URL: "https://example.com/"})}
			b.service.screens = newFakeBrowsers(t, chrome)
			b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
			b.service.credentialWork.Wait()
			view := newScreenBoard(t, b.service, chrome, false).open(t)
			view.next(t)
			handBack, err := json.Marshal(map[string]string{"type": "hand-back", "reason": test.words})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Act
			view.send(t, string(handBack))
			waited, err := WaitCredential(ctx, b.home.State, b.request.ID, nil)

			// Assert
			if err != nil || waited.State != "done" || waited.Reason != "Handed back from the board: "+test.reason {
				t.Fatalf("the goblin's wait ended with %v on a card %s saying %q", err, waited.State, waited.Reason)
			}
			if found := filesHolding(t, b.home.Root, canary); len(found) != 0 {
				t.Fatalf("the key is in %v", found)
			}
		})
	}
}

// A card he took control on offers Take control again, saying why, once the
// goblin's browser it showed closed, here as Chrome exited, so the step is
// never left with no way to take it; his press then starts the browser again
// and gives it to him.
func TestAStepCardOffersTakeControlAgainOnceItsBrowserClosed(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.Window, r.Link = true, "https://example.com/login" })
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	b.service.screens = newFakeBrowsers(t, chrome)
	b.service.screens.ended = b.service.endCardScreens
	b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
	b.service.credentialWork.Wait()
	taken := b.stored()
	chrome.browser.mu.Lock()
	chrome.browser.hasExited = true
	chrome.browser.mu.Unlock()

	// Act
	b.service.screens.sweep(b.home.State)
	eventually(t, "Take control offered again", func() bool { return b.stored().WindowState == "" })
	offered := b.stored()
	chrome.browser.mu.Lock()
	chrome.browser.hasExited = false
	chrome.browser.mu.Unlock()
	again := b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
	b.service.credentialWork.Wait()

	// Assert
	if taken.WindowState != "open" {
		t.Fatalf("the first press left the card %+v", taken)
	}
	if offered.State != "open" || offered.Reason != "The goblin's browser closed. Press Take control to open it again." {
		t.Fatalf("once the browser closed the card was %+v, want Take control offered again with why", offered)
	}
	started, _ := chrome.counts()
	browser := b.service.screens.shown("task-1", "g1")
	if again.Code != http.StatusOK || b.stored().WindowState != "open" || started != 2 || browser == nil || !b.service.screens.isDriven(browser) {
		t.Fatalf("the second press = %d %s, card %+v, %d starts; want the browser started again and his", again.Code, again.Body, b.stored(), started)
	}
}

// A card that closes while his Take control opens the goblin's browser leaves
// that browser the goblin's, as the card's close would have once he held it.
func TestACardClosedWhileTakeControlOpensLeavesTheBrowserTheGoblins(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.Window, r.Link = true, "https://example.com/login" })
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(), hold: make(chan struct{})}
	b.service.screens = newFakeBrowsers(t, chrome)
	b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
	eventually(t, "the browser's start", func() bool {
		b.service.screens.mu.Lock()
		defer b.service.screens.mu.Unlock()
		return b.service.screens.running["task-1"] != nil
	})

	// Act
	closed := b.send(b.handler, "/api/credentials/close", b.closeBody("dismiss"), nil)
	close(chrome.hold)
	b.service.credentialWork.Wait()

	// Assert
	if closed.Code != http.StatusOK || b.stored().State != "dismissed" {
		t.Fatalf("dismiss = %d %s, card %+v", closed.Code, closed.Body, b.stored())
	}
	if created := chrome.browser.named("Target.createTarget"); len(created) != 1 {
		t.Fatalf("created %+v, want the link opened as the press went on", created)
	}
	browser := b.service.screens.shown("task-1", "g1")
	if browser == nil || b.service.screens.isDriven(browser) {
		t.Fatal("the goblin's browser was left his after the card closed")
	}
}

// A supervisor that starts offers Take control again on a card one that
// stopped left open on the goblin's browser, and, once a browser it runs for
// a card closes, here as Chrome exited, offers it again on that card too.
func TestAStartedSupervisorOffersACardsTakeControlAgainOnceItsBrowserClosed(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	card, err := (&Service{Store: store}).acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "ceo", Task: "task-1", Project: "throwaway", Why: "Create the shop's test account", Link: "https://example.com/login", Window: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.updateCredential(card.ID, func(r *CredentialRequest) { r.WindowState = "open" }); err != nil {
		t.Fatal(err)
	}
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stored := func() CredentialRequest {
		request, _ := s.Store.credential(card.ID)
		return request
	}
	eventually(t, "the starting supervisor's offer", func() bool { return stored().WindowState == "" })
	chrome := &fakeGoblinChrome{browser: newFakeBrowser()}
	s.screens.launch = newFakeBrowsers(t, chrome).launch
	pressErr := s.requestCredentialWindow(card.ID, card.Generation)
	s.credentialWork.Wait()
	taken := stored()
	chrome.browser.mu.Lock()
	chrome.browser.hasExited = true
	chrome.browser.mu.Unlock()

	// Act
	s.screens.sweep(h.State)

	// Assert
	if pressErr != nil || taken.WindowState != "open" {
		t.Fatalf("take control = %v, card %+v; want the browser his", pressErr, taken)
	}
	eventually(t, "Take control offered again", func() bool {
		offered := stored()
		return offered.State == "open" && offered.WindowState == "" && offered.Reason == "The goblin's browser closed. Press Take control to open it again."
	})
}

// A press of Take control still opening the goblin's browser when that
// browser closes, which offers the card's Take control again, leaves the card
// offering it and the browser the goblin's: the press never marks open a card
// whose browser is gone.
func TestATakeControlStillOpeningWhenItsBrowserClosedLeavesTheCardOfferingIt(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.Window, r.Link = true, "https://example.com/login" })
	chrome := &fakeGoblinChrome{browser: newFakeBrowser(), hold: make(chan struct{})}
	b.service.screens = newFakeBrowsers(t, chrome)
	b.send(b.handler, "/api/credentials/window", b.windowBody(), nil)
	eventually(t, "the browser's start", func() bool {
		b.service.screens.mu.Lock()
		defer b.service.screens.mu.Unlock()
		return b.service.screens.running["task-1"] != nil
	})

	// Act
	b.service.endCardScreens("task-1", "g1")
	close(chrome.hold)
	b.service.credentialWork.Wait()

	// Assert
	if card := b.stored(); card.State != "open" || card.WindowState != "" || card.Reason != "The goblin's browser closed. Press Take control to open it again." {
		t.Fatalf("card = %+v, want Take control offered again with why", card)
	}
	browser := b.service.screens.shown("task-1", "g1")
	if browser == nil || b.service.screens.isDriven(browser) {
		t.Fatal("the press left the goblin's browser his")
	}
}
