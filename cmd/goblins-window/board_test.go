package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func writeBoard(t *testing.T, stateDir, url string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(stateDir, "board.json"), []byte(`{"pid":1,"url":"`+url+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The window follows the supervisor to the address it records after a
// restart, and ignores a record that is not a loopback board.
func TestTheWindowFollowsTheBoardToItsNewAddress(t *testing.T) {
	stateDir := t.TempDir()
	follow := &Follower{StateDir: stateDir, Current: "http://127.0.0.1:4310"}

	writeBoard(t, stateDir, "http://127.0.0.1:4310")
	_, same := follow.Next()
	writeBoard(t, stateDir, "http://example.com:4310")
	_, remote := follow.Next()
	writeBoard(t, stateDir, "http://127.0.0.1:52000")
	moved, did := follow.Next()

	if same || remote {
		t.Errorf("moved on the same address (%v) or a remote one (%v)", same, remote)
	}
	if !did || moved != "http://127.0.0.1:52000" || follow.Current != moved {
		t.Errorf("Next = %q, %v; want the new loopback address", moved, did)
	}
}

// What the window's page asks of the window counts only from a board's page,
// plain http on this PC: never from another site, and never from the error
// page WebView2 shows for a board that does not answer.
func TestOnlyALoopbackHTTPPageIsABoards(t *testing.T) {
	for address, want := range map[string]bool{
		"http://127.0.0.1:4310/":        true,
		"http://127.0.0.1:4310/#":       true,
		"http://[::1]:4310/":            true,
		"https://127.0.0.1:4310/":       false,
		"http://192.0.2.20:4310/":       false,
		"https://example.com/":          false,
		"chrome-error://chromewebdata/": false,
		"about:blank":                   false,
		"":                              false,
	} {
		if got := fromBoard(address); got != want {
			t.Errorf("fromBoard(%q) = %v, want %v", address, got, want)
		}
	}
}

// The window loads its board again only after the board came back from not
// answering and no board's page said it is there: a board that is shown is
// left alone, and a page that never answers is loaded once for each time the
// board comes back, never in a loop.
func TestTheWindowLoadsTheBoardOnlyWhenItsPageIsNotABoards(t *testing.T) {
	for name, test := range map[string]struct {
		// looks is whether the board answers at each look, and shown is the
		// looks after which a board's page says it is there.
		looks []bool
		shown []int
		want  []PageStep
	}{
		"a board that always answers":               {[]bool{true, true, true}, nil, []PageStep{Leave, Leave, Leave}},
		"a window started before its supervisor":    {[]bool{false, false, true, true, true}, nil, []PageStep{Leave, Leave, Ask, Load, Leave}},
		"a shown board whose supervisor restarts":   {[]bool{true, false, true, true, true}, []int{2}, []PageStep{Leave, Leave, Ask, Leave, Leave}},
		"a board that goes down again when asked":   {[]bool{false, true, false, true, true}, nil, []PageStep{Leave, Ask, Leave, Ask, Load}},
		"a page that never answers, over two stops": {[]bool{false, true, true, true, false, true, true, true}, nil, []PageStep{Leave, Ask, Load, Leave, Leave, Ask, Load, Leave}},
		"an answer from before the board went down": {[]bool{true, false, true, true}, []int{0}, []PageStep{Leave, Leave, Ask, Load}},
	} {
		t.Run(name, func(t *testing.T) {
			page := &Page{}

			var got []PageStep
			for at, answers := range test.looks {
				got = append(got, page.Look(answers))
				if slices.Contains(test.shown, at) {
					page.Shown()
				}
			}

			if !slices.Equal(got, test.want) {
				t.Errorf("steps = %v, want %v (0 leave, 1 ask, 2 load)", got, test.want)
			}
		})
	}
}

// What waits on the Overlord is what the board's page alerts: a pending
// question by its lead line unless it was asked from an open review page, an
// open review item, a command ready or running unless a credential card
// opened it, and an open request for credentials by its project, never by the
// credential names. A question asked from an open review page is returned as
// a key beside them, open without an alert of its own. The snapshot names the
// supervisor's instance too.
func TestWaitingListsWhatTheBoardsPageAlerts(t *testing.T) {
	snapshot := `{"instance":"i-1","questions":[{"id":"q1","text":"Which plan?\n- details","status":"pending"},{"id":"q2","text":"old","status":"answered"},
			{"id":"q3","text":"Asked from its page?","status":"pending","page":"r1"}],
		"reviews":[{"id":"r1","title":"Pick a layout","state":"open"},{"id":"r2","title":"done","state":"answered"}],
		"runs":[{"id":"u1","title":"Install the tool","state":"ready"},{"id":"u2","title":"ran","state":"finished"},
			{"id":"u3","title":"Sign in","state":"ready","credential_request":"c1"}],
		"credentials":[{"id":"c1","project":"shop","state":"open","names":["API_KEY"]},{"id":"c2","project":"blog","state":"saved","names":["TOKEN"]}]}`

	instance, items, folded, err := waiting([]byte(snapshot))

	if err != nil {
		t.Fatal(err)
	}
	want := []Item{{"question:q1", "Which plan?"}, {"review:r1", "Pick a layout"}, {"run:u1", "Install the tool"}, {"credential:c1", "Credentials wanted for shop"}}
	if instance != "i-1" || !slices.Equal(items, want) {
		t.Errorf("waiting = %q, %+v; want i-1, %+v", instance, items, want)
	}
	if !slices.Equal(folded, []string{"question:q3"}) {
		t.Errorf("folded = %v, want question:q3 alone", folded)
	}
}

// A question that was pending inside its page's card is not new when that
// page closes and the question becomes a card of its own; a question that was
// never there before still is.
func TestTheWatcherDoesNotTellAQuestionItsPageCarried(t *testing.T) {
	var mu sync.Mutex
	snapshot := `{"instance":"i-1","questions":[{"id":"q1","text":"Asked from its page?","status":"pending","page":"r1"}],
		"reviews":[{"id":"r1","title":"Pick a layout","state":"open"}]}`
	board := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(snapshot))
	}))
	defer board.Close()
	watcher := &Watcher{}

	first, _ := watcher.New(board.URL)
	mu.Lock()
	snapshot = `{"instance":"i-1","questions":[{"id":"q1","text":"Asked from its page?","status":"pending"},{"id":"q2","text":"Never there before?","status":"pending"}],
		"reviews":[{"id":"r1","title":"Pick a layout","state":"withdrawn"}]}`
	mu.Unlock()
	second, _ := watcher.New(board.URL)

	if len(first) != 0 {
		t.Errorf("first look = %+v, want nothing: it only learns what waits", first)
	}
	if !slices.Equal(second, []Item{{"question:q2", "Never there before?"}}) {
		t.Errorf("second look = %+v, want the question that was never there alone", second)
	}
}

