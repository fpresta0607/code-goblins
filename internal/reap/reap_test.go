package reap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type fixedInventory Inventory

func (f fixedInventory) Collect(context.Context) (Inventory, []string, error) {
	return Inventory(f), nil, nil
}

// gitRunner answers the questions the worktree gate and the worktree return
// ask, and refuses anything else, so a gate that started shelling out to
// something new fails loudly instead of silently passing.
type gitRunner struct {
	status string
	// statusFailure is what git prints when the question cannot be answered at
	// all, such as a concurrent git holding index.lock.
	statusFailure string
	unpushed      string
	killed        []int
	// pruned records the directory each git worktree prune was asked of, which
	// is the only evidence that a removed registration was actually cleared.
	pruned       []string
	pruneFailure string
	// toplevel is what git answers when asked whose repository it speaks for
	// at a path. Empty means it answers for the path it was asked from, which
	// is a real worktree; a fixture whose directory is not one sets the
	// repository that would really answer instead.
	toplevel string
}

func (r *gitRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if req.Name != "git" {
		return execx.Result{}, os.ErrInvalid
	}
	switch req.Args[0] {
	case "rev-parse":
		if r.toplevel != "" {
			return execx.Result{Stdout: []byte(r.toplevel)}, nil
		}
		return execx.Result{Stdout: []byte(req.Dir)}, nil
	case "status":
		if r.statusFailure != "" {
			return execx.Result{ExitCode: 128, Stderr: []byte(r.statusFailure)}, nil
		}
		return execx.Result{Stdout: []byte(r.status)}, nil
	case "log":
		return execx.Result{Stdout: []byte(r.unpushed)}, nil
	case "worktree":
		if len(req.Args) != 2 || req.Args[1] != "prune" {
			return execx.Result{}, os.ErrInvalid
		}
		r.pruned = append(r.pruned, req.Dir)
		if r.pruneFailure != "" {
			return execx.Result{ExitCode: 1, Stderr: []byte(r.pruneFailure)}, nil
		}
		return execx.Result{}, nil
	}
	return execx.Result{}, os.ErrInvalid
}

func testHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return home.Home{Root: root, State: stateDir, Data: filepath.Join(root, "data")}
}

func newService(t *testing.T, h home.Home, inventory Inventory, runner *gitRunner) Service {
	t.Helper()
	return Service{
		Home:      h,
		Inventory: fixedInventory(inventory),
		Commands:  runner,
		Sleep:     func(time.Duration) {},
		Now:       func() time.Time { return fixtureStart },
		Kill: func(_ context.Context, pid int) error {
			runner.killed = append(runner.killed, pid)
			return nil
		},
	}
}

func onlyFinding(t *testing.T, result Result, class Class) Finding {
	t.Helper()
	findings := classOf(result.Findings, class)
	if len(findings) != 1 {
		t.Fatalf("got %d %s findings, want 1: %+v", len(findings), class, result.Findings)
	}
	return findings[0]
}

func orphanProcessInventory() Inventory {
	return Inventory{
		FleetRootPIDs: []int{100},
		Processes: []Process{
			process(100, 1, "herdr.exe", "herdr server", fixtureStart),
			process(400, 100, "powershell.exe", "powershell", fixtureLater),
			process(31032, 400, "claude.exe", "claude --dangerously-skip-permissions", fixtureLatest),
		},
	}
}

// TestANamedBusyProcessIsKilledAndTheMeasurementReported is the lesson the
// fleet already paid for: a goblin waiting on an API response looks idle by
// log age but is not by processor time, so idleness is measured, never
// inferred. Naming the pid has always meant killing that process even if it is
// busy, so the measurement is reported beside the kill rather than refusing it.
func TestANamedBusyProcessIsKilledAndTheMeasurementReported(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	samples := []time.Duration{2 * time.Second, 4 * time.Second}
	call := 0
	service.CPU = func(int) (time.Duration, bool) {
		sample := samples[call]
		call++
		return sample, true
	}

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"31032": true}})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, result, OrphanProcess)
	if !strings.Contains(finding.Detail, "busy") {
		t.Fatalf("detail = %q, want the measurement reported beside the kill", finding.Detail)
	}
	if len(runner.killed) != 1 || runner.killed[0] != 31032 {
		t.Fatalf("killed = %v, want [31032]; a named pid is killed even when the measurement says it is busy", runner.killed)
	}
}

// A process the operator has not named is not killed, and the measurement is
// still taken: the report is what the operator reads to decide which pid to
// name, so the one fact that separates a live process from an abandoned one
// has to be in it rather than appearing only on the run that ends it.
func TestAnUnnamedProcessIsMeasuredAndLeftAlone(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	samples := []time.Duration{2 * time.Second, 4 * time.Second}
	call := 0
	service.CPU = func(int) (time.Duration, bool) {
		sample := samples[call]
		call++
		return sample, true
	}

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, result, OrphanProcess)
	if !strings.Contains(finding.Detail, "busy") {
		t.Fatalf("detail = %q, want the measurement in the report the operator decides from", finding.Detail)
	}
	if !strings.Contains(finding.Hold(), "31032") {
		t.Fatalf("hold = %q, want it to name the pid that authorises the kill", finding.Hold())
	}
	if len(runner.killed) != 0 {
		t.Fatalf("killed = %v, want nothing; no pid was named", runner.killed)
	}
}

// TestANamedUnmeasurableProcessIsKilledAndSaysSo: not being able to read
// processor time is not evidence of idleness, so the report says the
// measurement failed. It still does not refuse a kill the operator authorised
// by naming the pid.
func TestANamedUnmeasurableProcessIsKilledAndSaysSo(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	service.CPU = func(int) (time.Duration, bool) { return 0, false }

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"31032": true}})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanProcess); !strings.Contains(finding.Detail, "could not be read") {
		t.Fatalf("detail = %q, want the failed measurement reported", finding.Detail)
	}
	if len(runner.killed) != 1 || runner.killed[0] != 31032 {
		t.Fatalf("killed = %v, want [31032]; an unreadable measurement refuses nothing once the pid is named", runner.killed)
	}
}

