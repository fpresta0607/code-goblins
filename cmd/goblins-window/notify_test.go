package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// place is where a test puts the window.
type place struct{ isVisible, isMinimised bool }

func (p place) IsVisible() bool   { return p.isVisible }
func (p place) IsMinimised() bool { return p.isMinimised }

var (
	onTheScreen = place{isVisible: true}
	minimized   = place{isVisible: true, isMinimised: true}
	inTheTray   = place{}
)

// The window claims an item's alert under the name the board's page gives
// it, cut as the page cuts it, never through the middle of a character.
func TestAnItemIsClaimedUnderThePagesNameForIt(t *testing.T) {
	long := strings.Repeat("a", 153) + "😀"

	short, cut := announceKey("question:q1"), announceKey(long)

	if short != "alert:question:q1" {
		t.Errorf("announceKey = %q, want alert:question:q1", short)
	}
	if units := utf16.Encode([]rune(cut)); len(units) != 159 || cut != "alert:"+strings.Repeat("a", 153) {
		t.Errorf("a key cut inside a character is %d units long, %q; want 159 units and no half character", len(units), cut)
	}
}

// standIn is a supervisor's /api/announce: it hands each key to the first
// request that asks and hands out nothing in AFK mode, as the supervisor
// does, or refuses with a status of the test's choice, and records what it
// was asked.
type standIn struct {
	*httptest.Server
	mu      sync.Mutex
	asked   [][]string
	taken   map[string]bool
	isAFK   bool
	refusal int
}

const standInInstance = "i-1"

func newStandIn(t *testing.T) *standIn {
	t.Helper()
	board := &standIn{taken: map[string]bool{}}
	board.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		board.mu.Lock()
		defer board.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/api/announce" || r.Header.Get("Origin") != board.URL || r.Header.Get("X-CFO-Token") != standInInstance {
			http.Error(w, "Refresh the board before submitting an action", http.StatusForbidden)
			return
		}
		var input struct {
			Keys []string `json:"keys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		board.asked = append(board.asked, input.Keys)
		if board.refusal != 0 {
			http.Error(w, http.StatusText(board.refusal), board.refusal)
			return
		}
		claimed := []string{}
		for _, key := range input.Keys {
			if !board.taken[key] && !board.isAFK {
				claimed = append(claimed, key)
			}
			board.taken[key] = true
		}
		_ = json.NewEncoder(w).Encode(map[string][]string{"claimed": claimed})
	}))
	t.Cleanup(board.Close)
	return board
}

// raising is a Notifier for a window at a place of the test's choice, with
// what it raised and how long it waited.
type raising struct {
	*Notifier
	at     place
	sent   []Note
	waited []time.Duration
}

func notifierAt(at place, board *standIn) *raising {
	r := &raising{at: at}
	r.Notifier = &Notifier{
		Away:   func() bool { return away(r.at) },
		Client: board.Client(),
		Send:   func(note Note) error { r.sent = append(r.sent, note); return nil },
		Wait:   func(wait time.Duration) { r.waited = append(r.waited, wait) },
	}
	return r
}

var fresh = []Item{{"question:q1", "Which plan?"}, {"run:u1", "Install the tool"}}

// A Windows notification is for when the Overlord cannot see the board. A
// new item reaches the window twice, by its own look at the board and from
// the board's page: with the window on the screen neither raises one, since
// the board's own alert is the signal there, and nothing is claimed, so the
// page can; minimized or in the tray the two raise exactly one.
func TestANewItemIsNotifiedOnlyWhileTheWindowIsMinimizedOrInTheTray(t *testing.T) {
	for name, test := range map[string]struct {
		at        place
		wantNotes int
		wantAsks  int
	}{
		"on the screen": {onTheScreen, 0, 0},
		"minimized":     {minimized, 1, 1},
		"in the tray":   {inTheTray, 1, 1},
	} {
		t.Run(name, func(t *testing.T) {
			board := newStandIn(t)
			notifier := notifierAt(test.at, board)

			notifier.FromBoard(board.URL, standInInstance, fresh[:1])
			notifier.FromPage(Note{ID: "question:q1", Title: "CFO", Body: "The CFO asks: Which plan?"})

			if len(notifier.sent) != test.wantNotes || len(board.asked) != test.wantAsks {
				t.Errorf("raised %+v after asking the supervisor %v; want %d raised after %d requests", notifier.sent, board.asked, test.wantNotes, test.wantAsks)
			}
		})
	}
}

// The Overlord, 2026-10-02: "The Command Center should only give me questions
// that the CFO has for the Overlord, for me." A question the CFO asks him and
// what the CFO passes up name no task, and a window in its tray notifies
// them. A goblin's question is the CFO's to answer, beside its open page or
// once the CFO answered it: the window claims nothing for it, so nothing is
// raised.
func TestOnlyAQuestionTheCFOAsksIsNotifiedFromTheTray(t *testing.T) {
	for name, test := range map[string]struct {
		question string
		want     []Note
	}{
		"the CFO asks him":                     {`{"id":"pick-a-store","text":"Which store?","status":"pending"}`, []Note{{"question:pick-a-store", "Waiting on you", "Which store?"}}},
		"the CFO passes a goblin's ask up":     {`{"id":"from-billing","text":"billing asks: which store?","status":"pending"}`, []Note{{"question:from-billing", "Waiting on you", "billing asks: which store?"}}},
		"a goblin asks the CFO":                {`{"id":"notify-billing-7","text":"Which port?","status":"pending","task":"billing","generation":"g1","seq":7}`, nil},
		"a goblin asks beside its open page":   {`{"id":"notify-billing-8","text":"Which port?","status":"pending","task":"billing","generation":"g1","seq":8,"page":"plan-billing"}`, nil},
		"a goblin's question the CFO answered": {`{"id":"notify-billing-6","text":"Which port?","status":"succeeded","task":"billing","answered_by":"cfo"}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			questions := ""
			snapshots := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				_, _ = w.Write([]byte(`{"instance":"` + standInInstance + `","questions":[` + questions + `]}`))
			}))
			defer snapshots.Close()
			board := newStandIn(t)
			notifier := notifierAt(inTheTray, board)
			watcher := &Watcher{}
			watcher.New(snapshots.URL)
			mu.Lock()
			questions = test.question
			mu.Unlock()

			fresh, _ := watcher.New(snapshots.URL)
			notifier.FromBoard(board.URL, watcher.Instance, fresh)

			if !slices.Equal(notifier.sent, test.want) || len(board.asked) != len(test.want) {
				t.Errorf("raised %+v after asking the supervisor %v; want %+v raised after %d requests", notifier.sent, board.asked, test.want, len(test.want))
			}
		})
	}
}