// The window notifies only what is new since its last look: its first look
// learns what already waits, which the board shows, and a board that cannot
// be read shows nothing new.
func TestTheWatcherTellsOnlyNewItems(t *testing.T) {
	var mu sync.Mutex
	snapshot := `{"instance":"i-1","questions":[{"id":"q1","text":"First?","status":"pending"}]}`
	board := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/api/snapshot" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(snapshot))
	}))
	defer board.Close()
	watcher := &Watcher{}

	first, answered := watcher.New(board.URL)
	mu.Lock()
	snapshot = `{"instance":"i-2","questions":[{"id":"q1","text":"First?","status":"pending"},{"id":"q2","text":"Second?","status":"pending"}]}`
	mu.Unlock()
	second, _ := watcher.New(board.URL)
	third, _ := watcher.New(board.URL)
	unreadable, down := watcher.New("http://127.0.0.1:1")

	if len(first) != 0 {
		t.Errorf("first look = %+v, want nothing: it only learns what waits", first)
	}
	if !slices.Equal(second, []Item{{"question:q2", "Second?"}}) {
		t.Errorf("second look = %+v, want the new question alone", second)
	}
	if len(third) != 0 || len(unreadable) != 0 {
		t.Errorf("third look %+v, unreadable board %+v; want nothing new", third, unreadable)
	}
	if !answered || down {
		t.Errorf("the board answered %v, the unreadable one %v; want true, false", answered, down)
	}
	if watcher.Instance != "i-2" {
		t.Errorf("the watcher holds instance %q, want i-2, which the last snapshot it read named", watcher.Instance)
	}
}