func TestApplyKillsAnIdleOrphanAndRecordsIt(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	service.CPU = func(int) (time.Duration, bool) { return 2 * time.Second, true }

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"31032": true}})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanProcess); finding.Hold() != "" {
		t.Fatalf("an orphan whose pid the operator named was held: %q", finding.Hold())
	}
	if len(runner.killed) != 1 || runner.killed[0] != 31032 {
		t.Fatalf("killed = %v, want [31032]", runner.killed)
	}
	log, err := state.TailStatus(h.State, StatusID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || !strings.Contains(log[0], "orphan_process pid=31032") {
		t.Fatalf("reap log = %q, want one line naming the reaped process", log)
	}
	// AppendStatus stamps every event now, so the reaper must not stamp the
	// fleet log a second time: the event that follows the stamp has to be the
	// finding itself, not another stamp.
	recorded, event := state.SplitStatus(log[0])
	if recorded.IsZero() {
		t.Fatalf("reap log line %q carries no stamp", log[0])
	}
	if _, err := time.Parse(time.RFC3339, strings.SplitN(event, " ", 2)[0]); err == nil {
		t.Fatalf("reap log line %q is stamped twice", log[0])
	}
}

// TestForceNamesOneProcess proves --force is never a blanket override: the
// named pid is killed and the unnamed busy one beside it is not.
func TestForceNamesOneProcess(t *testing.T) {
	h := testHome(t)
	inventory := orphanProcessInventory()
	inventory.Processes = append(inventory.Processes,
		process(31033, 400, "claude.exe", "claude --dangerously-skip-permissions", fixtureLatest))
	runner := &gitRunner{}
	service := newService(t, h, inventory, runner)
	service.CPU = func(int) (time.Duration, bool) { return 0, false }

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"31032": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.killed) != 1 || runner.killed[0] != 31032 {
		t.Fatalf("killed = %v, want only the forced pid", runner.killed)
	}
	for _, finding := range classOf(result.Findings, OrphanProcess) {
		if finding.PID == 31033 && finding.Hold() == "" {
			t.Fatal("an unnamed unmeasurable process was not held")
		}
	}
}

// TestATaskForceNeverClearsARefusalAboutAProcess: a task id says that task is
// over and can say nothing about whether the sweep's view of what is running
// is complete. A pane that cannot report its processes leaves a live goblin
// indistinguishable from an orphan, and that refusal answers to the pid alone,
// so forcing the unreadable task behind this server must not kill it.
func TestATaskForceNeverClearsARefusalAboutAProcess(t *testing.T) {
	const worktree = `C:\dev\pd\.worktrees\gb-broken`
	inventory := Inventory{
		Panes:           []Pane{{ID: "pane-b", HasAgent: true}},
		UnresolvedPanes: []string{"pane-b"},
		UnreadableTasks: []string{"broken"},
		Worktrees:       []WorktreeDir{{Path: worktree, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "broken"}},
		Processes:       []Process{process(555, 1, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest)},
	}
	runner := &gitRunner{}
	service := newService(t, testHome(t), inventory, runner)
	samples := []time.Duration{2 * time.Second, 4 * time.Second}
	call := 0
	service.CPU = func(int) (time.Duration, bool) {
		sample := samples[call]
		call++
		return sample, true
	}

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"broken": true}})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, result, StaleServer)
	if hold := finding.Hold(); !strings.Contains(hold, "process identity") {
		t.Fatalf("hold = %q, want the unresolved-pane refusal to survive a task force", hold)
	}
	if len(runner.killed) != 0 {
		t.Fatalf("a task force killed a process the sweep could not account for: %v", runner.killed)
	}
	// A held finding is exactly what the operator reads to decide whether to
	// name this pid, so the one number that separates a live process from an
	// abandoned one has to be in it.
	if !strings.Contains(finding.Detail, "busy") {
		t.Fatalf("detail = %q, want the measurement reported on the held finding", finding.Detail)
	}
}

// populatedWorktree is a directory that holds a file, which is what makes it a
// worktree rather than a shell. The work gate asserts that premise before it
// believes any git answer about the path, so a fixture pointing at a directory
// that never existed would be testing the premise failure, not the work gate.
func populatedWorktree(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".worktrees", name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "tracked.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func worktreeInventory(t *testing.T, verb string) Inventory {
	path := populatedWorktree(t, "gb-old")
	inventory := Inventory{
		Worktrees: []WorktreeDir{{Path: path, Project: filepath.Dir(filepath.Dir(path)), Registration: RegistrationListed, TaskID: "old"}},
	}
	if verb != "" {
		inventory.Tasks = []Task{task("old", path, "pane-gone", verb)}
	}
	return inventory
}

func TestGateRefusesDirtyWorktree(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{status: " M internal/thing.go"}
	service := newService(t, h, worktreeInventory(t, "done"), runner)

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold(), "uncommitted") {
		t.Fatalf("hold = %q, want an uncommitted-work refusal", finding.Hold())
	}
}

func TestGateRefusesUnpushedBranch(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{unpushed: "abc1234 feat: the whole product of the run"}
	service := newService(t, h, worktreeInventory(t, "done"), runner)

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold(), "no remote") {
		t.Fatalf("hold = %q, want an unpushed-work refusal", finding.Hold())
	}
}

// TestForceNeverClearsTheWorkGate: --force covers the operator's judgement
// about idleness and about a task's status. It never covers destroying work.
func TestForceNeverClearsTheWorkGate(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{unpushed: "abc1234 feat: the whole product of the run"}
	service := newService(t, h, worktreeInventory(t, "working"), runner)

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"old": true}})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold(), "no remote") {
		t.Fatalf("hold = %q, want the unpushed-work refusal to survive --force", finding.Hold())
	}
}

