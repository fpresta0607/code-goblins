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

// shownScript asks the window's page to say it is there. The window takes
// the word of a board's page alone, so WebView2's error page, which has no
// way to answer, counts as no board.
const shownScript = `window.chrome?.webview?.postMessage("` + shownMessage + `");`

// PageStep is what the window does with its page after a look at the board.
type PageStep int

const (
	// Leave the page as it is.
	Leave PageStep = iota
	// Ask the page whether it is a board's, with shownScript.
	Ask
	// Load the board.
	Load
)

// Page decides when the window loads its board again. A window that looked
// for its board while the supervisor was down sits on WebView2's error page,
// whose own retries grow to half an hour apart, and nothing tells the window
// that a load failed. So when the board answers again after not answering,
// the window asks its page whether it is a board's, and loads the board when
// no board's page has said so by the next look. A board's page that is shown
// answers at once and is left as it is: it reconnects by itself, and a load
// would take an answer from under the Overlord's hands. A page that never
// answers is loaded once each time the board comes back, never in a loop.
type Page struct {
	mu sync.Mutex
	// down: the board did not answer at a look.
	down bool
	// asked: the page was asked whether it is a board's.
	asked bool
	// shown: a board's page answered since it was asked.
	shown bool
}

// Look takes whether the board answers now and returns what the window does
// with its page.
func (p *Page) Look(answers bool) PageStep {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case !answers:
		p.down, p.asked = true, false
		return Leave
	case !p.down:
		return Leave
	case !p.asked:
		p.asked, p.shown = true, false
		return Ask
	}
	p.down, p.asked = false, false
	if p.shown {
		return Leave
	}
	return Load
}

// Shown notes that a board's page said it is there.
func (p *Page) Shown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shown = true
}

// Item is one thing on the board that waits on the Overlord.
type Item struct {
	ID   string
	Text string
}

// waiting lists what a board snapshot holds for the Overlord, which is what
// the board's page alerts: a pending question that was not asked from an open
// review page, whose item it is; an open review item; a command ready or
// running that no credential card opened, since that terminal shows on its
// card; and an open request for credentials, by its project alone, so the
// credential names stay out of a Windows notification. Beside them it returns
// the keys of what is open on the board without being alerted on its own: a
// pending question asked from an open review page, which that page's card
// shows. It names the supervisor's instance too, which every request to the
// board that changes something carries.
func waiting(snapshot []byte) (string, []Item, []string, error) {
	var view struct {
		Instance  string `json:"instance"`
		Questions []struct {
			ID, Text, Status, Page string
		} `json:"questions"`
		Reviews []struct {
			ID, Title, State string
		} `json:"reviews"`
		Runs []struct {
			ID, Title, State  string
			CredentialRequest string `json:"credential_request"`
		} `json:"runs"`
		Credentials []struct {
			ID, Project, State string
		} `json:"credentials"`
	}
	if err := json.Unmarshal(snapshot, &view); err != nil {
		return "", nil, nil, err
	}
	var items []Item
	var folded []string
	for _, q := range view.Questions {
		if q.Status != "pending" {
			continue
		}
		if q.Page != "" {
			folded = append(folded, "question:"+q.ID)
			continue
		}
		lead, _, _ := strings.Cut(strings.TrimSpace(q.Text), "\n")
		items = append(items, Item{ID: "question:" + q.ID, Text: lead})
	}
	for _, r := range view.Reviews {
		if r.State == "open" {
			items = append(items, Item{ID: "review:" + r.ID, Text: r.Title})
		}
	}
	for _, r := range view.Runs {
		if (r.State == "ready" || r.State == "running") && r.CredentialRequest == "" {
			items = append(items, Item{ID: "run:" + r.ID, Text: r.Title})
		}
	}
	for _, c := range view.Credentials {
		if c.State == "open" {
			items = append(items, Item{ID: "credential:" + c.ID, Text: "Credentials wanted for " + c.Project})
		}
	}
	return view.Instance, items, folded, nil
}

// Watcher tells which items waiting on the Overlord are new since its last
// look. Its first look only learns what already waits, which the board shows.
// It remembers a question open inside its page's card as well, so that
// question is not new when its page closes and it becomes a card of its own.
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
func (w *Watcher) New(board string) ([]Item, bool) {
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
	instance, items, folded, err := waiting(data)
	if err != nil {
		return nil, false
	}
	w.Instance = instance
	first := w.seen == nil
	current := map[string]bool{}
	for _, key := range folded {
		current[key] = true
	}
	var fresh []Item
	for _, item := range items {
		current[item.ID] = true
		if !first && !w.seen[item.ID] {
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

// loginCommand is what Windows runs at login. For a window that goblins
// started it is the window alone, for the tray, which runs that goblins out of
// sight, so the supervisor starts first and no terminal shows. For a window
// started on its own it is the window on the same board, in the tray, where
// it follows the supervisor's record once one runs.
func loginCommand(launcher, window, board, stateDir string) string {
	if launcher != "" {
		return `"` + window + `" --background`
	}
	return `"` + window + `" --board ` + board + ` --state "` + stateDir + `" --background`
}

// adoptEarlierLogin makes the login entry an earlier window in this folder
// wrote, for a window that goblins started, this window's: that entry ran the
// goblins beside window with --window --background, in a terminal at every
// login, and becomes login, so the tray shows Start at login as on and turning
// it off removes the entry. It reports whether it rewrote the entry. Any other
// entry is left as it is, and a window started on its own adopts none.
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
