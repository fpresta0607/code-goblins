package supervisor

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// edgeProgram is the browser program in these tests, as every process of a
// browser runs it.
const edgeProgram = `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`

// The board's proof takes two things for the Overlord's without a parent at
// the desktop: this home's own desktop window, and a browser that holds a
// grant. The tests here build one of those and then spoil it.
var (
	boardOpened = time.Now().Add(-2 * time.Hour)
	// cutWindow is his desktop window after its opener exited: WebView2 holds
	// the connection, and the parents stop at the window.
	cutWindow = []proc.Entry{
		{PID: 7001, ParentPID: 6200, ExeBase: "msedgewebview2.exe", Start: boardOpened.Add(2 * time.Second)},
		{PID: 6200, ParentPID: 22504, ExeBase: "msedgewebview2.exe", Start: boardOpened.Add(time.Second)},
		{PID: 22504, ParentPID: 21820, ExeBase: "goblins-window.exe", Start: boardOpened},
	}
	// cutBrowser is a browser after its opener exited: one of its own
	// processes holds the connection, and the parents stop at the browser.
	cutBrowser = []proc.Entry{
		{PID: 7001, ParentPID: 6200, ExeBase: "msedge.exe", Start: boardOpened.Add(time.Second)},
		{PID: 6200, ParentPID: 5150, ExeBase: "msedge.exe", Start: boardOpened},
	}
	desktopShell = proc.Entry{PID: 900, ExeBase: "explorer.exe", Start: boardOpened.Add(-time.Hour)}
)

// under returns chain with above as its parents, without changing chain.
func under(chain []proc.Entry, above ...proc.Entry) []proc.Entry {
	return append(append([]proc.Entry{}, chain...), above...)
}

// boardAsk is one request to a board as these tests build it.
type boardAsk struct {
	ancestry []proc.Entry
	env      []string
	// programs and owners say what a process is and whose where it is not
	// the Overlord's browser.
	programs map[int]proc.Identity
	owners   map[int]proc.Owner
	// secret is the grant the request's browser holds, if any.
	secret string
}

