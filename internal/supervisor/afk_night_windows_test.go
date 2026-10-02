package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A night under AFK mode, from the Overlord turning it on to the report he
// reads when he turns it off, in one home with the supervisor's real pipe and
// the board's real endpoint. The Overlord's terminal is named (the test's own
// process runs under go test), and the CFO and the goblin are this test
// process, registered and proven the way every other test here does.
//
//   - A goblin asks a question the CFO may answer: nothing prompts the
//     Overlord, the CFO answers, the goblin has the answer once, and the log
//     holds the decision.
//   - A pull request passes the checks: the merge word is logged with its
//     evidence, then its outcome, as cfo pr merge sends them (its checks are
//     proven in cmd/cfo).
//   - A migration that drops a table is asked about: it is held for him, an
//     answer recorded as his is refused, and it is still waiting in the morning.
//   - He turns it off: the report says all of it.
func TestANightUnderAFKMode(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblinPane, cfo := goblinFixture(t, store)
	s := &Service{Store: store, Instance: "test-instance", Options: Options{CFO: cfo, Allowance: func(context.Context) ([]afk.Allowance, string) {
		return []afk.Allowance{{Provider: "claude", Window: "week", PercentUsed: 40}}, ""
	}}}
	runPipe(t, s)
	ctx := context.Background()
	pr := "https://github.com/acme/api/pull/12"

	// Act: the Overlord goes away.
	asOverlordsTerminal(s)
	if err := SwitchAFK(h, true); err != nil {
		t.Fatal(err)
	}

	// A goblin asks which store to use, which is the CFO's to answer.
	asked := surfaced(t, store, meta, record, cfo)
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	announcedAway := askToAnnounce(t, s, `{"keys":["alert:question:`+asked.ID+`","open:question:`+asked.ID+`"]}`)
	chosen, _, err := cfo.AnswerGoblin(ctx, fmt.Sprint(record.Seq), "sqlite", "smallest thing that works")
	if err != nil {
		t.Fatal(err)
	}

	// A pull request passed its checks, and the CFO gave the merge word.
	for _, entry := range []afk.Entry{
		{Kind: afk.KindMerge, What: pr, Link: pr, Evidence: "verified: gate run 41 passed and its test output was read; head 3f1a9c0; 7 checks completed green; mergeable; main's tip 9d8c7b6 is in the head"},
		{Kind: afk.KindMerge, What: pr, Link: pr, Evidence: "gh pr merge " + pr + " --merge --match-head-commit 3f1a9c0", Outcome: afk.OutcomeMerged},
	} {
		if err := LogAFKDecision(h, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.AppendStatus(h.State, meta.ID, "done: PR "+pr); err != nil {
		t.Fatal(err)
	}

	// A migration that drops a table is the Overlord's alone: the CFO leaves
	// it for him, and nothing answers it in his name.
	if err := cfo.PublishQuestion(ctx, "drop-legacy-invoices", "Migration 0042 drops the legacy_invoices table. Apply it?", []string{"Apply it", "Keep it held"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	_, decidedForHim := cfo.RecordAnswer(ctx, "drop-legacy-invoices", "Apply it", "", "chat")

	// The Overlord is back.
	if err := SwitchAFK(h, false); err != nil {
		t.Fatal(err)
	}
	announcedBack := askToAnnounce(t, s, `{"keys":["alert:question:drop-legacy-invoices","open:question:drop-legacy-invoices"]}`)

	// Assert
	if len(announcedAway) != 0 || len(announcedBack) != 0 {
		t.Errorf("the board was handed %q while he was away and %q once he was back, want nothing: both items were held", announcedAway, announcedBack)
	}
	if chosen != "SQLite" || len(goblinPane.prompts) != 1 || !strings.Contains(goblinPane.prompts[0], "SQLite. smallest thing that works") {
		t.Errorf("the goblin's pane = %q (chosen %q), want the CFO's answer delivered once", goblinPane.prompts, chosen)
	}
	if decidedForHim == nil || !strings.Contains(decidedForHim.Error(), "AFK mode is on") {
		t.Errorf("recording the migration answered as his = %v, want it refused", decidedForHim)
	}
	report, found, err := afk.ReadReport(h.State)
	if err != nil || !found {
		t.Fatalf("ReadReport = %v, %v, want the report of the night", found, err)
	}
	kinds := make([]string, len(report.Decisions))
	for i, decision := range report.Decisions {
		kinds[i] = decision.Kind
	}
	if !slices.Equal(kinds, []string{afk.KindAnswer, afk.KindMerge}) {
		t.Fatalf("decisions = %+v, want the answer and then the merge word", report.Decisions)
	}
	if answer := report.Decisions[0]; answer.What != asked.ID || answer.Task != meta.ID || !strings.Contains(answer.Evidence, "Which store?") || !strings.Contains(answer.Evidence, "SQLite") {
		t.Errorf("the answer = %+v, want the goblin's question and the CFO's choice", answer)
	}
	if merge := report.Decisions[1]; merge.What != pr || merge.Outcome != afk.OutcomeMerged || !strings.Contains(merge.Evidence, "gate run 41 passed") {
		t.Errorf("the merge word = %+v, want it with its evidence and merged", merge)
	}
	if len(report.Finished) != 1 || report.Finished[0].Task != meta.ID || report.Finished[0].PR != pr {
		t.Errorf("finished = %+v, want the goblin's pull request", report.Finished)
	}
	if len(report.Held) != 1 || report.Held[0].Item != "question:drop-legacy-invoices" || !report.Held[0].Waiting {
		t.Errorf("held = %+v, want only the migration, still waiting: the question the CFO answered is a decision", report.Held)
	}
	if got := store.Snapshot().Questions; !slices.ContainsFunc(got, func(q Question) bool {
		return q.ID == "drop-legacy-invoices" && q.Status == "pending" && q.AnsweredBy == ""
	}) {
		t.Errorf("questions = %+v, want the migration still pending for him", got)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || !strings.Contains(pending[len(pending)-1].Detail, "cfo afk report") {
		t.Errorf("the CFO's queue ends %+v (%v), want it told to write the report", pending, err)
	}
	var rendered bytes.Buffer
	if err := afk.Render(&rendered, report); err != nil {
		t.Fatal(err)
	}
	t.Logf("the report the Overlord reads:\n%s", rendered.String())
}
