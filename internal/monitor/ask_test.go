package monitor

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// editorSyncReply is the whole reply that ended cg-review-editor-sync's turn
// at 2026-10-05 20:55Z, read from its transcript. It sat at its prompt for
// about 2.5 hours after it, and the only wake was a generic goblin_idle.
const editorSyncReply = "Understood: nothing for me on PR 369, since the go (rest) failures are internal/verify admission tests that cg-verify-fast is fixing.\n\n" +
	"That leaves three items in my brief:\n" +
	"- **Live proof** that a choice picked on a Scrawl page reaches the goblin as its exact option text. This needs a scratch home, a separate Lavish server and a browser, so I'll start it once free memory is back over 5 GB.\n" +
	"- **History's three answer marks**: him, the CFO, and the CFO while he was away. The record shape was already agreed with cg-afk-mode.\n" +
	"- **His replacement choice**: the card that lets him change an answer the CFO gave, including when their answers cross.\n\n" +
	"I'll take the History marks next unless you want the live proof first."

type fakeReplies struct {
	reply string
	calls int
}

func (f *fakeReplies) LastReply(context.Context, state.TaskMeta, EndpointSample) string {
	f.calls++
	return f.reply
}

// askingService is idleService whose goblin last said reply.
func askingService(t *testing.T, now *time.Time, reply string) (Service, *fakeProber, *fakeProgress) {
	t.Helper()
	service, probe, progress, _ := idleService(t, now)
	service.Replies = &fakeReplies{reply: reply}
	return service, probe, progress
}

func wakesWith(wakes []Event, reason Reason) []Event {
	return slices.DeleteFunc(slices.Clone(wakes), func(event Event) bool { return !strings.HasPrefix(event.Detail, string(reason)+":") })
}

// The 2026-10-05 stall: a goblin that ended its turn offering the CFO a choice
// in prose, and sat at its prompt, wakes the CFO once with its own words and
// what to do, never as a generic idle goblin.
func TestAGoblinThatAsksInProseWakesWithItsQuestion(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 5, 20, 56, 0, 0, time.UTC)
	service, probe, _ := askingService(t, &now, editorSyncReply)

	// Act
	wakes := scanIdle(t, service, probe, herdr.AgentDone, "● I'll take the History marks next unless you want the live proof first.", &now, 20)

	// Assert
	if len(wakes) != 1 || !strings.HasPrefix(wakes[0].Detail, string(GoblinAsks)+":") {
		t.Fatalf("wakes = %+v, want exactly one goblin_asks wake", wakes)
	}
	for _, want := range []string{"g1 ", `It asked: "I'll take the History marks next unless you want the live proof first."`, `cfo send g1 "<your answer>"`} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake detail %q lacks %q", wakes[0].Detail, want)
		}
	}
	if wakes[0].Kind != "stale" || wakes[0].Key != "g1" {
		t.Errorf("wake = %+v, want a stale wake keyed by the goblin", wakes[0])
	}
	question, ok := wake.ProseAsk(wake.Record{Kind: wakes[0].Kind, Key: wakes[0].Key, Detail: wakes[0].Detail}, "g1")
	if !ok || question != "I'll take the History marks next unless you want the live proof first." {
		t.Errorf("the queue reads the question as %q (%t), want the goblin's sentence", question, ok)
	}
}

// A reply that only reports asks nothing, so the goblin wakes as idle, as
// before.
func TestAReportOnlyReplyStillWakesAsGoblinIdle(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 6, 16, 44, 0, 0, time.UTC)
	service, probe, _ := askingService(t, &now, "PR 383 is green on every check. Reporting it done.")

	// Act
	wakes := scanIdle(t, service, probe, herdr.AgentDone, "● PR 383 is green on every check. Reporting it done.", &now, 20)

	// Assert
	if len(wakes) != 1 || !strings.HasPrefix(wakes[0].Detail, string(GoblinIdle)+":") {
		t.Fatalf("wakes = %+v, want one goblin_idle wake and no goblin_asks", wakes)
	}
}

// A goblin still running a background command or a monitor it started is not
// idle: it resumes when that work reports back, so its asking reply wakes
// nobody while the work moves.
func TestAGoblinRunningItsOwnBackgroundWorkIsNotAskedAbout(t *testing.T) {
	for name, pane := range map[string]string{
		"its own job uses the processor":    "● I'll take the History marks next unless you want the live proof first.",
		"its pane shows a background shell": backgroundShellPane,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			now := time.Date(2026, 10, 5, 20, 56, 0, 0, time.UTC)
			service, probe, progress := askingService(t, &now, editorSyncReply)
			progress.cpuStep = 20 * time.Second

			// Act
			wakes := scanIdle(t, service, probe, herdr.AgentDone, pane, &now, 20)

			// Assert
			if len(wakes) != 0 {
				t.Fatalf("a goblin whose own work moves woke the CFO: %+v", wakes)
			}
		})
	}
}