// At login Windows runs the window alone where goblins started it, which opens
// the app through that goblins with no terminal. A window started on its own
// has no such goblins, so it starts itself on the same board, in the tray.
func TestTheLoginCommandIsTheWindowAloneOrTheWindowOnItsBoard(t *testing.T) {
	for name, test := range map[string]struct{ launcher, want string }{
		"started by goblins": {`C:\app\goblins.exe`, `"C:\app\goblins-window.exe" --background`},
		"started on its own": {"", `"C:\app\goblins-window.exe" --board http://127.0.0.1:4310 --state "C:\home\state" --background`},
	} {
		t.Run(name, func(t *testing.T) {
			got := loginCommand(test.launcher, `C:\app\goblins-window.exe`, "http://127.0.0.1:4310", `C:\home\state`)

			if got != test.want {
				t.Errorf("loginCommand = %s, want %s", got, test.want)
			}
		})
	}
}

// ownRunKey points runKey at a key of the test's own, in place of the user's
// Run key, and removes that key when the test ends.
func ownRunKey(t *testing.T) {
	t.Helper()
	var name [8]byte
	if _, err := rand.Read(name[:]); err != nil {
		t.Fatal(err)
	}
	runKey = `Software\CodeGoblinsTest\` + hex.EncodeToString(name[:])
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, runKey)
		// Removed only once no other run's key is under it.
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\CodeGoblinsTest`)
		runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	})
}

// loginEntry reads the login entry as it is written under runKey, and whether
// there is one.
func loginEntry(t *testing.T) (string, bool) {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	entry, _, err := key.GetStringValue(runValue)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return entry, true
}

// A window from before the app opened alone left a login entry that runs the
// goblins that started it, in a terminal. The window that goblins starts makes
// that entry its own, once, so its tray shows Start at login as on, and turning
// it off once removes the entry for good.
func TestTheEarlierLoginEntryBecomesTheWindowAlone(t *testing.T) {
	// Arrange
	ownRunKey(t)
	const launcher, window = `C:\app\goblins.exe`, `C:\app\goblins-window.exe`
	login := loginCommand(launcher, window, "http://127.0.0.1:4310", `C:\home\state`)
	if err := SetStartAtLogin(`"C:\app\goblins.exe" --window --background`, true); err != nil {
		t.Fatal(err)
	}

	// Act
	adopted, adoptErr := adoptEarlierLogin(launcher, window, login)
	entry, _ := loginEntry(t)
	on := StartsAtLogin(login)
	again, againErr := adoptEarlierLogin(launcher, window, login)
	entryAgain, _ := loginEntry(t)
	offErr := SetStartAtLogin(login, false)
	_, left := loginEntry(t)
	afterOff, afterOffErr := adoptEarlierLogin(launcher, window, login)
	_, back := loginEntry(t)

	// Assert
	if !adopted || entry != `"C:\app\goblins-window.exe" --background` || !on {
		t.Errorf("the first start rewrote the entry: %v, to %s, read as on: %v; want true, the window alone with --background, true", adopted, entry, on)
	}
	if again || entryAgain != entry {
		t.Errorf("the second start rewrote the entry: %v, leaving %s; want false, and %s as it was", again, entryAgain, entry)
	}
	if left || afterOff || back {
		t.Errorf("after turning it off once the entry is still there: %v, a start rewrote it: %v, and it is back: %v; want false, false, false", left, afterOff, back)
	}
	if adoptErr != nil || againErr != nil || offErr != nil || afterOffErr != nil {
		t.Errorf("errors: first start %v, second start %v, off %v, start after off %v", adoptErr, againErr, offErr, afterOffErr)
	}
}