// TestWorkGateSaysItCouldNotLookRatherThanThatItFoundWork: an unreadable git
// status is not a dirty worktree. The refusal stands and no --force clears
// it, because unreadable is not clean, but the line has to say the sweep
// could not establish the state and name resolving that as the remedy instead
// of stating a rule with nothing to do about it.
func TestWorkGateSaysItCouldNotLookRatherThanThatItFoundWork(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{statusFailure: "fatal: Unable to create '.git/index.lock': File exists"}
	inventory := worktreeInventory(t, "done")
	worktree := inventory.Worktrees[0].Path
	service := newService(t, h, inventory, runner)
	var cleaned []string
	service.Clean = func(_ context.Context, id string, _ bool) error {
		cleaned = append(cleaned, id)
		return nil
	}

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"old": true}})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, result, OrphanWorktree)
	if strings.Contains(finding.Hold(), "has uncommitted or untracked changes") {
		t.Fatalf("hold = %q, claims it found work when it could not read git status", finding.Hold())
	}
	for _, want := range []string{"could not read git status", "unknown rather than answered", "resolve that, then sweep again", "No --force clears this"} {
		if !strings.Contains(finding.Hold(), want) {
			t.Fatalf("hold = %q, want it to contain %q", finding.Hold(), want)
		}
	}
	if len(cleaned) != 0 || len(result.Applied) != 0 {
		t.Fatalf("cleaned = %v, applied = %v, want a --force on the task id to remove nothing", cleaned, result.Applied)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("the worktree was removed behind an unreadable git status: %v", err)
	}
}

func TestGateRefusesNonTerminalTask(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, worktreeInventory(t, "working"), runner)

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold(), "terminal status") {
		t.Fatalf("hold = %q, want a terminal-status refusal", finding.Hold())
	}
}

func TestApplyReturnsACleanOrphanWorktree(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, worktreeInventory(t, "done"), runner)
	var cleaned []string
	service.Clean = func(_ context.Context, id string, force bool) error {
		cleaned = append(cleaned, id)
		return nil
	}
	// A meta beside the record is what routes the removal through cfo cleanup
	// rather than through the bare worktree return.
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{
		ID: "old", Backend: "herdr", Project: `C:\dev\pd`, Worktree: `C:\dev\pd\.worktrees\gb-old`,
		HerdrSession: "default", HerdrWorkspaceID: "w", HerdrTabID: "t", HerdrPaneID: "p",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); finding.Hold() != "" {
		t.Fatalf("a clean, finished worktree was held: %q", finding.Hold())
	}
	if len(cleaned) != 1 || cleaned[0] != "old" {
		t.Fatalf("cleaned = %v, want [old] through the cfo cleanup path", cleaned)
	}
}

// TestTheReapedLineOutlivesTheRecordItRetires: the action for a worktree and
// for a meta is a cleanup, and cleanup removes state/<id>.meta, so asking
// after the action whether the task had a record answers no for exactly the
// tasks the sweep retires and their audit line is lost.
func TestTheReapedLineOutlivesTheRecordItRetires(t *testing.T) {
	meta := func(t *testing.T, h home.Home, worktree string) {
		t.Helper()
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{
			ID: "old", Backend: "herdr", Project: filepath.Dir(filepath.Dir(worktree)), Worktree: worktree,
			HerdrSession: "default", HerdrWorkspaceID: "w", HerdrTabID: "t", HerdrPaneID: "pane-gone",
		}); err != nil {
			t.Fatal(err)
		}
	}
	reapedLine := func(t *testing.T, h home.Home) string {
		t.Helper()
		lines, err := state.TailStatus(h.State, "old", 10)
		if err != nil {
			t.Fatalf("the task kept no history of its own reaping: %v", err)
		}
		return strings.Join(lines, "\n")
	}

	t.Run("orphan worktree", func(t *testing.T) {
		h := testHome(t)
		inventory := worktreeInventory(t, "done")
		meta(t, h, inventory.Worktrees[0].Path)
		service := newService(t, h, inventory, &gitRunner{})
		service.Clean = func(_ context.Context, id string, _ bool) error {
			return state.RemoveTaskMeta(h.State, id)
		}

		if _, err := service.Apply(context.Background(), Options{}); err != nil {
			t.Fatal(err)
		}
		if history := reapedLine(t, h); !strings.Contains(history, ReapedPrefix+string(OrphanWorktree)) {
			t.Fatalf("status log = %q, want the reaped line for the worktree", history)
		}
	})

	t.Run("orphan meta", func(t *testing.T) {
		h := testHome(t)
		gone := filepath.Join(t.TempDir(), ".worktrees", "gb-old")
		meta(t, h, gone)
		service := newService(t, h, Inventory{Tasks: []Task{task("old", gone, "pane-gone", "done")}}, &gitRunner{})
		service.Clean = func(_ context.Context, id string, _ bool) error {
			return state.RemoveTaskMeta(h.State, id)
		}

		if _, err := service.Apply(context.Background(), Options{}); err != nil {
			t.Fatal(err)
		}
		if history := reapedLine(t, h); !strings.Contains(history, ReapedPrefix+string(OrphanMeta)) {
			t.Fatalf("status log = %q, want the reaped line for the record", history)
		}
	})
}

func TestApplyArchivesAnOrphanStatusLog(t *testing.T) {
	h := testHome(t)
	if err := state.AppendStatus(h.State, "scout-old", "done: reported"); err != nil {
		t.Fatal(err)
	}
	runner := &gitRunner{}
	service := newService(t, h, Inventory{OrphanStatusIDs: []string{"scout-old"}}, runner)

	if _, err := service.Apply(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.State, "scout-old.status")); !os.IsNotExist(err) {
		t.Fatal("the orphan status log is still in the live listing")
	}
	entries, err := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "scout-old.status.") {
		t.Fatalf("archive holds %v, want the preserved log", entries)
	}
}

