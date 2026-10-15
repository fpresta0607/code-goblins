package afk

import (
	"bytes"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// recordedReturn is a stretch recorded in testdata, with the question in his
// History its decision changes and a goblin's before and after pictures.
func recordedReturn(t *testing.T) Return {
	t.Helper()
	data, err := os.ReadFile("testdata/return-stretch.json")
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return Return{
		Report: report,
		Questions: map[string]Question{
			"question:afk-left-20261015T050000.000000000Z": {ID: "afk-left-20261015T050000.000000000Z", Text: "Delete the idle cache database?", Options: []string{"Delete in a week", "Delete now", "Keep it longer"}, Answer: "Delete in a week"},
			"question:notify-garden-photos-7":              {ID: "notify-garden-photos-7", Text: "Should the gallery show photos from shared albums?", Options: []string{"Show them", "Hide them"}, Answer: "Show them"},
		},
		Pictures: []Picture{{Task: "garden-photos", Caption: "Before", File: "pictures/1.png"}, {Task: "garden-photos", Caption: "After", File: "pictures/2.png"}},
	}
}

// returnPage is the page r makes, and its words as he reads them: what its
// markup says, without its styles and script.
func returnPage(t *testing.T, r Return) (page, words string) {
	t.Helper()
	var out bytes.Buffer
	if err := RenderPage(&out, r); err != nil {
		t.Fatal(err)
	}
	page = out.String()
	words = regexp.MustCompile(`(?s)<style>.*?</style>|<script>.*?</script>|<[^>]*>`).ReplaceAllString(page, " ")
	return page, html.UnescapeString(strings.Join(strings.Fields(words), " "))
}

// The Overlord, 2026-10-15: "simplify into more of a scrawl based overview
// on return ... with walkthrough of what changed including what we currently
// would have in report". The page reads top down: how long he was away and
// whether anything needs him, what changed by project in the CFO's and the
// goblins' words with each pull request small beside it, the decisions made
// for him, what is under way and next, and what was spent.
func TestTheReturnPageWalksHimThroughTheStretch(t *testing.T) {
	// Arrange
	r := recordedReturn(t)

	// Act
	page, words := returnPage(t, r)

	// Assert
	for _, want := range []string{
		"Welcome back Away 2 hours 5 minutes",
		"Nothing needs you",
		"garden-app 1 The garden gallery shows every photo",
		"weather-api 1 Forecasts answer from the cache",
		"Live on both servers",
		"Before and after",
		"Decided for you",
		"Delete the idle cache database? Delete in a week",
		"Should the gallery show photos from shared albums? Show them",
		"Still at work Share a garden with a friend Ivy",
		"Next Frost alerts for the garden Queued",
		"Claude week 51% +16",
		"Codex week 4% reset",
	} {
		if !strings.Contains(words, want) {
			t.Errorf("the page reads %q, want it to say %q", words, want)
		}
	}
	for _, want := range []string{
		`href="https://github.com/example/garden-app/pull/12"`,
		`href="https://github.com/example/garden-app/pull/13"`,
		`<img src="pictures/1.png" alt="Before">`,
		`title="Claude week 35 to 51 percent"`,
		"Gallery loads all 300 photos",
		"Thumbnails cached on both servers, live",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Count(words, "The garden gallery shows every photo") != 1 {
		t.Errorf("the page reads %q, want the garden goblin's two pull requests told as one change", words)
	}
	if strings.Contains(words, "merge train") || strings.Contains(words, "pull/14") {
		t.Errorf("the page reads %q, want no merge train's words in the walkthrough itself", words)
	}
}

// Everything the report held is still on the page, a tap away: who switched
// it, an item that waited on him and was settled, a goblin paused at a floor,
// a step the CFO settled, a line it struck and what could not be read.
func TestTheReturnPageKeepsEverythingTheReportHeld(t *testing.T) {
	// Arrange
	r := recordedReturn(t)

	// Act
	page, words := returnPage(t, r)

	// Assert
	for _, want := range []string{
		"Turned on from your board",
		"Turned off from your board",
		"Which shop plan next month? Settled",
		"weather-cache: at the memory floor Paused",
		"Turn on two-factor sign-in for the shop account Settled by the CFO",
		"Restarted the stuck queue worker Struck by the CFO",
		"GitHub did not say whether these pull requests merged",
	} {
		if !strings.Contains(words, want) {
			t.Errorf("the page reads %q, want it to say %q", words, want)
		}
	}
	for _, want := range []string{"queue length fell from 40 to 0", "logged twice by mistake", "resumed at 5.2 GB free", "health check green on both servers", "No reads since 04:00"} {
		if !strings.Contains(html.UnescapeString(page), want) {
			t.Errorf("the page lacks %q behind its row", want)
		}
	}
}

// Each decision made for him has Change right on it: the question in his
// History it changes, its choices and the answer it has now, which the page
// sends as History's change.
func TestEachDecisionOnTheReturnPageCarriesItsChange(t *testing.T) {
	// Arrange
	r := recordedReturn(t)

	// Act
	page, _ := returnPage(t, r)

	// Assert
	page = html.UnescapeString(page)
	for _, want := range []string{
		`data-question="afk-left-20261015T050000.000000000Z" data-options="["Delete in a week","Delete now","Keep it longer"]" data-answer="Delete in a week"`,
		`data-question="notify-garden-photos-7" data-options="["Show them","Hide them"]" data-answer="Show them"`,
		`tag: "answer_change", text: row.dataset.question`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if got := strings.Count(page, `aria-label="Change answer"`); got != 2 {
		t.Errorf("the page has %d Change buttons, want one on each of its two decisions", got)
	}
}

// What needs him leads the page in place of the all clear, one row each: a
// step only he can take and each item still waiting in his Command Center.
func TestTheReturnPageLeadsWithWhatNeedsHim(t *testing.T) {
	// Arrange
	r := recordedReturn(t)
	r.Report.Held = append(r.Report.Held, Held{Item: "question:pick-a-name", What: "Pick the app's name", Waiting: true, Now: "still waiting on you"})
	r.Report.Decisions = append(r.Report.Decisions, Entry{Kind: KindHis, What: "Sign in to the shop once", Evidence: "only his sign-in works"})

	// Act
	_, words := returnPage(t, r)

	// Assert
	for _, want := range []string{"2 things need you", "Sign in to the shop once Only you can do this", "Pick the app's name In your Command Center"} {
		if !strings.Contains(words, want) {
			t.Errorf("the page reads %q, want it to say %q", words, want)
		}
	}
	if strings.Contains(words, "Nothing needs you") {
		t.Errorf("the page reads %q, want no all clear while something needs him", words)
	}
}

// He reads no semicolons and no em dashes, whatever the stretch held.
func TestTheReturnPageReadsWithoutSemicolonsOrDashes(t *testing.T) {
	_, words := returnPage(t, recordedReturn(t))
	if i := slices.IndexFunc([]rune(words), func(r rune) bool { return r == ';' || r == '—' }); i >= 0 {
		t.Errorf("the page reads %q, with a semicolon or an em dash", words)
	}
}
