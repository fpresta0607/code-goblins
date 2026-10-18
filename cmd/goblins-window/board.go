package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/waiting"
)

// boardAddress reads the board's address from the record cfo serve writes in
// the fleet's state folder, and refuses anything but a plain loopback board
// address, as goblins does.
func boardAddress(stateDir string) (string, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, "board.json"))
	if err != nil {
		return "", err
	}
	var record struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return "", err
	}
	parsed, err := url.Parse(record.URL)
	if err != nil || parsed.Scheme != "http" || parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", fmt.Errorf("board record names %q, not a board address", record.URL)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip == nil || !ip.IsLoopback() || parsed.Port() == "" {
		return "", fmt.Errorf("board record names %q, not a loopback address", record.URL)
	}
	return record.URL, nil
}

// Follower keeps the window on the board the supervisor serves now: a
// restarted supervisor may listen on another port.
type Follower struct {
	StateDir string
	Current  string
}

// Next returns the board's address and whether it moved since Current, which
// it then names. A record that cannot be read changes nothing, since the
// supervisor may be starting.
func (f *Follower) Next() (string, bool) {
	address, err := boardAddress(f.StateDir)
	if err != nil || address == f.Current {
		return f.Current, false
	}
	f.Current = address
	return address, true
}

// fromBoard reports whether a page's address is a board's: plain http on this
// PC. What the window's page asks of the window is honoured for such a page
// alone.
func fromBoard(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(parsed.Hostname())
	return ip != nil && ip.IsLoopback()
}

// shownMessage is what a page answers when the window asks whether it is
// there.
const shownMessage = "shown"

// shownScript asks the window's page to say it is there and drawing. The
// window takes the word of a board's page alone, so WebView2's error page,
// which has no way to answer, counts as no board. A board's page answers once
// its root holds what it drew and, while it is in sight, once it has drawn a
// frame: a page whose script never ran or whose app came down holds nothing,
// and one whose WebView2 cannot draw, such as while its GPU process is held at
// its start, draws no frame though it stays loaded and in sight.
const shownScript = `(() => {
  if (!document.getElementById("root")?.firstElementChild) return;
  const answer = () => window.chrome?.webview?.postMessage("` + shownMessage + `");
  if (document.visibilityState === "hidden") answer(); else requestAnimationFrame(answer);
})();`

// PageStep is what the window does with its page after a look at the board.
type PageStep int

const (
	// Leave the page as it is.
	Leave PageStep = iota
	// Ask the page whether it is a board's and drawing, with shownScript.
	Ask
	// Load the board.
	Load
	// Repaint: end the WebView2 GPU process, which WebView2 starts again.
	Repaint
)

// silentLooks is how many looks in a row a shown window's page may leave
// unanswered, about ten seconds, before the window mends it: long enough for
// a page busy drawing, short of leaving him a black window.
const silentLooks = 3

// retryLooks is how many looks a page that a load and a new GPU process did
// not mend stays silent before the window tries both again.
const retryLooks = 20

// mending is what the window does, in turn, for a page that stays silent
// while the window is shown: load the board, which mends a page whose script
// or app is gone, then end the GPU process, which mends one whose WebView2
// GPU process hangs.
var mending = []PageStep{Load, Repaint}

// Page decides when the window loads its board again. A window that looked
// for its board while the supervisor was down sits on WebView2's error page,
// whose own retries grow to half an hour apart, and nothing tells the window
// that a load failed. So when the board answers again after not answering,
// the window asks its page whether it is a board's, and loads the board when
// no board's page has said so by the next look. A board's page that answers
// is left as it is: it reconnects by itself, and a load would take an answer
// from under the Overlord's hands. A page that never answers is loaded once
// each time the board comes back, never in a loop. While the window is shown
// it asks at every look, since a page can stop drawing with nothing else
// changing, and mends a page that stays silent, saying so in the window.
type Page struct {
	mu sync.Mutex
	// down: the board did not answer at a look.
	down bool
	// asked: the page was asked whether it is a board's.
	asked bool
	// shown: a board's page answered since it was asked.
	shown bool
	// silent: looks in a row at which the shown window's page had not
	// answered.
	silent int
	// tries: the mending steps taken since the page last answered.
	tries int
	// stalled: the window is mending a page that draws nothing.
	stalled bool
}

