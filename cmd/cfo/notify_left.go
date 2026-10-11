package main

import (
	"errors"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// leftAgain is how long after its first reading a report reads again before
// it names anything: long enough for a tool call the harness runs beside
// the report, such as a git status, to end.
const leftAgain = 2 * time.Second

// defaultLeftRunning reads what task id's goblin started that still runs,
// placed as the supervisor's start watch places it, beside the report this
// process makes (leftByReport). A task whose terminal does not run here, or
// that has no record, started nothing that is read.
func defaultLeftRunning(h home.Home, id string) ([]fleettree.Placed, error) {
	record, err := host.ReadRecord(h.State, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !host.Running(record) {
		return nil, nil
	}
	meta, err := state.ReadTaskMeta(h.State, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	reader := &fleettree.Reader{Owner: lifecycle.OwnerOf(h)}
	goblins := []fleettree.Goblin{{Meta: meta, HarnessPID: record.ChildPID, HarnessStarted: record.ChildStart}}
	read := func() ([]fleettree.Placed, error) { return reader.Placed(goblins) }
	report := os.Getpid()
	return leftByReport(read, report, reportConsole(report, record.ChildPID), leftAgain)
}

// leftByReport reads with read what is left running beside report, the
// process that makes a report, whose console's host is console
// (leftBeside). It reads again, again later, and names only what still runs
// then, by its pid and its start.
func leftByReport(read func() ([]fleettree.Placed, error), report, console int, again time.Duration) ([]fleettree.Placed, error) {
	placed, err := read()
	if err != nil {
		return nil, err
	}
	left := leftBeside(placed, report, console, proc.ConsoleHost)
	if len(left) == 0 {
		return nil, nil
	}
	time.Sleep(again)
	placed, err = read()
	if err != nil {
		return nil, err
	}
	type started struct {
		pid     int
		created int64
	}
	running := make(map[started]bool, len(placed))
	for _, process := range placed {
		running[started{process.PID, process.Created}] = true
	}
	return slices.DeleteFunc(left, func(process fleettree.Placed) bool {
		return !running[started{process.PID, process.Created}]
	}), nil
}

// reportConsole is the host of report's console, which a tool call's shell
// and every program of its pipelines share. It is 0 when report has no
// console, and when report shares the console of terminal, the goblin's
// harness, as where a harness runs its tool calls in its terminal's own
// console: everything the goblin started there shares it too, so it says
// nothing of the reporting command. It is 0 too when either console cannot
// be read, so a console not proved the tool call's own hides nothing.
func reportConsole(report, terminal int) int {
	console, err := proc.ConsoleHost(report)
	if err != nil {
		return 0
	}
	if terminalConsole, err := proc.ConsoleHost(terminal); err != nil || terminalConsole == console {
		return 0
	}
	return console
}

// leftBeside are the processes among placed that are left running beside
// the report process report makes: all but the reporting command's own and
// a machine service, which serves every goblin. The reporting command's own
// end with its tool call: the report and what runs under it, every process
// attached to its console, whose host is console, that host, and every
// process those run under. So neither the shell the report runs in, nor the
// console host Windows starts beside that shell's hidden console, nor a
// program of the report's pipeline is named, though Git Bash starts such a
// program, or a timeout the report runs under, by replacing its own Windows
// process, which leaves it no parent. A program the tool call started with
// a console of its own is named. With console 0, only the report, what runs
// under it and what it runs under are its own. consoleOf reads a process's
// console host, and a process whose host cannot be read shares no console.
func leftBeside(placed []fleettree.Placed, report, console int, consoleOf func(pid int) (int, error)) []fleettree.Placed {
	byPID := make(map[int]fleettree.Placed, len(placed))
	for _, process := range placed {
		byPID[process.PID] = process
	}
	// A process's parent is its parent only when it started first: a parent
	// that exited can leave its id to a younger process.
	parentOf := map[int]int{}
	children := map[int][]int{}
	for _, process := range placed {
		if parent, isPlaced := byPID[process.ParentPID]; isPlaced && parent.PID != process.PID && !process.Started.Before(parent.Started) {
			parentOf[process.PID] = parent.PID
			children[parent.PID] = append(children[parent.PID], process.PID)
		}
	}
	own := map[int]bool{}
	for queue := []int{report}; len(queue) > 0; queue = queue[1:] {
		if !own[queue[0]] {
			own[queue[0]] = true
			queue = append(queue, children[queue[0]]...)
		}
	}
	for _, process := range placed {
		if console == 0 || own[process.PID] || process.Service {
			continue
		}
		if host, err := consoleOf(process.PID); process.PID == console || err == nil && host == console {
			own[process.PID] = true
		}
	}
	// A walk up stops at a process already the command's own, whose own walk
	// goes on above it.
	for _, pid := range slices.Collect(maps.Keys(own)) {
		for parent, hasParent := parentOf[pid]; hasParent && !own[parent]; parent, hasParent = parentOf[parent] {
			own[parent] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(placed), func(process fleettree.Placed) bool {
		return own[process.PID] || process.Service
	})
}
