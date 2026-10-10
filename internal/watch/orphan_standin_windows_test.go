package watch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The test binary plays every part of a goblin whose test drives a native
// terminal, as the spawn tests do, by its first argument: the goblin's
// terminal, the goblin in it, the shell that starts a command and exits, the
// test, and the host of the terminal the test starts. Copied as codex.exe it
// is the stand-in harness that terminal runs.
const (
	standInTerminal = "standin-terminal"
	standInGoblin   = "standin-goblin"
	standInShell    = "standin-shell"
	standInTest     = "standin-test"
	standInHost     = "native-spawn-host"
	standInWarm     = "standin-warm"
	standInFlag     = "--dangerously-bypass-approvals-and-sandbox"
)

// What the parts are told through their environment: where each reports the
// processes it started, one line each, the file whose making ends the test as
// a finished test ends, and where the test's own folders are.
const (
	standInPIDs        = "CFO_WATCH_STANDIN_PIDS"
	standInFinish      = "CFO_WATCH_STANDIN_FINISH"
	standInTestBinary  = "CFO_WATCH_STANDIN_TEST_BINARY"
	standInTestDir     = "CFO_WATCH_STANDIN_TEST_DIR"
	standInCodexDir    = "CFO_WATCH_STANDIN_CODEX_DIR"
	standInNestedState = "CFO_WATCH_STANDIN_NESTED_STATE"
	standInNestedDir   = "CFO_WATCH_STANDIN_NESTED_DIR"
)

// standInLife is how long a part waits to be ended before it ends itself.
const standInLife = 3 * time.Minute

// playStandIn runs the part this process was started as and reports whether
// it was one.
func playStandIn() bool {
	part := ""
	if len(os.Args) > 1 {
		part = os.Args[1]
	}
	report := func(line string) {
		file, err := os.OpenFile(os.Getenv(standInPIDs), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		_, _ = file.WriteString(line + "\n")
		_ = file.Close()
	}
	switch {
	case part == standInWarm:
	case part == standInTerminal:
		if err := host.Run(os.Args[2], host.Spec{ID: "g1", Args: []string{os.Args[0], standInGoblin}, Dir: os.Args[3], Cols: 80, Rows: 25}); err != nil {
			report("error the goblin's terminal: " + err.Error())
			os.Exit(1)
		}
	case part == standInGoblin:
		report("goblin " + strconv.Itoa(os.Getpid()))
		shell := exec.Command(os.Getenv(standInTestBinary), standInShell, os.Getenv(standInTestBinary), standInTest)
		shell.Dir = os.Getenv(standInTestDir)
		if err := shell.Run(); err != nil {
			report("error the shell: " + err.Error())
		}
		time.Sleep(standInLife)
	case part == standInShell:
		// Git Bash starts env and timeout, as a goblin's go test is run, by
		// replacing its own Windows process, so the chain of parents of what
		// it started stops at a process that has exited.
		started := exec.Command(os.Args[2], os.Args[3:]...)
		if err := started.Start(); err != nil {
			report("error the shell's command: " + err.Error())
			os.Exit(1)
		}
	case part == standInTest:
		// A spawn starts the terminal's host from the user's environment,
		// with no mark of the terminal that ran it.
		env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
			name, _, _ := strings.Cut(entry, "=")
			return strings.EqualFold(name, host.IDVariable) || strings.EqualFold(name, host.ProofVariable) || strings.EqualFold(name, "PATH")
		})
		env = append(env, "PATH="+os.Getenv(standInCodexDir)+string(os.PathListSeparator)+os.Getenv("PATH"))
		shell := os.Getenv("ComSpec")
		if shell == "" {
			shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		}
		nested := os.Getenv(standInNestedState)
		record, err := host.Launch(nested, []string{os.Args[0], standInHost}, env, host.Spec{ID: "task-7", Args: []string{shell, "/c", "codex", standInFlag}, Dir: os.Getenv(standInNestedDir), Cols: 80, Rows: 25})
		if err != nil {
			report("error the test's terminal: " + err.Error())
			os.Exit(1)
		}
		report(fmt.Sprintf("test %d\nhost %d\ncmd %d", os.Getpid(), record.HostPID, record.ChildPID))
		for deadline := time.Now().Add(standInLife); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(os.Getenv(standInFinish)); err == nil {
				break
			}
		}
		_ = host.Close(nested, record, 15*time.Second)
	case part == standInHost:
		if err := host.RunArgs(os.Args[2:]); err != nil {
			report("error the test's host: " + err.Error())
			os.Exit(1)
		}
	case strings.EqualFold(strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])), "codex"):
		if os.Getenv(standInPIDs) != "" {
			report("codex " + strconv.Itoa(os.Getpid()))
		}
		time.Sleep(standInLife)
	default:
		return false
	}
	return true
}

