package fleettree

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

const megabyte = 1 << 20

func jobNames(read harnessProcesses) []string {
	var names []string
	for _, job := range read.jobs {
		names = append(names, job.name())
	}
	return names
}

func TestHarnessJobsCountsOnlyWorkStartedAfterLaunch(t *testing.T) {
	launched := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	processes := []Process{
		{PID: 10, ParentPID: 1, Exe: "claude.exe", Started: launched, CPU: time.Hour},
		{PID: 11, ParentPID: 10, Exe: "cmd.exe", Started: launched.Add(13 * time.Second)},
		{PID: 12, ParentPID: 11, Exe: "python.exe", Started: launched.Add(14 * time.Second), CPU: 7 * time.Second},
		{PID: 13, ParentPID: 10, Exe: "bash.exe", Started: launched.Add(20 * time.Minute), CPU: time.Second},
		{PID: 14, ParentPID: 13, Exe: "go.exe", Started: launched.Add(21 * time.Minute), CPU: 40 * time.Second},
		// A process id the harness's own child once had, now reused by a
		// process older than the harness: never its child.
		{PID: 15, ParentPID: 10, Exe: "svchost.exe", Started: launched.Add(-time.Hour), CPU: time.Hour},
	}

	read, _ := readHarness(10, processes, HarnessLaunch)
	if names := jobNames(read); !slices.Equal(names, []string{"bash.exe (pid 13)"}) {
		t.Fatalf("jobs = %v, want only the bash the harness started after launching", names)
	}
	if used := read.jobs[0].cpu(); used != 41*time.Second {
		t.Errorf("processor time = %s, want the job and its child's 41s, not the MCP server's or the harness's own", used)
	}
}

func TestHarnessJobsWalksThroughALaunchShim(t *testing.T) {
	launched := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	processes := []Process{
		{PID: 20, ParentPID: 1, Exe: "node.exe", Started: launched},
		{PID: 21, ParentPID: 20, Exe: "codex.exe", Started: launched.Add(time.Second)},
		{PID: 22, ParentPID: 21, Exe: "node.exe", Started: launched.Add(3 * time.Second)},
		{PID: 23, ParentPID: 21, Exe: "powershell.exe", Started: launched.Add(5 * time.Minute), CPU: 2 * time.Second},
	}

	read, _ := readHarness(20, processes, HarnessLaunch)
	if names := jobNames(read); !slices.Equal(names, []string{"powershell.exe (pid 23)"}) || read.harness.PID != 21 {
		t.Errorf("jobs = %v under %d, want the command the codex binary behind its node shim is running", names, read.harness.PID)
	}
}

// PowerShell is a shell, not a harness launcher: whatever it started as it
// opened belongs to it, and is not walked through as though it were the harness.
func TestHarnessJobsDoesNotWalkThroughPowerShell(t *testing.T) {
	launched := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	processes := []Process{
		{PID: 30, ParentPID: 1, Exe: "pwsh.exe", Started: launched},
		{PID: 31, ParentPID: 30, Exe: "claude.exe", Started: launched.Add(time.Second)},
		{PID: 32, ParentPID: 31, Exe: "bash.exe", Started: launched.Add(5 * time.Minute), CPU: 2 * time.Second},
	}

	read, _ := readHarness(30, processes, HarnessLaunch)
	if names := jobNames(read); len(names) != 0 {
		t.Errorf("jobs = %v, want none: pwsh is the foreground program and its child started with it", names)
	}
}

func TestHarnessJobsReadsTheLiveProcessTable(t *testing.T) {
	processes, err := Processes()
	if err != nil {
		t.Fatal(err)
	}
	read, ok := readHarness(os.Getpid(), processes, 0)
	if !ok || read.harness.PID != os.Getpid() || read.harness.Memory == 0 || read.harness.CPU <= 0 || read.harness.Started.IsZero() {
		t.Fatalf("this test process as Windows lists it = %+v, %v; want its start, processor time and memory", read.harness, ok)
	}
}

// goblinProcesses is a Claude goblin's harness with an MCP server it started
// as it launched, a test run, a dev server and a browser it started later,
// and a reused process id that is no child of it.
func goblinProcesses(launched time.Time, testCPU time.Duration) []Process {
	return []Process{
		{PID: 100, ParentPID: 1, Exe: "claude.exe", Created: 1, Started: launched, CPU: time.Hour, Memory: 500 * megabyte},
		{PID: 101, ParentPID: 100, Exe: "node.exe", Created: 2, Started: launched.Add(10 * time.Second), Memory: 80 * megabyte},
		{PID: 102, ParentPID: 100, Exe: "bash.exe", Created: 3, Started: launched.Add(20 * time.Minute), Memory: 5 * megabyte},
		{PID: 103, ParentPID: 102, Exe: "go.exe", Created: 4, Started: launched.Add(20*time.Minute + time.Second), CPU: testCPU, Memory: 300 * megabyte},
		{PID: 104, ParentPID: 103, Exe: "monitor.test.exe", Created: 5, Started: launched.Add(21 * time.Minute), Memory: 200 * megabyte},
		{PID: 105, ParentPID: 100, Exe: "cmd.exe", Created: 6, Started: launched.Add(30 * time.Minute), Memory: 3 * megabyte},
		{PID: 106, ParentPID: 105, Exe: "node.exe", Created: 7, Started: launched.Add(30*time.Minute + time.Second), CPU: 2 * time.Second, Memory: 150 * megabyte},
		{PID: 107, ParentPID: 100, Exe: "chrome.exe", Created: 8, Started: launched.Add(40 * time.Minute), CPU: time.Second, Memory: 400 * megabyte},
		{PID: 108, ParentPID: 100, Exe: "svchost.exe", Created: 9, Started: launched.Add(-time.Hour), Memory: 900 * megabyte},
	}
}

