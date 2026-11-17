package supervisor

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
)

const (
	// screenQuality, screenMaxWidth and screenMaxHeight are the screencast's
	// JPEG quality and largest frame, a desktop page legible at full size.
	screenQuality   = 70
	screenMaxWidth  = 1280
	screenMaxHeight = 800
	// screenCall bounds each command the view's relay sends the browser.
	screenCall = 10 * time.Second
	// maxScreenText bounds a paste into the browser and maxHandBack the words
	// a person hands it back with, each in characters, as the board counts.
	maxScreenText = 4000
	maxHandBack   = 300
)

// screenView relays one Screen view: the frames of the page its goblin's
// browser shows, each sent once the view drew the one before, and, while a
// person drives the browser from this PC, their mouse and keys to that page.
// Nothing the view sends is logged or kept: what a person types reaches the
// page alone.
type screenView struct {
	h       *HTTP
	b       *taskBrowser
	view    *websocket.Conn
	client  screenClient
	isLocal bool
	// target is the page shown and session the view's own session on it.
	target, session string
	// pages are the page targets the browser has, the one opened or sent
	// somewhere last at the end, with what each shows.
	pages []screenPage
	// frame is the screencast session of the frame the view draws now,
	// acknowledged once it says it drew it, and zero while none is out.
	frame int
}

type screenPage struct {
	id, title, address string
	// url tells a page that went somewhere new from one that only changed
	// its title; a view is told only address, its host.
	url string
}

// screenMessage is what a view sends: ack once it drew a frame; take and
// hand-back, with the words it hands back with; and while it drives, mouse,
// key and text, which the browser's Input domain takes as they are named.
type screenMessage struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
	// Event is the mouse or key event's type in the Input domain.
	Event      string  `json:"event"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     string  `json:"button"`
	Buttons    int     `json:"buttons"`
	ClickCount int     `json:"click_count"`
	DeltaX     float64 `json:"delta_x"`
	DeltaY     float64 `json:"delta_y"`
	Modifiers  int     `json:"modifiers"`
	Key        string  `json:"key"`
	Code       string  `json:"code"`
	KeyCode    int     `json:"key_code"`
	Text       string  `json:"text"`
}

// browserScreenState is what a view is told of the browser: who drives it, whether
// this view may, the page shown, by its title and its host alone, since an
// address can carry a sign-in's code, and where on that page its goblin last
// pressed its mouse, a place and nothing else (#2537).
type browserScreenState struct {
	Type      string       `json:"type"`
	IsDriven  bool         `json:"is_driven"`
	CanDrive  bool         `json:"can_drive"`
	Title     string       `json:"title"`
	Host      string       `json:"host"`
	HasPage   bool         `json:"has_page"`
	DriveNote string       `json:"drive_note,omitempty"`
	Pointer   *screenPlace `json:"pointer,omitempty"`
}

// screenPlace is a place on a page, in its CSS pixels.
type screenPlace struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// screen serves a Screen view of a goblin's browser, which the board opens
// only while the view is on screen: its frames start when it opens and stop
// when it closes, so nothing draws, and nothing is sent, for a view nobody
// sees.
func (h *HTTP) screen(w http.ResponseWriter, r *http.Request) {
	// A browser cannot send the board's token in a WebSocket header, so it
	// comes in the query, as a terminal view's does.
	if r.Header.Get("Origin") != "http://"+h.Host || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(h.Service.Instance)) != 1 {
		apiError(w, http.StatusForbidden, "Refresh the board before opening a screen")
		return
	}
	view, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer view.CloseNow()
	view.SetReadLimit(64 << 10)
	select {
	case h.screenSlots <- struct{}{}:
		defer func() { <-h.screenSlots }()
	default:
		_ = view.Close(websocket.StatusTryAgainLater, "Too many screens are open.")
		return
	}
	query := r.URL.Query()
	if _, err := h.Service.screenTask(query.Get("task"), query.Get("generation")); err != nil {
		_ = view.Close(closeCode(err), closeReason(err.Error()))
		return
	}
	b := h.Service.screens.shown(query.Get("task"), query.Get("generation"))
	if b == nil {
		_ = view.Close(websocket.StatusNormalClosure, "This goblin's browser is not open.")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	client, err := b.source.Open(ctx)
	if err != nil {
		_ = view.Close(websocket.StatusTryAgainLater, "The goblin's browser did not answer.")
		return
	}
	defer client.Close()
	wake, release := h.Service.screens.watch(b)
	defer release()
	s := &screenView{h: h, b: b, view: view, client: client, isLocal: offMachine(r, h.Host) == ""}
	reason := s.run(ctx, wake)
	if s.session != "" {
		stop, done := context.WithTimeout(context.Background(), 2*time.Second)
		_ = client.Call(stop, s.session, "Page.stopScreencast", nil, nil)
		_ = client.Call(stop, "", "Target.detachFromTarget", map[string]string{"sessionId": s.session}, nil)
		done()
	}
	if reason != "" {
		_ = view.Close(websocket.StatusNormalClosure, closeReason(reason))
	}
}

// run relays until the view or the browser ends, and says why the browser
// did, for the view's close.
func (s *screenView) run(ctx context.Context, wake <-chan struct{}) string {
	if err := s.call(ctx, "", "Target.setDiscoverTargets", map[string]bool{"discover": true}, nil); err != nil {
		return "The goblin's browser did not answer."
	}
	var targets struct {
		Infos []targetInfo `json:"targetInfos"`
	}
	if err := s.call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return "The goblin's browser did not answer."
	}
	for _, info := range targets.Infos {
		s.notePage(info)
	}
	if err := s.showNewest(ctx); err != nil {
		return "The goblin's browser did not answer."
	}
	if err := s.tell(ctx); err != nil {
		return ""
	}
	messages := make(chan screenMessage)
	readErr := make(chan error, 1)
	go func() {
		for {
			kind, data, err := s.view.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			var message screenMessage
			if kind != websocket.MessageText || json.Unmarshal(data, &message) != nil {
				readErr <- errors.New("a screen view sends JSON text")
				return
			}
			select {
			case messages <- message:
			case <-ctx.Done():
				return
			}
		}
	}()
	events := s.client.Events()
	for {
		select {
		case <-ctx.Done():
			return ""
		case <-readErr:
			return ""
		case _, isOpen := <-wake:
			if !isOpen {
				return "The goblin's browser closed."
			}
			if err := s.tell(ctx); err != nil {
				return ""
			}
		case event, isOpen := <-events:
			if !isOpen {
				return "The goblin's browser closed."
			}
			if err := s.event(ctx, event); err != nil {
				return ""
			}
		case message := <-messages:
			if err := s.message(ctx, message); err != nil {
				return ""
			}
		}
	}
}

type targetInfo struct {
	ID    string `json:"targetId"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// call runs a command of the relay's own, bounded by screenCall.