// TestAuditNeverActs is the default posture: a sweep with no --apply changes
// nothing at all, whatever it found.
func TestAuditNeverActs(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	service.CPU = func(int) (time.Duration, bool) { return 0, true }

	result, err := service.Audit(context.Background(), Options{ProbeIdle: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) == 0 {
		t.Fatal("the audit found nothing to report")
	}
	if len(result.Applied) != 0 || len(runner.killed) != 0 {
		t.Fatalf("the audit acted: applied=%v killed=%v", result.Applied, runner.killed)
	}
	if _, err := state.TailStatus(h.State, StatusID, 1); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	h := testHome(t)
	if _, err := ReadRecord(h.State); !os.IsNotExist(err) {
		t.Fatalf("a home with no sweep returned %v, want os.ErrNotExist", err)
	}
	record := Record{Time: fixtureStart, Findings: []Finding{{Class: OrphanProcess, PID: 7, Detail: "d", Action: "kill"}}}
	if err := WriteRecord(h.State, record); err != nil {
		t.Fatal(err)
	}
	read, err := ReadRecord(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if read.Digest != FindingsDigest(record.Findings) || len(read.Findings) != 1 {
		t.Fatalf("read back %+v", read)
	}

	var out strings.Builder
	if err := RenderRecord(&out, h.State, fixtureStart.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 orphan_process") || !strings.Contains(out.String(), "swept 1m0s ago") {
		t.Fatalf("rendered %q", out.String())
	}
}

// TestEmptyShellIsNeverReportedAsADirtyWorktree is the observed failure:
// C:\dev\code-goblins\projects\siqsermon\.worktrees\gb-siqsermon-manuscript-header
// is an empty directory with no files in it, and it was reported as a worktree
// with uncommitted or untracked changes. The changes were code-goblins' own,
// because git asked from inside that directory answers for the enclosing
// repository. Both halves are proven here: the premise check keeps the
// directory out of the worktree class, and the file count catches the lie if
// anything ever puts it back.
func TestEmptyShellIsNeverReportedAsADirtyWorktree(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	// The enclosing repository's own dirt, which is what git reported as the
	// empty directory's.
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("the parent's file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(repo, ".worktrees", "gb-dead")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}

	service := Service{
		Home:      testHome(t),
		Commands:  execx.OSRunner{},
		Inventory: fixedInventory(Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnlisted, TaskID: "dead"}}}),
		Sleep:     func(time.Duration) {},
		Now:       func() time.Time { return fixtureStart },
	}

	result, err := service.Audit(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if worktrees := classOf(result.Findings, OrphanWorktree); len(worktrees) != 0 {
		t.Fatalf("an empty shell was reported as a worktree: %+v", worktrees)
	}
	if finding := onlyFinding(t, result, OrphanDirectory); finding.Hold() != "" {
		t.Fatalf("an empty shell nothing is using was held: %q", finding.Hold())
	}

	t.Run("without the premise git answers for the enclosing repository", func(t *testing.T) {
		// The premise check is what stops the report: restore the assumption
		// that a directory under .worktrees/ is a worktree and the old
		// question gets asked again, from inside a directory holding nothing.
		status, err := service.git(context.Background(), shell, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(status, "scratch.txt") {
			t.Fatalf("git status from inside the shell = %q, want the enclosing repository's untracked file", status)
		}
		service.Inventory = fixedInventory(Inventory{
			Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationListed, TaskID: "dead"}},
		})
		result, err := service.Audit(context.Background(), Options{})
		if err != nil {
			t.Fatal(err)
		}
		hold := onlyFinding(t, result, OrphanWorktree).Hold()
		if strings.Contains(hold, "uncommitted or untracked changes") {
			t.Fatalf("hold = %q, want the dirt not attributed to a directory with no files in it", hold)
		}
		if !strings.Contains(hold, "enclosing repository") {
			t.Fatalf("hold = %q, want it to name whose changes those are", hold)
		}
	})
}

// TestPopulatedDirectoryDoesNotBorrowTheEnclosingRepositorysDirt is the same
// lie in the shape emptiness never covered: a partial or retired clone under
// the home's projects folder leaves a directory that holds files and is not a
// worktree, its registration cannot be confirmed, and git asked from inside it
// answers for the repository around it. Those answers are not this path's, so
// none of them may be reported as its state, and the files are not the
// operator's to force away either, because the sweep cannot say whose they are.
func TestPopulatedDirectoryDoesNotBorrowTheEnclosingRepositorysDirt(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("the parent's file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(repo, ".worktrees", "gb-dead")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(shell, "leftover.txt")
	if err := os.WriteFile(leftover, []byte("what a failed removal left\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	service := Service{
		Home:      testHome(t),
		Commands:  execx.OSRunner{},
		Inventory: fixedInventory(Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnknown, TaskID: "dead"}}}),
		Sleep:     func(time.Duration) {},
		Now:       func() time.Time { return fixtureStart },
		Return: func(context.Context, string, string) error {
			return errors.New("the worktree return path was reached for a path git does not answer for")
		},
	}

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"dead": true}})
	if err != nil {
		t.Fatal(err)
	}
	hold := onlyFinding(t, result, OrphanWorktree).Hold()
	if strings.Contains(hold, "uncommitted or untracked changes") || strings.Contains(hold, "no remote") {
		t.Fatalf("hold = %q, want the enclosing repository's state not reported as this directory's", hold)
	}
	if !strings.Contains(hold, "different repository") {
		t.Fatalf("hold = %q, want it to say git here answers for somewhere else", hold)
	}
	if !strings.Contains(hold, "No --force clears this") {
		t.Fatalf("hold = %q, want files the sweep cannot attribute to stay unremovable", hold)
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("a force removed files the sweep could not attribute to anything: %v", err)
	}
}

