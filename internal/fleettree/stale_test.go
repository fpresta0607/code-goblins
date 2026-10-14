package fleettree

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The Overlord, 2026-10-13: "baby goblins still displayed in the
// orchestration in the cards. If they're silent for hours and they're no
// longer running any processes, then you need to clean them up." Claude Code
// records a monitor that ran out its time only as one last event, and leaves
// no record at all of a shell or monitor stopped from its own screen or at
// teardown, so such a child read as silent for as long as its goblin ran.

// A monitor Claude Code ended at its time limit has ended, though its record
// says so only in its last event.
func TestReadEndsAMonitorItsHarnessSaysExpired(t *testing.T) {
	for _, event := range []string{
		"[Monitor expired after 30m with 1 event delivered. Re-arm it if you still need the watch.]",
		"[Monitor expired after 30m with no events delivered. Re-arm it if you still need the watch - and widen the filter if silence was unexpected.]",
		"[Monitor timed out - re-arm if needed.]",
	} {
		t.Run(event, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			start := at.Add(-time.Hour)
			expired := notification("m1", "", "", `Monitor event: "Harness prepush (pid 24492)"`, event)
			goblin, _ := claudeGoblin(t, home,
				call("toolu_M", "Monitor", object{"description": "Harness prepush (pid 24492)", "command": "until done; do sleep 20; done", "timeout_ms": 1800000}, start),
				result("toolu_M", "Monitor started", object{"taskId": "m1", "timeoutMs": 1800000}, start),
				queued(expired, start.Add(30*time.Minute)),
				delivered(expired, start.Add(30*time.Minute+time.Second)),
			)
			reader := Reader{Home: home, Now: func() time.Time { return at }}

			// Act
			tree, _ := reader.Read(context.Background(), goblin)

			// Assert
			if monitor := child(t, tree, "monitor:m1"); monitor.State != Done || !monitor.Finished.Equal(start.Add(30*time.Minute)) {
				t.Errorf("expired monitor = %+v, want done when it expired", monitor)
			}
			if waits := tree.Waits(); len(waits) != 0 {
				t.Errorf("Waits() = %v, want nothing: the monitor ended", waits)
			}
		})
	}
}

// staleHarnessStart is when the goblin's harness started in the stale
// fixtures, long before any of its children.
var staleHarnessStart = at.Add(-5 * time.Hour)

// staleHarness is a goblin's Claude Code process and what it runs.
func staleHarness(running ...Process) []Process {
	return append([]Process{{PID: 100, ParentPID: 1, Exe: "claude.exe", Created: 1, Started: staleHarnessStart, Memory: 500 * megabyte}}, running...)
}

