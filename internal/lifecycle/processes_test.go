package lifecycle

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOwnedProcessesRequireTaskEvidence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gb-task")
	started := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		processes []Process
		want      []int
	}{
		{"worktree server", []Process{{PID: 1, Name: "node.exe", Started: started, Directory: root}}, []int{1}},
		{"scratch test", []Process{{PID: 2, Name: "test.exe", Started: started, Directory: filepath.Join(root, "scratch")}}, []int{2}},
		{"sibling prefix is unrelated", []Process{{PID: 3, Name: "node.exe", Started: started, Directory: root + "-other"}}, nil},
		{"command argument names task script", []Process{{PID: 4, Name: "node.exe", Started: started, Arguments: []string{"node", filepath.Join(root, "server.js")}}}, []int{4}},
		{"prose mentioning path is not ownership", []Process{{PID: 5, Name: "node.exe", Started: started, Arguments: []string{"node", "talk about " + root}}}, nil},
		{"editor viewing a task file is unrelated", []Process{{PID: 15, Name: "Code.exe", Started: started, Arguments: []string{"code", filepath.Join(root, "server.js")}}}, nil},
		{"visible browser viewing a task file is unrelated", []Process{{PID: 16, Name: "chrome.exe", Started: started, Arguments: []string{"chrome", filepath.Join(root, "review.html")}}}, nil},
		{"unknown creation time cannot authorize a kill", []Process{{PID: 6, Name: "node.exe", Directory: root}}, nil},
		{"parent pid alone is never enough", []Process{{PID: 7, Name: "claude.exe", Started: started, Directory: root}, {PID: 8, ParentPID: 7, Name: "chrome.exe", Started: started.Add(-time.Hour)}}, []int{7}},
		{"a browser the goblin opened is not inferred from its parent", []Process{{PID: 9, Name: "claude.exe", Started: started, Directory: root}, {PID: 10, ParentPID: 9, Name: "chrome.exe", Started: started.Add(time.Second)}}, []int{9}},
		{"detached browser bridge still belongs to worktree", []Process{{PID: 11, ParentPID: 900, Name: "node.exe", Started: started, Directory: root, Arguments: []string{"node", "chrome-devtools-axi/bridge.js"}}, {PID: 12, ParentPID: 901, Name: "chrome.exe", Started: started, Arguments: []string{"chrome", "--headless", "--user-data-dir=" + filepath.Join(root, "browser")}}}, []int{11, 12}},
	} {
		t.Run(test.name, func(t *testing.T) {
			owned := OwnedProcesses(test.processes, []string{root}, nil, nil)
			var got []int
			for _, process := range owned {
				got = append(got, process.PID)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selected %v, want %v", got, test.want)
			}
		})
	}
}

func TestOwnedProcessesAcceptVerifiedJobMembersButRejectReusedPIDs(t *testing.T) {
	started := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	processes := []Process{{PID: 42, Name: "node.exe", Started: started}, {PID: 43, Name: "chrome.exe", Started: started.Add(time.Hour)}}
	job := []Identity{{PID: 42, Started: started}, {PID: 43, Started: started}}
	owned := OwnedProcesses(processes, nil, job, nil)
	if len(owned) != 1 || owned[0].PID != 42 {
		t.Fatalf("job membership authorized a reused PID: %+v", owned)
	}
}

func TestOwnedProcessesNeverTreatRelativeOrRootDirectoryAsOwnership(t *testing.T) {
	started := time.Now()
	root := t.TempDir()
	process := Process{PID: 42, Name: "node.exe", Started: started, Directory: root}
	for _, directory := range []string{"", ".", filepath.VolumeName(root) + string(filepath.Separator)} {
		if got := OwnedProcesses([]Process{process}, []string{directory}, nil, nil); len(got) != 0 {
			t.Fatalf("unsafe ownership root %q selected %+v", directory, got)
		}
	}
}