func TestApplyRemovesAnEmptyShellAndHoldsAnythingElse(t *testing.T) {
	repo := t.TempDir()
	shell := filepath.Join(repo, ".worktrees", "gb-dead")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnlisted, TaskID: "dead"}}}

	h := testHome(t)
	service := newService(t, h, inventory, &gitRunner{})
	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanDirectory); finding.Hold() != "" {
		t.Fatalf("the empty shell was held: %q", finding.Hold())
	}
	if _, err := os.Stat(shell); !os.IsNotExist(err) {
		t.Fatalf("the empty shell is still there: %v", err)
	}

	t.Run("a directory with contents is reported, not removed", func(t *testing.T) {
		if err := os.MkdirAll(shell, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shell, "somebodys-file.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		service := newService(t, testHome(t), inventory, &gitRunner{})
		result, err := service.Apply(context.Background(), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if hold := onlyFinding(t, result, OrphanDirectory).Hold(); !strings.Contains(hold, "not empty") {
			t.Fatalf("hold = %q, want a refusal naming the contents", hold)
		}
		if _, err := os.Stat(filepath.Join(shell, "somebodys-file.txt")); err != nil {
			t.Fatalf("the contents were removed: %v", err)
		}
	})
}

// TestUnreadableShellSaysItCouldNotLookAndNamesTheRemedy: the refusal on a
// directory the sweep cannot read stands, because unreadable is not empty,
// but it must say it could not look rather than that it found contents, and
// it owes the operator the remedy that does exist.
func TestUnreadableShellSaysItCouldNotLookAndNamesTheRemedy(t *testing.T) {
	repo := t.TempDir()
	// A path that is not a directory fails the same read, and unlike an ACL
	// it fails identically on every machine the suite runs on.
	shell := filepath.Join(repo, ".worktrees", "gb-dead")
	if err := os.MkdirAll(filepath.Dir(shell), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shell, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory := Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnlisted, TaskID: "dead"}}}
	service := newService(t, testHome(t), inventory, &gitRunner{})

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	hold := onlyFinding(t, result, OrphanDirectory).Hold()
	for _, want := range []string{"could not read", "sweep again", "No --force clears this"} {
		if !strings.Contains(hold, want) {
			t.Fatalf("hold = %q, want it to say %q", hold, want)
		}
	}
	if strings.Contains(hold, "is not empty") {
		t.Fatalf("hold = %q, want it to say the sweep could not look, not that it found contents", hold)
	}
	if _, err := os.Stat(shell); err != nil {
		t.Fatalf("the unreadable path was removed: %v", err)
	}
}

// TestEmptyShellUnderUnconfirmableRegistrationIsTheOperatorsToAnswer: when
// git worktree list cannot be read, an empty shell takes the worktree class,
// and its premise refusal is not a work-preservation one, because a directory
// holding no files holds no work. The refusal says what was established, that
// the path holds nothing and git there speaks for an enclosing repository, and
// deliberately names no cause: the same emptiness arrives from a registration
// that could not be read and from a listed worktree whose files are gone. The
// task id has to clear it, and nothing may be removed until it does.
func TestEmptyShellUnderUnconfirmableRegistrationIsTheOperatorsToAnswer(t *testing.T) {
	repo := t.TempDir()
	shell := filepath.Join(repo, ".worktrees", "gb-dead")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnknown, TaskID: "dead"}}}
	runner := &gitRunner{toplevel: repo}
	service := newService(t, testHome(t), inventory, runner)
	// The worktree return path runs git inside the directory it is returning,
	// which for an empty one is answered by the enclosing repository: the very
	// lie this branch removes. So it must not be reached at all, and the fake
	// says so rather than standing in for it. An earlier version of this test
	// stubbed this with os.Remove, which made the wrong path look right.
	var returned []string
	service.Return = func(_ context.Context, _, worktree string) error {
		returned = append(returned, worktree)
		return errors.New("the worktree return path asked git about a directory holding no files")
	}

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	hold := onlyFinding(t, result, OrphanWorktree).Hold()
	for _, want := range []string{"holds no files", "enclosing repository", "establish what it is"} {
		if !strings.Contains(hold, want) {
			t.Fatalf("hold = %q, want it to say %q", hold, want)
		}
	}
	if strings.Contains(hold, "No --force clears this") {
		t.Fatalf("hold = %q, want no permanent refusal on a directory that holds no work", hold)
	}
	if len(returned) != 0 {
		t.Fatalf("returned = %v, want nothing removed while the finding is held", returned)
	}
	if _, err := os.Stat(shell); err != nil {
		t.Fatalf("the shell was removed behind its own hold: %v", err)
	}

	t.Run("the task id the operator names clears it, and nothing asks git about the directory", func(t *testing.T) {
		result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"dead": true}})
		if err != nil {
			t.Fatal(err)
		}
		if finding := onlyFinding(t, result, OrphanWorktree); finding.Hold() != "" {
			t.Fatalf("hold = %q, want the named task id to answer it", finding.Hold())
		}
		if len(returned) != 0 {
			t.Fatalf("returned = %v, want the empty directory removed directly, never through a path that asks git about it", returned)
		}
		if len(runner.pruned) != 0 {
			t.Fatalf("pruned = %v, want no git asked of a project whose registration could not be read", runner.pruned)
		}
		if _, err := os.Stat(shell); !os.IsNotExist(err) {
			t.Fatalf("the empty shell survived the force that answered its hold: %v", err)
		}
	})
}

