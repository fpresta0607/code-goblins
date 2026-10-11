package startwatch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// at is the fixtures' clock: a moment on 2026-10-11.
var at = time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC)

// started is a process of owner's, started at its first reading.
func started(owner string, pid int, exe, command string) Process {
	return Process{Placed: fleettree.Placed{Process: fleettree.Process{PID: pid, ParentPID: 1, Exe: exe, Created: int64(pid), Started: at}, Owner: owner, Command: command}}
}

func details(findings []Finding) []string {
	var lines []string
	for _, finding := range findings {
		lines = append(lines, finding.Detail)
	}
	return lines
}

// The Overlord on 2026-10-11: "make sure if your testign or goblins open up
// terminals or windows to check or run tests tataht it finsihes and celans
// up". A process of a goblin's that shows on his screen is told once, with
// whose it is and what it runs, and a browser's helpers shown with it are
// told with it.
func TestAWindowAGoblinsProcessShowsIsToldOnce(t *testing.T) {
	// Arrange
	browser := started("cg-a", 300, "msedge.exe", `"C:\Program Files\Microsoft\Edge\Application\msedge.exe" --user-data-dir=C:\scratch\cg-a\profile http://localhost:5173/?token=abc`)
	browser.Shown = "a window of its own"
	helper := started("cg-a", 301, "msedge.exe", `msedge.exe --type=renderer`)
	helper.ParentPID, helper.Shown = 300, "a window of its own"
	hidden := started("cg-a", 302, "node.exe", "node server.js")
	reading := Reading{At: at, CFO: "cfo", Processes: []Process{browser, helper, hidden}}
	var watch Watch

	// Act
	first := watch.Read(reading)
	reading.At = at.Add(5 * time.Second)
	second := watch.Read(reading)

	// Assert
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("findings = %q then %q, want the browser once", details(first), details(second))
	}
	finding := first[0]
	if finding.Owner != "cg-a" || finding.Identity != "window/cg-a/300/300" {
		t.Errorf("finding = %+v, want cg-a's browser by its pid and start", finding)
	}
	for _, want := range []string{"window_shown: cg-a's msedge.exe (pid 300)", "shows a window of its own on the Overlord's screen", "http://localhost:5173/", "Next: tell cg-a to end it by its pid"} {
		if !strings.Contains(finding.Detail, want) {
			t.Errorf("detail = %q, want %q", finding.Detail, want)
		}
	}
	if strings.Contains(finding.Detail, "token=abc") {
		t.Errorf("detail = %q shows a URL's query, which can hold a credential", finding.Detail)
	}
}

// A command one owner starts again and again is a loop: told once, when the
// tenth new process of it within two minutes is seen. A carrier, a process
// its harness started itself and a machine service are no loop: Git Bash
// forks a copy of a script's shell for each pipeline, a poll waits with
// sleep, and a goblin's own turns start the same hook again and again.
func TestACommandStartedAgainAndAgainIsALoopToldOnce(t *testing.T) {
	// Arrange
	var watch Watch
	var findings []Finding

	// Act: a reading every 5 seconds for a minute and a half, each seeing a
	// new process of each command.
	for reading := 0; reading < 18; reading++ {
		pid := 1000 + 10*reading
		server := started("cg-a", pid, "node.exe", "node server.js --port 3000")
		other := started("cg-b", pid+1, "node.exe", "node server.js --port 3000")
		shell := started("cg-a", pid+2, "bash.exe", "bash tools/run.sh")
		wait := started("cg-a", pid+3, "sleep.exe", "sleep 5")
		hook := started("cg-a", pid+4, "cfo.exe", "cfo.exe native claude")
		hook.ByHarness = true
		agent := started("cg-a", pid+5, "claude.exe", "claude.exe -p")
		agent.Service = true
		processes := []Process{server, shell, wait, hook, agent}
		if reading%2 == 0 {
			processes = append(processes, other)
		}
		findings = append(findings, watch.Read(Reading{At: at.Add(time.Duration(reading) * 5 * time.Second), CFO: "cfo", Generations: map[string]string{"cg-a": "s1", "cg-b": "s2"}, Processes: processes})...)
	}

	// Assert
	if len(findings) != 1 {
		t.Fatalf("findings = %q, want cg-a's server once", details(findings))
	}
	for _, want := range []string{"start_loop: cg-a started node.exe 10 times in the last 45s, the last as pid 1090: node server.js --port 3000", "Next: peek cg-a"} {
		if !strings.Contains(findings[0].Detail, want) {
			t.Errorf("detail = %q, want %q", findings[0].Detail, want)
		}
	}
	if findings[0].Owner != "cg-a" || !strings.HasPrefix(findings[0].Identity, "loop/cg-a/s1/") || strings.Contains(findings[0].Identity, "server") {
		t.Errorf("finding = %+v, want cg-a's loop in its generation, by a digest of the command", findings[0])
	}
}

