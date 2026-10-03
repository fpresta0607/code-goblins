package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
)

// sight is what the window knows of where it is on the screen.
type sight interface {
	IsVisible() bool
	IsMinimised() bool
}

// away reports whether the Overlord cannot see the board: the window is
// hidden to its tray or minimized. A window on the screen is not away, in
// front or not. One wholly behind other windows is not told apart from one he
// sees: Windows has no such answer, and working it out from the windows above
// this one counts an overlay that draws nothing as covering it.
func away(window sight) bool {
	return !window.IsVisible() || window.IsMinimised()
}

// announceLimit is how long a key the supervisor records may be, in the units
// the board's page counts.
const announceLimit = 160

// announceKey is the name the supervisor records an item's alert under, as
// the board's page forms it, so the page and the window claim the same event.
func announceKey(id string) string {
	units := utf16.Encode([]rune("alert:" + id))
	if len(units) > announceLimit {
		units = units[:announceLimit]
		// The cut never leaves half of a character.
		if last := units[len(units)-1]; last >= 0xd800 && last <= 0xdbff {
			units = units[:len(units)-1]
		}
	}
	return string(utf16.Decode(units))
}

// claim asks the board's supervisor which of keys this window may announce,
// as the board's page asks before it alerts: the supervisor hands each key to
// the first that asks, and none while AFK mode is on. The request names the
// board as its origin and carries the supervisor's instance, as the page's
// does, which the supervisor checks on whatever changes its record.
func claim(client *http.Client, board, instance string, keys []string) ([]string, error) {
	body, err := json.Marshal(struct {
		Keys []string `json:"keys"`
	}{keys})
	if err != nil {
		return nil, err
	}
	origin := strings.TrimSuffix(board, "/")
	request, err := http.NewRequest(http.MethodPost, origin+"/api/announce", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("X-CFO-Token", instance)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the board answered %s", response.Status)
	}
	var answer struct {
		Claimed []string `json:"claimed"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&answer); err != nil {
		return nil, err
	}
	return answer.Claimed, nil
}

// hiddenWait is how long a tab of the board that he cannot see waits before
// it asks the supervisor, so a tab he is looking at claims first and shows
// the alert where he sees it. The window asks only while he cannot see it,
// and waits as long.
const hiddenWait = 1500 * time.Millisecond

// claimWait is how long the window waits for the supervisor's answer. The
// board's page waits however long it takes; a supervisor that has not
// answered by then counts as one that cannot be asked.
const claimWait = time.Minute

// Notifier raises the window's Windows notifications. One is raised only
// while the Overlord cannot see the board, never while the window is on the
// screen, where the board's own alert is the signal. What the window finds
// waiting by itself it claims from the supervisor first, as the board's page
// does, so an item is announced once, by whichever asks first, and not at all
// while AFK mode is on.
type Notifier struct {
	// Away reports whether the Overlord cannot see the board.
	Away func() bool
	// Client asks the supervisor what the window may announce.
	Client *http.Client
	// Send raises one Windows notification.
	Send func(Note) error
	// Wait lets that much time pass.
	Wait   func(time.Duration)
	raised Raised
}

// FromPage raises a notification the board's page asked for. The page
// claimed its event before it asked.
func (n *Notifier) FromPage(note Note) {
	if n.Away() {
		n.raise(note)
	}
}

// FromBoard raises a notification for each of items, which newly wait on the
// Overlord by the window's own look at the board, that the supervisor hands
// this window. A supervisor that cannot be asked lets the window announce
// them all, as it lets the board's page: one older than the claims keeps none
// and has no AFK mode, and one in AFK mode answers, with nothing.
func (n *Notifier) FromBoard(board, instance string, items []Item) {
	if len(items) == 0 || !n.Away() {
		return
	}
	n.Wait(hiddenWait)
	// Brought back meanwhile, he sees the board's own alert.
	if !n.Away() {
		return
	}
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = announceKey(item.ID)
	}
	claimed, err := claim(n.Client, board, instance, keys)
	if err != nil {
		log.Printf("ask the board what to announce: %v", err)
		claimed = keys
	}
	for i, item := range items {
		if slices.Contains(claimed, keys[i]) {
			n.raise(Note{ID: item.ID, Title: "Waiting on you", Body: item.Text})
		}
	}
}

// raise sends note unless a notification of its id was raised lately.
func (n *Notifier) raise(note Note) {
	if !n.raised.First(note.ID, time.Now()) {
		return
	}
	if err := n.Send(note); err != nil {
		log.Printf("notify of %s: %v", note.ID, err)
	}
}
