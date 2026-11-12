package digest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// waitOnTheCEO gives h a live goblin, Jerry, whose latest report, made at
// at, is a wait on the CEO.
func waitOnTheCEO(t *testing.T, h home.Home, at time.Time) {
	t.Helper()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "cg-waits", GoblinName: "Jerry", Project: `C:\dev\code-goblins`, Harness: "claude", Backend: "native", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "cg-waits.status"), at.UTC().Format(time.RFC3339)+" waiting on ceo: which schema do I migrate first?\n")
}

// turnAFKOn turns AFK mode on in h when isAFKOn, and fails unless the
// switch then reads as asked.
func turnAFKOn(t *testing.T, h home.Home, isAFKOn bool) {
	t.Helper()
	if isAFKOn {
		if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if switched, err := afk.Read(h.State); err != nil || switched.On != isAFKOn {
		t.Fatalf("AFK mode reads %+v, %v; want on: %t", switched, err, isAFKOn)
	}
}

// A goblin that waits on the CEO is listed in the checkpoint with how long
// it has waited and what for, and the checkpoint no longer says no goblin
// waits on the CEO's answer: on 2026-11-02 it said so while a goblin had
// waited two hours on a pull request held for the CEO's review (#1912). AFK
// mode changes none of it.
func TestTheCheckpointListsEachGoblinWaitingOnTheCEO(t *testing.T) {
	for _, isAFKOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("AFK mode on: %t", isAFKOn), func(t *testing.T) {
			// Arrange
			h := newDigestHome(t)
			now := time.Date(2026, 11, 2, 16, 30, 0, 0, time.UTC)
			waitOnTheCEO(t, h, now.Add(-42*time.Minute))
			turnAFKOn(t, h, isAFKOn)

			// Act
			checkpoint := readCheckpoint(t, h, now)

			// Assert
			if waits := section(t, checkpoint, "== WAITING ON THE CEO =="); !strings.Contains(waits, "Jerry (cg-waits) has waited 42 minutes: waiting on ceo: which schema do I migrate first?") {
				t.Errorf("WAITING ON THE CEO does not list Jerry's wait:\n%s", waits)
			}
			if questions := section(t, checkpoint, "== OPEN QUESTIONS =="); strings.Contains(questions, "No goblin waits on your answer.") {
				t.Errorf("OPEN QUESTIONS says no goblin waits while Jerry does:\n%s", questions)
			}
		})
	}
}

// The session digest, long and brief, lists the goblins that wait on the CEO
// right after the wake queue, in the same section whether AFK mode is on or
// off.
func TestTheDigestListsTheGoblinsWaitingOnTheCEOAfterTheWakeQueue(t *testing.T) {
	for _, isAFKOn := range []bool{false, true} {
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
			t.Run(fmt.Sprintf("%s, AFK mode on: %t", test.name, isAFKOn), func(t *testing.T) {
				// Arrange
				h := newDigestHome(t)
				waitOnTheCEO(t, h, time.Now().Add(-42*time.Minute))
				turnAFKOn(t, h, isAFKOn)

				// Act
				out := test.compose(t, h)

				// Assert
				assertHeaderOrder(t, out, []string{"== WAKE QUEUE ==", "== WAITING ON THE CEO ==", "== SUPERVISION OPERATING INSTRUCTIONS =="})
				waits := out[strings.Index(out, "== WAITING ON THE CEO =="):strings.Index(out, "== SUPERVISION OPERATING INSTRUCTIONS ==")]
				if want := "== WAITING ON THE CEO ==\nJerry (cg-waits) has waited 42 minutes: waiting on ceo: which schema do I migrate first?\n"; waits != want {
					t.Errorf("WAITING ON THE CEO = %q, want %q", waits, want)
				}
				if strings.Contains(out, "AFK MODE IS ON") != isAFKOn {
					t.Errorf("the digest's AFK notice shows: %t, want %t", !isAFKOn, isAFKOn)
				}
			})
		}
	}
}

// A digest with no goblin waiting on the CEO says so rather than leaving a
// heading over nothing.
func TestTheDigestSaysWhenNoGoblinWaitsOnTheCEO(t *testing.T) {
	h := newDigestHome(t)

	out := composeBrief(t, h)

	if !strings.Contains(out, "== WAITING ON THE CEO ==\nNo goblin waits on the CEO.\n") {
		t.Errorf("the digest does not say no goblin waits on the CEO:\n%s", out)
	}
}

// A fleet whose every goblin waits on the CEO keeps the brief within its
// budget: the section lists the goblins that have waited longest and counts
// the rest rather than dropping them silently.
func TestTheBriefCountsTheGoblinsWaitingOnTheCEOThatDoNotFit(t *testing.T) {
	// Arrange
	h := bigHome(t, 40, 300)
	started := time.Now().Add(-3 * time.Hour)
	for i := 1; i <= 40; i++ {
		status := fmt.Sprintf("%s waiting on ceo: step %d needs the CEO's word on the schema before it goes on\n", started.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339), i)
		writeFile(t, filepath.Join(h.State, fmt.Sprintf("cg-goblin-%02d.status", i)), status)
	}

	// Act
	out := composeBrief(t, h)

	// Assert
	if len(out) > Budget {
		t.Fatalf("the brief digest is %d bytes, over its %d budget", len(out), Budget)
	}
	waits := out[strings.Index(out, "== WAITING ON THE CEO =="):strings.Index(out, "== SUPERVISION OPERATING INSTRUCTIONS ==")]
	listed, more := strings.Count(waits, " has waited "), 0
	for _, line := range strings.Split(waits, "\n") {
		_, _ = fmt.Sscanf(line, "(+%d more goblins wait on the CEO", &more)
	}
	if listed == 0 || more == 0 || listed+more != 40 {
		t.Fatalf("WAITING ON THE CEO accounts for %d listed and %d more, want some of each and 40 goblins:\n%s", listed, more, waits)
	}
	if !strings.Contains(waits, "cg-goblin-01 has waited") {
		t.Errorf("WAITING ON THE CEO does not list the goblin that has waited longest first:\n%s", waits)
	}
}
