package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/axi"
)

// answeredWhileAway is a decision the CFO answered in the Overlord's place
// while AFK mode was on, settled in his History as AFK mode settles one when
// it turns off.
func answeredWhileAway(t *testing.T, store *Store, identity string) Question {
	t.Helper()
	at := time.Now().UTC().Add(-time.Hour)
	q := Question{ID: "afk-left-20261015T051200.000000000Z", Identity: identity, Text: "Delete the idle cache database?", Options: []string{"Delete in a week", "Delete now", "Keep it longer"}, CreatedAt: at, Status: "succeeded", Message: "Answered by the CFO."}
	q.Answer, q.AnswerKind, q.AnsweredOption, q.AnsweredBy, q.AnsweredAt, q.AnsweredAway = "Delete in a week", "option", "Delete in a week", "cfo", &at, true
	if err := store.settleQuestion(q); err != nil {
		t.Fatal(err)
	}
	return q
}

// returnItem publishes an item of the CFO's own with its page at page, as the
// return page is published.
func returnItem(t *testing.T, store *Store, identity, page string) Review {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := Review{ID: "afk-return-test-stretch", Identity: identity, Title: "Back from AFK", Lavish: pageLink, LavishPage: page, State: "open", CreatedAt: now, UpdatedAt: now}
	if err := store.acceptReview(r); err != nil {
		t.Fatal(err)
	}
	return r
}

// The Overlord, 2026-10-15: each decision on the page he comes back to has
// "the control to change it right there (the same change path History uses
// today)". His Change on the return page reaches whoever acted on the answer
// once, as a change, exactly as History's does, the page tells him so, and
// the page stays open for the rest of what he reads there.
func TestHisChangeOnTheReturnPageIsHistorysChange(t *testing.T) {
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	// Arrange
	store, h := testStore(t)
	_, identity, cfo, connection := primaryFixture(t, store)
	q := answeredWhileAway(t, store, identity)
	page := filepath.Join(h.State, afkReturnDir, "afk-20261015T043416.901Z", "index.html")
	returnItem(t, store, identity, page)
	var replies []string
	polls := []axi.PagePoll{{Status: "feedback", Changes: []axi.PageChange{{Question: q.ID, Answer: "Keep it longer"}}}, {Status: "ended", Ended: true, EndedBy: "agent"}}
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(_ context.Context, _, reply string, _ time.Duration) (axi.PagePoll, error) {
		replies = append(replies, reply)
		poll := polls[0]
		polls = polls[1:]
		return poll, nil
	}}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()
	runAll(t, store, s)

	// Assert
	typed := cfo.waitForLines(t, 1)
	told := changeLines(typed)
	if len(told) != 1 || !strings.Contains(told[0], q.ID) {
		t.Fatalf("the CFO received %q, want one change to its answer", typed)
	}
	wantTheChange(t, told[0], "Delete in a week", "Keep it longer")
	wantHisAnswer(t, store.Snapshot().Questions[0], "Keep it longer", "Delete in a week")
	if len(replies) != 2 || !strings.Contains(replies[1], `Changed to "Keep it longer"`) {
		t.Errorf("the page's replies = %q, want the poll after his change to tell him it changed", replies)
	}
	if got := store.Snapshot().Reviews[0]; got.AnsweredBy == "overlord" {
		t.Errorf("the return page's item = %+v, want his change taken as no answer to the page", got)
	}
}

// Only the supervisor's own return page changes his answers: a page a goblin
// or the CFO wrote anywhere else that sends the same change changes nothing.
func TestAChangeFromAnyOtherPageChangesNothing(t *testing.T) {
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	// Arrange
	store, h := testStore(t)
	_, identity, _, connection := primaryFixture(t, store)
	q := answeredWhileAway(t, store, identity)
	returnItem(t, store, identity, filepath.Join(h.Root, ".lavish", "afk-return", "index.html"))
	polls := []axi.PagePoll{{Status: "feedback", Changes: []axi.PageChange{{Question: q.ID, Answer: "Delete now"}}, Prompts: []string{"see the change"}}, {Status: "ended", Ended: true, EndedBy: "agent"}}
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		poll := polls[0]
		polls = polls[1:]
		return poll, nil
	}}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()
	runAll(t, store, s)

	// Assert
	if got := store.Snapshot().Questions[0]; got.Answer != "Delete in a week" || got.AnsweredBy != "cfo" {
		t.Errorf("question = %+v, want the CFO's answer kept", got)
	}
}