// standInFleet is a home of the test's own with one goblin, g1, at work in a
// native terminal, whose test started a terminal of its own with a stand-in
// harness in it.
type standInFleet struct {
	home home.Home
	// pids are the goblin's terminal, the goblin, its test, the host of the
	// terminal the test started, the cmd in it and the stand-in codex.
	pids map[string]int
	held map[string]windows.Handle
	// finish ends the test as a finished test ends, closing its terminal,
	// and returns once nothing it started runs.
	finish func()
}

// copyProgram copies this test binary to path and starts the copy once:
// Windows scans a program the first time it starts, which a loaded machine
// can take seconds over.
func copyProgram(t *testing.T, path string) {
	t.Helper()
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(path, standInWarm).CombinedOutput(); err != nil {
		t.Fatalf("%s did not run: %v\n%s", path, err, output)
	}
}

// withoutMark is this process's environment with no terminal's mark in it.
func withoutMark() []string {
	return slices.DeleteFunc(os.Environ(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(name, host.IDVariable) || strings.EqualFold(name, host.ProofVariable)
	})
}

// startStandInFleet starts the goblin's terminal and waits until its test's
// stand-in harness runs. The test binary the goblin's go test would have
// built is in the task's scratch folder, where its GOTMPDIR points, and runs
// from the task's worktree; the stand-in codex is in the test's own temporary
// folder there.
func startStandInFleet(t *testing.T) *standInFleet {
	t.Helper()
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	project := filepath.Join(root, "checkout", "proj")
	worktree := filepath.Join(h.Worktrees(), "proj", "g1")
	scratch := filepath.Join(h.Scratch(), "g1")
	taskTmp := filepath.Join(h.State, "tasktmp", "g1")
	testDir := filepath.Join(worktree, "internal", "spawn")
	testTmp := filepath.Join(scratch, "TestANativeGoblinStarts1")
	nestedDir := filepath.Join(testTmp, "003")
	for _, dir := range []string{project, testDir, taskTmp, nestedDir, h.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g1", Window: "native", Worktree: worktree, Project: project, TaskTmp: taskTmp, Scratch: scratch, Harness: "claude", Kind: "ship", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "g1", "working: running the spawn tests"); err != nil {
		t.Fatal(err)
	}
	testBinary := filepath.Join(scratch, "go-build1", "b001", "spawn.test.exe")
	copyProgram(t, testBinary)
	copyProgram(t, filepath.Join(testTmp, "001", "codex.exe"))

	pids := filepath.Join(root, "pids")
	finish := filepath.Join(root, "finish")
	terminal := exec.Command(os.Args[0], standInTerminal, h.State, worktree)
	terminal.Env = append(withoutMark(),
		standInPIDs+"="+pids, standInFinish+"="+finish, standInTestBinary+"="+testBinary, standInTestDir+"="+testDir,
		standInCodexDir+"="+filepath.Join(testTmp, "001"), standInNestedState+"="+filepath.Join(testTmp, "002", "state"), standInNestedDir+"="+nestedDir)
	terminal.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := terminal.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { _ = terminal.Wait(); close(ended) }()
	t.Cleanup(func() {
		_ = terminal.Process.Kill()
		<-ended
	})

	fleet := &standInFleet{home: h, pids: map[string]int{"terminal": terminal.Process.Pid}, held: map[string]windows.Handle{}}
	parts := []string{"goblin", "test", "host", "cmd", "codex"}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		data, _ := os.ReadFile(pids)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			name, value, _ := strings.Cut(line, " ")
			if name == "error" {
				t.Fatalf("the stand-in fleet did not start: %s", value)
			}
			if pid, err := strconv.Atoi(value); err == nil && fleet.held[name] == 0 {
				fleet.pids[name] = pid
				fleet.held[name] = standin.Hold(t, pid)
			}
		}
		if !slices.ContainsFunc(parts, func(part string) bool { return fleet.held[part] == 0 }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in fleet started %v, want %v", fleet.pids, parts)
		}
	}
	fleet.finish = func() {
		if err := os.WriteFile(finish, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, part := range []string{"test", "host", "cmd", "codex"} {
			if event, _ := windows.WaitForSingleObject(fleet.held[part], 30000); event != windows.WAIT_OBJECT_0 {
				t.Fatalf("the test's %s, pid %d, still runs after the test finished", part, fleet.pids[part])
			}
		}
	}
	return fleet
}