var goblinCommands = map[int]string{
	102: `"C:\Program Files\Git\bin\bash.exe" -c "source snapshot.sh && eval 'go test ./internal/... 2>&1 | tail -5' \< /dev/null"`,
	103: `go test ./internal/...`,
	105: `C:\WINDOWS\system32\cmd.exe /d /s /c "npm run dev"`,
	106: `node node_modules/vite/bin/vite.js --port 5173`,
	107: `chrome.exe --headless=new`,
}

func TestReadGroupsAGoblinsProcessesIntoJobsWithMemory(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	testCPU := 30 * time.Second
	now := at
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return goblinProcesses(launched, testCPU), nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{106: {5173}}, nil },
		CommandLine: func(pid int) (string, error) { return goblinCommands[pid], nil },
		Now:         func() time.Time { return now },
	}
	goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100}

	// Act: two readings fifteen seconds apart, the test run using the
	// processor between them and the dev server not.
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}
	now, testCPU = at.Add(15*time.Second), testCPU+5*time.Second
	tree, err := reader.Read(context.Background(), goblin)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	test := child(t, tree, "process:102:3")
	if test.Group != GroupTest || test.Label != "Test run" || test.Memory != 505*megabyte || test.State != Working || test.Detail != "go, monitor.test, 3 processes" {
		t.Errorf("test run = %+v", test)
	}
	server := child(t, tree, "process:105:6")
	if server.Group != GroupDevServer || server.Label != "Dev server :5173" || server.Memory != 153*megabyte || server.State != Waiting {
		t.Errorf("dev server = %+v, want an idle dev server on its port", server)
	}
	if browser := child(t, tree, "process:107:8"); browser.Group != GroupBrowser || browser.Memory != 400*megabyte {
		t.Errorf("browser = %+v", browser)
	}
	if len(tree.Children) != 3 {
		t.Errorf("children = %+v, want three jobs: the MCP server is the harness's, the reused id no one's", tree.Children)
	}
	if tree.Memory != (500+80+5+300+200+3+150+400)*megabyte || tree.OwnMemory != 500*megabyte {
		t.Errorf("memory = %d, own %d; want the harness and everything under it, and the harness alone", tree.Memory, tree.OwnMemory)
	}
	if names, used := tree.Jobs(); !slices.Equal(names, []string{"bash.exe (pid 102)", "cmd.exe (pid 105)", "chrome.exe (pid 107)"}) || used != 35*time.Second+2*time.Second+time.Second {
		t.Errorf("Jobs() = %v, %s; want each job named as a wake names it, with its processor time", names, used)
	}
}

func TestClassifyNamesWhatAJobIsDoing(t *testing.T) {
	started := at
	for name, test := range map[string]struct {
		exes     []string
		commands []string
		listens  []int
		group    Group
		label    string
	}{
		"go test":              {[]string{"bash.exe", "go.exe"}, []string{"", "go test ./..."}, nil, GroupTest, "Test run"},
		"a go test binary":     {[]string{"pkg.test.exe"}, []string{""}, []int{51234}, GroupTest, "Test run"},
		"playwright":           {[]string{"cmd.exe", "node.exe", "chrome.exe"}, []string{"", "npx playwright test", ""}, []int{5173}, GroupTest, "Test run"},
		"cfo gate test":        {[]string{"cfo.exe"}, []string{`"C:\home\bin\cfo.exe" gate test --level fast`}, nil, GroupTest, "Test run"},
		"vitest":               {[]string{"node.exe"}, []string{"node vitest run"}, nil, GroupTest, "Test run"},
		"go build":             {[]string{"go.exe"}, []string{"go build ./cmd/cfo"}, nil, GroupBuild, "Build"},
		"npm run build":        {[]string{"cmd.exe", "node.exe"}, []string{"npm run build", ""}, nil, GroupBuild, "Build"},
		"a dev server":         {[]string{"node.exe"}, []string{"vite"}, []int{5174, 5173}, GroupDevServer, "Dev server :5173"},
		"a browser":            {[]string{"msedge.exe", "msedge.exe"}, []string{"", ""}, nil, GroupBrowser, "Browser"},
		"anything else":        {[]string{"bash.exe", "rg.exe"}, []string{`bash -c "eval 'rg -n '\''needle'\'' .'"`, ""}, nil, GroupOther, "rg -n 'needle' ."},
		"a command line alone": {[]string{"python.exe"}, []string{"python -I survey.py"}, nil, GroupOther, "python -I survey.py"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var members []Process
			facts := jobFacts{commands: map[int]string{}, listens: map[int][]int{}}
			for i, exe := range test.exes {
				members = append(members, Process{PID: 10 + i, Exe: exe, Started: started})
				facts.commands[10+i] = test.commands[i]
			}
			facts.listens[10] = test.listens

			// Act
			group, label, _ := classify(job{root: members[0], members: members}, facts)

			// Assert
			if group != test.group || label != test.label {
				t.Errorf("classify = %s %q, want %s %q", group, label, test.group, test.label)
			}
		})
	}
}
