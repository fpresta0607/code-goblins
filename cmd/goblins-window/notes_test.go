package main

import (
	"strings"
	"testing"
	"time"
)

// The board's page hands the window each notification it raises. The window
// raises it for a board's own page, given an id and a title, and for nothing
// else.
func TestOnlyABoardsNotificationIsRaised(t *testing.T) {
	const board = "http://127.0.0.1:4310/"
	for name, test := range map[string]struct {
		message, sender string
		want            Note
	}{
		"a question from the board":    {`notify:{"id":"question:q1","title":"CFO","body":"The CFO asks: Which plan?"}`, board, Note{"question:q1", "CFO", "The CFO asks: Which plan?"}},
		"a note with no body":          {`notify:{"id":"task:demo","title":"Build review panel"}`, board, Note{"task:demo", "Build review panel", ""}},
		"another site's page":          {`notify:{"id":"x","title":"Prize"}`, "https://example.com/", Note{}},
		"a message that asks nothing":  {"shown", board, Note{}},
		"a note with no id":            {`notify:{"title":"CFO"}`, board, Note{}},
		"a note with no title":         {`notify:{"id":"question:q1"}`, board, Note{}},
		"a note with an id of no end":  {`notify:{"id":"` + strings.Repeat("a", 257) + `","title":"CFO"}`, board, Note{}},
		"a note that is not an object": {`notify:["question:q1","CFO"]`, board, Note{}},
		"a note that does not parse":   {`notify:{"id":`, board, Note{}},
		"a note with nothing after it": {"notify:", board, Note{}},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := noteToRaise(test.message, test.sender)

			if got != test.want || ok != (test.want != Note{}) {
				t.Errorf("noteToRaise(%q, %q) = %+v, %v; want %+v, %v", test.message, test.sender, got, ok, test.want, test.want != Note{})
			}
		})
	}
}

// A long title or body is cut to what Windows is handed, by characters, never
// through the middle of one.
func TestALongNotificationIsCutByCharacters(t *testing.T) {
	title, body := strings.Repeat("é", 250), strings.Repeat("好", 1200)

	note, ok := noteToRaise(`notify:{"id":"n","title":"`+title+`","body":"`+body+`"}`, "http://127.0.0.1:4310/")

	if !ok || note.Title != strings.Repeat("é", 200) || note.Body != strings.Repeat("好", 1000) {
		t.Errorf("the note is cut to %d and %d characters (ok %v), want 200 and 1000 whole characters", len([]rune(note.Title)), len([]rune(note.Body)), ok)
	}
}

// What newly waits on the Overlord reaches the window twice, from its own look
// and from the board's page. The second copy is dropped, another event is
// not, and the same id much later is an event of its own.
func TestANotificationIsRaisedOnceForEachEvent(t *testing.T) {
	raised := &Raised{}
	start := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)

	first := raised.First("question:q1", start)
	copied := raised.First("question:q1", start.Add(90*time.Second))
	other := raised.First("task:demo:g1:done:pull/12", start.Add(91*time.Second))
	later := raised.First("question:q1", start.Add(repeatWindow))

	if !first || copied || !other || !later {
		t.Errorf("first %v, its copy %v, another event %v, the same id after the repeat window %v; want true, false, true, true", first, copied, other, later)
	}
}

// The id of a clicked notification reaches the board's page as a script that
// cannot be broken out of.
func TestAClickedNotificationsIdIsQuotedForThePage(t *testing.T) {
	for id, want := range map[string]string{
		"question:q1":      `window.codeGoblinsNoteClicked?.("question:q1");`,
		`"); alert(1); ("`: `window.codeGoblinsNoteClicked?.("\"); alert(1); (\"");`,
	} {
		if got := noteClicked(id); got != want {
			t.Errorf("noteClicked(%q) = %s, want %s", id, got, want)
		}
	}
	if got := noteClicked("</script><script>x()"); strings.ContainsAny(got, "<>") {
		t.Errorf("noteClicked leaves markup in its script: %s", got)
	}
}