// TestForcedEmptyWorktreeIsPrunedRatherThanLeftRegistered: a worktree the
// project does list, whose files are gone, still has an administrative entry,
// and nothing else in the fleet ever clears it. Removing the directory alone
// leaves a registration that makes the next git worktree add for that name
// fail, while the reaped line claims a cleanup that never ran.
func TestForcedEmptyWorktreeIsPrunedRatherThanLeftRegistered(t *testing.T) {
	repo := t.TempDir()
	shell := filepath.Join(repo, ".worktrees", "gb-utah")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationListed, TaskID: "utah"}}}
	runner := &gitRunner{toplevel: repo}
	service := newService(t, testHome(t), inventory, runner)
	// The return path asks git from inside the worktree, which for an empty
	// one is answered by the enclosing repository, so it must not be reached.
	service.Return = func(_ context.Context, _, worktree string) error {
		return errors.New("the worktree return path asked git about a directory holding no files")
	}

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"utah": true}})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, result, OrphanWorktree)
	if finding.Hold() != "" {
		t.Fatalf("hold = %q, want the named task id to answer it", finding.Hold())
	}
	if _, err := os.Stat(shell); !os.IsNotExist(err) {
		t.Fatalf("the empty worktree is still there: %v", err)
	}
	if len(runner.pruned) != 1 || runner.pruned[0] != repo {
		t.Fatalf("pruned = %v, want one prune asked of %s, which is the repository that registered the path", runner.pruned, repo)
	}
	if len(result.Applied) != 1 || !strings.Contains(result.Applied[0], "prune its registration") {
		t.Fatalf("applied = %v, want the line to describe the removal and the prune that actually ran", result.Applied)
	}
	if strings.Contains(result.Applied[0], "through cfo cleanup") {
		t.Fatalf("applied = %q, want no claim of a cleanup that never ran", result.Applied[0])
	}

	t.Run("a prune that fails is an action failure, because the registration outlives the directory", func(t *testing.T) {
		if err := os.MkdirAll(shell, 0o755); err != nil {
			t.Fatal(err)
		}
		runner := &gitRunner{toplevel: repo, pruneFailure: "fatal: could not prune"}
		service := newService(t, testHome(t), inventory, runner)
		result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"utah": true}})
		if err != nil {
			t.Fatal(err)
		}
		if hold := onlyFinding(t, result, OrphanWorktree).Hold(); !strings.Contains(hold, "action failed") {
			t.Fatalf("hold = %q, want the failed prune reported as an action failure", hold)
		}
		if len(result.Applied) != 0 {
			t.Fatalf("applied = %v, want nothing reported as done when the prune failed", result.Applied)
		}
	})
}

// TestUnconfirmableRegistrationKeepsTheWorkGate: git worktree list can fail on
// a perfectly healthy repository, most realistically on a safe.directory
// refusal. When it does, the worktree holding unpushed commits must still be
// gated by the work check rather than offered up as an empty shell to delete
// and a record to force-archive.
func TestUnconfirmableRegistrationKeepsTheWorkGate(t *testing.T) {
	path := populatedWorktree(t, "gb-utah")
	inventory := Inventory{
		Tasks: []Task{task("utah", path, "pane-gone", "done")},
		// The zero value, which is what the collector produces for every
		// directory under a root whose registration could not be read.
		Worktrees: []WorktreeDir{{Path: path, Project: filepath.Dir(filepath.Dir(path)), TaskID: "utah"}},
	}
	runner := &gitRunner{unpushed: "abc1234 feat: the whole product of the run"}
	service := newService(t, testHome(t), inventory, runner)
	var cleaned []string
	service.Clean = func(_ context.Context, id string, force bool) error {
		cleaned = append(cleaned, id)
		return nil
	}

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold(), "no remote") {
		t.Fatalf("hold = %q, want the unpushed-work refusal", finding.Hold())
	}
	if directories := classOf(result.Findings, OrphanDirectory); len(directories) != 0 {
		t.Fatalf("a worktree with unpushed commits was offered up for removal: %+v", directories)
	}
	if len(cleaned) != 0 {
		t.Fatalf("the record of a worktree with unpushed commits was archived: %v", cleaned)
	}
}

// TestApplyHoldsAnUnfinishedTasksShell: the empty shell is removable, but
// --apply never reaps a task that has not finished, so the removal waits for
// the operator to name the id.
func TestApplyHoldsAnUnfinishedTasksShell(t *testing.T) {
	repo := t.TempDir()
	shell := filepath.Join(repo, ".worktrees", "gb-wedged")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := Inventory{
		Tasks:     []Task{task("wedged", shell, "pane-gone", "working")},
		Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnlisted, TaskID: "wedged"}},
	}

	result, err := newService(t, testHome(t), inventory, &gitRunner{}).Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if hold := onlyFinding(t, result, OrphanDirectory).Hold(); !strings.Contains(hold, "terminal status") {
		t.Fatalf("hold = %q, want the terminal-status refusal", hold)
	}
	if _, err := os.Stat(shell); err != nil {
		t.Fatalf("an unfinished task's shell was removed: %v", err)
	}
}

