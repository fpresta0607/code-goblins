package monitor

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A goblin whose turn ended on an API error its harness gave up retrying wakes
// once, naming that error, rather than as a goblin awaiting an answer, and
// the reading the supervisor's progress check takes says so. The wake says
// the supervisor carries it on once the API answers again only where it does,
// for a native goblin whose API it knows; a pi goblin on a model that names no
// provider, and a goblin in Herdr, which the supervisor never types into, are
// the CEO's to carry on (#2863). Claude Code's screen is the one a worker's
// terminal showed on 2026-11-24.
func TestAGoblinWhoseTurnEndedOnAnAPIErrorWakesOnceNamingTheError(t *testing.T) {
	rule := strings.Repeat("─", 120)
	claudeScreen := []string{"● Reading the tests to place a red regression test.", "", "● API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", "", strings.Repeat(" ", 80) + "× Auto-update failed · Run claude doctor", rule, "> Resend your message to try again", rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	for name, test := range map[string]struct {
		backend     string
		harness     string
		model       string
		screen      []string
		failure     string
		isCarriedOn bool
	}{
		"claude":          {"native", "claude", "", claudeScreen, "API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", true},
		"claude in herdr": {"herdr", "claude", "", claudeScreen, "API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", false},
		"codex": {"native", "codex", "", []string{"• Running the focused tests next.", "", "■ stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)", "", "› Ask Codex to do anything", "", `  gpt-6-astra low · ~\work\task`},
			"stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)", true},
		"pi on an API the supervisor does not know": {"native", "pi", "your-open-model", []string{" Reading the failing test first.", "", " Error: Retry failed after 3 attempts: fetch failed", rule, "", rule, "↑8.4k ↓357 R15k CH91.8% $0.001 0.8%/1.0M (auto)"},
			"Retry failed after 3 attempts: fetch failed", false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			service, probe, meta, clock := nativeTurnFixture(t, turnsFake{known: false})
			meta.Backend, meta.Harness, meta.Model = test.backend, test.harness, test.model
			if test.backend == "herdr" {
				meta.HerdrSession, meta.HerdrWorkspaceID, meta.HerdrTabID, meta.HerdrPaneID = "test", "w1", "tab1", "p1"
			}
			writeTask(t, service.StateDir, meta)
			screen := strings.Join(test.screen, "\n")

			// Act
			first := nativeTurnScan(t, service, probe, meta, clock, herdr.AgentDone, screen)
			observation, err := ReadObservation(service.StateDir, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
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
			if observation.Reason != APIError {
				t.Errorf("the reading the monitor kept says %q, want %q", observation.Reason, APIError)
			}
			if isPromised := strings.Contains(first.Event.Detail, "the supervisor carries it on"); isPromised != test.isCarriedOn {
				t.Errorf("wake %q promises the supervisor's carry-on %v, want %v", first.Event.Detail, isPromised, test.isCarriedOn)
			}
			if again.Event != nil {
				t.Errorf("the next scan woke again: %+v", *again.Event)
			}
		})
	}
}