// A turn that ends asking, with nothing of the goblin's own left running,
// wakes as the ask at once instead of as a turn awaiting input.
func TestATurnThatEndsAskingWakesAsItsQuestion(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 5, 20, 56, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)
	service.Replies = &fakeReplies{reply: editorSyncReply}

	// Act
	result := scanPane(t, service, probe, herdr.AgentDone, "❯", &now, time.Minute)

	// Assert
	if result.Event == nil || !strings.HasPrefix(result.Event.Detail, string(GoblinAsks)+":") || !strings.Contains(result.Event.Detail, "unless you want the live proof first") {
		t.Fatalf("event = %+v, want a goblin_asks wake with the question", result.Event)
	}
}

// The ask is one wake until the goblin works again; each new ask after that is
// its own wake, with no idle gap to wait out.
func TestAnAskWakesOnceUntilTheGoblinWorksAgain(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 5, 20, 56, 0, 0, time.UTC)
	service, probe, _ := askingService(t, &now, editorSyncReply)

	// Act
	first := scanIdle(t, service, probe, herdr.AgentDone, "❯", &now, 25)
	scanIdle(t, service, probe, herdr.AgentWorking, "✽ Reticulating… (3s · esc to interrupt)", &now, 1)
	second := scanIdle(t, service, probe, herdr.AgentDone, "❯", &now, 4)

	// Assert
	if len(first) != 1 || len(wakesWith(first, GoblinAsks)) != 1 {
		t.Fatalf("first stretch wakes = %+v, want the one ask", first)
	}
	if len(second) != 1 || len(wakesWith(second, GoblinAsks)) != 1 {
		t.Fatalf("second stretch wakes = %+v, want its own ask four minutes in", second)
	}
}

// An ask nobody acked is an answer owed, and the ledger asks it again.
func TestAnUnackedAskIsReAsked(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 5, 20, 56, 0, 0, time.UTC)
	service, probe, _ := askingService(t, &now, editorSyncReply)

	// Act
	wakes := scanIdleAcking(t, service, probe, herdr.AgentDone, "❯", &now, 15, false)

	// Assert
	if len(wakes) != 2 || !strings.HasPrefix(wakes[0].Detail, string(GoblinAsks)+":") || !strings.HasPrefix(wakes[1].Detail, string(AwaitingDecision)+":") {
		t.Fatalf("wakes = %+v, want the ask then one re-ask", wakes)
	}
}