func (s *screenView) call(ctx context.Context, session, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, screenCall)
	defer cancel()
	return s.client.Call(ctx, session, method, params, result)
}

// notePage records info when it is a page. A page that opened or went
// somewhere new is the newest, last; one that only changed its title keeps
// its place. It answers whether the pages' order changed.
func (s *screenView) notePage(info targetInfo) bool {
	if info.Type != "page" || strings.HasPrefix(info.URL, "devtools://") || strings.HasPrefix(info.URL, "chrome-extension://") {
		return false
	}
	page := screenPage{id: info.ID, title: info.Title, address: pageHost(info.URL), url: info.URL}
	if readsAsAddress(info.Title) {
		page.title = ""
	}
	if at := slices.IndexFunc(s.pages, func(each screenPage) bool { return each.id == info.ID }); at >= 0 {
		if s.pages[at].url == info.URL {
			s.pages[at] = page
			return false
		}
		s.pages = slices.Delete(s.pages, at, at+1)
	}
	s.pages = append(s.pages, page)
	return true
}

// isBlank says a page shows nothing yet, as the tab a new browser opens with.
func isBlank(pageURL string) bool {
	return pageURL == "" || pageURL == "about:blank" || strings.HasPrefix(pageURL, "chrome://newtab") || strings.HasPrefix(pageURL, "chrome://new-tab-page")
}

// readsAsAddress says title reads as an address, as Chrome titles a page with
// no title of its own, or one that has not drawn its title yet: no space, and
// a scheme's :// or a host, localhost or one holding a dot or a port, followed
// by a path, query or fragment. Chrome shows that address with its host in
// Unicode and parts of it unescaped, so the title is read as it stands, never
// matched against the page's address.
func readsAsAddress(title string) bool {
	if strings.ContainsFunc(title, unicode.IsSpace) {
		return false
	}
	if strings.Contains(title, "://") {
		return true
	}
	at := strings.IndexAny(title, "/?#")
	if at <= 0 {
		return false
	}
	host := title[:at]
	return host == "localhost" || strings.ContainsAny(host, ".:")
}

// pageHost is the host of address, the only part of it a view is shown.
func pageHost(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.Host
}