// A background shell or monitor whose processes have all ended, and that has
// been quiet past the silent mark, has ended though no record says so, and
// is no child of the goblin's any more. One whose process still runs stays,
// however quiet, as a gate turn waiter queued in the line is. One whose
// processes ended moments ago still shows until the mark, since its record
// of how it ended may still be on its way.
func TestReadEndsAChildWhoseProcessesEndedOnceSilentPastTheMark(t *testing.T) {
	quiet := at.Add(-2 * time.Hour)
	recent := at.Add(-2 * time.Minute)
	output := `C:\Users\goblin\AppData\Local\Temp\claude\tasks\b5.output`
	for _, tc := range []struct {
		name    string
		tool    string
		command string
		started time.Time
		running []Process
		lines   map[int]string
		want    State
	}{
		{name: "a shell whose processes ended, quiet for hours", tool: "Bash", command: "go test ./...", started: quiet, want: Done},
		{name: "a monitor whose processes ended, quiet for hours", tool: "Monitor", command: "until done; do sleep 20; done", started: quiet, want: Done},
		{name: "a quiet shell whose process still runs", tool: "Bash", command: "goblins gate turn -- go test ./...", started: quiet,
			running: []Process{{PID: 200, ParentPID: 100, Exe: "bash.exe", Created: 20, Started: quiet}},
			lines:   map[int]string{200: `"C:\Program Files\Git\bin\bash.exe" -c "source snapshot.sh && eval 'goblins gate turn -- go test ./...' < /dev/null"`}, want: Silent},
		{name: "a quiet shell with quotes in its command whose process still runs", tool: "Bash", command: "echo \"it's\"; sleep 600", started: quiet,
			running: []Process{{PID: 200, ParentPID: 100, Exe: "bash.exe", Created: 20, Started: quiet}},
			lines:   map[int]string{200: `"C:\Program Files\Git\bin\bash.exe" -c "source snapshot.sh && eval 'echo \"it'\"'\"'s\"; sleep 600' < /dev/null"`}, want: Silent},
		{name: "a quiet PowerShell shell whose process still runs", tool: "PowerShell", command: "Wait-Process -Id 4242", started: quiet,
			running: []Process{{PID: 200, ParentPID: 100, Exe: "cmd.exe", Created: 20, Started: quiet}},
			lines:   map[int]string{200: `C:\WINDOWS\System32\cmd.exe /d /s /c ""C:\WINDOWS\System32\chcp.com" 65001 >nul & "powershell.exe" -NoProfile > "` + output + `" 2>&1"`}, want: Silent},
		{name: "a quiet monitor whose process still runs", tool: "Monitor", command: "until done; do sleep 20; done", started: quiet,
			running: []Process{{PID: 200, ParentPID: 100, Exe: "bash.exe", Created: 20, Started: quiet}},
			lines:   map[int]string{200: `"C:\Program Files\Git\bin\bash.exe" -c "source snapshot.sh && eval 'until done; do sleep 20; done' < /dev/null"`}, want: Silent},
		{name: "a shell whose processes ended moments ago", tool: "Bash", command: "go test ./...", started: recent, want: Working},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			recorded := object{"backgroundTaskId": "b5"}
			id := "shell:b5"
			if tc.tool == "Monitor" {
				recorded, id = object{"taskId": "b5"}, "monitor:b5"
			}
			goblin, _ := claudeGoblin(t, home,
				call("toolu_S", tc.tool, object{"command": tc.command, "description": "Wait on the run", "run_in_background": true}, tc.started),
				result("toolu_S", "Command running in background with ID: b5. Output is being written to: "+output, recorded, tc.started),
			)
			goblin.HarnessPID = 100
			reader := Reader{
				Home:        home,
				Processes:   func() ([]Process, error) { return staleHarness(tc.running...), nil },
				Listeners:   func() (map[int][]int, error) { return nil, nil },
				CommandLine: func(pid int) (string, error) { return tc.lines[pid], nil },
				Now:         func() time.Time { return at },
			}

			// Act
			tree, err := reader.Read(context.Background(), goblin)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := child(t, tree, id); got.State != tc.want {
				t.Errorf("%s = %+v, want %s", id, got, tc.want)
			}
			// A child the monitor reads as holding the goblin's turn is named
			// by its process while one runs, else as a wait.
			jobs, _ := tree.Jobs()
			if named := append(jobs, tree.Waits()...); (tc.want == Done) != (len(named) == 0) {
				t.Errorf("Jobs() and Waits() = %v, want the child named only while it is not done", named)
			}
			for _, node := range tree.Children {
				if node.Kind == KindProcess {
					t.Errorf("the child's own process is drawn again as %+v", node)
				}
			}
		})
	}
}