// fixtureProcesses is the sweep's own reading of the machine, cut down to the
// processes the test started, so a goblin or a session of this machine's own
// is never judged against the test's home.
type fixtureProcesses struct {
	inner reap.ProcessLister
	keep  func() []int
	// listed runs once the reading is taken, before the sweep has it.
	listed func()
	seen   *[]reap.Process
}

func (f fixtureProcesses) List(ctx context.Context) ([]reap.Process, error) {
	all, err := f.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	keep := f.keep()
	kept := slices.DeleteFunc(all, func(process reap.Process) bool { return !slices.Contains(keep, process.PID) })
	*f.seen = slices.Clone(kept)
	if f.listed != nil {
		f.listed()
	}
	return kept, nil
}

// sweepOf is the watcher's own sweep of home h as ConfigFromEnv builds it for
// production, reading only the processes keep names. It returns the config
// and the processes its last sweep read.
func sweepOf(t *testing.T, h home.Home, keep func() []int, listed func()) (Config, *[]reap.Process) {
	t.Helper()
	cfg := ConfigFromEnv(h)
	if cfg.Cleanup != nil {
		t.Cleanup(cfg.Cleanup)
	}
	cfg.JanitorEvery = 0
	collector, isCollector := cfg.Reap.Inventory.(reap.Collector)
	if !isCollector {
		t.Fatalf("the watcher's sweep collects with %T, want reap.Collector", cfg.Reap.Inventory)
	}
	seen := &[]reap.Process{}
	collector.Panes = nil
	collector.Processes = fixtureProcesses{inner: collector.Processes, keep: keep, listed: listed, seen: seen}
	cfg.Reap.Inventory = collector
	return cfg, seen
}

// orphanWakes are the wakes the sweep raised in stateDir.
func orphanWakes(t *testing.T, stateDir string) []wake.Record {
	t.Helper()
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(records, func(record wake.Record) bool { return record.Kind != "orphan" })
}

// On 2026-10-10 the sweep woke the CFO twice in half an hour for a goblin's
// own test at work: "3 orphan_process", a spawn.test.exe hosting the test's
// terminal, the cmd in it and the stand-in codex.exe, each held as "could not
// determine what this process belongs to", and all three gone on the next
// look. The sweep that began at 11:12:27Z recorded a cmd.exe and a
// spawn.test.exe born at 11:12:17Z with nothing read about either: a spawn
// test lasts about ten seconds, so they ended between the sweep's reading of
// the machine and its reading of whose each process is. A process a running
// goblin's test started is that goblin's, and one that has ended is nobody's
// orphan.
func TestTheSweepRaisesNoOrphanForAStandInAGoblinsTestStarted(t *testing.T) {
	for name, endsMidSweep := range map[string]bool{
		"while the test runs": false,
		"when the test ends while the sweep reads the machine": true,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			fleet := startStandInFleet(t)
			var listed func()
			if endsMidSweep {
				listed = fleet.finish
			}
			keep := func() []int {
				var pids []int
				for _, pid := range fleet.pids {
					pids = append(pids, pid)
				}
				return pids
			}
			cfg, seen := sweepOf(t, fleet.home, keep, listed)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()

			// Act
			reason := sweepOrphans(ctx, cfg)

			// Assert
			for _, part := range []string{"terminal", "host", "cmd", "codex"} {
				if !slices.ContainsFunc(*seen, func(process reap.Process) bool { return process.PID == fleet.pids[part] }) {
					t.Fatalf("the sweep's reading of the machine held no %s, pid %d, so it judged nothing: %+v", part, fleet.pids[part], *seen)
				}
			}
			record, err := reap.ReadRecord(fleet.home.State)
			if err != nil {
				t.Fatalf("the sweep kept no record: %v", err)
			}
			if record.Error != "" {
				t.Fatalf("the sweep failed: %s", record.Error)
			}
			var orphans []string
			for _, finding := range reap.Actionable(record.Findings) {
				orphans = append(orphans, finding.Line())
			}
			if len(orphans) != 0 {
				t.Errorf("the sweep reported %d running findings for a working goblin's own test:\n%s", len(orphans), strings.Join(orphans, "\n"))
			}
			if wakes := orphanWakes(t, fleet.home.State); reason != "" || len(wakes) != 0 {
				t.Errorf("the sweep woke the CFO for a working goblin's own test: reason %q, wakes %+v", reason, wakes)
			}
		})
	}
}

