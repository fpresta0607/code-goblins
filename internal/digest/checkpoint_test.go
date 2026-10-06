package digest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// writeFile writes one fixture file, making its folder.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readCheckpoint writes the checkpoint of h at now and returns it.
func readCheckpoint(t *testing.T, h home.Home, now time.Time) string {
	t.Helper()
	if err := WriteCheckpoint(h, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(h.State, CheckpointFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// section is the text of checkpoint from header to the next section.
func section(t *testing.T, checkpoint, header string) string {
	t.Helper()
	_, rest, found := strings.Cut(checkpoint, header+"\n")
	if !found {
		t.Fatalf("the checkpoint has no %s section:\n%s", header, checkpoint)
	}
	body, _, _ := strings.Cut(rest, "\n== ")
	return body
}

// A checkpoint holds what a CFO loses to a compaction and can read back from
// disk: each goblin with its pull request and last status lines, what is
// paused and whether AFK mode is on, what waits on an answer, the wakes not
// yet acknowledged, and where the CFO's own handoff is.
func TestTheCheckpointHoldsTheFleetHoldsQuestionsAndWhatIsOwed(t *testing.T) {
	// Arrange
	h := newDigestHome(t)
	now := time.Date(2026, 10, 2, 18, 30, 0, 0, time.UTC)
	writeFile(t, filepath.Join(h.State, "cg-wakes.meta"), "goblin_id=cg-wakes\nharness=claude\nmodel=claude-opus-5-5\nkind=ship\nspawn_gen=s2\npr=https://github.com/o/r/pull/9\n")
	writeFile(t, filepath.Join(h.State, "cg-wakes.status"), "2026-10-02T18:00:00Z working: the first step\n2026-10-02T18:10:00Z working: the second step\n2026-10-02T18:20:00Z done: PR https://github.com/o/r/pull/9\n2026-10-02T18:25:00Z waiting on ci: the checks run\n")
	writeFile(t, filepath.Join(h.State, "cg-tickets.meta"), "goblin_id=cg-tickets\nharness=codex\nmodel=gpt-6-astra\nkind=ship\nspawn_gen=s5\n")
	for _, record := range []state.Lifecycle{
		{ID: "cg-tickets", Generation: "s5", Operation: "op-2", Action: "pause", Phase: "paused", Reason: "Requested by the operator"},
		// A pause of an earlier generation is over: the goblin runs again.
		{ID: "cg-wakes", Generation: "s1", Operation: "op-1", Action: "pause", Phase: "paused", Reason: "Requested by the operator"},
	} {
		if err := state.WriteLifecycle(h.State, record); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	saved, err := json.Marshal(supervisor.Database{
		Schema:    1,
		Questions: []supervisor.Question{{ID: "drop-legacy-invoices", Text: "Drop the legacy invoices table?", Status: "pending"}, {ID: "old", Text: "An answered question", Status: "succeeded"}},
		Reviews:   []supervisor.Review{{ID: "copy-review", Task: "cg-wakes", Title: "Look at the new copy", State: "open"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, ".supervisor.json"), string(saved))
	if _, err := wake.Append(h.State, "notify", "cg-wakes", "blocked: Which key do I use? options: The stored one | A new one"); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(h.State, "stale", "cg-tickets", "awaiting_answer: agent turn ended"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.Data, "cfo-handoff-2026-09-29.md"), "an older handoff\n")
	newest := filepath.Join(h.Data, "cfo-handoff-2026-10-01.md")
	writeFile(t, newest, "main is held for PR 235\n")
	if err := os.Chtimes(filepath.Join(h.Data, "cfo-handoff-2026-09-29.md"), now.Add(-72*time.Hour), now.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Act
	checkpoint := readCheckpoint(t, h, now)

	// Assert
	first, _, _ := strings.Cut(checkpoint, "\n== ")
	for _, want := range []string{"written 2026-10-02T18:30:00Z", "only what is on disk", "declared only in conversation"} {
		if !strings.Contains(first, want) {
			t.Errorf("the checkpoint's opening lacks %q:\n%s", want, first)
		}
	}
	fleet := section(t, checkpoint, "== FLEET ==")
	for _, want := range []string{"cg-wakes  claude/claude-opus-5-5  ship  https://github.com/o/r/pull/9", "working: the second step", "done: PR https://github.com/o/r/pull/9", "waiting on ci: the checks run", "cg-tickets  codex/gpt-6-astra  ship  no pull request"} {
		if !strings.Contains(fleet, want) {
			t.Errorf("FLEET lacks %q:\n%s", want, fleet)
		}
	}
	if strings.Contains(fleet, "the first step") {
		t.Errorf("FLEET kept more than a goblin's last three status lines:\n%s", fleet)
	}
	holds := section(t, checkpoint, "== HOLDS AND FREEZES ==")
	if !strings.Contains(holds, "AFK MODE IS ON") || !strings.Contains(holds, "cg-tickets: paused (Requested by the operator)") {
		t.Errorf("HOLDS AND FREEZES lacks AFK mode or the paused goblin:\n%s", holds)
	}
	if strings.Contains(holds, "cg-wakes") {
		t.Errorf("HOLDS AND FREEZES lists a pause of an earlier generation:\n%s", holds)
	}
	questions := section(t, checkpoint, "== OPEN QUESTIONS ==")
	for _, want := range []string{"question drop-legacy-invoices (the CFO): Drop the legacy invoices table?", "review copy-review (cg-wakes): Look at the new copy", "cg-wakes blocked: Which key do I use?"} {
		if !strings.Contains(questions, want) {
			t.Errorf("OPEN QUESTIONS lacks %q:\n%s", want, questions)
		}
	}
	if strings.Contains(questions, "An answered question") {
		t.Errorf("OPEN QUESTIONS lists an answered question:\n%s", questions)
	}
	owed := section(t, checkpoint, "== OWED ==")
	if !strings.Contains(owed, "2 wakes are not acknowledged") || !strings.Contains(owed, "awaiting_answer: agent turn ended") {
		t.Errorf("OWED lacks the unacknowledged wakes:\n%s", owed)
	}
	next := section(t, checkpoint, "== READ THIS NEXT ==")
	if !strings.Contains(next, newest) || strings.Contains(next, "cfo-handoff-2026-09-29.md") {
		t.Errorf("READ THIS NEXT does not name the newest handoff alone:\n%s", next)
	}
	if !strings.Contains(next, filepath.Join(h.State, FullDigestFile)) {
		t.Errorf("READ THIS NEXT does not name the long digest:\n%s", next)
	}
}

// An empty home's checkpoint says each section is empty rather than leaving
// a heading over nothing.
func TestACheckpointOfAnEmptyHomeSaysWhatIsEmpty(t *testing.T) {
	h := newDigestHome(t)

	checkpoint := readCheckpoint(t, h, time.Date(2026, 10, 2, 18, 30, 0, 0, time.UTC))

	for header, want := range map[string]string{
		"== FLEET ==":             "(no goblins in flight)",
		"== HOLDS AND FREEZES ==": "AFK mode is off.\nNo goblin is paused or stopped.",
		"== OPEN QUESTIONS ==":    "Nothing waits on the Overlord in the Command Center.\nNo goblin waits on your answer.",
		"== OWED ==":              "No wake waits to be acknowledged.",
		"== READ THIS NEXT ==":    "no cfo-handoff-*.md",
	} {
		if body := section(t, checkpoint, header); !strings.Contains(body, want) {
			t.Errorf("%s lacks %q:\n%s", header, want, body)
		}
	}
}

// A goblin that asked the CFO in prose waits on its answer as one that filed
// a blocked notify does, so the checkpoint lists its question rather than
// saying no goblin waits.
func TestACheckpointListsAQuestionAGoblinAskedInProse(t *testing.T) {
	// Arrange
	h := newDigestHome(t)
	if _, err := wake.Append(h.State, "stale", "cg-wakes", `goblin_asks: cg-wakes ended its turn asking in prose instead of with cfo notify --blocked and waits at its prompt for the answer; next: answer it with cfo send cg-wakes "<your answer>" (cfo answer takes only a notify's question). It asked: "Should I open the PR?"`); err != nil {
		t.Fatal(err)
	}

	// Act
	checkpoint := readCheckpoint(t, h, time.Now())

	// Assert
	questions := section(t, checkpoint, "== OPEN QUESTIONS ==")
	if !strings.Contains(questions, "cg-wakes Should I open the PR?") || strings.Contains(questions, "No goblin waits on your answer.") {
		t.Errorf("OPEN QUESTIONS does not list the prose question:\n%s", questions)
	}
}

// A file the checkpoint cannot read is named with its error in its own
// section, and the rest of the checkpoint is still written.
func TestACheckpointNamesWhatItCouldNotRead(t *testing.T) {
	h := newDigestHome(t)
	writeFile(t, filepath.Join(h.State, ".supervisor.json"), "{not json")

	checkpoint := readCheckpoint(t, h, time.Date(2026, 10, 2, 18, 30, 0, 0, time.UTC))

	if questions := section(t, checkpoint, "== OPEN QUESTIONS =="); !strings.Contains(questions, "Command Center: UNREADABLE (") {
		t.Errorf("OPEN QUESTIONS does not say the Command Center could not be read:\n%s", questions)
	}
	section(t, checkpoint, "== READ THIS NEXT ==")
}

func TestACheckpointNamesUnreadableInputsAndKeepsTheRemainingSections(t *testing.T) {
	for _, test := range []struct {
		name        string
		path        string
		content     string
		isDirectory bool
		header      string
		want        string
	}{
		{name: "status", path: "task-1.status", isDirectory: true, header: "== FLEET ==", want: "status UNREADABLE"},
		{name: "lifecycle", path: "lifecycle/task-1.json", content: "{broken", header: "== HOLDS AND FREEZES ==", want: "lifecycle UNREADABLE"},
		{name: "AFK", path: "afk.json", content: "{broken", header: "== HOLDS AND FREEZES ==", want: "AFK MODE: UNREADABLE"},
		{name: "queue", path: ".wake-queue", content: "{broken\n", header: "== OWED ==", want: "wake queue: UNREADABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newDigestHome(t)
			writeFile(t, filepath.Join(h.State, "task-1.meta"), "goblin_id=task-1\nharness=pi\nkind=ship\nspawn_gen=s1\n")
			path := filepath.Join(h.State, filepath.FromSlash(test.path))
			if test.isDirectory {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, path, test.content)
			}

			checkpoint := readCheckpoint(t, h, time.Now())

			if body := section(t, checkpoint, test.header); !strings.Contains(body, test.want) {
				t.Errorf("%s lacks %q:\n%s", test.header, test.want, body)
			}
			section(t, checkpoint, "== READ THIS NEXT ==")
		})
	}
}

func TestACheckpointListsOnlyCurrentLifecycleHolds(t *testing.T) {
	for _, phase := range []string{"pausing", "paused", "resuming", "stopping", "stopped", "running", "failed"} {
		t.Run(phase, func(t *testing.T) {
			h := newDigestHome(t)
			writeFile(t, filepath.Join(h.State, "task-1.meta"), "goblin_id=task-1\nspawn_gen=s1\n")
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "task-1", Generation: "s1", Operation: "op-1", Action: "pause", Phase: phase, Reason: "the operator chose it"}); err != nil {
				t.Fatal(err)
			}

			body := section(t, readCheckpoint(t, h, time.Now()), "== HOLDS AND FREEZES ==")

			shouldList := phase != "running" && phase != "failed"
			if strings.Contains(body, "task-1: "+phase) != shouldList {
				t.Errorf("lifecycle %s should be listed = %t:\n%s", phase, shouldList, body)
			}
		})
	}
}

// A pause whose goblin's metadata cannot be read is neither confirmed as the
// current generation's hold nor hidden behind a claim that nothing is paused.
func TestACheckpointDoesNotConfirmOrDenyAHoldWhoseMetadataIsUnreadable(t *testing.T) {
	// Arrange
	h := newDigestHome(t)
	target, link := t.TempDir(), filepath.Join(h.State, "task-1.meta")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	} else if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadMeta(filepath.Join(h.State, "task-1.meta")); err == nil {
		t.Fatal("premise: the metadata reads")
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "task-1", Generation: "s1", Operation: "op-1", Action: "pause", Phase: "paused", Reason: "the operator chose it"}); err != nil {
		t.Fatal(err)
	}

	// Act
	checkpoint := readCheckpoint(t, h, time.Now())

	// Assert
	holds := section(t, checkpoint, "== HOLDS AND FREEZES ==")
	if !strings.Contains(holds, "task-1: lifecycle paused not confirmed for its current spawn generation (metadata UNREADABLE)") {
		t.Errorf("HOLDS AND FREEZES does not report the unverified pause:\n%s", holds)
	}
	if strings.Contains(holds, "task-1: paused (") || strings.Contains(holds, "No goblin is paused or stopped.") {
		t.Errorf("HOLDS AND FREEZES confirms or denies a pause it cannot verify:\n%s", holds)
	}
	for _, header := range []string{"== FLEET ==", "== OPEN QUESTIONS ==", "== OWED ==", "== READ THIS NEXT =="} {
		section(t, checkpoint, header)
	}
}