// A Claude Code shell's command line holds the command it runs as one bash
// word inside a Windows argument: Windows escapes each double quote, and
// Claude Code writes each single quote as '"'"'. The command is read back
// as typed, so a shell's processes are matched to it whatever it quotes.
func TestShellCommandReadsTheCommandAsTyped(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{"a heredoc with quotes on several lines", "\"C:\\Program Files\\Git\\bin\\bash.exe\" -c \"source snapshot.sh 2>/dev/null || true && eval 'cat <<'\\\"'\\\"'X'\\\"'\\\"' > /dev/null\nit'\\\"'\\\"'s a \\\"quoted\\\" heredoc\nX\necho \\\"it'\\\"'\\\"'s multi\nline\\\"; sleep 600' < /dev/null && pwd -P >| /tmp/cwd\"",
			"cat <<'X' > /dev/null\nit's a \"quoted\" heredoc\nX\necho \"it's multi\nline\"; sleep 600"},
		{"a single quote written the older way", `bash.exe -c "eval 'echo it'\''s' < /dev/null"`, "echo it's"},
		{"a plain command", `"C:\Program Files\Git\bin\bash.exe" -c "source snapshot.sh && eval 'go test ./internal/... 2>&1 | tail -5' \< /dev/null"`, "go test ./internal/... 2>&1 | tail -5"},
		{"no command evaluated", `C:\WINDOWS\system32\cmd.exe /d /s /c "npm run dev"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellCommand(tc.line); got != tc.want {
				t.Errorf("shellCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// A process that only helps a child, such as a sleep in a monitor's loop,
// the shell a monitor runs in, a command wrapper or the gate line's waiter,
// is no child of its own: it counts in the child that started it, or in the
// goblin when the goblin's own command did. Only a server, a test run, a
// build or a browser is a child of its own. Git Bash runs each program of a
// loop by replacing its own process, which leaves the program no parent, so
// a sleep is placed by the console it shares with the shell that runs it.
func TestReadDrawsNoHelperProcessAsAChildOfItsOwn(t *testing.T) {
	// Arrange
	home := t.TempDir()
	start := at.Add(-time.Hour)
	monitorCommand := `until [ -f done ]; do sleep 20; done`
	goblin, _ := claudeGoblin(t, home,
		call("toolu_M", "Monitor", object{"description": "Wait for the gate run", "command": monitorCommand, "timeout_ms": 1800000}, start),
		result("toolu_M", "Monitor started", object{"taskId": "m1"}, start),
	)
	goblin.HarnessPID = 100
	processes := staleHarness(
		// The monitor's shell and its hidden console's host.
		Process{PID: 200, ParentPID: 100, Exe: "bash.exe", Created: 20, Started: start, Memory: 5 * megabyte},
		Process{PID: 201, ParentPID: 200, Exe: "conhost.exe", Created: 21, Started: start, Memory: 7 * megabyte},
		// A sleep of the monitor's loop, its parent gone.
		Process{PID: 300, ParentPID: 999, Exe: "sleep.exe", Created: 30, Started: at.Add(-5 * time.Second), Memory: megabyte},
		// A command the goblin runs in the foreground.
		Process{PID: 400, ParentPID: 100, Exe: "cmd.exe", Created: 40, Started: at.Add(-5 * time.Minute), Memory: 5 * megabyte},
		// The gate line's waiter, started hidden in a console of its own.
		Process{PID: 500, ParentPID: 998, Exe: "goblins.exe", Created: 50, Started: at.Add(-40 * time.Minute), Memory: 9 * megabyte},
		Process{PID: 501, ParentPID: 500, Exe: "conhost.exe", Created: 51, Started: at.Add(-40 * time.Minute), Memory: 7 * megabyte},
		// A dev server.
		Process{PID: 600, ParentPID: 100, Exe: "cmd.exe", Created: 60, Started: at.Add(-50 * time.Minute), Memory: 3 * megabyte},
		Process{PID: 601, ParentPID: 600, Exe: "node.exe", Created: 61, Started: at.Add(-50 * time.Minute), Memory: 150 * megabyte},
	)
	commands := map[int]string{
		200: `"C:\Program Files\Git\bin\bash.exe" -c "source snapshot.sh && eval '` + monitorCommand + `' < /dev/null"`,
		300: `"C:\Program Files\Git\usr\bin\sleep.exe" 20`,
		400: `C:\WINDOWS\system32\cmd.exe /d /s /c "lavish-axi poll page.html"`,
		500: `"C:\dev\code-goblins\bin\goblins.exe" gate turn -- powershell -File run.ps1`,
		600: `C:\WINDOWS\system32\cmd.exe /d /s /c "npm run dev"`,
		601: `node node_modules/vite/bin/vite.js --port 5173`,
	}
	consoles := map[int]int{300: 201, 500: 501}
	reader := Reader{
		Home:        home,
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{601: {5173}}, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		Owner:       func(pid int) string { return map[int]string{300: "tree", 500: "tree"}[pid] },
		Console: func(pid int) (int, error) {
			if host, ok := consoles[pid]; ok {
				return host, nil
			}
			return 0, errors.New("no console")
		},
		Now: func() time.Time { return at },
	}

	// Act
	tree, err := reader.Read(context.Background(), goblin)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, node := range tree.Children {
		ids = append(ids, node.ID)
	}
	if !slices.Equal(ids, []string{"monitor:m1", "process:600:60"}) {
		t.Fatalf("children = %v, want the monitor and the dev server only", ids)
	}
	if monitor := child(t, tree, "monitor:m1"); monitor.Memory != 13*megabyte {
		t.Errorf("monitor memory = %d MB, want its shell, its console's host and its sleep, 13 MB", monitor.Memory/megabyte)
	}
	if tree.Memory != 687*megabyte {
		t.Errorf("the goblin's memory = %d MB, want every process counted once, 687 MB", tree.Memory/megabyte)
	}
	if names, _ := tree.Jobs(); !slices.Contains(names, "cmd.exe (pid 400)") {
		t.Errorf("Jobs() = %v, want the goblin's own command still named for the monitor", names)
	}
}

// Each child the board opens shows its own output: a background shell what
// it wrote, a monitor its events, and a job of processes what each of its
// programs runs, never another child's and never the goblin's terminal.
func TestChildLinesAreEachChildsOwnOutput(t *testing.T) {
	// Arrange
	home := t.TempDir()
	start := at.Add(-20 * time.Minute)
	first, second := filepath.Join(home, "tasks", "b1.output"), filepath.Join(home, "tasks", "b2.output")
	writeFile(t, first, "\x1b[32mok\x1b[0m  internal/monitor 41.2s\n", at)
	writeFile(t, second, "building the board\r\ndist/board ready\n", at)
	goblin, _ := claudeGoblin(t, home,
		call("toolu_S1", "Bash", object{"command": "go test ./internal/monitor/", "description": "Run the monitor tests", "run_in_background": true}, start),
		result("toolu_S1", "Command running in background with ID: b1. Output is being written to: "+first, object{"backgroundTaskId": "b1"}, start),
		call("toolu_S2", "Bash", object{"command": "npm run build", "description": "Build the board", "run_in_background": true}, start),
		result("toolu_S2", "Command running in background with ID: b2. Output is being written to: "+second, object{"backgroundTaskId": "b2"}, start),
		call("toolu_M", "Monitor", object{"description": "CI checks", "command": "gh pr checks 398 --watch"}, start),
		result("toolu_M", "Monitor started", object{"taskId": "m1"}, start),
		queued(notification("m1", "", "", `Monitor event: "CI checks"`, "test: pending\nlint: pass"), at.Add(-time.Minute)),
	)
	goblin.HarnessPID = 100
	processes := staleHarness(
		Process{PID: 600, ParentPID: 100, Exe: "cmd.exe", Created: 60, Started: start, Memory: 3 * megabyte},
		Process{PID: 601, ParentPID: 600, Exe: "node.exe", Created: 61, Started: start, Memory: 150 * megabyte},
	)
	commands := map[int]string{600: `C:\WINDOWS\system32\cmd.exe /d /s /c "npm run dev"`, 601: `node node_modules/vite/bin/vite.js --port 5173`}
	reader := Reader{
		Home:        home,
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{601: {5173}}, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		Now:         func() time.Time { return at },
	}
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		node string
		want []string
	}{
		{"shell:b1", []string{"> go test ./internal/monitor/", "ok  internal/monitor 41.2s"}},
		{"shell:b2", []string{"> npm run build", "building the board", "dist/board ready"}},
		{"monitor:m1", []string{"> gh pr checks 398 --watch", "test: pending", "lint: pass"}},
		{"process:600:60", []string{"> npm run dev", "cmd.exe (pid 600), 3 MB: npm run dev", "node.exe (pid 601), 150 MB: node node_modules/vite/bin/vite.js --port 5173"}},
	} {
		t.Run(tc.node, func(t *testing.T) {
			// Act
			lines, err := reader.ChildLines("tree", tc.node)

			// Assert
			if err != nil || !slices.Equal(lines, tc.want) {
				t.Errorf("ChildLines(%s) = %q, %v; want %q", tc.node, lines, err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ task, node string }{{"tree", "shell:b9"}, {"other", "shell:b1"}} {
		t.Run("none for "+tc.task+" "+tc.node, func(t *testing.T) {
			if lines, err := reader.ChildLines(tc.task, tc.node); !errors.Is(err, ErrNoRecord) {
				t.Errorf("ChildLines(%s, %s) = %q, %v; want ErrNoRecord", tc.task, tc.node, lines, err)
			}
		})
	}
}