// A process started now and then is no loop: nine within two minutes, or
// the same command a minute apart for an hour, stays untold.
func TestACommandStartedNowAndThenIsNoLoop(t *testing.T) {
	for name, every := range map[string]time.Duration{"nine in two minutes": 14 * time.Second, "once a minute": time.Minute} {
		t.Run(name, func(t *testing.T) {
			var watch Watch
			for reading := 0; reading < 60; reading++ {
				process := started("cg-a", 2000+reading, "gh.exe", "gh run view 1 --json status")
				if findings := watch.Read(Reading{At: at.Add(time.Duration(reading) * every), Processes: []Process{process}}); len(findings) != 0 {
					t.Fatalf("reading %d found %q, want nothing", reading, details(findings))
				}
			}
		})
	}
}

// A goblin that reports done and rests with something it started still
// running is told once a report, a minute into its rest, with each process
// it left: a dev server, with the browser under it. A machine service it
// started is no leftover, and neither is a process started since it rests.
func TestAGoblinThatRestsWithAProcessLeftRunningIsToldOnceAReport(t *testing.T) {
	// Arrange
	server := started("cg-a", 400, "node.exe", "node node_modules/vite/bin/vite.js --port 5173")
	browser := started("cg-a", 401, "chrome.exe", "chrome.exe --headless=new")
	browser.ParentPID = 400
	daemon := started("cg-a", 402, "no-mistakes.exe", "no-mistakes.exe daemon")
	daemon.Service = true
	rest := Rest{Report: "done: PR https://github.com/o/r/pull/9", ReportedAt: at}
	var watch Watch
	read := func(offset time.Duration, rest Rest, processes ...Process) []Finding {
		return watch.Read(Reading{At: at.Add(offset), CFO: "cfo", Processes: processes, Resting: map[string]Rest{"cg-a": rest}})
	}

	// Act
	var findings []Finding
	findings = append(findings, read(0, rest, server, browser, daemon)...)
	findings = append(findings, read(30*time.Second, rest, server, browser, daemon)...)
	late := started("cg-a", 403, "sleep.exe", "sleep 30")
	told := read(time.Minute, rest, server, browser, daemon, late)
	findings = append(findings, read(2*time.Minute, rest, server, browser, daemon, late)...)
	again := read(3*time.Minute, Rest{Report: "done: PR https://github.com/o/r/pull/10", ReportedAt: at.Add(150 * time.Second)}, server, browser)
	again = append(again, read(4*time.Minute, Rest{Report: "done: PR https://github.com/o/r/pull/10", ReportedAt: at.Add(150 * time.Second)}, server, browser)...)

	// Assert
	if len(findings) != 0 || len(told) != 1 || len(again) != 1 {
		t.Fatalf("findings = %q, then %q at a minute, then %q for the next report, want one at a minute and one for the next report", details(findings), details(told), details(again))
	}
	want := `left_running: cg-a reported done at 04:00Z and rests with 2 processes it started still running: pid 400 node.exe "node node_modules/vite/bin/vite.js --port 5173" (under it chrome.exe pid 401). Next: tell cg-a to end each it no longer needs by its pid`
	if !strings.HasPrefix(told[0].Detail, want) {
		t.Errorf("detail = %q, want it to start %q", told[0].Detail, want)
	}
	if told[0].Identity == again[0].Identity {
		t.Errorf("both reports were told as %s, want one identity a report", told[0].Identity)
	}
}

// Named names each process whose parent is not among those named, with
// the programs that run under it.
func TestNamedNamesEachTreeOnce(t *testing.T) {
	var processes []fleettree.Placed
	for pid := 1; pid <= 5; pid++ {
		processes = append(processes, fleettree.Placed{Process: fleettree.Process{PID: pid * 10, ParentPID: (pid - 1) * 10, Exe: fmt.Sprintf("p%d.exe", pid)}, Command: fmt.Sprintf("p%d", pid)})
	}
	processes = append(processes, fleettree.Placed{Process: fleettree.Process{PID: 99, ParentPID: 5, Exe: "other.exe"}, Command: "other --flag"})
	if got, want := Named(processes), `pid 10 p1.exe "p1" (under it p2.exe pid 20, p3.exe pid 30, p4.exe pid 40 and 1 more), pid 99 other.exe "other --flag"`; got != want {
		t.Errorf("Named = %q, want %q", got, want)
	}
	var many []fleettree.Placed
	for pid := 1; pid <= 7; pid++ {
		many = append(many, fleettree.Placed{Process: fleettree.Process{PID: pid, ParentPID: 900, Exe: "sleep.exe"}, Command: "sleep 30"})
	}
	if got := Named(many); !strings.HasSuffix(got, `pid 5 sleep.exe "sleep 30", and 2 more`) {
		t.Errorf("Named = %q, want five trees and how many more", got)
	}
}