func TestACheckpointKeepsAnsweredWakesWithoutReopeningTheirQuestions(t *testing.T) {
	h := newDigestHome(t)
	record, err := wake.Append(h.State, "notify", "task-1", "blocked: Which plan?")
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.MarkAnswered(h.State, record.Seq, wake.AnsweredByCFO, "Keep the plan"); err != nil {
		t.Fatal(err)
	}

	checkpoint := readCheckpoint(t, h, time.Now())

	if questions := section(t, checkpoint, "== OPEN QUESTIONS =="); strings.Contains(questions, "Which plan?") {
		t.Errorf("an answered wake reopens its question:\n%s", questions)
	}
	if owed := section(t, checkpoint, "== OWED =="); !strings.Contains(owed, "answered by cfo: Keep the plan") {
		t.Errorf("the pending wake lost its answer:\n%s", owed)
	}
	records, err := wake.Pending(h.State)
	if err != nil || len(records) != 1 || records[0].Seq != record.Seq || records[0].Answered != "Keep the plan" {
		t.Fatalf("checkpoint changed the wake queue: %v, %v", records, err)
	}
}

func TestWriteCheckpointReportsAWriteFailure(t *testing.T) {
	h := newDigestHome(t)
	if err := os.Mkdir(filepath.Join(h.State, CheckpointFile), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := WriteCheckpoint(h, time.Now()); err == nil {
		t.Fatal("checkpoint reported success when its path is a directory")
	}
}

// briefAfterCompact is the brief digest a session is handed after a
// compaction, by a session that holds the home.
func briefAfterCompact(t *testing.T, h home.Home) string {
	t.Helper()
	t.Cleanup(func() { _ = lock.Release(h.State) })
	var out strings.Builder
	if err := ComposeBrief(h, os.Getpid(), "s1", true, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// After a compaction the brief names the checkpoint ahead of the long digest,
// and still fits what a session is handed whole.
func TestTheBriefAfterACompactionNamesTheCheckpointFirst(t *testing.T) {
	h := bigHome(t, 40, 60)
	if err := WriteCheckpoint(h, time.Now()); err != nil {
		t.Fatal(err)
	}

	output := briefAfterCompact(t, h)

	if len(output) > Budget {
		t.Fatalf("the brief after a compaction is %d bytes, over its budget of %d", len(output), Budget)
	}
	next := section(t, output, "== READ THIS NEXT ==")
	checkpoint, long := strings.Index(next, filepath.Join(h.State, CheckpointFile)), strings.Index(next, filepath.Join(h.State, FullDigestFile))
	if checkpoint < 0 || long < 0 || checkpoint > long {
		t.Fatalf("READ THIS NEXT does not name the checkpoint ahead of the long digest:\n%s", next)
	}
}

// A session that starts without a compaction is not pointed at a checkpoint:
// one on disk is from some earlier compaction.
func TestTheBriefAtStartupNamesNoCheckpoint(t *testing.T) {
	h := bigHome(t, 3, 2)
	if err := WriteCheckpoint(h, time.Now()); err != nil {
		t.Fatal(err)
	}

	output := composeBrief(t, h)

	if strings.Contains(output, CheckpointFile) {
		t.Fatalf("a brief at startup names the checkpoint:\n%s", output)
	}
}

// A compaction the pre-compact hook did not run before is said plainly: the
// CFO is not handed an earlier compaction's checkpoint as this one's.
func TestTheBriefAfterACompactionSaysWhenNoFreshCheckpointWasWritten(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, h home.Home){
		"no checkpoint": func(*testing.T, home.Home) {},
		"not a file": func(t *testing.T, h home.Home) {
			if err := os.Mkdir(filepath.Join(h.State, CheckpointFile), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"an old checkpoint": func(t *testing.T, h home.Home) {
			if err := WriteCheckpoint(h, time.Now()); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(filepath.Join(h.State, CheckpointFile), old, old); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := bigHome(t, 3, 2)
			arrange(t, h)

			next := section(t, briefAfterCompact(t, h), "== READ THIS NEXT ==")

			if !strings.Contains(next, "no checkpoint was written before this compaction") || !strings.Contains(next, "cfo install") {
				t.Fatalf("READ THIS NEXT does not say the pre-compact hook did not run:\n%s", next)
			}
		})
	}
}