// openedAt is an OpenPage that opens every page at link and records each one.
func openedAt(link string, opened *[]string) func(context.Context, string) (string, error) {
	return func(_ context.Context, file string) (string, error) {
		*opened = append(*opened, file)
		return link, nil
	}
}

// The Overlord, 2026-10-15: one Scrawl page made when AFK mode turns off,
// opened from a single card in his Command Center, in place of the board's
// recap. His off writes the page from the report into the stretch's own
// folder, with the board's goblin faces beside it and each decision's Change
// naming the question in his History, opens it in Scrawl, and puts one card
// for it in his Command Center.
func TestTurningAFKOffMakesHisPageAndOneCardForIt(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	var opened []string
	s.Options.OpenPage = openedAt(pageLink, &opened)
	s.Options.BoardAssets = fstest.MapFS{"assets/goblin-personas.png": {Data: []byte("faces")}}
	left := afk.Entry{Kind: afk.KindLeft, What: "Delete the idle cache database?", Evidence: "No reads since 04:00", Diagnosis: "It bills while nothing reads it", Tried: "Watched its reads", Options: []string{"Delete in a week", "Delete now"}, Answer: "Delete in a week"}
	earlier := filepath.Join(h.State, afkReturnDir, "afk-20261014T010000.000Z")
	if err := os.MkdirAll(earlier, 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	logged := awayAndBack(t, s, h, left)
	s.pageWork.Wait()

	// Assert
	if _, err := os.Stat(earlier); !os.IsNotExist(err) {
		t.Errorf("an earlier stretch's page with no open card = %v, want it removed", err)
	}
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(h.State, afkReturnDir, switched.Session, "index.html")
	if len(opened) != 1 || opened[0] != page {
		t.Fatalf("opened %q, want only the stretch's page %s", opened, page)
	}
	written, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(written); !strings.Contains(text, "Delete the idle cache database?") || !strings.Contains(text, `data-question="`+afk.LeftQuestionID(logged[0])+`"`) {
		t.Errorf("the page lacks the decision with the question in his History it changes")
	}
	if faces, err := os.ReadFile(filepath.Join(filepath.Dir(page), "goblin-personas.png")); err != nil || string(faces) != "faces" {
		t.Errorf("the goblin faces beside the page = %q, %v, want the board's own", faces, err)
	}
	var cards []Review
	for _, item := range s.Store.Snapshot().Reviews {
		if item.LavishPage == page {
			cards = append(cards, item)
		}
	}
	if len(cards) != 1 || cards[0].State != "open" || cards[0].Task != "" || cards[0].Lavish != pageLink || !s.isReturnPage(cards[0]) {
		t.Fatalf("Command Center items for the page = %+v, want one open card of the CFO's that opens it", cards)
	}
	if view, err := s.afkView(s.Store.Snapshot()); err != nil || view.Page != pageLink {
		t.Errorf("the board's AFK view = %+v, %v, want it to open the page of the stretch from %s", view, err, pageLink)
	}
}

// A page Scrawl cannot open is no card: the CFO is told, so it tells him
// what the stretch held itself.
func TestAReturnPageThatCannotOpenTellsTheCFO(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	s.Options.OpenPage = func(context.Context, string) (string, error) { return "", errors.New("lavish-axi is not installed") }

	// Act
	awayAndBack(t, s, h)
	s.pageWork.Wait()

	// Assert
	if items := s.Store.Snapshot().Reviews; len(items) != 0 {
		t.Errorf("Command Center items = %+v, want none", items)
	}
	wakes := reviewWakes(t, h.State, "afk")
	if len(wakes) == 0 || !strings.Contains(wakes[len(wakes)-1].Detail, "could not be put in his Command Center (lavish-axi is not installed)") {
		t.Errorf("the CFO's afk wakes = %+v, want the last to say the page could not be put in his Command Center and why", wakes)
	}
}
