package monitor

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A goblin whose turn ended on an API error its harness gave up retrying wakes
// once, naming that error, rather than as a goblin awaiting an answer: the
// supervisor carries it on once the API answers again (#2863). Claude Code's
// screen is the one a worker's terminal showed on 2026-11-24.
func TestAGoblinWhoseTurnEndedOnAnAPIErrorWakesOnceNamingTheError(t *testing.T) {
	rule := strings.Repeat("─", 120)
	for name, test := range map[string]struct {
		harness string
		screen  []string
		failure string
	}{
		"claude": {"claude", []string{"● Reading the tests to place a red regression test.", "", "● API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", "", strings.Repeat(" ", 80) + "× Auto-update failed · Run claude doctor", rule, "> Resend your message to try again", rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
			"API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)"},
		"codex": {"codex", []string{"• Running the focused tests next.", "", "■ stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)", "", "› Ask Codex to do anything", "", `  gpt-6-astra low · ~\work\task`},
			"stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			service, probe, meta, clock := nativeTurnFixture(t, turnsFake{known: false})
			meta.Harness = test.harness
			writeTask(t, service.StateDir, meta)
			screen := strings.Join(test.screen, "\n")

			// Act
			first := nativeTurnScan(t, service, probe, meta, clock, herdr.AgentDone, screen)
			if first.Event != nil {
				record, err := service.Publish(*first.Event)
				if err != nil {
					t.Fatal(err)
				}
				if err := wake.AckThrough(service.StateDir, record.Seq); err != nil {
					t.Fatal(err)
				}
			}
			again := nativeTurnScan(t, service, probe, meta, clock, herdr.AgentDone, screen)

			// Assert
			want := string(APIError) + ": its turn ended on an API error: " + test.failure + ";"
			if first.Event == nil || !strings.HasPrefix(first.Event.Detail, want) {
				t.Fatalf("event = %+v, want a wake opening %q", first.Event, want)
			}
			if again.Event != nil {
				t.Errorf("the next scan woke again: %+v", *again.Event)
			}
		})
	}
}
