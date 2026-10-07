package fleettree

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

// A goblin counts as working while any child works: a working child holds
// it, and its activity is the goblin's.
func TestTreeWorksWhileAnyChildWorks(t *testing.T) {
	for name, test := range map[string]struct {
		children  []Node
		isWorking bool
		activity  time.Time
	}{
		"a working sub-agent": {[]Node{{Kind: KindSubagent, State: Working, LastActivity: at}, {Kind: KindShell, State: Done, LastActivity: at.Add(-time.Hour)}}, true, at},
		"a working job":       {[]Node{{Kind: KindProcess, State: Working, LastActivity: at}}, true, at.Add(-time.Hour)},
		"only silent":         {[]Node{{Kind: KindShell, State: Silent, LastActivity: at.Add(-20 * time.Minute)}}, false, at.Add(-20 * time.Minute)},
		"all finished":        {[]Node{{Kind: KindSubagent, State: Done, LastActivity: at.Add(-30 * time.Minute)}, {Kind: KindProcess, State: Waiting}}, false, at.Add(-30 * time.Minute)},
		"no children":         {nil, false, at.Add(-time.Hour)},
	} {
		t.Run(name, func(t *testing.T) {
			tree := Tree{ConversationAt: at.Add(-time.Hour), Children: test.children}
			if tree.Working() != test.isWorking {
				t.Errorf("Working() = %v, want %v", tree.Working(), test.isWorking)
			}
			if !tree.ActivityAt().Equal(test.activity) {
				t.Errorf("ActivityAt() = %v, want %v: the conversation or an agent, shell or monitor, never a job's start", tree.ActivityAt(), test.activity)
			}
		})
	}
}

// Jobs names what holds the goblin's turn: each job of processes as a wake
// names it, and each agent, shell or monitor still running.
func TestTreeJobsNamesWhatHoldsTheGoblinsTurn(t *testing.T) {
	tree := Tree{Children: []Node{
		{Kind: KindSubagent, Label: "Map plumbing", State: Working},
		{Kind: KindSubagent, Label: "Research", State: Done},
		{Kind: KindMonitor, Label: "CI checks", State: Silent},
		{Kind: KindProcess, State: Waiting, process: "node.exe (pid 9)", cpu: 2 * time.Second},
		{Kind: KindGate, State: Working},
	}}

	names, used := tree.Jobs()

	if !slices.Equal(names, []string{`subagent "Map plumbing"`, `monitor "CI checks"`, "node.exe (pid 9)"}) || used != 2*time.Second {
		t.Errorf("Jobs() = %v, %s", names, used)
	}
}

// A background shell's own processes are shown as the shell, with their
// memory and what they do, and not again as a job.
func TestReadFoldsABackgroundShellsProcessesIntoTheShell(t *testing.T) {
	// Arrange
	home := t.TempDir()
	launched := at.Add(-time.Hour)
	shellStart := launched.Add(20 * time.Minute)
	goblin, _ := claudeGoblin(t, home,
		call("toolu_S1", "Bash", object{"command": "go test ./internal/... 2>&1 | tail -5", "description": "Run the Go tests", "run_in_background": true}, shellStart.Add(-time.Second)),
		result("toolu_S1", "running", object{"backgroundTaskId": "b1"}, shellStart),
	)
	goblin.HarnessPID = 100
	reader := Reader{
		Home:        home,
		Processes:   func() ([]Process, error) { return goblinProcesses(launched, 30*time.Second), nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{106: {5173}}, nil },
		CommandLine: func(pid int) (string, error) { return goblinCommands[pid], nil },
		Now:         func() time.Time { return at },
	}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	shell := child(t, tree, "shell:b1")
	if shell.Memory != 505*megabyte || shell.Group != GroupTest {
		t.Errorf("shell = %+v, want the test run's memory and group", shell)
	}
	for _, node := range tree.Children {
		if node.ID == "process:102:3" {
			t.Errorf("the shell's job is listed again: %+v", node)
		}
	}
	if names, _ := tree.Jobs(); !slices.Contains(names, "bash.exe (pid 102)") {
		t.Errorf("Jobs() = %v, want the shell's processes still named for the monitor", names)
	}
}

