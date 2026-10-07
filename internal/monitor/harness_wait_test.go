package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

// waitingOnAgentPane is cg-home-isolation's screen at wake 5967 on
// 2026-10-07: its turn over, Claude Code waiting on the code reviewer it
// started in the background.
const waitingOnAgentPane = "- Evidence is in data/cg-home-isolation/scratch-proof-20261007/.\n" +
	"\n" +
	"✻ Waiting for 1 background agent to finish\n" +
	"\n" +
	"> \n" +
	"⏵⏵ bypass permissions on (shift+tab to cycle) · ← 2 agents · ↓ to manage\n" +
	"● main\n" +
	"( ) code-reviewer  Tracing archive naming in cleanup.go          2m 14s · ↓ 131.4k tokens"

// A goblin waiting on its own sub-agent, background shell or monitor is not
// stale while its harness reports the wait, on its screen or in the
// transcript its family tree is read from: wake 5967 woke the CFO about a
// goblin waiting on its reviewer, and cg-ci-green's goblin_idle wakes on
// 2026-10-07 came while it waited on its own CI monitor. The wait is bounded
// as any other work is: with no sign of progress for the busy budget the
// goblin wakes the CFO once.
func TestAGoblinWaitingOnWorkItsHarnessReportsIsNotStale(t *testing.T) {
	for _, test := range []struct {
		name  string
		pane  string
		waits []string
	}{
		{"its screen waits on a background agent", waitingOnAgentPane, nil},
		{"its transcript has a monitor open", "● Watching CI with a monitor; I will pick it up when it reports.\n\n❯ ", []string{`monitor "CI checks for PR 421"`}},
		{"its transcript has a background shell open", "● The suite runs in the background.\n\n❯ ", []string{`shell "go test ./..."`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.UTC)
			service, probe, progress, _ := idleService(t, &now)
			progress.sample.Jobs = append(progress.sample.Jobs, test.waits...)
			progress.sample.Waits = test.waits

			// Act
			early := scanIdle(t, service, probe, herdr.AgentDone, test.pane, &now, 9)
			late := scanIdle(t, service, probe, herdr.AgentDone, test.pane, &now, 21)

			// Assert
			if len(early) != 0 {
				t.Fatalf("woke within the busy budget while its harness reported the wait: %+v", early)
			}
			if len(late) != 1 || !strings.HasPrefix(late[0].Detail, string(AwaitingAnswer)+":") && !strings.HasPrefix(late[0].Detail, string(GoblinIdle)+":") {
				t.Fatalf("past the busy budget with no progress = %+v, want exactly one stale wake", late)
			}
		})
	}
}
