package harness

import (
	"reflect"
	"strings"
	"testing"
)

// claudeOver is a Claude Code 2.1.322 screen as a native terminal read it on
// 2026-11-09 against a fake Messages API: over rows, then its ruled composer
// and footer.
func claudeOver(footer string, over ...string) []string {
	return claudeScreen(append([]string{"❯ third", ""}, over...), footer)
}

const (
	claudeIdleFooter    = "  ⏸ manual mode on · ? for shortcuts"
	claudeWorkingFooter = "  ⏸ manual mode on · esc to interrupt"
)

// The status Claude Code draws over its composer reaches the board as its
// gauge and its notices (#2236). A failed update is one notice that names the
// update check the supervisor runs for it outside the session, with the
// terminal's own advice to run claude doctor, whole or cut short, no second
// notice and no command: /doctor is a prompt that starts a turn (#2333).
func TestAClaudeStatusIsReadAsItsGaugeAndNotices(t *testing.T) {
	doctor := []Notice{{Text: "Auto-update failed", Check: "update"}}
	for _, test := range []struct {
		name   string
		screen []string
		want   Status
		isRead bool
	}{
		{"a notice alone", claudeOver(claudeIdleFooter, strings.Repeat(" ", 78)+"✘ Auto-update failed · Run claude doctor  "), Status{Notices: doctor}, true},
		{"the gauge and a notice", claudeOver(claudeIdleFooter, strings.Repeat(" ", 58)+"83% context used · ✘ Auto-update failed · Run claude doctor  "), Status{Gauge: "83% context used", Notices: doctor}, true},
		{"the gauge alone", claudeOver(claudeIdleFooter, strings.Repeat(" ", 100)+"100% context used  "), Status{Gauge: "100% context used"}, true},
		{"2.1.321's words, cut short", claudeOver(claudeIdleFooter, " 0% until auto-compact · × Auto-update failed · Run claude do…"),
			Status{Gauge: "0% until auto-compact", Notices: doctor}, true},
		{"beside a turn row", claudeOver(claudeWorkingFooter, "✽ Compacting conversation… (5s)         0% until auto-compact · × Auto-update failed"),
			Status{Gauge: "0% until auto-compact", Notices: doctor}, true},
		{"nothing over the composer", claudeOver(claudeIdleFooter, "● Fake reply.", ""), Status{}, false},
		{"a retry's hint", claudeOver(claudeWorkingFooter, "✻ 529 Overloaded · Retrying in 3s · attempt 3/10", "  ⎿  If it persists, check your inference gateway (127.0.0.1:5000)."), Status{}, false},
		{"the command list", claudeOver("  ⏸ manual mode on", "  ❯ /compact                    Free up context by summarizing the conversation so far", "    /autocompact                Set how full the context gets before auto-summarizing"), Status{}, false},
		{"a reply that quotes it", claudeOver(claudeIdleFooter, "● It said 83% context used · ✘ Auto-update failed"), Status{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			screens, _ := NativeScreens(Claude)

			// Act
			status, isRead := screens.Status(test.screen)

			// Assert
			if isRead != test.isRead || !reflect.DeepEqual(status, test.want) {
				t.Errorf("Status = %+v, %v; want %+v, %v", status, isRead, test.want, test.isRead)
			}
		})
	}
}

// While Claude Code waits to send a request that failed again, its turn still
// runs, and the board reads the failure and the attempt rather than any
// words of a turn: on main the row read as no turn at all, so the chat showed
// the session idle through every retry (#2236).
func TestAClaudeRetryReadsAsATurnWithItsAttempt(t *testing.T) {
	for _, test := range []struct {
		name      string
		turn      string
		retry     Retry
		isRetry   bool
		isWorking bool
		words     string
	}{
		{"an API error", "✻ API error · Retrying in 1s · attempt 1/10", Retry{Reason: "API error", Attempt: 1, Attempts: 10}, true, true, ""},
		{"an overload", "✻ 529 Overloaded · Retrying in 20s · attempt 6/10", Retry{Reason: "529 Overloaded", Attempt: 6, Attempts: 10}, true, true, ""},
		{"another frame", "· 529 Overloaded · Retrying in 3s · attempt 3/10", Retry{Reason: "529 Overloaded", Attempt: 3, Attempts: 10}, true, true, ""},
		{"a compaction", "✻ Compacting conversation… (19s)", Retry{}, false, true, "Compacting conversation"},
		{"a turn's verb", "✶ Cogitating… (12s · esc to interrupt)", Retry{}, false, true, "Cogitating"},
		{"a turn that ended", "✻ Cogitated for 38s · done 9:18 PM", Retry{}, false, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			screens, _ := NativeScreens(Claude)
			screen := claudeOver(claudeWorkingFooter, test.turn, "  ⎿  If it persists, check your inference gateway (127.0.0.1:5000).")

			// Act
			retry, isRetry := screens.Retry(screen)

			// Assert
			if isRetry != test.isRetry || retry != test.retry {
				t.Errorf("Retry = %+v, %v; want %+v, %v", retry, isRetry, test.retry, test.isRetry)
			}
			if working := screens.IsWorking(screen); working != test.isWorking {
				t.Errorf("IsWorking = %v, want %v", working, test.isWorking)
			}
			if words := screens.WorkingWords(screen); words != test.words {
				t.Errorf("WorkingWords = %q, want %q", words, test.words)
			}
		})
	}
}