// A child an earlier run of the harness started cannot still run: the
// harness's processes end with it.
func TestReadEndsChildrenOfAnEarlierRunOfTheHarness(t *testing.T) {
	// Arrange
	home := t.TempDir()
	harnessStart := at.Add(-10 * time.Minute)
	goblin, _ := claudeGoblin(t, home,
		call("toolu_A", "Agent", object{"description": "Before the restart"}, at.Add(-time.Hour)),
		result("toolu_A", "launched", object{"status": "async_launched", "agentId": "a1"}, at.Add(-time.Hour)),
		call("toolu_B", "Agent", object{"description": "After the restart"}, at.Add(-time.Minute)),
	)
	goblin.HarnessPID = 100
	reader := Reader{Home: home, Processes: func() ([]Process, error) {
		return []Process{{PID: 100, ParentPID: 1, Exe: "claude.exe", Started: harnessStart}}, nil
	}, Now: func() time.Time { return at }}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	if before := child(t, tree, "subagent:toolu_A"); before.State != Failed || !before.Finished.Equal(harnessStart) {
		t.Errorf("earlier run's agent = %+v, want ended when the harness restarted", before)
	}
	if after := child(t, tree, "subagent:toolu_B"); after.State != Working {
		t.Errorf("this run's agent = %+v, want working", after)
	}
}

// Every node and the tree carry when their source last changed and when
// they were read.
func TestReadStampsFreshnessOnEveryNode(t *testing.T) {
	// Arrange
	home := t.TempDir()
	goblin, session := claudeGoblin(t, home,
		call("toolu_A", "Agent", object{"description": "Map plumbing"}, at.Add(-time.Hour)),
		result("toolu_A", "launched", object{"status": "async_launched", "agentId": "a1"}, at.Add(-time.Hour)),
	)
	writeLines(t, filepath.Join(session, "subagents", "agent-a1.jsonl"), at.Add(-3*time.Minute), said("reading", at.Add(-3*time.Minute)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	agent := child(t, tree, "subagent:toolu_A")
	if !agent.SourceUpdatedAt.Equal(at.Add(-3*time.Minute)) || !agent.FetchedAt.Equal(at) {
		t.Errorf("agent freshness = %v, %v", agent.SourceUpdatedAt, agent.FetchedAt)
	}
	if !tree.FetchedAt.Equal(at) || !tree.SourceUpdatedAt.Equal(at.Add(-3*time.Minute)) {
		t.Errorf("tree freshness = %v, %v; want the newest source among its nodes", tree.SourceUpdatedAt, tree.FetchedAt)
	}
}

func TestOwnedSessionTakesOnlyThisGenerationsGoblin(t *testing.T) {
	stateDir := t.TempDir()
	writeFile(t, filepath.Join(stateDir, ".supervisor.json"), `{"sessions":{"n1":{"native_id":"conv-1","harness":"claude","role":"goblin","task_id":"tree","generation":"s2"}},"task_sessions":{"tree":"n1"}}`, at)
	for name, test := range map[string]struct {
		meta state.TaskMeta
		want string
	}{
		"its own goblin":       {state.TaskMeta{ID: "tree", Harness: "claude", SpawnGen: "s2"}, "conv-1"},
		"an earlier one":       {state.TaskMeta{ID: "tree", Harness: "claude", SpawnGen: "s3"}, ""},
		"another harness":      {state.TaskMeta{ID: "tree", Harness: "codex", SpawnGen: "s2"}, ""},
		"a harness with none":  {state.TaskMeta{ID: "tree", Harness: "pi", SpawnGen: "s2"}, ""},
		"a task with no entry": {state.TaskMeta{ID: "other", Harness: "claude", SpawnGen: "s2"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := OwnedSession(stateDir, test.meta); err != nil || got != test.want {
				t.Errorf("OwnedSession = %q, %v; want %q", got, err, test.want)
			}
		})
	}
	writeFile(t, filepath.Join(stateDir, ".supervisor.json"), "{", at)
	if _, err := OwnedSession(stateDir, state.TaskMeta{ID: "tree", Harness: "claude", SpawnGen: "s2"}); err == nil {
		t.Error("an unreadable record read as none, want an error")
	}
}

// A process id the host recorded for the goblin's harness, now held by a
// later process, is no part of the goblin: nothing under it is the
// goblin's.
func TestReadIgnoresAProcessThatReusedTheHarnessesID(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return goblinProcesses(launched, time.Minute), nil },
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(pid int) (string, error) { return goblinCommands[pid], nil },
		Now:         func() time.Time { return at },
	}
	meta := state.TaskMeta{ID: "tree", Harness: "codex"}

	// Act
	reused, _ := reader.Read(context.Background(), Goblin{Meta: meta, HarnessPID: 100, HarnessStarted: launched.Add(-3 * time.Hour)})
	own, _ := reader.Read(context.Background(), Goblin{Meta: meta, HarnessPID: 100, HarnessStarted: launched.Add(300 * time.Millisecond)})

	// Assert
	if len(reused.Children) != 0 || reused.Memory != 0 || len(reused.Unread) == 0 {
		t.Errorf("tree over a reused id = %+v, want nothing of another program's, and why", reused)
	}
	if len(own.Children) != 3 || own.Memory == 0 {
		t.Errorf("tree over the recorded process = %+v, want its jobs", own)
	}
}
