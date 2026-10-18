package waiting

import (
	"slices"
	"strings"
	"testing"
)

// What waits on the Overlord is what the board's page alerts, keyed as the
// page keys it, by its id and publishing: a pending question by its lead
// unless it was asked from an open review page, an open review item, a
// command ready or running unless a credential card or Remote access opened
// it, and an open request for credentials by its project, never by the
// credential names. A goblin's question, which names its task, is the CFO's
// to answer: it is not listed while the CFO has it, and a goblin blocked,
// failed or done is no item at all. The snapshot names the supervisor's
// instance too.
func TestWaitingListsWhatTheBoardsPageAlerts(t *testing.T) {
	snapshot := `{"instance":"i-1","tasks":[{"id":"shop","title":"Open the shop; Claude Code","phase":"failed","reason":"Waiting on the CFO: Requested by the operator; worktree C:\\work"},{"id":"blog","title":"Blog","phase":"done","pr":"https://github.com/o/r/pull/7"}],
		"questions":[{"id":"q1","text":"Which plan?\n- details","status":"pending","created_at":"2026-10-01T12:00:00Z"},{"id":"q2","text":"old","status":"answered","created_at":"2026-10-01T12:00:00Z"},
			{"id":"notify-shop-4","text":"Which port?","status":"pending","task":"shop","created_at":"2026-10-01T12:04:00Z"},
			{"id":"notify-shop-5","text":"Which colour?","status":"pending","task":"shop","created_at":"2026-10-01T12:05:00Z"}],
		"reviews":[{"id":"r1","title":"Pick a layout","state":"open","task":"shop","created_at":"2026-10-01T12:01:00Z"},{"id":"r2","title":"done","state":"answered","created_at":"2026-10-01T12:00:00Z"}],
		"runs":[{"id":"u1","title":"Install the tool","state":"ready","created_at":"2026-10-01T12:02:00Z"},{"id":"u2","title":"ran","state":"finished","created_at":"2026-10-01T12:00:00Z"},
			{"id":"u3","title":"Sign in","state":"ready","credential_request":"c1","created_at":"2026-10-01T12:00:00Z"},
			{"id":"u4","title":"Mend Tailscale for remote access","state":"running","remote_fix":"tailscale","created_at":"2026-10-01T12:03:00Z"}],
		"credentials":[{"id":"c1","project":"shop","state":"open","task":"shop","names":["API_KEY"],"created_at":"2026-10-01T12:06:00Z"},{"id":"c2","project":"blog","state":"saved","names":["TOKEN"],"created_at":"2026-10-01T12:00:00Z"},
			{"id":"c3","project":"shop","state":"open","task":"shop","names":[],"why":"Create the shop's test account","created_at":"2026-10-01T12:07:00Z"}]}`

	instance, items, err := Items([]byte(snapshot))

	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{"question:q1@2026-10-01T12:00:00Z", "CFO", "The CFO asks: Which plan?", false},
		{"review:r1@2026-10-01T12:01:00Z", "Open the shop", "Open the shop wants your review: Pick a layout", false},
		{"run:u1@2026-10-01T12:02:00Z", "CFO", "A command waits for you to run it: Install the tool", false},
		{"credential:c1@2026-10-01T12:06:00Z", "Open the shop", "Open the shop asks for credentials for shop", false},
		{"credential:c3@2026-10-01T12:07:00Z", "Open the shop", "Open the shop needs you for a step: Create the shop's test account", false},
	}
	if instance != "i-1" || !slices.Equal(items, want) {
		t.Errorf("waiting = %q, %+v; want i-1, %+v", instance, items, want)
	}
}