// A turn its harness gave up on the model's API for a connection that failed
// or a request that timed out reads as that error, the last block over its
// composer, in the harness's own words, wrapped rows and all (#2863). Claude
// Code 2.1.331's screens are those a worker's terminal showed on 2026-11-24;
// Codex 0.160's and pi 1.4's rows are those their builds draw, pi's each its
// whole message. An error the conversation went on past, a turn still
// retrying, a refusal of another kind, a reply that quotes an error and a
// goblin's own reply that opens as one, drawn under the same mark, are none.
func TestATurnThatEndedOnAnAPIErrorReadsAsThatError(t *testing.T) {
	rule := strings.Repeat("─", 120)
	claudeStatus := strings.Repeat(" ", 80) + "× Auto-update failed · Run claude doctor"
	claudeFooter := "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← 2 agents"
	codexFooter := `  gpt-6-astra low · ~\work\task`
	piFooter := "↑8.4k ↓357 R15k CH91.8% $0.001 0.8%/1.0M (auto)"
	for name, test := range map[string]struct {
		kind    Kind
		screen  []string
		failure string
		isEnded bool
	}{
		"claude unreachable": {Claude, []string{"● Reading the tests to place a red regression test.", "  Read 5 files, ran 5 shell commands", "", "● API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", "", claudeStatus, rule, "> Resend your message to try again", rule, claudeFooter},
			"API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", true},
		"claude timed out, wrapped": {Claude, []string{"> CEO: Your run ended with exit 0 after 2h42m23s; read its output and carry on from it.", "", "● Request timed out. If the API is slow to answer, or a proxy or gateway holds responses, set API_TIMEOUT_MS", "  above the limit in force (600000 by default).", "", rule, "> Resend your message to try again", rule, claudeFooter},
			"Request timed out. If the API is slow to answer, or a proxy or gateway holds responses, set API_TIMEOUT_MS above the limit in force (600000 by default).", true},
		"claude connection dropped": {Claude, []string{"● API Error: Connection dropped (ECONNRESET)", "", rule, "❯ ", rule, claudeFooter},
			"API Error: Connection dropped (ECONNRESET)", true},
		"codex disconnected": {Codex, []string{"• Running the focused tests next.", "", "■ stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)", "", "› Ask Codex to do anything", "", codexFooter},
			"stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)", true},
		"pi out of retries": {Pi, []string{" Reading the failing test first.", "", " Error: Retry failed after 3 attempts: fetch failed", rule, "", rule, piFooter},
			"Retry failed after 3 attempts: fetch failed", true},
		"claude api timed out":      {Claude, []string{"● API Error: Request timed out.", "", rule, "> Resend your message to try again", rule, claudeFooter}, "API Error: Request timed out.", true},
		"pi connection error":       {Pi, []string{" Error: Connection error.", rule, "", rule, piFooter}, "Connection error.", true},
		"pi request timed out":      {Pi, []string{" Error: Request timed out.", rule, "", rule, piFooter}, "Request timed out.", true},
		"pi fetch failed":           {Pi, []string{" Error: fetch failed", rule, "", rule, piFooter}, "fetch failed", true},
		"claude reply on a timeout": {Claude, []string{"● Request timed out on the deploy check; I'll wait for CI.", "", rule, "❯ ", rule, claudeFooter}, "", false},
		"pi reply on a timeout":     {Pi, []string{" Error: Request timed out on the deploy check, so I'll wait for CI.", rule, "", rule, piFooter}, "", false},
		"pi reply naming an error":  {Pi, []string{" Error: the deploy check hit a connection error, so I ran it again.", rule, "", rule, piFooter}, "", false},
		"claude carried on past it": {Claude, []string{"● API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", "", "> CEO: the API answers again, so carry on", "", "● Carrying on from the failing test.", "", rule, "❯ ", rule, claudeFooter}, "", false},
		"claude still retrying":     {Claude, []string{"> next", "", "✻ API error · Retrying in 1s · attempt 1/10", "", rule, "❯ ", rule, "  ⏵⏵ bypass permissions on · esc to interrupt"}, "", false},
		"claude refused on quota":   {Claude, []string{"● API Error: Request rejected (429) · this may be a temporary capacity issue.", "", rule, "> Resend your message to try again", rule, claudeFooter}, "", false},
		"claude quoting one":        {Claude, []string{"● The run printed API Error: Can't reach the API server, so I ran it again.", "", rule, "❯ ", rule, claudeFooter}, "", false},
		"codex interrupted":         {Codex, []string{"■ Conversation interrupted - tell the model what to do differently.", "", "› Ask Codex to do anything", "", codexFooter}, "", false},
		"pi refused its key":        {Pi, []string{" Error: 401 Unauthorized", rule, "", rule, piFooter}, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			screens, _ := NativeScreens(test.kind)

			// Act
			failure, isEnded := screens.EndedOnAPIError(test.screen)

			// Assert
			if failure != test.failure || isEnded != test.isEnded {
				t.Errorf("EndedOnAPIError = %q, %v; want %q, %v", failure, isEnded, test.failure, test.isEnded)
			}
		})
	}
}
