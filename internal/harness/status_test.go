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
// gauge and its notices, and a notice that names claude doctor carries
// /doctor, also cut short (#2236).
func TestAClaudeStatusIsReadAsItsGaugeAndNotices(t *testing.T) {
	doctor := []Notice{{Text: "Auto-update failed"}, {Text: "Run claude doctor", Action: "/doctor"}}
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
			Status{Gauge: "0% until auto-compact", Notices: []Notice{{Text: "Auto-update failed"}, {Text: "Run claude do…", Action: "/doctor"}}}, true},
		{"beside a turn row", claudeOver(claudeWorkingFooter, "✽ Compacting conversation… (5s)         0% until auto-compact · × Auto-update failed"),
			Status{Gauge: "0% until auto-compact", Notices: []Notice{{Text: "Auto-update failed"}}}, true},
		{"nothing over the composer", claudeOver(claudeIdleFooter, "● Fake reply.", ""), Status{}, false},
		{"a retry's hint", claudeOver(claudeWorkingFooter, "✻ 529 Overloaded · Retrying in 3s · attempt 3/10", "  ⎿  If it persists, check your inference gateway (127.0.0.1:5000)."), Status{}, false},
		{"the command list", claudeOver("  ⏸ manual mode on", "  ❯ /compact                    Free up context by summarizing the conversation so far", "    /autocompact                Set how full the context gets before auto-summarizing"), Status{}, false},
		{"a reply that quotes it", []string{"● It said 83% context used · ✘ Auto-update failed", "", "❯ "}, Status{}, false},
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