// boardAsked is a supervisor whose board is asked as ask describes, and the
// request that asks it.
func boardAsked(t *testing.T, store *Store, ask boardAsk) (*Service, *http.Request) {
	t.Helper()
	s := boardService(store)
	s.peerOf = func(netip.AddrPort, netip.AddrPort) (int, error) { return 7001, nil }
	s.inspectCaller = func(int) ([]proc.Entry, []string, error) { return ask.ancestry, ask.env, nil }
	s.windows = (&standInWindows{image: edgeProgram, programs: ask.programs, owners: ask.owners}).system()
	request := httptest.NewRequest("POST", "http://"+credentialBoardHost+"/api/afk", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	if ask.secret != "" {
		request.AddCookie(&http.Cookie{Name: boardGrantCookie, Value: ask.secret})
	}
	return s, request
}

// windowIn is his desktop window, pid 22504, as Windows names its program in
// the home whose bin is bin, changed by change when a test spoils it.
func windowIn(bin string, change func(*proc.Identity)) map[int]proc.Identity {
	window := proc.Identity{Image: filepath.Join(bin, windowName), Arguments: []string{filepath.Join(bin, windowName)}}
	if change != nil {
		change(&window)
	}
	return map[int]proc.Identity{22504: window}
}

// What is not the Overlord's stays refused where his window and a browser
// with a grant are taken: nothing that makes those two his makes an agent's
// program, another user's, a driven browser or a copied secret his. Each case
// would pass if the proof took the window's name, the grant's secret or the
// lack of a mark for enough.
func TestWhatIsNotHisIsRefusedWhereHisWindowAndAGrantedBrowserAreTaken(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	bin := h.Bin()
	now := time.Now()
	granter := boardService(store)
	grant := func(at time.Time) string {
		t.Helper()
		secret, err := granter.grantBoard(edgeProgram, at)
		if err != nil {
			t.Fatal(err)
		}
		return secret
	}
	stale := grant(now.Add(-boardGrantLife - time.Minute))
	his := grant(now.Add(-time.Hour))
	chromium := `C:\Users\overlord\AppData\Local\ms-playwright\chromium-1140\chrome-win\chrome.exe`
	webview := `C:\Program Files (x86)\Microsoft\EdgeWebView\Application\msedgewebview2.exe`
	const unproven = "nothing proves the program that shows this board is his"
	for _, c := range []struct {
		name    string
		ask     boardAsk
		refusal string
	}{
		{"a window a goblin started, by the window's own environment", boardAsk{ancestry: cutWindow, programs: windowIn(bin, func(w *proc.Identity) { w.Environment = []string{"CFO_ROLE=goblin"} })}, "runs in a goblin's terminal"},
		{"a window a goblin started, by its WebView2's environment", boardAsk{ancestry: cutWindow, env: []string{"CFO_ROLE=goblin"}, programs: windowIn(bin, nil)}, "runs in a goblin's terminal"},
		{"a window a gate agent started", boardAsk{ancestry: cutWindow, env: []string{"NO_MISTAKES_GATE=1"}, programs: windowIn(bin, nil)}, "as a gate agent"},
		{"a window started from a terminal of the fleet", boardAsk{ancestry: cutWindow, programs: windowIn(bin, func(w *proc.Identity) { w.Environment = []string{"CFO_HOST_ID=cfo"} })}, "in native terminal cfo"},
		{"a window under an agent harness whose own opener exited", boardAsk{ancestry: under(cutWindow, proc.Entry{PID: 21820, ParentPID: 3000, ExeBase: "node.exe", Start: boardOpened.Add(-time.Minute)}), programs: windowIn(bin, nil)}, "under an agent harness (node.exe pid 21820)"},
		{"a window a test drives through its WebView2", boardAsk{ancestry: cutWindow, programs: map[int]proc.Identity{22504: windowIn(bin, nil)[22504], 6200: {Image: webview, Arguments: []string{webview, "--embedded-browser-webview=1", "--remote-debugging-port=9222"}}}}, "another program can drive the browser that shows this board (msedgewebview2.exe pid 6200 was started with --remote-debugging-port)"},
		{"a window started with a debugging port of its own", boardAsk{ancestry: cutWindow, programs: windowIn(bin, func(w *proc.Identity) {
			w.Arguments = append(w.Arguments, "--webview-args", "--remote-debugging-port=9222")
		})}, "another program can drive the browser that shows this board (goblins-window.exe pid 22504 was started with --remote-debugging-port)"},
		{"a window of another Windows user", boardAsk{ancestry: cutWindow, programs: windowIn(bin, nil), owners: map[int]proc.Owner{22504: {User: "S-1-5-21-1004-1002", Session: hisDesktop.Session}}}, "runs as another Windows user"},
		{"a window in another Windows session", boardAsk{ancestry: cutWindow, programs: windowIn(bin, nil), owners: map[int]proc.Owner{22504: {User: hisDesktop.User, Session: 2}}}, "runs in Windows session 2, and the supervisor in session 1"},
		{"a program named as his window, run from another folder", boardAsk{ancestry: cutWindow, programs: windowIn(`C:\Users\overlord\Downloads`, nil)}, unproven},
		{"another program run from the home's bin", boardAsk{ancestry: []proc.Entry{cutWindow[0], cutWindow[1], {PID: 22504, ParentPID: 21820, ExeBase: "cfo.exe", Start: boardOpened}}, programs: map[int]proc.Identity{22504: {Image: filepath.Join(bin, "cfo.exe")}}}, unproven},

		{"a goblin's browser that holds a copy of his grant", boardAsk{ancestry: cutBrowser, env: []string{"CFO_ROLE=goblin"}, secret: his}, "runs in a goblin's terminal"},
		{"a goblin's headless browser that holds a copy of his grant", boardAsk{ancestry: cutBrowser, secret: his, programs: map[int]proc.Identity{6200: {Image: edgeProgram, Arguments: []string{edgeProgram, "--headless=new", `--user-data-dir=C:\tmp\profile`}}}}, "another program can drive the browser that shows this board (msedge.exe pid 6200 was started with --headless)"},
		{"a browser a program drives through a pipe, with a copy of his grant", boardAsk{ancestry: cutBrowser, secret: his, programs: map[int]proc.Identity{6200: {Image: edgeProgram, Arguments: []string{edgeProgram, "--remote-debugging-pipe", "--enable-automation"}}}}, "was started with --remote-debugging-pipe"},
		{"a Firefox a program drives, with a copy of his grant", boardAsk{ancestry: cutBrowser, secret: his, programs: map[int]proc.Identity{6200: {Image: edgeProgram, Arguments: []string{"firefox.exe", "-marionette"}}}}, "was started with --marionette"},
		{"a gate agent's browser that holds a copy of his grant", boardAsk{ancestry: cutBrowser, env: []string{"NO_MISTAKES_GATE=1"}, secret: his}, "as a gate agent"},
		{"a script in a harness's process tree that sends a copy of his grant", boardAsk{ancestry: []proc.Entry{{PID: 7001, ParentPID: 6200, ExeBase: "python.exe", Start: boardOpened.Add(time.Second)}, {PID: 6200, ParentPID: 5150, ExeBase: "claude.exe", Start: boardOpened}}, secret: his}, "under an agent harness (claude.exe pid 6200)"},
		{"a script whose harness is gone but left its mark, with a copy of his grant", boardAsk{ancestry: cutBrowser[:1], env: []string{"CLAUDECODE=1"}, secret: his}, "its environment carries CLAUDECODE"},
		{"a script with nothing of an agent's left on it, with a copy of his grant", boardAsk{ancestry: []proc.Entry{{PID: 7001, ParentPID: 5150, ExeBase: "curl.exe", Start: boardOpened}}, secret: his, programs: map[int]proc.Identity{7001: {Image: `C:\Windows\System32\curl.exe`}}}, unproven},
		{"another browser that holds a copy of his grant", boardAsk{ancestry: cutBrowser, secret: his, programs: map[int]proc.Identity{7001: {Image: chromium}, 6200: {Image: chromium}}}, unproven},
		{"a browser of another Windows user that holds a copy of his grant", boardAsk{ancestry: cutBrowser, secret: his, owners: map[int]proc.Owner{6200: {User: "S-1-5-21-1004-1002", Session: hisDesktop.Session}}}, "runs as another Windows user"},
		{"a browser in another Windows session that holds a copy of his grant", boardAsk{ancestry: cutBrowser, secret: his, owners: map[int]proc.Owner{6200: {User: hisDesktop.User, Session: 3}}}, "runs in Windows session 3"},
		{"his browser with a grant given too long ago", boardAsk{ancestry: cutBrowser, secret: stale}, unproven},
		{"his browser with a secret this home never gave", boardAsk{ancestry: cutBrowser, secret: "c2VjcmV0LW9mLWFub3RoZXItaG9tZQ"}, unproven},
		{"his browser with the hash the home records, in place of the secret", boardAsk{ancestry: cutBrowser, secret: hashedGrant(his)}, unproven},
		{"his browser with no grant", boardAsk{ancestry: cutBrowser}, unproven},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, request := boardAsked(t, store, c.ask)

			// Act
			from, err := s.overlordsBoard(request, credentialBoardHost, now, askingBoard)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("overlordsBoard = %q, %v, want it refused as %q", from, err, c.refusal)
			}
			var refused *boardRefusal
			if !errors.As(err, &refused) {
				t.Fatalf("the refusal is a %T, want the board's, which has the sentence he reads", err)
			}
			assertOneShortSentence(t, refused.say)
		})
	}

	// The same window and the same browser, unspoiled, are his: without these
	// every refusal above could pass for the wrong reason.
	for name, ask := range map[string]boardAsk{
		"his window": {ancestry: cutWindow, programs: windowIn(bin, nil)},
		"his window on a renamed old program": {ancestry: cutWindow, programs: windowIn(bin, func(w *proc.Identity) {
			w.Image = filepath.Join(bin, "goblins-window.exe.LU4BWTHK5ISXTSKSYN7HMYSJ32.old")
		})},
		"his browser with its grant": {ancestry: cutBrowser, secret: his},
	} {
		t.Run(name, func(t *testing.T) {
			s, request := boardAsked(t, store, ask)

			// Act
			from, err := s.overlordsBoard(request, credentialBoardHost, now, askingBoard)

			// Assert
			top := ask.ancestry[len(ask.ancestry)-1]
			if err != nil || !strings.HasPrefix(from, "his own board ("+top.ExeBase+" pid ") {
				t.Fatalf("overlordsBoard = %q, %v, want %s taken for his", from, err, top.ExeBase)
			}
		})
	}
}