// showNewest shows the newest page, the one a goblin or a sign-in opened or
// sent somewhere last, and stops showing the one before. A page with
// something on it comes before a blank one, since a view that opens on a
// running browser cannot tell which came last: Chrome lists its pages in no
// set order.
func (s *screenView) showNewest(ctx context.Context) error {
	if len(s.pages) == 0 {
		s.target = ""
		return nil
	}
	newest := s.pages[len(s.pages)-1].id
	for _, page := range slices.Backward(s.pages) {
		if !isBlank(page.url) {
			newest = page.id
			break
		}
	}
	if newest == s.target {
		return nil
	}
	if s.session != "" {
		_ = s.call(ctx, s.session, "Page.stopScreencast", nil, nil)
		_ = s.call(ctx, "", "Target.detachFromTarget", map[string]string{"sessionId": s.session}, nil)
	}
	s.target, s.session, s.frame = newest, "", 0
	var attached struct {
		Session string `json:"sessionId"`
	}
	if err := s.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": newest, "flatten": true}, &attached); err != nil {
		return err
	}
	s.session = attached.Session
	return s.call(ctx, s.session, "Page.startScreencast", map[string]any{"format": "jpeg", "quality": screenQuality, "maxWidth": screenMaxWidth, "maxHeight": screenMaxHeight, "everyNthFrame": 1}, nil)
}

// tell sends the view what it shows and who drives it.
func (s *screenView) tell(ctx context.Context) error {
	isDriven := s.h.Service.screens.isDriven(s.b)
	page := screenPage{}
	if at := slices.IndexFunc(s.pages, func(each screenPage) bool { return each.id == s.target }); at >= 0 {
		page = s.pages[at]
	}
	told := browserScreenState{Type: "state", IsDriven: isDriven, CanDrive: s.isLocal, Title: page.title, Host: page.address, HasPage: s.target != ""}
	if pointer := s.h.Service.screens.pointerOf(s.b); pointer.target == s.target && s.target != "" {
		told.Pointer = &screenPlace{X: pointer.x, Y: pointer.y}
	}
	if !s.isLocal {
		told.DriveNote = "Take control from the board on this PC."
	}
	return s.send(ctx, told)
}

func (s *screenView) send(ctx context.Context, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, screenCall)
	defer cancel()
	return s.view.Write(ctx, websocket.MessageText, data)
}

