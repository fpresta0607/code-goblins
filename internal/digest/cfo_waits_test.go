package digest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// waitOnTheCFO gives h a live goblin, Jerry, whose latest report, made at
// at, is a wait on the CFO.
func waitOnTheCFO(t *testing.T, h home.Home, at time.Time) {
	t.Helper()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "cg-waits", GoblinName: "Jerry", Project: `C:\dev\code-goblins`, Harness: "claude", Backend: "native", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "cg-waits.status"), at.UTC().Format(time.RFC3339)+" waiting on cfo: which schema do I migrate first?\n")
}

// A goblin that waits on the CFO is listed in the checkpoint with how long
// it has waited and what for, and the checkpoint no longer says no goblin
// waits on the CFO's answer: on 2026-11-02 it said so while a goblin had
// waited two hours on a pull request held for the CFO's review (#1912).
func TestTheCheckpointListsEachGoblinWaitingOnTheCFO(t *testing.T) {
	// Arrange
	h := newDigestHome(t)
	now := time.Date(2026, 11, 2, 16, 30, 0, 0, time.UTC)
	waitOnTheCFO(t, h, now.Add(-42*time.Minute))

	// Act
	checkpoint := readCheckpoint(t, h, now)

	// Assert
	if waits := section(t, checkpoint, "== WAITING ON THE CFO =="); !strings.Contains(waits, "Jerry (cg-waits) has waited 42 minutes: waiting on cfo: which schema do I migrate first?") {
		t.Errorf("WAITING ON THE CFO does not list Jerry's wait:\n%s", waits)
	}
	if questions := section(t, checkpoint, "== OPEN QUESTIONS =="); strings.Contains(questions, "No goblin waits on your answer.") {
		t.Errorf("OPEN QUESTIONS says no goblin waits while Jerry does:\n%s", questions)
	}
}

// The session digest, long and brief, lists the goblins that wait on the CFO
// right after the wake queue.
func TestTheDigestListsTheGoblinsWaitingOnTheCFOAfterTheWakeQueue(t *testing.T) {
	for _, test := range []struct {
		name    string
		compose func(t *testing.T, h home.Home) string
	}{
		{name: "the long digest", compose: func(t *testing.T, h home.Home) string {
			var out bytes.Buffer
			if err := Compose(h, os.Getpid(), "s1", &out); err != nil {
				t.Fatalf("Compose: %v", err)
			}
			return out.String()
		}},
		{name: "the brief digest", compose: composeBrief},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := newDigestHome(t)
			waitOnTheCFO(t, h, time.Now().Add(-42*time.Minute))

			// Act
			out := test.compose(t, h)

			// Assert
			assertHeaderOrder(t, out, []string{"== WAKE QUEUE ==", "== WAITING ON THE CFO ==", "== SUPERVISION OPERATING INSTRUCTIONS =="})
			if !strings.Contains(out, "== WAITING ON THE CFO ==\nJerry (cg-waits) has waited 42 minutes: waiting on cfo: which schema do I migrate first?\n") {
				t.Errorf("the digest does not list Jerry's wait under WAITING ON THE CFO:\n%s", out)
			}
		})
	}
}

// A digest with no goblin waiting on the CFO says so rather than leaving a
// heading over nothing.
func TestTheDigestSaysWhenNoGoblinWaitsOnTheCFO(t *testing.T) {
	h := newDigestHome(t)

	out := composeBrief(t, h)

	if !strings.Contains(out, "== WAITING ON THE CFO ==\nNo goblin waits on the CFO.\n") {
		t.Errorf("the digest does not say no goblin waits on the CFO:\n%s", out)
	}
}

// A fleet whose every goblin waits on the CFO keeps the brief within its
// budget: the section lists the goblins that have waited longest and counts
// the rest rather than dropping them silently.
func TestTheBriefCountsTheGoblinsWaitingOnTheCFOThatDoNotFit(t *testing.T) {
	// Arrange
	h := bigHome(t, 40, 300)
	started := time.Now().Add(-3 * time.Hour)
	for i := 1; i <= 40; i++ {
		status := fmt.Sprintf("%s waiting on cfo: step %d needs the CFO's word on the schema before it goes on\n", started.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339), i)
		writeFile(t, filepath.Join(h.State, fmt.Sprintf("cg-goblin-%02d.status", i)), status)
	}

	// Act
	out := composeBrief(t, h)

	// Assert
	if len(out) > Budget {
		t.Fatalf("the brief digest is %d bytes, over its %d budget", len(out), Budget)
	}
	waits := out[strings.Index(out, "== WAITING ON THE CFO =="):strings.Index(out, "== SUPERVISION OPERATING INSTRUCTIONS ==")]
	listed, more := strings.Count(waits, " has waited "), 0
	for _, line := range strings.Split(waits, "\n") {
		_, _ = fmt.Sscanf(line, "(+%d more goblins wait on the CFO", &more)
	}
	if listed == 0 || more == 0 || listed+more != 40 {
		t.Fatalf("WAITING ON THE CFO accounts for %d listed and %d more, want some of each and 40 goblins:\n%s", listed, more, waits)
	}
	if !strings.Contains(waits, "cg-goblin-01 has waited") {
		t.Errorf("WAITING ON THE CFO does not list the goblin that has waited longest first:\n%s", waits)
	}
}