// A harness that carries no mark of a running terminal, works in no task's
// folders and has no parent of a running terminal is an orphan beside a
// goblin's test as it is alone: the sweep names it, and it alone, and wakes
// the CFO for it once.
func TestAHarnessOfNoRunningTerminalStillWakesTheCFOOnce(t *testing.T) {
	// Arrange
	fleet := startStandInFleet(t)
	away := t.TempDir()
	standin.RemoveAtCleanup(t, away)
	program := filepath.Join(away, "codex.exe")
	copyProgram(t, program)
	// Whatever this process starts is its own to the sweep it runs, so the
	// orphan is started by a shell that exits, as one a closed session left.
	reported := filepath.Join(away, "pids")
	shell := exec.Command(os.Args[0], standInShell, program, standInFlag)
	shell.Dir = away
	shell.Env = append(withoutMark(), standInPIDs+"="+reported)
	if output, err := shell.CombinedOutput(); err != nil {
		t.Fatalf("the orphan did not start: %v\n%s", err, output)
	}
	orphan := 0
	for deadline := time.Now().Add(time.Minute); orphan == 0; time.Sleep(50 * time.Millisecond) {
		data, _ := os.ReadFile(reported)
		if _, value, isReported := strings.Cut(strings.TrimSpace(string(data)), "codex "); isReported {
			orphan, _ = strconv.Atoi(value)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the orphan reported %q, want its pid", data)
		}
	}
	standin.Hold(t, orphan)
	keep := func() []int {
		pids := []int{orphan}
		for _, pid := range fleet.pids {
			pids = append(pids, pid)
		}
		return pids
	}
	cfg, seen := sweepOf(t, fleet.home, keep, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	// Act: the sweep that finds it, then the next one due.
	first := sweepOrphans(ctx, cfg)
	record, err := reap.ReadRecord(fleet.home.State)
	if err != nil {
		t.Fatalf("the sweep kept no record: %v", err)
	}
	found := reap.Actionable(record.Findings)
	record.Time = time.Now().UTC().Add(-2 * cfg.ReapEvery)
	if err := reap.WriteRecord(fleet.home.State, record); err != nil {
		t.Fatal(err)
	}
	second := sweepOrphans(ctx, cfg)

	// Assert
	if !slices.ContainsFunc(*seen, func(process reap.Process) bool { return process.PID == orphan }) {
		t.Fatalf("the sweep's reading of the machine held no orphan, pid %d: %+v", orphan, *seen)
	}
	if len(found) != 1 || found[0].Class != reap.OrphanProcess || found[0].PID != orphan {
		var lines []string
		for _, finding := range found {
			lines = append(lines, finding.Line())
		}
		t.Fatalf("the sweep reported %d running findings, want the orphan codex.exe, pid %d, alone:\n%s", len(found), orphan, strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(first, "orphan:") || !strings.Contains(first, "still running: 1 orphan_process") {
		t.Errorf("the sweep that found the orphan = %q, want a wake naming one orphan process", first)
	}
	if second != "" {
		t.Errorf("the next sweep = %q, want no second wake for the orphan the CFO was told of", second)
	}
	if wakes := orphanWakes(t, fleet.home.State); len(wakes) != 1 {
		t.Errorf("the sweeps raised %d wakes, want one: %+v", len(wakes), wakes)
	}
}
