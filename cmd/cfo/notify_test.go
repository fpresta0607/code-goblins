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
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr); exit != 0 {
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
	if exit := runNotify([]string{"g1", "--done"}, &stdout, &stderr); exit != 2 || !strings.Contains(stderr.String(), "--pr") {
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
	if exit := runNotify([]string{"g1"}, &stdout, &stderr); exit != 2 || !strings.Contains(stderr.String(), "exactly one") {
		t.Fatalf("exit=%d stderr=%q, want exactly-one refusal", exit, stderr.String())
	}
}

func TestNotifyTargetsStateOverrideWithoutCFOHome(t *testing.T) {
	worktree := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	t.Chdir(worktree)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr); exit != 0 {
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
	if exit := runNotify([]string{"g1", "--done", "--pr", "https://example.test/pr/1"}, &stdout, &stderr); exit != 0 {
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
	if exit := runNotify([]string{"g1", "--blocked", "Should I\nmerge this?"}, &stdout, &stderr); exit != 0 {
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
			if exit := runNotify([]string{"g1", "--blocked", "Which store? options: Postgres | SQLite"}, &stdout, &stderr); exit != 0 {
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
	// No Herdr pane, so the Command Center step stops before any Herdr call.
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
		if exit := runNotify(append([]string{"g1"}, c.args...), &stdout, &stderr); exit != c.exit {
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
	if exit := runNotify([]string{"g1", "--blocked", question, "--image", inside, "--image", filepath.Join(dir, "work", ".", "grid.png")}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%q, want the notify recorded", exit, stderr.String())
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "blocked: "+question {
		t.Fatalf("wake records = %+v %v, want the question", records, err)
	}
}

// Working, and waiting on another task, CI or a deploy, are status for the
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
		if exit := runNotify(c.args, &stdout, &stderr); exit != c.exit {
			t.Errorf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	for _, c := range []struct {
		args []string
		line string
	}{
		{[]string{"g1", "--working", "fixing the lint step"}, "working: fixing the lint step"},
		{[]string{"g1", "--waiting-on", "ci", "PR 45 checks"}, "waiting on ci: PR 45 checks"},
		{[]string{"g1", "--waiting-on", "board-ui", "its API contract"}, "waiting on board-ui: its API contract"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr); exit != 0 {
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
	if exit := runNotify([]string{"g1", "--waiting-on", "overlord", "log in to Stripe"}, &stdout, &stderr); exit != 0 {
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
		if exit := runNotify(c.args, &stdout, &stderr); exit != c.exit {
			t.Errorf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q after refused notifies, want nothing recorded", lines)
	}

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", page}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
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
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", page}, &stdout, &stderr)

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