// event takes one of the browser's events: a frame of the page shown, which
// goes to the view, and a page that opened, changed or closed.
func (s *screenView) event(ctx context.Context, e browserlogin.Event) error {
	switch e.Method {
	case "Page.screencastFrame":
		if e.Session != s.session || s.session == "" {
			return nil
		}
		var frame struct {
			Data     string `json:"data"`
			Session  int    `json:"sessionId"`
			Metadata struct {
				Width  float64 `json:"deviceWidth"`
				Height float64 `json:"deviceHeight"`
			} `json:"metadata"`
		}
		if json.Unmarshal(e.Params, &frame) != nil || frame.Session == 0 {
			return nil
		}
		s.frame = frame.Session
		return s.send(ctx, struct {
			Type   string  `json:"type"`
			Data   string  `json:"data"`
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
		}{"frame", frame.Data, frame.Metadata.Width, frame.Metadata.Height})
	case "Target.targetCreated", "Target.targetInfoChanged":
		var changed struct {
			Info targetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(e.Params, &changed) != nil {
			return nil
		}
		if s.notePage(changed.Info) {
			if err := s.showNewest(ctx); err != nil {
				return err
			}
		}
		return s.tell(ctx)
	case "Target.targetDestroyed":
		var destroyed struct {
			ID string `json:"targetId"`
		}
		if json.Unmarshal(e.Params, &destroyed) != nil {
			return nil
		}
		s.pages = slices.DeleteFunc(s.pages, func(each screenPage) bool { return each.id == destroyed.ID })
		if destroyed.ID == s.target {
			s.session = ""
			if err := s.showNewest(ctx); err != nil {
				return err
			}
		}
		return s.tell(ctx)
	}
	return nil
}

// The Input domain's events a view may send, and the buttons a mouse event
// names.
var (
	mouseEvents  = []string{"mousePressed", "mouseReleased", "mouseMoved", "mouseWheel"}
	mouseButtons = []string{"none", "left", "middle", "right", "back", "forward"}
	keyEvents    = []string{"keyDown", "keyUp"}
)

// message takes what the view sends. Only a view on this PC takes control,
// and only while a person drives does its mouse and keys reach the page;
// any view may hand it back.
func (s *screenView) message(ctx context.Context, m screenMessage) error {
	switch m.Type {
	case "ack":
		if s.frame == 0 || s.session == "" {
			return nil
		}
		frame := s.frame
		s.frame = 0
		// A page that went meanwhile acknowledges nothing, which is no
		// failure of the view's.
		_ = s.call(ctx, s.session, "Page.screencastFrameAck", map[string]int{"sessionId": frame}, nil)
		return nil
	case "take":
		if !s.isLocal || !s.h.Service.screens.drive(s.b, true) {
			return s.tell(ctx)
		}
		return nil
	case "hand-back":
		s.h.Service.handBackScreen(s.b, m.Reason)
		return nil
	case "mouse", "key", "text":
		if s.isLocal && s.session != "" && s.h.Service.screens.isDriven(s.b) {
			s.input(ctx, m)
		}
		return nil
	}
	return errors.New("a screen view sends ack, take, hand-back, mouse, key or text")
}

// input dispatches one of the person's mouse or key events, or a paste, to
// the page shown. What fails to arrive is dropped unsaid: no error about it
// names what was typed.
func (s *screenView) input(ctx context.Context, m screenMessage) {
	finite := func(values ...float64) bool {
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 100_000 {
				return false
			}
		}
		return true
	}
	modifiers := m.Modifiers & 15
	switch m.Type {
	case "mouse":
		button := m.Button
		if button == "" {
			button = "none"
		}
		if !slices.Contains(mouseEvents, m.Event) || !slices.Contains(mouseButtons, button) || !finite(m.X, m.Y, m.DeltaX, m.DeltaY) || m.ClickCount < 0 || m.ClickCount > 3 {
			return
		}
		params := map[string]any{"type": m.Event, "x": m.X, "y": m.Y, "button": button, "buttons": m.Buttons & 31, "clickCount": m.ClickCount, "modifiers": modifiers}
		if m.Event == "mouseWheel" {
			params["deltaX"], params["deltaY"] = m.DeltaX, m.DeltaY
		}
		_ = s.call(ctx, s.session, "Input.dispatchMouseEvent", params, nil)
	case "key":
		if !slices.Contains(keyEvents, m.Event) || len(m.Key) > 32 || len(m.Code) > 32 || len(m.Text) > 8 || m.KeyCode < 0 || m.KeyCode > 255 {
			return
		}
		kind := m.Event
		// A key that types nothing goes down raw, as Chrome's own keyboard
		// sends Enter's command or an arrow without a character.
		if kind == "keyDown" && m.Text == "" {
			kind = "rawKeyDown"
		}
		params := map[string]any{"type": kind, "key": m.Key, "code": m.Code, "windowsVirtualKeyCode": m.KeyCode, "nativeVirtualKeyCode": m.KeyCode, "modifiers": modifiers}
		if m.Text != "" {
			params["text"], params["unmodifiedText"] = m.Text, m.Text
		}
		_ = s.call(ctx, s.session, "Input.dispatchKeyEvent", params, nil)
	case "text":
		if m.Text == "" || utf8.RuneCountInString(m.Text) > maxScreenText {
			return
		}
		_ = s.call(ctx, s.session, "Input.insertText", map[string]string{"text": m.Text}, nil)
	}
}

// screenCard is the open step card of task whose step a person takes on its
// browser's screen, or empty when it has none.
func (s *Service) screenCard(task string) string {
	for _, r := range s.Store.Snapshot().Credentials {
		if r.State == "open" && r.Window && r.Task == task && r.WindowState == "open" {
			return r.ID
		}
	}
	return ""
}

// handBackScreen gives b back to its goblin, and closes the step card it was
// driven for as done, with the person's words, which the goblin's wait on
// the card prints as it goes on. Words that hold one shaped like a key are
// never passed on, as his answer on a card is not.
func (s *Service) handBackScreen(b *taskBrowser, words string) {
	s.screens.drive(b, false)
	card := s.screenCard(b.task)
	if card == "" {
		return
	}
	words = strings.Join(strings.Fields(words), " ")
	if words == "" || holdsKey(words) {
		words = "the step is taken"
	}
	if characters := []rune(words); len(characters) > maxHandBack {
		words = string(characters[:maxHandBack])
	}
	// A card that still waits for values closes once they arrive, so its
	// refusal leaves it open, and the browser is the goblin's again either way.
	_, _ = s.closeCredential(card, "", "done", "overlord", "Handed back from the board: "+words)
}
