package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestNotifyBlockedWritesStatusAndWakesTheCFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	stateDir := filepath.Join(dir, "state")
	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one blocked line", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one blocked line", lines, err)
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "notify" || records[0].Key != "g1" || records[0].Detail != "blocked: Should I merge this?" {
		t.Fatalf("wake records = %+v, want one notify carrying the question", records)
	}

	ep, err := wake.ReadEpisode(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 1 {
		t.Errorf("episode = %+v, want pending:1 so the CFO is actually woken", ep)
	}
}

func TestNotifyDoneRequiresPR(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--done"}, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), "--pr") {
		t.Fatalf("exit=%d stderr=%q, want --pr refusal", exit, stderr.String())
	}
}

func TestNotifyRequiresExactlyOneOutcome(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1"}, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), "exactly one") {
		t.Fatalf("exit=%d stderr=%q, want exactly-one refusal", exit, stderr.String())
	}
}

func TestNotifyTargetsStateOverrideWithoutCFOHome(t *testing.T) {
	worktree := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	// With no CFO_HOME the home is the per-user one, here the test's own.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Chdir(worktree)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one blocked line in the override dir", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one blocked line in the override dir", lines, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("notify polluted the worktree with a state dir: %v", err)
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "notify" {
		t.Fatalf("wake records = %+v, want one notify in the override dir", records)
	}
}

// TestNotifyTargetsStateOverrideWithAGlobalCFOHome covers what changes once
// CFO_HOME is set at user scope: a goblin pane inherits it, so a goblin that
// used to resolve its home to its own worktree now resolves it to the fleet
// root. The wake queue must still land in the state directory the spawn
// pinned, which is what CFO_STATE_OVERRIDE - not CFO_HOME - has always
// decided.
func TestNotifyTargetsStateOverrideWithAGlobalCFOHome(t *testing.T) {
	worktree := t.TempDir()
	fleetRoot := t.TempDir()
	stateDir := filepath.Join(fleetRoot, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", fleetRoot)
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	t.Chdir(worktree)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--done", "--pr", "https://example.test/pr/1"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one done line in the fleet state dir", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "done: PR https://example.test/pr/1" {
		t.Fatalf("status = %v, %v; want one done line in the fleet state dir", lines, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("notify polluted the worktree with a state dir: %v", err)
	}
}

func TestNotifyNormalizesControlCharactersInTheDetail(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I\nmerge this?"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	stateDir := filepath.Join(dir, "state")
	lines, err := state.TailStatus(stateDir, "g1", 5)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one normalized blocked line", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one normalized blocked line", lines, err)
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Detail != "blocked: Should I merge this?" {
		t.Fatalf("wake records = %+v, want one normalized notify detail", records)
	}
}