// A program whose owner Windows will not name is not taken for his.
func TestABoardWhoseOwnerCannotBeReadIsRefused(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s, request := boardAsked(t, store, boardAsk{ancestry: cutWindow, programs: windowIn(h.Bin(), nil)})
	s.windows.owner = func(int, time.Time) (proc.Owner, error) { return proc.Owner{}, errors.New("access is denied") }

	// Act
	_, err := s.overlordsBoard(request, credentialBoardHost, time.Now(), askingBoard)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "could not read the program that shows this board") {
		t.Fatalf("overlordsBoard = %v, want it refused as a program that cannot be read", err)
	}
}

// loadBoard asks the board for its page as a browser does, and returns the
// grant the answer gives that browser, or nil.
func loadBoard(t *testing.T, s *Service, secret string) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest("GET", "http://"+credentialBoardHost+"/", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	if secret != "" {
		request.AddCookie(&http.Cookie{Name: boardGrantCookie, Value: secret})
	}
	response := httptest.NewRecorder()
	NewHTTP(s, credentialBoardHost, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body>board</body></html>")}}).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "board") {
		t.Fatalf("GET / = %d %s, want the board's page whatever is proven", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == boardGrantCookie {
			return cookie
		}
	}
	return nil
}

// "for a browser opened from another program that has since closed": a
// browser he started loads the board while its parents reach the desktop and
// is given a grant. Its opener exits, and later it restarts itself, as a
// browser does after its own update, and it still turns AFK mode on and off.
func TestABrowserHeStartedKeepsHisSwitchAfterItsOpenerExitsAndAfterItRestarts(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s, _ := boardAsked(t, store, boardAsk{})
	chain := under(cutBrowser, proc.Entry{PID: 5150, ParentPID: 900, ExeBase: "Code.exe", Start: boardOpened.Add(-time.Minute)}, desktopShell)
	s.inspectCaller = func(int) ([]proc.Entry, []string, error) { return chain, []string{"USERNAME=overlord"}, nil }
	turn := func(secret string, on bool) (int, string) {
		t.Helper()
		body := `{"on":false}`
		if on {
			body = `{"on":true}`
		}
		return askTheBoard(t, s, "POST", "/api/afk", body, func(r *http.Request) {
			if secret != "" {
				r.AddCookie(&http.Cookie{Name: boardGrantCookie, Value: secret})
			}
		})
	}

	// Act: it loads the board, then the program that opened it closes.
	grant := loadBoard(t, s, "")
	if grant == nil {
		t.Fatal("a browser whose parents reach the desktop loaded the board and was given no grant")
	}
	chain = cutBrowser
	bareCode, bareBody := turn("", true)
	onCode, onBody := turn(grant.Value, true)
	on, onErr := afk.Read(h.State)
	// It restarts itself: new processes of the same program, started by the
	// one that exited.
	chain = []proc.Entry{
		{PID: 9001, ParentPID: 8200, ExeBase: "msedge.exe", Start: time.Now().Add(-time.Second)},
		{PID: 8200, ParentPID: 6200, ExeBase: "msedge.exe", Start: time.Now().Add(-2 * time.Second)},
	}
	offCode, offBody := turn(grant.Value, false)
	off, offErr := afk.Read(h.State)

	// Assert
	if !grant.HttpOnly || grant.SameSite != http.SameSiteStrictMode || grant.Path != "/" || grant.MaxAge != int(boardGrantLife/time.Second) || len(grant.Value) < 40 {
		t.Errorf("the grant's cookie = %+v, want one no page can read, sent only from the board's own pages, for the grant's life", grant)
	}
	if bareCode != http.StatusForbidden {
		t.Errorf("the same browser without its grant = %d %s, want it refused, since nothing else proves it", bareCode, bareBody)
	}
	if onCode != http.StatusOK || strings.TrimSpace(onBody) != `{"state":"on"}` || onErr != nil || !on.On || on.From != "his own board (msedge.exe pid 6200)" {
		t.Fatalf("POST on after its opener exited = %d %s, the switch %+v (%v), want AFK mode turned on from the browser", onCode, onBody, on, onErr)
	}
	if offCode != http.StatusOK || strings.TrimSpace(offBody) != `{"state":"off"}` || offErr != nil || off.On || off.EndedFrom != "his own board (msedge.exe pid 8200)" {
		t.Fatalf("POST off after it restarted itself = %d %s, the switch %+v (%v), want AFK mode turned off from the browser it restarted as", offCode, offBody, off, offErr)
	}
	recorded, err := fsx.ReadFile(filepath.Join(h.State, boardGrantFile))
	if err != nil || strings.Contains(string(recorded), grant.Value) || !strings.Contains(string(recorded), hashedGrant(grant.Value)) {
		t.Errorf("the home's record = %s (%v), want the secret's hash and never the secret", recorded, err)
	}
}

// A grant stands on a desktop proof and on nothing else: a load that proves
// nothing gives none, an agent's browser gets none, a grant gives no grant,
// and a browser whose grant is fresh keeps it.
func TestOnlyALoadWhoseParentsReachTheDesktopIsGivenAGrant(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	started := under(cutBrowser, desktopShell)
	s, _ := boardAsked(t, store, boardAsk{})
	loadAs := func(ask boardAsk) *http.Cookie {
		t.Helper()
		s.inspectCaller = func(int) ([]proc.Entry, []string, error) { return ask.ancestry, ask.env, nil }
		s.windows = (&standInWindows{image: edgeProgram, programs: ask.programs, owners: ask.owners}).system()
		return loadBoard(t, s, ask.secret)
	}

	// Act
	first := loadAs(boardAsk{ancestry: started})
	if first == nil {
		t.Fatal("a browser he started from the desktop loaded the board and was given no grant")
	}
	none := map[string]*http.Cookie{
		"a browser whose grant is fresh":                loadAs(boardAsk{ancestry: started, secret: first.Value}),
		"a browser whose parents stop short":            loadAs(boardAsk{ancestry: cutBrowser}),
		"a browser that holds a grant and nothing else": loadAs(boardAsk{ancestry: cutBrowser, secret: first.Value}),
		"a goblin's browser from the desktop":           loadAs(boardAsk{ancestry: started, env: []string{"CFO_ROLE=goblin"}}),
		"a driven browser from the desktop":             loadAs(boardAsk{ancestry: started, programs: map[int]proc.Identity{6200: {Image: edgeProgram, Arguments: []string{edgeProgram, "--remote-debugging-port=9222"}}}}),
		"his desktop window, which needs none":          loadAs(boardAsk{ancestry: cutWindow, programs: windowIn(h.Bin(), nil)}),
	}
	// A day later the same browser, still from the desktop, loads it again.
	aged, err := readBoardGrants(h.State)
	if err != nil || len(aged) != 1 {
		t.Fatalf("the home's record = %+v, %v, want the one grant given", aged, err)
	}
	aged[0].At = aged[0].At.Add(-boardGrantFresh - time.Minute)
	data, err := json.Marshal(aged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, boardGrantFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	later := loadAs(boardAsk{ancestry: started, secret: first.Value})

	// Assert
	for name, given := range none {
		if given != nil {
			t.Errorf("%s was given a grant, want none", name)
		}
	}
	if later == nil || later.Value == first.Value {
		t.Errorf("a day later the browser was given %+v, want a new grant in place of its old one", later)
	}
}

// A record of grants that cannot be read proves nothing: a browser that holds
// a secret is refused, the refusal says the record is why, and the next grant
// given replaces it.
func TestARecordOfGrantsThatCannotBeReadGrantsNothingAndIsReplaced(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	secret, err := boardService(store).grantBoard(edgeProgram, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, boardGrantFile), []byte(`[{"secret": tr`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, request := boardAsked(t, store, boardAsk{ancestry: cutBrowser, secret: secret})

	// Act
	_, refused := s.overlordsBoard(request, credentialBoardHost, time.Now(), askingBoard)
	s.inspectCaller = func(int) ([]proc.Entry, []string, error) { return under(cutBrowser, desktopShell), nil, nil }
	given := loadBoard(t, s, secret)

	// Assert
	if refused == nil || !strings.Contains(refused.Error(), "the grants this home gave its boards could not be read") {
		t.Errorf("overlordsBoard = %v, want it refused for want of a record it can read", refused)
	}
	if given == nil {
		t.Fatal("the browser, from the desktop again, was given no grant")
	}
	if grants, err := readBoardGrants(h.State); err != nil || len(grants) != 1 || grants[0].Secret != hashedGrant(given.Value) {
		t.Errorf("the home's record = %+v, %v, want the one new grant in place of what could not be read", grants, err)
	}
}

// Every sentence a board shows the Overlord when it refuses him says what to
// do and nothing else, whichever of his switches he pressed and wherever.
func TestEverySentenceABoardShowsHimIsOneShortSentence(t *testing.T) {
	for name, who := range map[string]asker{"AFK on": switchingBoard(true), "AFK off": switchingBoard(false), "Update": updatingBoard} {
		t.Run(name, func(t *testing.T) {
			assertOneShortSentence(t, who.say)
			assertOneShortSentence(t, who.sayInWindow)
			if !strings.Contains(who.say, "in the Code Goblins window") || strings.Contains(who.sayInWindow, "in the Code Goblins window") || !strings.Contains(who.sayInWindow, "open it again from the Start menu") {
				t.Errorf("a browser is told %q and his window %q, want the browser sent to his window, and the window, which cannot be sent to itself, told to open again", who.say, who.sayInWindow)
			}
		})
	}
}

// "No dialog ever shows a raw refusal paragraph: one short sentence at most,
// and what failed goes to the CFO as a wake" (2026-10-09). His window, started
// by an agent, is told to open again, and a switch that fails after it was
// proven his is one sentence too.
func TestABoardIsToldOneShortSentenceAndTheCFOWhatFailed(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s, _ := boardAsked(t, store, boardAsk{ancestry: under(cutWindow, proc.Entry{PID: 21820, ParentPID: 900, ExeBase: "claude.exe", Start: boardOpened.Add(-time.Minute)}, desktopShell), programs: windowIn(h.Bin(), nil)})

	// Act
	refusedCode, refusedBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil)
	s.inspectCaller = func(int) ([]proc.Entry, []string, error) { return cutWindow, nil, nil }
	if err := os.WriteFile(filepath.Join(h.State, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
		t.Fatal(err)
	}
	failedCode, failedBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)

	// Assert
	var refused, failed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(refusedBody), &refused); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(failedBody), &failed); err != nil {
		t.Fatal(err)
	}
	if refusedCode != http.StatusForbidden || refused.Error != "Quit Code Goblins from its tray icon, open it again from the Start menu, then switch AFK there." {
		t.Errorf("POST off from his window under an agent = %d %q, want the one sentence his window shows", refusedCode, refused.Error)
	}
	if failedCode != http.StatusConflict || failed.Error != "Turn AFK off to reset its switch, which cannot be read." {
		t.Errorf("POST on with a switch that cannot be read = %d %q, want what to do in one sentence", failedCode, failed.Error)
	}
	assertOneShortSentence(t, failed.Error)
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 2 {
		t.Fatalf("the CFO's queue = %+v, %v, want the refusal and the failure", pending, err)
	}
	if !strings.Contains(pending[0].Detail, "A press of AFK mode's switch on a board was refused") || !strings.Contains(pending[0].Detail, "under an agent harness (claude.exe pid 21820)") || !strings.Contains(pending[0].Detail, "The board told him: Quit Code Goblins") {
		t.Errorf("the CFO is told %q, want what the supervisor found and what the board told him", pending[0].Detail)
	}
	if !strings.Contains(pending[1].Detail, "failed") || !strings.Contains(pending[1].Detail, "cannot be read") {
		t.Errorf("the CFO is told %q, want why the switch failed", pending[1].Detail)
	}
}