// What a reply asks is read from real turn endings of the fleet: the goblins'
// own words at the end of a turn, with the reply's last two paragraphs, and
// whether each asks the CFO something or offers it a choice. The PR that added
// this check lists the same cases and what it decides for each.
func TestWhatARealTurnEndingAsks(t *testing.T) {
	for _, test := range []struct {
		name, reply string
		asked       []string
	}{
		{"cg-review-editor-sync 2026-10-05 20:55 offers the next item", editorSyncReply,
			[]string{"I'll take the History marks next unless you want the live proof first."}},
		{"cg-panel-clarity 2026-10-06 00:02 waits on the CFO's answer",
			"The two text notes ask to drop the sentence under the status, which conflicts with approving as shown. The question I filed has three options:\n- **Hide it under every status, then merge (recommended):** the sentence stays one click away under Details.\n- **Hide it only for Working and Pause failed, then merge.**\n- **Merge as approved and change it later.**\n\nThe page is closed and I won't reopen it. I haven't changed anything since and haven't merged. Your answer decides whether I make one more push.",
			[]string{"The question I filed has three options:", "Hide it under every status, then merge (recommended): the sentence stays one click away under Details.", "Your answer decides whether I make one more push."}},
		{"cg-review-editor-sync 2026-10-05 18:03 waits for the CFO's choice",
			"Main only checks for an empty composer before typed wakes, not before board answers, so PR 332 is still needed for the draft-line defect. Waiting for the CFO's choice before moving it.",
			[]string{"Waiting for the CFO's choice before moving it."}},
		{"cg-review-editor-sync 2026-10-05 19:52 says what it asked",
			"I've asked the CFO whether to open the run-card PR now or wait for memory to test it locally; I'll continue on the answer.",
			[]string{"I've asked the CFO whether to open the run-card PR now or wait for memory to test it locally; I'll continue on the answer."}},
		{"cg-verify-fast 2026-10-05 18:37 says what it asked",
			"Most of the specification is still open. Not done yet:\n- measuring what PR 360 saves after a week;\n- why the two Lavish install tests take 2 to 4 minutes each;\n- frontend selection and warm environments;\n- milestones on the board;\n- adapters for managed projects.\n\nI've asked whether to take the install tests next or stop here.",
			[]string{"I've asked whether to take the install tests next or stop here."}},
		{"cg-verify-fast 2026-10-05 19:42 says what it asked",
			"The only measurement that finished before Claude Code stopped the run for low memory: `internal/installscript` took 485.6 s for 19 tests with the fix, against 584.7 s for 18 tests on PR 359's head. The runs had different load, so treat the gap as indicative.\n\n`internal/installtest` still has to run on the fix and on PR 359's head before I push. I've asked the CFO whether to rerun now; free memory is 3.9 GB.",
			[]string{"I've asked the CFO whether to rerun now; free memory is 3.9 GB."}},
		{"cg-native-desktop-2 2026-10-05 18:46 waits on the CFO's answer",
			"**Decision needed:** the release workflow runs from the tagged commit, so the tag has to point at a commit that includes #363. I asked you as a blocked question whether to move v0.4.0 to that main commit or cut v0.4.1. I recommend moving v0.4.0: nothing was ever downloadable from it, so deleting and re-pushing the tag affects no one.\n\nOnce you answer and #363 is merged with main's CI green, I'll tag that commit. Then I'll report the draft release address and the `CodeGoblinsSetup.exe` line from SHA256SUMS.",
			[]string{"I asked you as a blocked question whether to move v0.4.0 to that main commit or cut v0.4.1.", "Once you answer and #363 is merged with main's CI green, I'll tag that commit."}},
		{"cg-local-voice 2026-10-02 15:13 asks before starting its brief",
			"Do you want me to carry out that brief? Say the word and I will start.",
			[]string{"Do you want me to carry out that brief?", "Say the word and I will start."}},
		{"pd-agent-api-v2-console 2026-09-29 01:37 asks a question",
			"**Decision needed:** the Claude skill still names the old address. Decision 3027's 30-day zero-call clock only starts once the skill moves too, and changing it republishes the skill at precisiondocs.ai/skills. Should it move now?",
			[]string{"Should it move now?"}},
		{"cg-review-editor-sync 2026-10-06 01:25 reports a page wait on the Overlord",
			"**Gaps the build will have to close,** found while he looks and now in the handoff:\n- The reason the CFO gives with `cfo answer --note` goes only to the goblin, so History has no reason to show yet.\n- Nothing records that the CFO answered while he was away.\n- A CFO answer marks its question succeeded at once, even before the goblin has taken it, so nothing yet says whether it can still be undone.\n- There is no way yet to swap a queued CFO answer for his.\n\nAny notify I send, such as done on 270, withdraws that page wait. After each one I'll file it again until he answers; this is in the handoff too.",
			nil},
		{"cg-review-editor-sync 2026-10-06 03:27 leaves the merges to the CFO's queue",
			"**Tests:**\n- The new Go tests failed before their code. Mutation checks showed they catch real breakage, such as a delivery that skips the recheck or a supervisor that trusts the sender's \"away\" flag.\n- The board's unit tests, lint and type check pass.\n- The PR body has screenshots of History and both card states.\n\nEvery brief item is now built. What's left is your calls on 366, 367, 369, 370 and 298 once 378 lands.",
			nil},
		{"cg-panel-clarity 2026-10-05 21:46 waits on the Overlord's page",
			"- **Polling:** I'm not polling the page myself; the supervisor does, and his answer comes through you.\n- **PR 375:** already reported done to you. It stays unmerged until he approves.\n\nI'm idle until his answer reaches me. If he picks Request changes, I'll make the changes on this branch, re-run the checks, update the page and file the wait again.",
			nil},
		{"cg-tidy-home 2026-10-05 23:38 resumes by itself on memory",
			"Still left:\n1. The full Go run (`go test ./... -p 4`).\n2. The frontend checks.\n3. Commit, PR and one CI run.\n\nI'll resume when you say so, or on my own once free memory reaches 6 GB, which gives the Go run room to stay above your 4 GB line.",
			nil},
		{"cg-verify-fast 2026-10-05 18:09 has a watch running",
			"The `internal/gatetest` run has started, and a watch will tell me if free memory drops under 3 GB. The `cmd/cfo` plan tests follow once it passes.",
			nil},
		{"cg-fleet-auto-resume 2026-10-05 16:30 reports PRs waiting for the merge",
			"I wrote the pause handoff and it is in place, so the task can stop now. Nothing needed pushing: the worktree is clean and parked on main.\n\n- **PRs:** 331 is merged. 324 and 326 are pushed, green on all 10 checks and reported done, waiting for your merge.\n- **Resume:** ready for 2026-10-09 22:27 UTC, when Codex image generation is back.",
			nil},
		{"cg-verify-fast 2026-10-05 18:09 reports a decision already made",
			"- **PRs 328 and 333** (hosted CI evidence reuse): parked per your decision, archive-tagged, with a backlog row naming what reuse needs.\n\nTwo problems on main, outside my PRs:\n- `TestAdmissionCapacityTwoNeverExceedsTwoConcurrentHolders` fails on main's own code on this workstation: it allows 5 seconds for six waiters. It passes in hosted CI.",
			nil},
		{"pd-ci-minutes 2026-10-05 20:46 waits on GitHub Actions",
			"Claude Code stopped my background status poll because the machine ran low on memory, the same thing that happened to the CI waiter. I haven't restarted it and I'm not polling. I've asked the CFO to message me when Actions is back to operational.\n\nWhen that message comes I'll:\n1. Re-run once: only #1488's cancelled jobs (`changes` and `verify` already passed), then the cancelled jobs on the two probe PRs.\n2. Fill in #1488's before/after billed-minutes table and the checks list for each kind of pull request.\n3. Close the probe PRs, delete their branches, and send the done notice.",
			nil},
		{"cg-fleet-auto-resume 2026-10-05 16:22 names the options marker in code",
			"The Command Center couldn't show the options on my mockup question, because I put the detail lines after `options:` and the parser read them as part of the last choice. Next time I'll put `options:` last.\n\nThe worktree is clean and parked on main. This task made no extra worktrees, so there's nothing to clean up.",
			nil},
		{"cg-panel-clarity 2026-10-06 02:22 waits to be told a PR landed",
			"Nothing else has changed. PR 375 is still waiting on cg-bundle-out-of-git, and I'll merge main in and remove my bundle changes once you tell me that PR has landed.",
			nil},
		{"cg-ci-green 2026-10-06 16:43 reports done", "PR 383 is green on every check. Reporting it done.", nil},
		{"cg-tidy-home 2026-10-06 16:19 reports done",
			"**Scratch:** I removed the build caches, so `state\\tasktmp\\cg-tidy-home` is now 6.3 MB.\n\nI've notified the CFO that the task is done.",
			nil},
		{"cg-site-download-hero-2 2026-10-06 00:51 notes the standing rule",
			"I've sent the done notice, and the handoff file now says the task is delivered.\n\nI've noted the new standing rule: I'll take the recommended option on reversible choices and only ask about the listed cases.",
			nil},
		{"cg-cfo-wakes-2 2026-10-05 15:44 plans to ask later",
			"Still to do: run the affected tests for each branch (one at a time in the shared slot), push, open or update the PRs, and send the done notify listing which extra worktrees the CFO can remove. The live Codex/pi compaction proof needs a CFO decision, because Codex's weekly allowance is used up and pi would spend OpenRouter credit. I'll ask that once, with options, after the PRs are up.",
			nil},
		{"cg-site-download-hero-2 (Codex) 2026-10-02 23:08 waits for a release",
			"The preview remains protected, as documented in the PR. CFO notified and handoff updated. Waiting for v0.4.0 and merge approval.",
			nil},
		{"cg-review-editor-sync 2026-10-05 17:13 waits on its tests", "Waiting on PR 298's Go tests before pushing it.", nil},
		{"cg-board-polish-4 2026-10-05 15:17 reports a review page still owed an answer",
			"- **PR 301** (choose a task's harness, model and effort in its panel): I merged current main into it. A fresh run of the Go tests is queued for the machine's one-at-a-time test slot. After that I'll push and run CI once.\n- **PR 301 still needs your answer:** your review page for it opened on Oct 3 but was withdrawn a few minutes later, so you never answered it. I've refreshed it and will reopen it as my last report. The brief says it merges only after your answer.\n- **Worktrees for the CFO to remove:** the voice-test one (PR 352 merged).\n\nItems 1 to 3, 4 to 4d, 4f and 5 of the brief were already merged before I took over (PRs 283, 284, 296).",
			nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			asked := askedIn(test.reply)

			// Assert
			if !slices.Equal(asked, test.asked) {
				t.Errorf("asked = %q, want %q", asked, test.asked)
			}
		})
	}
}
