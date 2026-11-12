package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// screenBoard is a board whose goblin task-1 runs a fake browser, served on
// a real socket so its Screen view is a real WebSocket.
type screenBoard struct {
	service *Service
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
	return &screenBoard{service: service, server: server, chrome: chrome, browser: b}
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	query := url.Values{"task": {"task-1"}, "generation": {"g1"}, "token": {board.service.Instance}}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(board.server.URL, "http")+"/api/screen?"+query.Encode(), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {board.server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return testScreenView{conn}
}

// next is what the view is told next.
func (v testScreenView) next(t *testing.T) screenSaid {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
			if !told.IsDriven || !told.CardOpen {
				t.Fatalf("the view was told %s, want him driving for an open card", told.raw)
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