// What the window finds waiting it claims under the page's name for it,
// after the wait of a tab he cannot see, and raises what it is handed; what
// the board's page asks for is raised as it is.
func TestAWindowThatIsAwayClaimsWhatItFindsAsAHiddenTabDoes(t *testing.T) {
	board := newStandIn(t)
	notifier := notifierAt(inTheTray, board)
	askedByTheWait := -1
	notifier.Wait = func(wait time.Duration) {
		notifier.waited = append(notifier.waited, wait)
		askedByTheWait = len(board.asked)
	}

	notifier.FromBoard(board.URL, standInInstance, fresh)
	notifier.FromPage(Note{ID: "task:demo:g1:done:pull/12", Title: "demo", Body: "demo finished"})

	wantAsked := [][]string{{"alert:question:q1", "alert:run:u1"}}
	wantSent := []Note{{"question:q1", "Waiting on you", "Which plan?"}, {"run:u1", "Waiting on you", "Install the tool"}, {"task:demo:g1:done:pull/12", "demo", "demo finished"}}
	if !slices.EqualFunc(board.asked, wantAsked, slices.Equal[[]string]) || !slices.Equal(notifier.sent, wantSent) {
		t.Errorf("asked %v, raised %+v; want asked %v and raised %+v", board.asked, notifier.sent, wantAsked, wantSent)
	}
	if !slices.Equal(notifier.waited, []time.Duration{1500 * time.Millisecond}) || askedByTheWait != 0 {
		t.Errorf("waited %v, with %d requests made by then; want one wait of 1.5s before the first request", notifier.waited, askedByTheWait)
	}
}

// A window he brings back while it waits to ask leaves the item to the
// board's page, which shows its alert where he now looks.
func TestAWindowBroughtBackWhileItWaitsClaimsNothing(t *testing.T) {
	board := newStandIn(t)
	notifier := notifierAt(minimized, board)
	notifier.Wait = func(time.Duration) { notifier.at = onTheScreen }

	notifier.FromBoard(board.URL, standInInstance, fresh)

	if len(notifier.sent) != 0 || len(board.asked) != 0 {
		t.Errorf("raised %+v after asking the supervisor %v; want nothing raised or asked", notifier.sent, board.asked)
	}
}

// A supervisor that hands the window nothing, as one in AFK mode does, gets
// no notification raised; an item another viewer announced first is not
// raised either, and the rest is.
func TestWhatTheSupervisorDoesNotHandTheWindowIsNotRaised(t *testing.T) {
	for name, test := range map[string]struct {
		isAFK bool
		taken []string
		want  []Note
	}{
		"AFK mode is on":                    {true, nil, nil},
		"another viewer announced one item": {false, []string{"alert:question:q1"}, []Note{{"run:u1", "Waiting on you", "Install the tool"}}},
	} {
		t.Run(name, func(t *testing.T) {
			board := newStandIn(t)
			board.isAFK = test.isAFK
			for _, key := range test.taken {
				board.taken[key] = true
			}
			notifier := notifierAt(inTheTray, board)

			notifier.FromBoard(board.URL, standInInstance, fresh)

			if !slices.Equal(notifier.sent, test.want) || len(board.asked) != 1 {
				t.Errorf("raised %+v after %d requests; want %+v raised after one request", notifier.sent, len(board.asked), test.want)
			}
		})
	}
}

// A supervisor that cannot be asked lets the window announce, as it lets the
// board's page: one older than the claims has no such address and no AFK
// mode, and one in AFK mode answers.
func TestASupervisorThatCannotBeAskedLetsTheWindowAnnounce(t *testing.T) {
	for name, test := range map[string]struct {
		instance string
		arrange  func(*standIn)
	}{
		"it is older than the claims":          {standInInstance, func(board *standIn) { board.refusal = http.StatusNotFound }},
		"it fails":                             {standInInstance, func(board *standIn) { board.refusal = http.StatusInternalServerError }},
		"it restarted since the window's look": {"the instance before", func(*standIn) {}},
		"it is gone":                           {standInInstance, func(board *standIn) { board.Close() }},
	} {
		t.Run(name, func(t *testing.T) {
			board := newStandIn(t)
			notifier := notifierAt(inTheTray, board)
			test.arrange(board)

			notifier.FromBoard(board.URL, test.instance, fresh)

			want := []Note{{"question:q1", "Waiting on you", "Which plan?"}, {"run:u1", "Waiting on you", "Install the tool"}}
			if !slices.Equal(notifier.sent, want) {
				t.Errorf("raised %+v, want both items raised", notifier.sent)
			}
		})
	}
}