// Only the entry an earlier window wrote for the goblins beside this window is
// made this window's: no entry, another home's, another command of the same
// goblins, the entry of a window on its board and one that is this window's
// already are left as they are, and a window started on its own adopts none.
func TestNoOtherLoginEntryIsRewritten(t *testing.T) {
	const window, board, stateDir = `C:\app\goblins-window.exe`, "http://127.0.0.1:4310", `C:\home\state`
	onItsBoard := loginCommand("", window, board, stateDir)
	for name, test := range map[string]struct {
		launcher, entry string
		on              bool
	}{
		"no entry":             {`C:\app\goblins.exe`, "", false},
		"another home's entry": {`C:\app\goblins.exe`, `"C:\elsewhere\goblins.exe" --window --background`, false},
		"the entry of a goblins in another folder":  {`C:\elsewhere\goblins.exe`, `"C:\elsewhere\goblins.exe" --window --background`, false},
		"another command of the same goblins":       {`C:\app\goblins.exe`, `"C:\app\goblins.exe" --board`, false},
		"the entry of a window on its board":        {`C:\app\goblins.exe`, onItsBoard, false},
		"already the window alone":                  {`C:\app\goblins.exe`, `"C:\APP\GOBLINS-WINDOW.EXE" --background`, true},
		"a window started on its own":               {"", `"C:\app\goblins.exe" --window --background`, false},
		"a window on its board, with its own entry": {"", onItsBoard, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			ownRunKey(t)
			if test.entry != "" {
				if err := SetStartAtLogin(test.entry, true); err != nil {
					t.Fatal(err)
				}
			}
			login := loginCommand(test.launcher, window, board, stateDir)

			// Act
			adopted, err := adoptEarlierLogin(test.launcher, window, login)

			// Assert
			if adopted || err != nil {
				t.Errorf("adoptEarlierLogin = %v, %v; want false and no error", adopted, err)
			}
			if entry, there := loginEntry(t); entry != test.entry || there != (test.entry != "") {
				t.Errorf("the entry is %q (there: %v), want %q as it was", entry, there, test.entry)
			}
			if on := StartsAtLogin(login); on != test.on {
				t.Errorf("Start at login reads as on: %v, want %v", on, test.on)
			}
		})
	}
}

// Start at login is one entry under the user's Run key holding the login
// command, added and removed; this test uses a key of its own.
func TestStartAtLoginAddsAndRemovesItsEntry(t *testing.T) {
	ownRunKey(t)
	command := `"C:\Users\someone\AppData\Local\CodeGoblins\goblins.exe" --window --background`

	before := StartsAtLogin(command)
	onErr := SetStartAtLogin(command, true)
	on := StartsAtLogin(command)
	otherCommand := StartsAtLogin(`"C:\elsewhere\goblins.exe" --window --background`)
	offErr := SetStartAtLogin(command, false)
	off := StartsAtLogin(command)
	againErr := SetStartAtLogin(command, false)

	if before || !on || otherCommand || off {
		t.Errorf("starts at login: before %v, on %v, for another command %v, off %v; want false, true, false, false", before, on, otherCommand, off)
	}
	if onErr != nil || offErr != nil || againErr != nil {
		t.Errorf("errors: on %v, off %v, off again %v", onErr, offErr, againErr)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if _, _, err := key.GetStringValue(runValue); err == nil {
		t.Error("the entry is still there after removing it")
	}
}