// TestEveryRefusalInAHoldNeedsItsOwnForce: a shell can carry two refusals at
// once and they answer to different authorities. Naming a pid accepts
// responsibility for that process and cannot say whether a task finished;
// naming the task id says the task is over and cannot speak for a process
// still holding the directory. AGENTS.md's contract is that --apply never
// reaps a task that has not finished, and the report's own pid attribution is
// the leak this scan exists to catch, so neither key answers for the other.
func TestEveryRefusalInAHoldNeedsItsOwnForce(t *testing.T) {
	repo := t.TempDir()
	shell := filepath.Join(repo, ".worktrees", "gb-wedged")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := Inventory{
		Tasks:     []Task{task("wedged", shell, "pane-gone", "working")},
		Processes: []Process{process(4242, 1, "rg.exe", "rg --files "+shell, fixtureLater)},
		Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnlisted, TaskID: "wedged"}},
	}

	result, err := newService(t, testHome(t), inventory, &gitRunner{}).Apply(context.Background(), Options{Force: map[string]bool{"4242": true}})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, result, OrphanDirectory)
	if !strings.Contains(finding.Hold(), "terminal status") {
		t.Fatalf("hold = %q, want the terminal-status refusal to survive a pid force", finding.Hold())
	}
	// The refusal the operator answered is gone from the text and the one
	// they did not answer remains, so the hold always reads as exactly what is
	// still standing rather than as the full list it started with.
	if strings.Contains(finding.Hold(), "pid 4242") {
		t.Fatalf("hold = %q, want the refusal the operator answered to be gone from it", finding.Hold())
	}
	if _, err := os.Stat(shell); err != nil {
		t.Fatalf("a pid force removed the shell of a task that has not finished: %v", err)
	}

	t.Run("the task id alone does not clear it either", func(t *testing.T) {
		result, err := newService(t, testHome(t), inventory, &gitRunner{}).Apply(context.Background(), Options{Force: map[string]bool{"wedged": true}})
		if err != nil {
			t.Fatal(err)
		}
		if hold := onlyFinding(t, result, OrphanDirectory).Hold(); !strings.Contains(hold, "pid 4242") {
			t.Fatalf("hold = %q, want the process attribution to survive a task force", hold)
		}
		if _, err := os.Stat(shell); err != nil {
			t.Fatalf("a task force removed a directory a process is holding: %v", err)
		}
	})

	t.Run("naming both is what clears it", func(t *testing.T) {
		result, err := newService(t, testHome(t), inventory, &gitRunner{}).Apply(context.Background(), Options{Force: map[string]bool{"4242": true, "wedged": true}})
		if err != nil {
			t.Fatal(err)
		}
		if hold := onlyFinding(t, result, OrphanDirectory).Hold(); hold != "" {
			t.Fatalf("hold = %q, want responsibility taken for both reasons to clear it", hold)
		}
		if _, err := os.Stat(shell); !os.IsNotExist(err) {
			t.Fatalf("the shell survived a force naming every reason it was held: %v", err)
		}
	})
}

// TestReapingARecordLessShellManufacturesNoStatusLog: state.AppendStatus
// creates the log it is handed, so a per-task line for a task whose record
// was archived long ago would leave behind a status log holding nothing but
// the reaper's own line - and state.ScanIDs reports a status log with no meta
// beside it as an orphan, so the next sweep finds a leak this one created.
func TestReapingARecordLessShellManufacturesNoStatusLog(t *testing.T) {
	repo := t.TempDir()
	shell := filepath.Join(repo, ".worktrees", "gb-utah")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	h := testHome(t)
	inventory := Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnlisted, TaskID: "utah"}}}

	result, err := newService(t, h, inventory, &gitRunner{}).Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("applied = %v, want the empty shell removed", result.Applied)
	}
	if _, err := os.Stat(filepath.Join(h.State, "utah.status")); !os.IsNotExist(err) {
		t.Fatalf("the reaper manufactured a status log for a task with no record: %v", err)
	}
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.OrphanStatusIDs) != 0 {
		t.Fatalf("the sweep created its own next finding: %v", scan.OrphanStatusIDs)
	}
	lines, err := state.TailStatus(h.State, StatusID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "orphan_directory") {
		t.Fatalf("fleet log = %v, want the one reaped line carrying it instead", lines)
	}

	// A goblin spawned a moment ago has a record and has reported nothing:
	// spawn publishes state/<id>.meta before the harness starts and appends a
	// status line only when the launch fails. That task exists, so the audit
	// line belongs in its history; the only difference from the case above is
	// that the record is still there.
	t.Run("a task with a record but no history yet still gets the line", func(t *testing.T) {
		repo := t.TempDir()
		shell := filepath.Join(repo, ".worktrees", "gb-fresh")
		if err := os.MkdirAll(shell, 0o755); err != nil {
			t.Fatal(err)
		}
		h := testHome(t)
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{
			ID: "fresh", Backend: "herdr", Project: repo, Worktree: shell,
			HerdrSession: "default", HerdrWorkspaceID: "w", HerdrTabID: "t", HerdrPaneID: "pane-gone",
		}); err != nil {
			t.Fatal(err)
		}
		inventory := Inventory{Worktrees: []WorktreeDir{{Path: shell, Project: repo, Registration: RegistrationUnlisted, TaskID: "fresh"}}}
		if _, err := newService(t, h, inventory, &gitRunner{}).Apply(context.Background(), Options{}); err != nil {
			t.Fatal(err)
		}
		lines, err := state.TailStatus(h.State, "fresh", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "orphan_directory") {
			t.Fatalf("task log = %v, want the reaped line recorded against the live record", lines)
		}
	})
}

// TestAnUnestablishedRefusalIsOverridableExactlyAsItsHoldImplies pins the
// mechanism to the words. The pane refusal answers to the same pid the kill
// does, so the one key the line names clears the whole hold, and the line says
// exactly that and no more. Claiming the evidence outlives that force would be
// the command describing what it wishes were true, and an operator whose Herdr
// cannot report pane identity would read themselves as locked out of killing a
// genuine orphan until they fixed Herdr.
func TestAnUnestablishedRefusalIsOverridableExactlyAsItsHoldImplies(t *testing.T) {
	inventory := orphanProcessInventory()
	inventory.Panes = []Pane{{ID: "pane-b", HasAgent: true}}
	inventory.UnresolvedPanes = []string{"pane-b"}

	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, inventory, runner)
	service.CPU = func(int) (time.Duration, bool) { return 0, true }

	held, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, held, OrphanProcess)
	if !strings.Contains(finding.Hold(), "process identity") {
		t.Fatalf("hold = %q, want the unresolved-pane refusal", finding.Hold())
	}
	if !strings.Contains(finding.Hold(), "Name 31032 with --force to take responsibility for it") {
		t.Fatalf("hold = %q, want the one key that clears every refusal on the line", finding.Hold())
	}
	if strings.Contains(finding.Hold(), "resolve it rather than overriding it") {
		t.Fatalf("hold = %q, claims the pane evidence outlives the --force the same line names", finding.Hold())
	}
	if len(runner.killed) != 0 {
		t.Fatalf("a process was killed on incomplete pane evidence: %v", runner.killed)
	}

	t.Run("naming its key overrides it", func(t *testing.T) {
		runner := &gitRunner{}
		service := newService(t, testHome(t), inventory, runner)
		service.CPU = func(int) (time.Duration, bool) { return 0, true }
		result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"31032": true}})
		if err != nil {
			t.Fatal(err)
		}
		if finding := onlyFinding(t, result, OrphanProcess); finding.Hold() != "" {
			t.Fatalf("hold = %q, want the operator's own pid force to clear it", finding.Hold())
		}
		if len(runner.killed) != 1 || runner.killed[0] != 31032 {
			t.Fatalf("killed = %v, want the forced pid, because a broken Herdr must not lock the operator out", runner.killed)
		}
	})
}