// On 2026-09-23 the CFO took three goblins' blocked questions as lost while
// cfo serve ran the board. Each had reached the wake queue the second its
// notify ran; only the CFO's rewake was missing. A blocked notify is in the
// queue when cfo notify returns, and on serve's board, whether or not serve
// holds the home.
func TestBlockedNotifyIsQueuedAtOnceWithAndWithoutServe(t *testing.T) {
	for _, serving := range []bool{false, true} {
		t.Run(map[bool]string{false: "without serve", true: "with serve"}[serving], func(t *testing.T) {
			dir := t.TempDir()
			h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
			if err := os.Mkdir(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", dir)
			t.Setenv("CFO_STATE_OVERRIDE", "")
			var board *supervisor.Service
			if serving {
				s, err := supervisor.Start(context.Background(), h, supervisor.Options{Example: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				board = s
			}

			var stdout, stderr bytes.Buffer
			start := time.Now()
			if exit := runNotify([]string{"g1", "--blocked", "Which store? options: Postgres | SQLite"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
				t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("cfo notify took %v, want it back at once", elapsed)
			}

			want := "blocked: Which store? options: Postgres | SQLite"
			records, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].Kind != "notify" || records[0].Key != "g1" || records[0].Detail != want || records[0].Time.Before(start.Add(-time.Second)) {
				t.Fatalf("wake records = %+v, want the question queued by the time notify returned", records)
			}
			if board != nil {
				snapshot, err := board.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(snapshot.Decisions, func(r wake.Record) bool { return r.Seq == records[0].Seq && r.Detail == want }) {
					t.Fatalf("board decisions = %+v, want serve to show the queued question", snapshot.Decisions)
				}
			}
		})
	}
}

// The Overlord, 2026-10-02: "it does seem like alert sent, complete delays
// can be improved to avoid lagging notifications". A report told the running
// supervisor nothing, so its boards showed it at their next refresh, up to
// fifteen seconds later. Every report tells it, and it tells its boards at
// once.
func TestNotifyTellsTheRunningSupervisorWhichTellsItsBoardsAtOnce(t *testing.T) {
	for name, args := range map[string][]string{
		"at work": {"g1", "--working", "the lint step"},
		"failed":  {"g1", "--failed", "the build broke"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: a supervisor that has gone quiet after starting, well
			// before its next refresh is due.
			dir := t.TempDir()
			h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
			if err := os.Mkdir(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", dir)
			t.Setenv("CFO_STATE_OVERRIDE", "")
			board, err := supervisor.Start(context.Background(), h, supervisor.Options{Example: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(board.Close)
			started := time.Now()
			before := board.Revision()
			for quiet := time.Now(); time.Since(quiet) < 2*time.Second; time.Sleep(50 * time.Millisecond) {
				if now := board.Revision(); now != before {
					before, quiet = now, time.Now()
				}
			}
			if since := time.Since(started); since > 8*time.Second {
				t.Skipf("the supervisor took %s to go quiet, too near its refresh to tell a report from it", since.Round(time.Second))
			}

			// Act
			var stdout, stderr bytes.Buffer
			if exit := runNotify(args, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
				t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
			}

			// Assert
			for deadline := time.Now().Add(3 * time.Second); board.Revision() == before; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the supervisor told its boards nothing within three seconds of the report")
				}
			}
		})
	}
}

// A choice is the answer itself, because the Overlord picks from a plain list
// of answers: a choice that is only a letter or number is refused before
// anything is recorded, and answers written as phrases are not.
func TestNotifyRefusesAChoiceThatIsOnlyALetterOrNumber(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	for _, question := range []string{
		"Merge now? options: a | b",
		"Merge now? options: a (Recommended) | b",
		"Merge now? options: Merge now (Recommended) | 2",
		"Merge now? options: (c) | Wait",
		"Merge now? options: B) | Wait",
		"Merge now? options: 12 | Wait",
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify([]string{"g1", "--blocked", question}, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), "write the answer itself") {
			t.Fatalf("%q: exit=%d stderr=%q, want it refused with how to write the answer", question, exit, stderr.String())
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("a refused notify recorded status %q", lines)
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
		t.Fatalf("a refused notify woke the CFO: %+v %v", records, err)
	}
	question := "Merge now? options: Fix it next (Recommended) | Keep 300 s | A plan"
	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", question}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%q, want answers written as phrases recorded", exit, stderr.String())
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "blocked: "+question {
		t.Fatalf("wake records = %+v %v, want the question", records, err)
	}
}

// Review images are checked before anything is recorded: a wrong count or a
// path outside the task fails the whole notify, so the CFO is never woken
// with a question whose images the Overlord cannot see.
func TestNotifyImagesAreCheckedBeforeAnythingIsRecorded(t *testing.T) {
	dir := t.TempDir()
	stateDir, worktree := filepath.Join(dir, "state"), filepath.Join(dir, "work")
	for _, d := range []string{stateDir, worktree} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CFO_HOME", dir)
	// No native terminal, so the Command Center step stops at the goblin's
	// proof.
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "g1", Project: worktree, Worktree: worktree, Harness: "codex", Mode: "no-mistakes", Kind: "ship", SpawnGen: "g1"}); err != nil {
		t.Fatal(err)
	}
	var shot bytes.Buffer
	if err := png.Encode(&shot, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	inside, outside := filepath.Join(worktree, "grid.png"), filepath.Join(t.TempDir(), "list.png")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, shot.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	question := "Which layout? options: Grid | List"
	for name, c := range map[string]struct {
		args []string
		exit int
	}{
		"a failed notify":   {[]string{"--failed", question, "--image", inside, "--image", inside}, 2},
		"no choices":        {[]string{"--blocked", "Which layout?", "--image", inside}, 2},
		"one image too few": {[]string{"--blocked", question, "--image", inside}, 2},
		"outside the task":  {[]string{"--blocked", question, "--image", inside, "--image", outside}, 1},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(append([]string{"g1"}, c.args...), &stdout, &stderr, defaultCommandRuntime()); exit != c.exit {
			t.Fatalf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("a refused notify recorded status %q", lines)
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
		t.Fatalf("a refused notify woke the CFO: %+v %v", records, err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", question, "--image", inside, "--image", filepath.Join(dir, "work", ".", "grid.png")}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%q, want the notify recorded", exit, stderr.String())
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "blocked: "+question {
		t.Fatalf("wake records = %+v %v, want the question", records, err)
	}
}

// Working, and waiting on another task, CI, a deploy or memory, are status for the
// board and wake nobody; waiting on the Overlord wakes the CFO like a
// question, and a wait needs a valid target and a reason.
func TestNotifyWorkingAndWaitingOnWakeOnlyForTheOverlord(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	for name, c := range map[string]struct {
		args []string
		exit int
	}{
		"waiting on itself":    {[]string{"g1", "--waiting-on", "g1", "on me"}, 2},
		"waiting on a bad ID":  {[]string{"g1", "--waiting-on", "not/a/task", "why"}, 2},
		"waiting without why":  {[]string{"g1", "--waiting-on", "ci"}, 2},
		"working and done":     {[]string{"g1", "--working", "lint", "--done", "--pr", "https://example.com/pr/1"}, 2},
		"working with a stray": {[]string{"g1", "--working", "lint", "extra"}, 2},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != c.exit {
			t.Errorf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	for _, c := range []struct {
		args []string
		line string
	}{
		{[]string{"g1", "--working", "fixing the lint step"}, "working: fixing the lint step"},
		{[]string{"g1", "--waiting-on", "ci", "PR 45 checks"}, "waiting on ci: PR 45 checks"},
		// memory_ready names a goblin by this very line.
		{[]string{"g1", "--waiting-on", "memory", "the build needs 4 GB"}, "waiting on memory: the build needs 4 GB"},
		{[]string{"g1", "--waiting-on", "board-ui", "its API contract"}, "waiting on board-ui: its API contract"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
			t.Fatalf("%v: exit=%d stderr=%q", c.args, exit, stderr.String())
		}
		lines, err := state.TailStatus(stateDir, "g1", 1)
		if _, event := state.SplitStatus(lines[0]); err != nil || event != c.line {
			t.Fatalf("status = %q %v, want %q", lines, err, c.line)
		}
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
		t.Fatalf("wake records = %+v %v, want none for working or a wait on a task or CI", records, err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--waiting-on", "overlord", "log in to Stripe"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "waiting on overlord: log in to Stripe" {
		t.Fatalf("wake records = %+v %v, want the wait on the Overlord", records, err)
	}
}

// fakeLavish puts a stand-in lavish-axi alone on PATH that opens any page at
// a fixed address.
func fakeLavish(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "@echo off\r\necho session:\r\necho   url: \"http://127.0.0.1:4387/session/f26e\"\r\necho   status: opened\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
}

// A wait on the Overlord can name the Lavish page he answers on: the page is
// checked and opened before anything is recorded, and its link travels with
// the wait to the CFO.
func TestNotifyWaitNamesTheLavishPageTheOverlordAnswersOn(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	page := filepath.Join(dir, "plan.html")
	notes := filepath.Join(dir, "notes.txt")
	for _, path := range []string{page, notes} {
		if err := os.WriteFile(path, []byte("<html></html>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fakeLavish(t)
	for name, c := range map[string]struct {
		args []string
		exit int
	}{
		"a page with a question":  {[]string{"g1", "--blocked", "which plan?", "--lavish", page}, 2},
		"a page with a CI wait":   {[]string{"g1", "--waiting-on", "ci", "checks", "--lavish", page}, 2},
		"a page that is missing":  {[]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", filepath.Join(dir, "gone.html")}, 2},
		"a page that is not HTML": {[]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", notes}, 2},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != c.exit {
			t.Errorf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q after refused notifies, want nothing recorded", lines)
	}

	// g1 runs in no native terminal, so the wait's item cannot be published and
	// nothing would watch the page: the notify fails loudly, and the CFO still
	// has the wait.
	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", page}, &stdout, &stderr, defaultCommandRuntime())

	if exit != 1 || !strings.Contains(stderr.String(), "nothing watches the page "+page) || !strings.Contains(stderr.String(), "ask in text with --blocked") {
		t.Fatalf("exit=%d stderr=%q, want a failure naming the unwatched page and saying to ask in text", exit, stderr.String())
	}
	want := "waiting on overlord: pick a plan (page http://127.0.0.1:4387/session/f26e)"
	lines, err := state.TailStatus(stateDir, "g1", 1)
	if _, event := state.SplitStatus(lines[0]); err != nil || event != want {
		t.Fatalf("status = %q %v, want %q", lines, err, want)
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != want {
		t.Fatalf("wake records = %+v %v, want the wait with its page", records, err)
	}
}

// Without lavish-axi the page cannot be shown, so the notify is refused before
// anything is recorded and the goblin asks in text instead.
func TestNotifyWaitWithAPageIsRefusedWithoutLavish(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	page := filepath.Join(dir, "plan.html")
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", page}, &stdout, &stderr, defaultCommandRuntime())

	if exit != 1 || !strings.Contains(stderr.String(), "ask in text with --blocked") {
		t.Fatalf("exit=%d stderr=%q, want a refusal that says to ask in text", exit, stderr.String())
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q, want nothing recorded", lines)
	}
	if records, _ := wake.Pending(stateDir); len(records) != 0 {
		t.Fatalf("wake records = %+v, want none", records)
	}
}

// A wait on the Overlord names the one link he goes to with --link, which his
// card opens: it travels with the wait to the CFO, and a link that is not a
// safe web link, or one on anything but a wait on him, is refused before
// anything is recorded.
func TestNotifyWaitGivesTheLinkTheOverlordGoesTo(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	link := "https://dash.cloudflare.com/precisiondocs/dns"
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"a link on a question":  {[]string{"g1", "--blocked", "which plan?", "--link", link}, "--link goes with --waiting-on overlord"},
		"a link on a CI wait":   {[]string{"g1", "--waiting-on", "ci", "checks", "--link", link}, "--link goes with --waiting-on overlord"},
		"a bare host name":      {[]string{"g1", "--waiting-on", "overlord", "add the records", "--link", "mcp.precisiondocs.ai"}, "absolute URL"},
		"a link with a query":   {[]string{"g1", "--waiting-on", "overlord", "add the records", "--link", link + "?token=x"}, "query"},
		"a plain http web link": {[]string{"g1", "--waiting-on", "overlord", "add the records", "--link", "http://dash.cloudflare.com/dns"}, "https"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want 2 naming %q", name, exit, stderr.String(), c.says)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q after refused notifies, want nothing recorded", lines)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "add the records", "--link", link}, &stdout, &stderr, defaultCommandRuntime())

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	want := "waiting on overlord: add the records (link " + link + ")"
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != want {
		t.Fatalf("wake records = %+v %v, want the wait with its link", records, err)
	}
}

// A wait or a question can carry a command for the Overlord to run with one
// click (his words, 2026-10-02: "run in powershell button"): --run names its
// file, checked before anything is recorded, and the wake tells the CFO a
// command rides with it.
func TestNotifyCarriesACommandForTheOverlordToRun(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	command := write("sign-in.ps1", "gh auth login\n")
	page := write("plan.html", "<html></html>")
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"a command on a CI wait":        {[]string{"g1", "--waiting-on", "ci", "checks", "--run", command}, "--run goes with --waiting-on overlord or --blocked"},
		"a command on a working report": {[]string{"g1", "--working", "building", "--run", command}, "--run goes with --waiting-on overlord or --blocked"},
		"a command beside a page":       {[]string{"g1", "--waiting-on", "overlord", "sign in", "--lavish", page, "--run", command}, "--run and --lavish"},
		"a file that is not there":      {[]string{"g1", "--waiting-on", "overlord", "sign in", "--run", filepath.Join(dir, "missing.ps1")}, "--run"},
		"a file of another kind":        {[]string{"g1", "--waiting-on", "overlord", "sign in", "--run", write("sign-in.txt", "gh auth login\n")}, ".ps1"},
		"an empty command":              {[]string{"g1", "--waiting-on", "overlord", "sign in", "--run", write("empty.ps1", "  \n")}, "empty"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want 2 naming %q", name, exit, stderr.String(), c.says)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q after refused notifies, want nothing recorded", lines)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "Sign in to GitHub so I can push", "--run", command}, &stdout, &stderr, defaultCommandRuntime())

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	want := "waiting on overlord: Sign in to GitHub so I can push (runs sign-in.ps1)"
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != want {
		t.Fatalf("wake records = %+v %v, want the wait naming its command", records, err)
	}
	// No goblin runs here, so the board cannot take the command, and the
	// notify says so instead of failing: the CFO has the wait.
	if !strings.Contains(stderr.String(), "the Command Center cannot show this command") {
		t.Errorf("stderr = %q, want it said that the command is not on the board", stderr.String())
	}
}

// While AFK mode is on a goblin that reports a wait on the Overlord is told he
// is away and to move to work that does not depend on it, so it does not sit
// all night on something only he can do. While it is off the notify says
// nothing of it.
func TestNotifyTellsAGoblinWaitingOnTheOverlordToMoveOnWhileAFKModeIsOn(t *testing.T) {
	for name, away := range map[string]bool{"on": true, "off": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			stateDir := filepath.Join(dir, "state")
			if err := os.Mkdir(stateDir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", dir)
			if away {
				if _, _, err := afk.TurnOn(stateDir, "the board", nil, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify([]string{"g1", "--waiting-on", "overlord", "log in to Stripe"}, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 0 || !strings.Contains(stdout.String(), "notified g1 waiting on overlord: log in to Stripe") {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want the wait reported", exit, stdout.String(), stderr.String())
			}
			told := strings.Contains(stdout.String(), "AFK mode is on") && strings.Contains(stdout.String(), "move to that next piece") && strings.Contains(stdout.String(), `cfo notify g1 --working`)
			if told != away {
				t.Errorf("stdout = %q, want the goblin told to move on only while AFK mode is on (%v)", stdout.String(), away)
			}
		})
	}
}

// helperHome is a home where goblin g1 has helper g1-h1, with a runtime that
// records what is typed into a terminal instead of typing it, and fails the
// way sendErr says.
func helperHome(t *testing.T, sendErr error) (home.Home, commandRuntime, *[][2]string) {
	t.Helper()
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []state.TaskMeta{{ID: "g1"}, {ID: "g1-h1", Parent: "g1", Mode: "local-only"}} {
		meta.Window, meta.Harness, meta.Kind, meta.Backend, meta.Worktree = "native", "claude", "ship", "native", filepath.Join(h.Root, meta.ID)
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
	}
	runtime := testCommandRuntimeForHome(h)
	var typed [][2]string
	runtime.sendText = func(_ context.Context, _ home.Home, target, text string) error {
		typed = append(typed, [2]string{target, text})
		return sendErr
	}
	return h, runtime, &typed
}

func TestAHelpersReportsReachItsParentAndNotTheCFO(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"g1-h1", "--done"}, []string{"Your helper g1-h1 is done", "cfo helper merge g1"}},
		{[]string{"g1-h1", "--blocked", "Which table?\n- users or accounts"}, []string{"Your helper g1-h1 asks: Which table?", "cfo send g1-h1"}},
		{[]string{"g1-h1", "--failed", "the build is broken"}, []string{"Your helper g1-h1 failed: the build is broken", "cfo kill g1-h1"}},
	}
	for _, c := range cases {
		t.Run(c.args[1], func(t *testing.T) {
			// Arrange
			h, runtime, typed := helperHome(t, nil)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify(c.args, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 {
				t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
			}
			if len(*typed) != 1 || (*typed)[0][0] != "g1" {
				t.Fatalf("typed = %q, want one report in g1's terminal", *typed)
			}
			for _, want := range c.want {
				if !strings.Contains((*typed)[0][1], want) {
					t.Errorf("the report %q does not say %q", (*typed)[0][1], want)
				}
			}
			if strings.Contains((*typed)[0][1], "\n") {
				t.Errorf("the report %q spans lines; a line break would submit it early", (*typed)[0][1])
			}
			if records, err := wake.Pending(h.State); err != nil || len(records) != 0 {
				t.Errorf("wake records = %+v, %v; want the CFO left out of a helper's report", records, err)
			}
			if lines, _ := state.TailStatus(h.State, "g1-h1", 1); len(lines) != 1 {
				t.Errorf("status = %v, want the report on the helper's own log", lines)
			}
		})
	}
}

func TestAHelpersReportWakesTheCFOWhenItsParentCannotTakeIt(t *testing.T) {
	// Arrange
	h, runtime, _ := helperHome(t, errors.New("g1's terminal has ended"))
	var stdout, stderr bytes.Buffer

	// Act
	exit := runNotify([]string{"g1-h1", "--done"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	records, err := wake.Pending(h.State)
	if err != nil || len(records) != 1 || records[0].Key != "g1-h1" || !strings.Contains(records[0].Detail, "could not reach its parent g1") || !strings.Contains(records[0].Detail, "terminal has ended") {
		t.Errorf("wake records = %+v, %v; want the undelivered report woken to the CFO with why", records, err)
	}
}

func TestOnlyAHelperReportsDoneWithoutAPullRequest(t *testing.T) {
	// Arrange
	h, runtime, typed := helperHome(t, nil)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runNotify([]string{"g1", "--done"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "--pr") || len(*typed) != 0 {
		t.Errorf("exit = %d, stderr = %q, typed = %q; want the parent's done refused without --pr", exit, stderr.String(), *typed)
	}
	if lines, _ := state.TailStatus(h.State, "g1", 1); len(lines) != 0 {
		t.Errorf("status = %v, want nothing recorded for a refused notify", lines)
	}
}

func TestAHelpersWorkingReportStaysOnTheBoard(t *testing.T) {
	_, runtime, typed := helperHome(t, nil)
	var stdout, stderr bytes.Buffer

	exit := runNotify([]string{"g1-h1", "--working", "writing the migration"}, &stdout, &stderr, runtime)

	if exit != 0 || len(*typed) != 0 {
		t.Errorf("exit = %d, typed = %q; want a working report recorded and typed nowhere", exit, *typed)
	}
}
