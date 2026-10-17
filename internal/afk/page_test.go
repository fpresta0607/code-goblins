package afk

import (
	"bytes"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"slices"
	"strconv"
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
			"question:afk-left-20261015T050000.000000000Z": {ID: "afk-left-20261015T050000.000000000Z", Text: "Delete the idle cache database?", Answer: "Delete in a week"},
			"question:notify-garden-photos-7":              {ID: "notify-garden-photos-7", Text: "Should the gallery show photos from shared albums?", Answer: "Show them"},
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
// History it changes, opened through the board's existing change card.
func TestEachDecisionOnTheReturnPageCarriesItsChange(t *testing.T) {
	// Arrange
	r := recordedReturn(t)

	// Act
	page, _ := returnPage(t, r)

	// Assert
	page = html.UnescapeString(page)
	for _, want := range []string{
		`data-item="change:afk-left-20261015T050000.000000000Z"`,
		`data-item="change:notify-garden-photos-7"`,
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
// step only he can take and each item still waiting in his Command Center,
// with who asks as the Command Center names them.
func TestTheReturnPageLeadsWithWhatNeedsHim(t *testing.T) {
	// Arrange
	r := recordedReturn(t)
	r.Report.Held = append(r.Report.Held, Held{Item: "question:pick-a-name", What: "Pick the app's name", Waiting: true, Now: "still waiting on you"})
	r.Report.Decisions = append(r.Report.Decisions, Entry{Kind: KindHis, What: "Sign in to the shop once", Evidence: "only his sign-in works"})

	// Act
	_, words := returnPage(t, r)

	// Assert
	for _, want := range []string{"Needs you 2", "The CFO Sign in to the shop once Only you can do this", "The CFO Pick the app's name"} {
		if !strings.Contains(words, want) {
			t.Errorf("the page reads %q, want it to say %q", words, want)
		}
	}
	if strings.Contains(words, "Nothing needs you") {
		t.Errorf("the page reads %q, want no all clear while something needs him", words)
	}
}

// The Overlord, 2026-10-17: "styling should be command center based ... Same
// type of controls." Each item still waiting is the Command Center's waiting
// row: the whole row carries its item, who asks is its goblin, or Code Goblins
// for its own release update, and its Answer wears the icon of its kind.
func TestEachItemThatNeedsHimIsTheCommandCentersWaitingRow(t *testing.T) {
	// Arrange
	r := recordedReturn(t)
	r.Report.Held = append(r.Report.Held,
		Held{Item: "question:pick-a-name", What: "Pick the app's name", Waiting: true},
		Held{Item: "run:restart-db", Task: "garden-share", What: "Restart the dev database", Waiting: true},
		Held{Item: "run:update-v1.4.0-0123456789abcdef", What: "Update Code Goblins from v1.3.0 to v1.4.0", Waiting: true})

	// Act
	page, words := returnPage(t, r)

	// Assert
	page = html.UnescapeString(page)
	for _, want := range []string{
		`<ul class="inbox-list waiting" aria-label="Needs you">`,
		`<li data-item="question:pick-a-name">`,
		`<li data-item="run:restart-db">`,
		`aria-label="Answer The CFO: Pick the app's name"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="` + icons["question"] + `"/>`,
		`aria-label="Answer Ivy: Restart the dev database"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="` + icons["play"] + `"/>`,
		`<li data-item="run:update-v1.4.0-0123456789abcdef"><span class="goblin-avatar persona-releases"></span>`,
		`aria-label="Answer Code Goblins: Update Code Goblins from v1.3.0 to v1.4.0"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4v11M7 10.5l5 5 5-5M5 19.5h14"/>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if !strings.Contains(words, "Ivy Restart the dev database") {
		t.Errorf("the page reads %q, want the goblin named as who asks", words)
	}
	if strings.Contains(page, `class="verdict needs"`) || strings.Contains(page, "Open in Command Center") {
		t.Errorf("the page keeps its own warning or its own link to the Command Center")
	}
}

// He reads no semicolons and no em dashes, whatever the stretch held.
func TestTheReturnPageReadsWithoutSemicolonsOrDashes(t *testing.T) {
	_, words := returnPage(t, recordedReturn(t))
	if i := slices.IndexFunc([]rune(words), func(r rune) bool { return r == ';' || r == '—' }); i >= 0 {
		t.Errorf("the page reads %q, with a semicolon or an em dash", words)
	}
}

// Pull request numbers stay together while the title and status wrap.
func TestAChangeKeepsItsPullRequestNumbersOnOneLine(t *testing.T) {
	// Arrange
	r := recordedReturn(t)
	for n := 20; n < 22; n++ {
		r.Report.Finished = append(r.Report.Finished, Finish{Task: "garden-photos", PR: "https://github.com/example/garden-app/pull/" + strconv.Itoa(n), Title: "The garden gallery shows every photo"})
	}

	// Act
	page, words := returnPage(t, r)

	// Assert
	if !strings.Contains(words, "#12 #13 +2") {
		t.Errorf("the page reads %q, want the change's first two numbers and +2", words)
	}
	if !strings.Contains(page, "https://github.com/example/garden-app/pull/21") {
		t.Errorf("the page lacks the last pull request behind its card")
	}
	for _, rule := range []string{".pulls { display: flex; flex-wrap: nowrap;", ".inbox-text strong { font-weight: 800; line-height: 1.3; overflow-wrap: anywhere; }", ".inbox-text small { display: flex; flex-wrap: wrap;"} {
		if !strings.Contains(page, rule) {
			t.Errorf("the page lacks the rule %q that keeps the full row readable", rule)
		}
	}
}

// A change shows only the pictures its goblin made, and a change without any
// shows no picture strip at all.
func TestAChangeShowsOnlyThePicturesItsGoblinMade(t *testing.T) {
	// Arrange
	r := recordedReturn(t)
	r.Pictures = nil

	// Act
	page, words := returnPage(t, r)

	// Assert
	if strings.Contains(page, `class="question-thumbs"`) || strings.Contains(words, "Before and after") {
		t.Errorf("the page shows a picture strip with no pictures made")
	}
}

func TestReturnModalFixture(t *testing.T) {
	r := recordedReturn(t)
	long := "Review the complete migration and confirm every archived invoice remains available before removing the old database and its shared backup configuration"
	r.Report.Held = append(r.Report.Held, Held{Item: "question:q-drop-table", What: long, Waiting: true, Now: "still waiting on you"})
	r.Report.UnderWay[0].Title = long
	r.Report.Next[0].Title = long
	page, _ := returnPage(t, r)
	if directory := os.Getenv("AFK_PROOF_DIR"); directory != "" {
		if err := os.WriteFile(directory+"/report.html", []byte(page), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheReturnPageWrapsAndLinksToItsCommandCenterCards(t *testing.T) {
	r := recordedReturn(t)
	r.Report.Held = append(r.Report.Held, Held{Item: "question:q-drop-table", What: "Review the migration", Waiting: true})
	page, _ := returnPage(t, r)
	if strings.Contains(page, "-webkit-line-clamp") {
		t.Error("report titles are clipped")
	}
	if !strings.Contains(page, `data-item="question:q-drop-table"`) {
		t.Error("the report does not link to its Command Center card")
	}
}