// TestOneTaskActedOnTwiceKeepsBothReapedLines: findings are acted on in class
// order and two of them can name one task, so a worktree cleanup that retires
// the record runs before the stale server beside it. Asking whether the record
// existed per finding reads the world the earlier action already changed; the
// question is about the fleet the sweep classified, so it is asked once, up
// front, for every task.
func TestOneTaskActedOnTwiceKeepsBothReapedLines(t *testing.T) {
	h := testHome(t)
	worktree := populatedWorktree(t, "gb-old")
	inventory := Inventory{
		Tasks:     []Task{task("old", worktree, "pane-gone", "done")},
		Worktrees: []WorktreeDir{{Path: worktree, Project: filepath.Dir(filepath.Dir(worktree)), Registration: RegistrationListed, TaskID: "old"}},
		Processes: []Process{process(555, 1, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest)},
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{
		ID: "old", Backend: "herdr", Project: `C:\dev\pd`, Worktree: worktree,
		HerdrSession: "default", HerdrWorkspaceID: "w", HerdrTabID: "t", HerdrPaneID: "p",
	}); err != nil {
		t.Fatal(err)
	}

	runner := &gitRunner{}
	service := newService(t, h, inventory, runner)
	service.CPU = func(int) (time.Duration, bool) { return 0, true }
	// The cleanup path retires the record, which is what the second finding
	// would otherwise read as a task that never had one.
	service.Clean = func(_ context.Context, id string, _ bool) error {
		return state.RemoveTaskMeta(h.State, id)
	}

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"555": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("applied = %v, want both the worktree and the server acted on", result.Applied)
	}
	lines, err := state.TailStatus(h.State, "old", 10)
	if err != nil {
		t.Fatal(err)
	}
	var reaped []string
	for _, line := range lines {
		if _, event := state.SplitStatus(line); strings.HasPrefix(strings.TrimSpace(event), ReapedPrefix) {
			reaped = append(reaped, event)
		}
	}
	if len(reaped) != 2 {
		t.Fatalf("the task's log holds %d reaped lines, want both: %q", len(reaped), lines)
	}
}

// TestApplyNeverKillsWithoutANamedPID is the second half of the 19 September
// 2026 incident. The CFO ran cfo reap --apply to retire a harmless status log
// and it also killed pid 35012, a goblin's vite server, because --apply acts on
// every finding it is not holding and one flag covered both. The brief this
// package was rewritten under said the sweep "must still refuse to kill
// anything without an explicitly named pid"; it did not, and this is what that
// cost.
func TestApplyNeverKillsWithoutANamedPID(t *testing.T) {
	h := testHome(t)
	inventory := orphanProcessInventory()
	inventory.OrphanStatusIDs = []string{"scout-old"}
	if err := state.AppendStatus(h.State, "scout-old", "done: reported"); err != nil {
		t.Fatal(err)
	}
	runner := &gitRunner{}
	service := newService(t, h, inventory, runner)
	service.CPU = func(int) (time.Duration, bool) { return 0, true }

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.killed) != 0 {
		t.Fatalf("killed = %v, want nothing ended by a sweep that named no process", runner.killed)
	}
	hold := onlyFinding(t, result, OrphanProcess).Hold()
	if !strings.Contains(hold, "authorised only by naming this process") {
		t.Fatalf("hold = %q, want it to say what authorises a kill", hold)
	}
	if !strings.Contains(hold, "31032") {
		t.Fatalf("hold = %q, want the pid the operator would have to name", hold)
	}

	// The tidying the operator actually asked for still happens, which is the
	// whole point: the two actions stop sharing one flag.
	if len(result.Applied) != 1 || !strings.Contains(result.Applied[0], "orphan_status") {
		t.Fatalf("applied = %v, want the status log archived and nothing else", result.Applied)
	}
	if _, err := os.Stat(filepath.Join(h.State, "scout-old.status")); !os.IsNotExist(err) {
		t.Fatal("the status log the operator asked to retire is still in the live listing")
	}

	t.Run("naming the pid is what ends it", func(t *testing.T) {
		runner := &gitRunner{}
		service := newService(t, testHome(t), orphanProcessInventory(), runner)
		service.CPU = func(int) (time.Duration, bool) { return 0, true }
		if _, err := service.Apply(context.Background(), Options{Force: map[string]bool{"31032": true}}); err != nil {
			t.Fatal(err)
		}
		if len(runner.killed) != 1 || runner.killed[0] != 31032 {
			t.Fatalf("killed = %v, want the named process ended", runner.killed)
		}
	})
}

// TestAnIdleReadingIsReportedRatherThanLeftSilent: the operator decides
// whether to name a pid from the reported line, and the idle case is exactly
// the one they act on. Reporting it as nothing made a measured idle process
// indistinguishable from one nobody measured.
func TestAnIdleReadingIsReportedRatherThanLeftSilent(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	service.CPU = func(int) (time.Duration, bool) { return 2 * time.Second, true }

	result, err := service.Audit(context.Background(), Options{ProbeIdle: true})
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, result, OrphanProcess)
	if !strings.Contains(finding.Detail, "idle:") {
		t.Fatalf("detail = %q, want the idle reading reported on the line the operator decides from", finding.Detail)
	}
	if len(runner.killed) != 0 {
		t.Fatalf("an audit killed something: %v", runner.killed)
	}
}