// The Overlord, 2026-10-07: an alert is one plain line in the asker's words,
// never raw text. Each item is said as the board's page says it: who
// asks, by its goblin's title without its harness, and the item's own words
// on one line, without Markdown's marks, a table of values, a wait's queue
// prefix or its page's link.
func TestEachItemIsNotifiedInItsAskersWordsOnOnePlainLine(t *testing.T) {
	page := "http://127.0.0.1:4387/session/f26e"
	longTitle := strings.Repeat("Answers are never lost ", 5)
	for name, test := range map[string]struct {
		item string
		want Item
	}{
		"the CFO's question, its marks dropped and its details left out": {`"questions":[{"id":"q1","text":"Ship **now**?\n\n- details","status":"pending","created_at":"T"}]`,
			Item{"question:q1@T", "CFO", "The CFO asks: Ship now?", false}},
		"a question that opens with a table of values": {`"questions":[{"id":"q1","text":"| Type | Name |\n| --- | --- |\n| CNAME | ` + "`mcp`" + ` |\nMay I add these records?","status":"pending","created_at":"T"}]`,
			Item{"question:q1@T", "CFO", "The CFO asks: May I add these records?", false}},
		"a question whose lead is a bullet": {`"questions":[{"id":"q1","text":"- Pick **one** of ` + "`a`" + ` or ` + "`b`" + `\n- details","status":"pending","created_at":"T"}]`,
			Item{"question:q1@T", "CFO", "The CFO asks: Pick one of a or b", false}},
		"a goblin's wait, with a table and its page": {`"reviews":[{"id":"waiting-shop-4","task":"shop","state":"open","created_at":"T","lavish":"` + page + `","title":"Waiting on you: Add the DNS records in **Cloudflare**\n| Type | Name |\n| --- | --- |\n| CNAME | ` + "`mcp`" + ` |\nthen tell me (page ` + page + `)"}]`,
			Item{"review:waiting-shop-4@T", "Open the shop", "Open the shop is waiting on you: Add the DNS records in Cloudflare then tell me", false}},
		"a goblin no longer on the board": {`"reviews":[{"id":"r1","task":"gone","state":"open","created_at":"T","title":"Review the plan"}]`,
			Item{"review:r1@T", "gone", "gone wants your review: Review the plan", false}},
		"a goblin whose title is a whole sentence": {`"reviews":[{"id":"r1","task":"long","state":"open","created_at":"T","title":"Review the plan"}]`,
			Item{"review:r1@T", strings.TrimSpace(longTitle), "Answers are never lost Answers are never lost Answers are n… wants your review: Review the plan", false}},
		"a goblin's command to run": {`"runs":[{"id":"u1","task":"shop","state":"ready","created_at":"T","title":"Sign in to **GitHub**"}]`,
			Item{"run:u1@T", "Open the shop", "Open the shop asks you to run a command: Sign in to GitHub", false}},
		"a new release": {`"runs":[{"id":"update-v0.6.0","state":"ready","created_at":"T","title":"Update","update":{"from":"v0.5.1","to":"v0.6.0"}}]`,
			Item{"run:update-v0.6.0@T", "Code Goblins", "Code Goblins v0.6.0 is ready: update from v0.5.1", false}},
		"a question past what a notification says": {`"questions":[{"id":"q1","text":"Ship it? ` + strings.Repeat("x", 300) + `","status":"pending","created_at":"T"}]`,
			Item{"question:q1@T", "CFO", "The CFO asks: Ship it? " + strings.Repeat("x", 136) + "…", false}},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := `{"instance":"i-1","tasks":[{"id":"shop","title":"Open the shop; Claude Code"},{"id":"long","title":"` + longTitle + `"}],` + test.item + `}`

			_, items, err := Items([]byte(snapshot))

			if err != nil || !slices.Equal(items, []Item{test.want}) {
				t.Errorf("waiting = %+v, %v; want %+v", items, err, test.want)
			}
		})
	}
}

// What waits on him is what the board's page counts: a goblin's question once
// the CFO has left it ten minutes, quiet as the page never alerts it, not
// before; not a page he sent a revision on; not a goblin's wait that follows
// a decision still pending, though one whose decision closed waits; and a
// browser sign-in under way, not one done.
func TestItemsAreWhatTheBoardsPageCounts(t *testing.T) {
	snapshot := `{"at":"2026-10-18T12:20:00Z","tasks":[{"id":"shop","title":"Open the shop"}],
		"questions":[{"id":"notify-shop-1","text":"Which port?","status":"pending","task":"shop","created_at":"2026-10-18T12:05:00Z"},
			{"id":"notify-shop-2","text":"Which colour?","status":"pending","task":"shop","created_at":"2026-10-18T12:15:00Z"},
			{"id":"cfo-question-3","text":"Ship it?","status":"pending","created_at":"2026-10-18T12:16:00Z"}],
		"reviews":[{"id":"r1","title":"Pick a layout","state":"open","task":"shop","created_at":"T","revising_since":"2026-10-18T12:10:00Z"},
			{"id":"r4","title":"Add the DNS records","state":"open","task":"shop","follows":"cfo-question-3","created_at":"T"},
			{"id":"r5","title":"Review the plan","state":"open","task":"shop","follows":"cfo-question-9","created_at":"T"}],
		"browser_signins":[{"id":"b1","task":"shop","state":"open","provider":"GitHub","project":"shop","created_at":"T"},
			{"id":"b2","task":"shop","state":"done","provider":"GitHub","project":"shop","created_at":"T"}]}`

	_, items, err := Items([]byte(snapshot))

	want := []Item{
		{"question:notify-shop-1@2026-10-18T12:05:00Z", "Open the shop", "Open the shop asks: Which port?", true},
		{"question:cfo-question-3@2026-10-18T12:16:00Z", "CFO", "The CFO asks: Ship it?", false},
		{"review:r5@T", "Open the shop", "Open the shop wants your review: Review the plan", false},
		{"browser-signin:b1@T", "Open the shop", "Open the shop asks: Sign in to GitHub for shop", false},
	}
	if err != nil || !slices.Equal(items, want) {
		t.Errorf("Items = %+v, %v; want %+v", items, err, want)
	}
}
