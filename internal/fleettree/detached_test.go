package fleettree

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A process that lost its parent is reachable from no harness, so the tree
// dropped it: on 2026-10-09 two browser bridges held 3.7 GB that no card
// showed, and one goblin's 2 hour old server was in no tree. A detached tree
// carries its terminal's mark, and shows under the goblin that mark names
// with its memory, counted in the goblin's total. It is no sign the goblin
// works: a stuck leftover using the processor must not hide a stalled
// goblin.
func TestReadShowsAGoblinsDetachedTreesUnderItWithTheirMemory(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
	processes := append(goblinProcesses(launched, 0),
		// A browser bridge and its browser, started from a shell that exited.
		Process{PID: 200, ParentPID: 900, Exe: "node.exe", Created: 20, Started: later(45), CPU: time.Minute, Memory: 200 * megabyte},
		Process{PID: 201, ParentPID: 200, Exe: "chrome.exe", Created: 21, Started: later(46), CPU: time.Minute, Memory: 2300 * megabyte},
		// A Git Bash shell, which carries no mark, left holding a server.
		Process{PID: 210, ParentPID: 901, Exe: "bash.exe", Created: 22, Started: later(47), Memory: megabyte},
		Process{PID: 211, ParentPID: 210, Exe: "node.exe", Created: 23, Started: later(48), Memory: 99 * megabyte},
		// Another goblin's detached server, and a program that is nobody's.
		Process{PID: 220, ParentPID: 902, Exe: "node.exe", Created: 24, Started: later(49), Memory: 700 * megabyte},
		Process{PID: 230, ParentPID: 903, Exe: "explorer.exe", Created: 25, Started: later(-600), Memory: 300 * megabyte},
	)
	marks := map[int]string{100: "tree", 200: "tree", 201: "tree", 211: "tree", 220: "another"}
	asked := map[int]int{}
	now := at
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{106: {5173}, 211: {47391}}, nil },
		CommandLine: func(pid int) (string, error) { return goblinCommands[pid], nil },
		Owner:       func(pid int) string { asked[pid]++; return marks[pid] },
		Now:         func() time.Time { return now },
	}
	goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100}
	attached, err := (&Reader{Home: t.TempDir(), Processes: reader.Processes, Listeners: reader.Listeners, CommandLine: reader.CommandLine, Now: reader.Now}).Read(context.Background(), goblin)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	tree, err := reader.Read(context.Background(), goblin)
	if err != nil {
		t.Fatal(err)
	}
	now = at.Add(time.Minute)
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}

	// Assert
	detached := map[string]Node{}
	for _, child := range tree.Children {
		if child.Detached {
			detached[child.ID] = child
		}
	}
	bridge, server := detached["process:200:20"], detached["process:210:22"]
	if len(detached) != 2 || bridge.Memory != 2500*megabyte || server.Memory != 100*megabyte {
		t.Fatalf("detached trees = %+v, want the bridge with its browser at 2500 MB and the shell with its server at 100 MB, and neither another goblin's nor nobody's", detached)
	}
	if bridge.Group != GroupBrowser || server.Group != GroupDevServer {
		t.Errorf("the bridge is a %q and the server a %q, want a browser and a dev server", bridge.Group, server.Group)
	}
	if want := attached.Memory + 2600*megabyte; tree.Memory != want {
		t.Errorf("the goblin's memory = %d MB, want its harness's tree and its detached trees, %d MB", tree.Memory/megabyte, want/megabyte)
	}
	names, _ := tree.Jobs()
	if slices.ContainsFunc(names, func(name string) bool { return strings.Contains(name, "pid 200") || strings.Contains(name, "pid 210") }) {
		t.Errorf("the goblin's jobs %v name a detached tree, which is no sign that it works", names)
	}
	if attached.Working() != tree.Working() {
		t.Errorf("the goblin reads as working = %t with its detached trees and %t without, want the same", tree.Working(), attached.Working())
	}
	for pid, times := range asked {
		if times != 1 {
			t.Errorf("process %d was asked whose it is %d times over two reads, want once", pid, times)
		}
	}
	if asked[100] != 0 || asked[106] != 0 {
		t.Errorf("processes under the harness were asked whose they are (%v), want only those that reach no harness", asked)
	}
}