// A goblin's teardown never ends a machine service it started. On 2026-10-07
// a pause stopped Docker Desktop, its build and its WSL processes as the
// goblin's: Docker Desktop had started from the goblin's worktree, inside its
// terminal's job. On 2026-10-01 one stopped the no-mistakes daemon every gate
// shared, with every other goblin's gate agents under it. The daemon's agent
// at work in this task's own gate worktree is still the task's to stop, and
// the goblin's own commands, a wsl.exe it ran included, are still its own.
func TestOwnedProcessesSpareMachineServicesAGoblinStarted(t *testing.T) {
	// Arrange
	root := filepath.Join(t.TempDir(), "gb-task")
	gate := filepath.Join(t.TempDir(), "gate-run")
	started := time.Date(2026, 10, 7, 12, 45, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return started.Add(time.Duration(seconds) * time.Second) }
	processes := []Process{
		{PID: 10, ParentPID: 1, Name: "pwsh.exe", Started: at(0), Directory: root},
		{PID: 11, ParentPID: 10, Name: "Docker Desktop.exe", Started: at(1), Directory: root, Arguments: []string{`C:\Program Files\Docker\Docker\Docker Desktop.exe`}},
		{PID: 12, ParentPID: 11, Name: "com.docker.backend.exe", Started: at(2), Directory: root},
		{PID: 13, ParentPID: 12, Name: "wsl.exe", Started: at(3), Arguments: []string{"wsl.exe", "-d", "docker-desktop"}},
		{PID: 14, ParentPID: 13, Name: "wslhost.exe", Started: at(4)},
		{PID: 15, ParentPID: 11, Name: "com.docker.build.exe", Started: at(5)},
		{PID: 20, ParentPID: 10, Name: "no-mistakes.exe", Started: at(6), Directory: root, Arguments: []string{"no-mistakes", "daemon", "run", "--root", `C:\Users\o\.no-mistakes`}},
		{PID: 21, ParentPID: 20, Name: "claude.exe", Started: at(7), Directory: filepath.Join(t.TempDir(), "another-gate-run")},
		{PID: 22, ParentPID: 20, Name: "claude.exe", Started: at(8), Directory: gate},
		{PID: 30, ParentPID: 10, Name: "wsl.exe", Started: at(9), Arguments: []string{"wsl.exe", "-e", "go", "test"}},
		{PID: 31, ParentPID: 10, Name: "node.exe", Started: at(10), Directory: root},
	}
	var job []Identity
	for _, process := range processes {
		job = append(job, Identity{PID: process.PID, Started: process.Started})
	}

	// Act
	owned := OwnedProcesses(processes, []string{root, gate}, job, nil)

	// Assert
	var got []int
	for _, process := range owned {
		got = append(got, process.PID)
	}
	if want := []int{10, 22, 30, 31}; !reflect.DeepEqual(got, want) {
		t.Fatalf("owned %v, want %v: the goblin's shell, its own gate's agent, its wsl command and its server, never Docker Desktop's processes or the shared daemon and another gate's agent", got, want)
	}
}

