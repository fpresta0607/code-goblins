package fleettree

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/verify"
)

// Process is one running process as one system snapshot read it.
type Process struct {
	PID       int
	ParentPID int
	Exe       string
	// Created is the creation time in the units Windows keeps it, 100 ns
	// intervals since 1601, the form a harness's own record of its process
	// names it in; Started is the same instant.
	Created int64
	Started time.Time
	// CPU is the processor time the process has used, and Memory its
	// private working set in bytes.
	CPU    time.Duration
	Memory uint64
}

// HarnessLaunch is how long after a harness starts the processes it starts
// still belong to launching it: its MCP servers, or the real binary a shim
// runs. A process it starts later is work it was asked to do.
const HarnessLaunch = 2 * time.Minute

// launchShims are the programs a harness is commonly started through. One
// that launched a single process as it started is walked through to the
// harness it runs, so a harness installed behind node or a .cmd wrapper is
// read where it actually runs its tools.
var launchShims = map[string]bool{"cmd": true, "node": true}

// job is one process the harness started after launching, with everything
// under it.
type job struct {
	root    Process
	members []Process
}

func (j job) cpu() time.Duration {
	var used time.Duration
	for _, member := range j.members {
		used += member.CPU
	}
	return used
}

func (j job) memory() uint64 {
	var used uint64
	for _, member := range j.members {
		used += member.Memory
	}
	return used
}

// harnessProcesses is a goblin's harness as the process list shows it.
type harnessProcesses struct {
	// top is the process the goblin's terminal runs, and harness the
	// harness itself, past any launch shim.
	top     Process
	harness Process
	// all is the process the goblin's terminal runs and every process under
	// it, the harness and its MCP servers included.
	all []Process
	// jobs are what the harness started after launching, by process id.
	jobs []job
}

// readHarness finds the harness under root and the jobs it started after
// launching, in processes. A process started within launch of the harness is
// part of the harness - its MCP servers are the common case, and they idle
// for its whole life - so it is never a job. A child created before its
// parent is a reused process id, not a child, and is skipped. ok is false
// when root is not running.
func readHarness(root int, processes []Process, launch time.Duration) (harnessProcesses, bool) {
	byPID := make(map[int]Process, len(processes))
	children := make(map[int][]Process)
	for _, process := range processes {
		byPID[process.PID] = process
		children[process.ParentPID] = append(children[process.ParentPID], process)
	}
	top, found := byPID[root]
	if !found {
		return harnessProcesses{}, false
	}
	childrenOf := func(pid int, after time.Time) []Process {
		var kept []Process
		for _, child := range children[pid] {
			if child.PID != pid && !child.Started.Before(after) {
				kept = append(kept, child)
			}
		}
		return kept
	}
	launchEnds := top.Started.Add(launch)
	harness := top
	for launchShims[executableName(harness.Exe)] {
		kids := childrenOf(harness.PID, top.Started)
		if len(kids) != 1 || kids[0].Started.After(launchEnds) {
			break
		}
		harness = kids[0]
	}

	read := harnessProcesses{top: top, harness: harness}
	seen := map[int]bool{}
	var walk func(process Process, into *[]Process)
	walk = func(process Process, into *[]Process) {
		if seen[process.PID] {
			return
		}
		seen[process.PID] = true
		*into = append(*into, process)
		for _, child := range childrenOf(process.PID, process.Started) {
			walk(child, into)
		}
	}
	var started []Process
	for _, child := range childrenOf(harness.PID, top.Started) {
		if child.Started.After(launchEnds) {
			started = append(started, child)
		}
	}
	sort.Slice(started, func(i, j int) bool { return started[i].PID < started[j].PID })
	// The jobs are walked first, so each process under one belongs to it.
	seen[harness.PID] = true
	for _, root := range started {
		var members []Process
		walk(root, &members)
		read.jobs = append(read.jobs, job{root: root, members: members})
	}
	delete(seen, harness.PID)
	for _, member := range read.jobs {
		read.all = append(read.all, member.members...)
	}
	walk(top, &read.all)
	return read, true
}

// Custody is what a process is to the program a terminal runs.
//
// Closing a terminal ends everything in its job, so a caller deciding whether
// the terminal holds work it would strand has to tell the harness itself from
// the work the harness was asked to do. When the terminal's program is a
// harness installed behind an npm .cmd shim or a node launcher, the harness
// runs one process further down (readHarness), and nothing in its name,
// wrapper or process tree separates that chain from a command the harness
// started.
type Custody int

const (
	// CustodyUnplaced is a process this read could not place under the
	// terminal's program, such as one whose parent has exited. Closing the
	// terminal would end it, so a caller must take it for work.
	CustodyUnplaced Custody = iota
	// CustodyHarness is the terminal's program, any launch shim between it
	// and the harness, the harness itself, and what the harness started as
	// it launched, its MCP servers included.
	CustodyHarness
	// CustodyWork is a job the harness started after launching, with
	// everything under it: the work it was asked to do.
	CustodyWork
)

// HarnessCustody places each of pids against the harness the terminal's
// program root runs, as readHarness reads it: the launch chain is the
// harness, and the jobs it started after launching are work. ok is false
// when root is not running, so nothing can be placed and a caller must place
// none of them.
func HarnessCustody(root int, pids []int, processes []Process, launch time.Duration) (map[int]Custody, bool) {
	read, ok := readHarness(root, processes, launch)
	if !ok {
		return nil, false
	}
	placed := make(map[int]Custody, len(read.all))
	for _, process := range read.all {
		placed[process.PID] = CustodyHarness
	}
	for _, job := range read.jobs {
		for _, member := range job.members {
			placed[member.PID] = CustodyWork
		}
	}
	custody := make(map[int]Custody, len(pids))
	for _, pid := range pids {
		if kind, found := placed[pid]; found {
			custody[pid] = kind
		}
	}
	return custody, true
}

// name is how a wake names a job: "bash.exe (pid 13)".
func (j job) name() string {
	return fmt.Sprintf("%s (pid %d)", j.root.Exe, j.root.PID)
}

// executableName is a program's name without its folder or .exe, in lower
// case.
func executableName(exe string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
}

// heavyLine is one reading of the machine's heavy line.
type heavyLine struct {
	holding, waiting []verify.Standing
}

// movedFor is when the line last moved for a run of job's that waits in it:
// when the turn last passed to a run that holds one, or when the run joined
// the line, whichever is later. A run waiting its turn uses no processor,
// so this is its activity (#965). It is zero for a job with no run waiting.
func (line heavyLine) movedFor(job job) time.Time {
	var moved time.Time
	for _, run := range line.waiting {
		if slices.ContainsFunc(job.members, func(member Process) bool { return member.PID == run.PID }) {
			moved = later(moved, run.Since)
		}
	}
	if moved.IsZero() {
		return moved
	}
	for _, run := range line.holding {
		moved = later(moved, run.Since)
	}
	return moved
}