// Look takes whether the board answers now and whether the window is shown,
// and returns what the window does with its page.
func (p *Page) Look(answers, seen bool) PageStep {
	p.mu.Lock()
	defer p.mu.Unlock()
	unanswered := p.asked && !p.shown
	if p.asked && p.shown {
		p.down, p.silent, p.tries, p.stalled = false, 0, 0, false
	}
	p.asked = false
	if !seen {
		p.stalled = false
	}
	switch {
	case !answers:
		p.down = true
		return Leave
	case unanswered && p.down:
		p.down = false
		return Load
	case unanswered && seen:
		p.silent++
	}
	if step, ok := p.mend(); ok {
		return step
	}
	if p.down || seen {
		p.asked, p.shown = true, false
		return Ask
	}
	return Leave
}

// mend returns the next mending step once the page has been silent long
// enough for it.
func (p *Page) mend() (PageStep, bool) {
	switch {
	case p.silent >= silentLooks && p.tries < len(mending):
	case p.silent >= retryLooks:
		p.tries = 0
	default:
		return Leave, false
	}
	step := mending[p.tries]
	p.tries++
	p.silent, p.stalled = 0, true
	return step, true
}

// Stalled reports whether the window is mending a page that draws nothing,
// which it says in place of the page.
func (p *Page) Stalled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stalled
}

// Shown notes that a board's page said it is there.
func (p *Page) Shown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shown = true
}

// Watcher tells which items waiting on the Overlord are new since its last
// look. Its first look only learns what already waits, which the board shows.
type Watcher struct {
	Client *http.Client
	// Instance is the supervisor's instance, as the last snapshot read named
	// it.
	Instance string
	seen     map[string]bool
}

// New reads the board's snapshot and returns the items that were not
// waiting at the last look, and whether the board answered. A board that
// cannot be read shows nothing new.
func (w *Watcher) New(board string) ([]waiting.Item, bool) {
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Get(strings.TrimSuffix(board, "/") + "/api/snapshot")
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, false
	}
	instance, items, err := waiting.Items(data)
	if err != nil {
		return nil, false
	}
	w.Instance = instance
	first := w.seen == nil
	current := map[string]bool{}
	var fresh []waiting.Item
	for _, item := range items {
		current[item.ID] = true
		if !first && !w.seen[item.ID] && !item.IsQuiet {
			fresh = append(fresh, item)
		}
	}
	w.seen = current
	return fresh, true
}

// runKey is where Windows keeps the programs it starts at the user's login;
// tests point it at a key of their own.
var runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// runValue names Code Goblins' entry under runKey.
const runValue = "CodeGoblins"

// launcherVariable is how the goblins that starts the window names itself to
// it. A flag would stop a window older than its goblins from starting at all.
const launcherVariable = "CODE_GOBLINS_LAUNCHER"

// loginCommand is what Windows runs at login. A known launcher lets the
// window locate its board out of sight at each sign-in. Without a launcher,
// the entry must keep the explicitly supplied board and state folder.
func loginCommand(launcher, window, board, stateDir string) string {
	if launcher != "" {
		return `"` + window + `" --background`
	}
	return `"` + window + `" --board ` + board + ` --state "` + stateDir + `" --background`
}

// adoptEarlierLogin makes the login entry an earlier window in this folder
// wrote, for a window with a known launcher, this window's: that entry ran the
// goblins beside window with --window --background, in a terminal at every
// login, and becomes login, so the tray shows Start at login as on and turning
// it off removes the entry. It reports whether it rewrote the entry. Any other
// entry is left as it is, and a window without a launcher adopts none.
func adoptEarlierLogin(launcher, window, login string) (bool, error) {
	earlier := `"` + filepath.Join(filepath.Dir(window), goblinsName) + `" --window --background`
	if launcher == "" || !StartsAtLogin(earlier) {
		return false, nil
	}
	if err := SetStartAtLogin(login, true); err != nil {
		return false, err
	}
	return true, nil
}

// StartsAtLogin reports whether Windows runs command at login.
func StartsAtLogin(command string) bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(runValue)
	return err == nil && strings.EqualFold(value, command)
}

// keepStartAtLogin turns Start at login on or off from the tray and keeps the
// choice in the home at stateDir, as the board's switch does, so an install
// keeps it.
func keepStartAtLogin(command, stateDir string, on bool) error {
	choice := install.StartAtLoginOff
	if on {
		choice = install.StartAtLoginOn
	}
	if err := install.WriteStartAtLogin(stateDir, choice); err != nil {
		return err
	}
	return SetStartAtLogin(command, on)
}

// SetStartAtLogin adds the login entry running command, or removes the entry.
func SetStartAtLogin(command string, on bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if on {
		return key.SetStringValue(runValue, command)
	}
	if err := key.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