// A process carries the proof value of the terminal it was started in, and
// keeps it when its parent exits and when Git Bash starts it outside the
// terminal's job. On 2026-10-09 two browser bridges whose parents were gone
// held 3.7 GB through five memory pauses: they worked in no folder of a
// task's and sat in no terminal's job, so nothing a stop could read made
// them a task's. The mark makes them the terminal's own, and never makes a
// task's own of a machine service, a gate's agent, or a program the Overlord
// uses himself.
func TestOwnedProcessesFollowTheTerminalsMark(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gb-task")
	gate := filepath.Join(t.TempDir(), "gate-run")
	elsewhere := t.TempDir()
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return started.Add(time.Duration(seconds) * time.Second) }
	own := Mark{Terminal: "gb-task", ProofSum: "own-digest"}
	earlier := Mark{Terminal: "gb-task", ProofSum: "earlier-digest"}
	other := Mark{Terminal: "gb-other", ProofSum: "other-digest"}
	forged := Mark{Terminal: "gb-task", ProofSum: "no-digest-of-this-terminal"}
	for _, test := range []struct {
		name      string
		processes []Process
		want      []int
	}{
		{"a detached bridge whose parent is gone", []Process{
			{PID: 1, ParentPID: 900, Name: "node.exe", Started: at(0), Directory: elsewhere, Arguments: []string{"node", `C:\npm\chrome-devtools-axi\dist\bin\chrome-devtools-axi-bridge.js`}, Mark: own},
		}, []int{1}},
		{"a server started before the terminal was switched", []Process{
			{PID: 2, ParentPID: 900, Name: "node.exe", Started: at(0), Directory: elsewhere, Mark: earlier},
		}, []int{2}},
		{"another terminal's process", []Process{
			{PID: 3, Name: "node.exe", Started: at(0), Directory: elsewhere, Mark: other},
		}, nil},
		{"a value no host of this terminal gave", []Process{
			{PID: 4, Name: "node.exe", Started: at(0), Directory: elsewhere, Mark: forged},
		}, nil},
		{"an unmarked process outside the task's folders", []Process{
			{PID: 5, Name: "node.exe", Started: at(0), Directory: elsewhere},
		}, nil},
		{"a gate's agent the daemon started with the goblin's environment", []Process{
			{PID: 6, ParentPID: 900, Name: "codex.exe", Started: at(0), Directory: elsewhere, Mark: own, IsGateAgent: true},
			{PID: 7, ParentPID: 900, Name: "codex.exe", Started: at(1), Directory: gate, Mark: own, IsGateAgent: true},
		}, []int{7}},
		{"the Scrawl server every goblin's page is served by", []Process{
			{PID: 8, ParentPID: 900, Name: "node.exe", Started: at(0), Directory: root, Arguments: []string{"node", `C:\Users\o\AppData\Roaming\npm\node_modules\lavish-axi\dist\server.mjs`, "server", "--port", "4455"}, Mark: own},
		}, nil},
		{"a browser the goblin opened for the Overlord, with its pages", []Process{
			{PID: 9, ParentPID: 900, Name: "chrome.exe", Started: at(0), Arguments: []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, "https://github.com/login/device"}, Mark: own},
			{PID: 10, ParentPID: 9, Name: "chrome.exe", Started: at(1), Arguments: []string{"chrome.exe", "--type=renderer"}, Mark: own},
		}, nil},
		{"a headless browser a tool drives, with its pages", []Process{
			{PID: 11, ParentPID: 900, Name: "chrome.exe", Started: at(0), Arguments: []string{"chrome.exe", "--headless", "--remote-debugging-pipe", "--user-data-dir=" + filepath.Join(elsewhere, "profile")}, Mark: own},
			{PID: 12, ParentPID: 11, Name: "chrome.exe", Started: at(1), Arguments: []string{"chrome.exe", "--type=renderer"}, Mark: own},
		}, []int{11, 12}},
		{"a program that shows a window, with what it started", []Process{
			{PID: 13, ParentPID: 900, Name: "Code.exe", Started: at(0), Directory: elsewhere, Mark: own, HasWindow: true},
			{PID: 14, ParentPID: 13, Name: "node.exe", Started: at(1), Directory: elsewhere, Mark: own},
		}, nil},
		{"a packaged desktop app", []Process{
			{PID: 15, ParentPID: 900, Name: "claude.exe", Started: at(0), Arguments: []string{`C:\Program Files\WindowsApps\Claude_1.2.3.0_x64__pzs8sxrjxfjjc\app\claude.exe`}, Mark: own},
		}, nil},
		{"a shell tool under the goblin's shell, which carries no mark of its own", []Process{
			{PID: 16, Name: "bash.exe", Started: at(0), Directory: elsewhere, Mark: own},
			{PID: 17, ParentPID: 16, Name: "sleep.exe", Started: at(1), Directory: elsewhere},
			{PID: 18, ParentPID: 17, Name: "grep.exe", Started: at(2), Directory: elsewhere},
		}, []int{16, 17, 18}},
		{"a process that took the pid of an owned one's parent", []Process{
			{PID: 19, Name: "bash.exe", Started: at(5), Directory: elsewhere, Mark: own},
			{PID: 20, ParentPID: 19, Name: "sleep.exe", Started: at(0), Directory: elsewhere},
		}, []int{19}},
		{"a window the goblin's shell opened", []Process{
			{PID: 21, Name: "bash.exe", Started: at(0), Directory: elsewhere, Mark: own},
			{PID: 22, ParentPID: 21, Name: "notepad.exe", Started: at(1), HasWindow: true},
		}, []int{21}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			owned := OwnedProcesses(test.processes, []string{root, gate}, nil, []Mark{own, earlier})

			// Assert
			var got []int
			for _, process := range owned {
				got = append(got, process.PID)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("owned %v, want %v", got, test.want)
			}
		})
	}
}
