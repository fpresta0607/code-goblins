package reap

import (
	"context"
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

// gitRunner answers the two questions the worktree gate asks and refuses
// anything else, so a gate that started shelling out to something new fails
// loudly instead of silently passing.
type gitRunner struct {
	status   string
	unpushed string
	killed   []int
}

func (r *gitRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if req.Name != "git" {
		return execx.Result{}, os.ErrInvalid
	}
	switch req.Args[0] {
	case "status":
		return execx.Result{Stdout: []byte(r.status)}, nil
	case "log":
		return execx.Result{Stdout: []byte(r.unpushed)}, nil
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

// TestGateRefusesBusyProcess is the lesson the fleet already paid for: a
// goblin waiting on an API response looks idle by log age but is not by
// processor time, so idleness is measured, never inferred.
func TestGateRefusesBusyProcess(t *testing.T) {
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
	if !strings.Contains(finding.Hold, "busy") {
		t.Fatalf("hold = %q, want a busy refusal", finding.Hold)
	}
	if len(runner.killed) != 0 {
		t.Fatalf("a busy process was killed: %v", runner.killed)
	}
}

// TestGateRefusesUnmeasurableProcess: not being able to read processor time is
// not evidence of idleness.
func TestGateRefusesUnmeasurableProcess(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	service.CPU = func(int) (time.Duration, bool) { return 0, false }

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanProcess); !strings.Contains(finding.Hold, "could not be read") {
		t.Fatalf("hold = %q, want an unmeasurable refusal", finding.Hold)
	}
	if len(runner.killed) != 0 {
		t.Fatalf("an unmeasurable process was killed: %v", runner.killed)
	}
}

func TestApplyKillsAnIdleOrphanAndRecordsIt(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, orphanProcessInventory(), runner)
	service.CPU = func(int) (time.Duration, bool) { return 2 * time.Second, true }

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanProcess); finding.Hold != "" {
		t.Fatalf("an idle orphan was held: %q", finding.Hold)
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
		if finding.PID == 31033 && finding.Hold == "" {
			t.Fatal("an unnamed unmeasurable process was not held")
		}
	}
}

func worktreeInventory(verb string) Inventory {
	path := `C:\dev\pd\.worktrees\gb-old`
	inventory := Inventory{
		Worktrees: []WorktreeDir{{Path: path, Project: `C:\dev\pd`, TaskID: "old"}},
	}
	if verb != "" {
		inventory.Tasks = []Task{task("old", path, "pane-gone", verb)}
	}
	return inventory
}

func TestGateRefusesDirtyWorktree(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{status: " M internal/thing.go"}
	service := newService(t, h, worktreeInventory("done"), runner)

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold, "uncommitted") {
		t.Fatalf("hold = %q, want an uncommitted-work refusal", finding.Hold)
	}
}

func TestGateRefusesUnpushedBranch(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{unpushed: "abc1234 feat: the whole product of the run"}
	service := newService(t, h, worktreeInventory("done"), runner)

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold, "no remote") {
		t.Fatalf("hold = %q, want an unpushed-work refusal", finding.Hold)
	}
}

// TestForceNeverClearsTheWorkGate: --force covers the operator's judgement
// about idleness and about a task's status. It never covers destroying work.
func TestForceNeverClearsTheWorkGate(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{unpushed: "abc1234 feat: the whole product of the run"}
	service := newService(t, h, worktreeInventory("working"), runner)

	result, err := service.Apply(context.Background(), Options{Force: map[string]bool{"old": true}})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold, "no remote") {
		t.Fatalf("hold = %q, want the unpushed-work refusal to survive --force", finding.Hold)
	}
}

func TestGateRefusesNonTerminalTask(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, worktreeInventory("working"), runner)

	result, err := service.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if finding := onlyFinding(t, result, OrphanWorktree); !strings.Contains(finding.Hold, "terminal status") {
		t.Fatalf("hold = %q, want a terminal-status refusal", finding.Hold)
	}
}

func TestApplyReturnsACleanOrphanWorktree(t *testing.T) {
	h := testHome(t)
	runner := &gitRunner{}
	service := newService(t, h, worktreeInventory("done"), runner)
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
	if finding := onlyFinding(t, result, OrphanWorktree); finding.Hold != "" {
		t.Fatalf("a clean, finished worktree was held: %q", finding.Hold)
	}
	if len(cleaned) != 1 || cleaned[0] != "old" {
		t.Fatalf("cleaned = %v, want [old] through the cfo cleanup path", cleaned)
	}
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
