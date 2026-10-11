// Package startwatch watches what each goblin and the CFO's terminal start,
// placed under its owner the way the family tree places processes, and says
// once what the CFO should know of it: a process that shows on the
// Overlord's screen, one command started again and again, and a goblin that
// rests after its report with something it started still running. It reads
// what it is given and ends nothing: ending stays with the sweeps.
package startwatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// loopStarts new processes of one owner's same command line, seen within
// loopSpan, are a loop. Read every 5 seconds for 10 minutes on 2026-10-11,
// with nine terminals at work, no command line but a carrier's was seen as
// a new process more than 5 times in any 2 minutes, while a process started
// again and again that lives 5 seconds or more is seen at every reading,
// 24 times in 2 minutes.
const (
	loopStarts = 10
	loopSpan   = 2 * time.Minute
)

// leftAfter is how long a goblin has rested, and what it still runs been
// seen running, before the watch tells of it: a hook its harness runs as its
// turn ends lasts under a second.
const leftAfter = time.Minute

// carriers only carry a command or wait between two, so a loop is what runs
// under them. Git Bash's shell also starts a copy of itself, with its own
// command line, for each pipeline and subshell, and Windows starts a console
// host beside each hidden console program.
var carriers = []string{"bash", "sh", "cmd", "powershell", "pwsh", "conhost", "openconsole", "timeout", "sleep"}

// Process is one process of an owner's at one reading.
type Process struct {
	fleettree.Placed
	// Shown says how the process shows on the Overlord's screen, as a wake
	// says it, such as "a window of its own", and is empty when it does not.
	Shown string
}

// Rest is a goblin that reported done or failed and rests since: the report
// and when it made it.
type Rest struct {
	Report     string
	ReportedAt time.Time
}

// Reading is what one reading of the machine found of every owner.
type Reading struct {
	At        time.Time
	Processes []Process
	// Generations names each owner's spawn generation: a loop is told once
	// in each.
	Generations map[string]string
	// Resting holds each goblin that rests after its report, by task.
	Resting map[string]Rest
	// CFO is the owner that is the CFO's terminal, which the CFO is told of
	// as its own.
	CFO string
}

// Finding is what the CFO is told once: a check wake's detail for its owner,
// and the identity that makes it once, across restarts of the supervisor.
type Finding struct {
	Identity string
	Owner    string
	Detail   string
}

// Watch keeps what it has seen between readings. Its zero value is ready,
// and it is not safe for use by more than one reader at once.
type Watch struct {
	// seen is when each running process was first seen, starts the new
	// processes of each owner's command line seen within loopSpan, rested
	// when each goblin was first seen resting after its report, and told
	// the identities found since the watch began.
	seen   map[instance]time.Time
	starts map[string]*restarts
	rested map[string]time.Time
	told   map[string]bool
}

type instance struct {
	owner   string
	pid     int
	created int64
}

// restarts are the new processes of one owner's command line seen within
// loopSpan, and the last of them.
type restarts struct {
	at     []time.Time
	latest Process
}

// Read takes one reading and returns what it found that the watch had not
// found before.
func (w *Watch) Read(reading Reading) []Finding {
	if w.seen == nil {
		w.seen, w.starts, w.rested, w.told = map[instance]time.Time{}, map[string]*restarts{}, map[string]time.Time{}, map[string]bool{}
	}
	now := reading.At
	running := make(map[instance]bool, len(reading.Processes))
	for _, process := range reading.Processes {
		key := instance{process.Owner, process.PID, process.Created}
		running[key] = true
		if _, isSeen := w.seen[key]; isSeen {
			continue
		}
		w.seen[key] = now
		if isStart(process) {
			loop := loopKey(process, reading.Generations[process.Owner])
			if w.starts[loop] == nil {
				w.starts[loop] = &restarts{}
			}
			w.starts[loop].at = append(w.starts[loop].at, now)
			w.starts[loop].latest = process
		}
	}
	for key := range w.seen {
		if !running[key] {
			delete(w.seen, key)
		}
	}
	var findings []Finding
	findings = append(findings, w.windows(reading)...)
	findings = append(findings, w.loops(reading)...)
	findings = append(findings, w.left(reading)...)
	return findings
}

// isStart says a new process is one its owner started, as a loop counts
// starts: not a carrier, not a machine service, which works for every
// goblin, not one the harness started itself, which is a turn of its
// goblin's, a tool call or a hook, and not one a test run or a build
// started, which starts the same tools again and again within its own time
// limit.
func isStart(process Process) bool {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(process.Exe)), ".exe")
	return process.Command != "" && !process.Service && !process.ByHarness && !process.InRun && !slices.Contains(carriers, name)
}

// loopKey is the owner, its generation and the command line, the last as
// a digest, since a command line can hold a credential.
func loopKey(process Process, generation string) string {
	digest := sha256.Sum256([]byte(process.Command))
	return process.Owner + "/" + generation + "/" + hex.EncodeToString(digest[:8])
}

// windows are the processes newly shown on the Overlord's screen. One shown
// under another that is shown, as a browser's helper or a program in the
// same console, is told with it.
func (w *Watch) windows(reading Reading) []Finding {
	shown := map[instance]bool{}
	for _, process := range reading.Processes {
		if process.Shown != "" {
			shown[instance{process.Owner, process.PID, 0}] = true
		}
	}
	var findings []Finding
	for _, process := range reading.Processes {
		if process.Shown == "" || shown[instance{process.Owner, process.ParentPID, 0}] {
			continue
		}
		identity := fmt.Sprintf("window/%s/%d/%d", process.Owner, process.PID, process.Created)
		if w.told[identity] {
			continue
		}
		w.told[identity] = true
		next := "tell " + process.Owner + " to end it by its pid unless the Overlord uses it, and to run what it tests headless or hidden unless a picture needs a window"
		if process.Owner == reading.CFO {
			next = "end it by its pid unless the Overlord uses it"
		}
		findings = append(findings, Finding{Identity: identity, Owner: process.Owner, Detail: fmt.Sprintf("window_shown: %s's %s (pid %d), started %s, shows %s on the Overlord's screen: %s. Next: %s", ownerName(process.Owner, reading.CFO), filepath.Base(process.Exe), process.PID, process.Started.UTC().Format("15:04Z"), process.Shown, shownCommand(process.Command), next)})
	}
	return findings
}

// loops are the command lines started again and again.
func (w *Watch) loops(reading Reading) []Finding {
	var findings []Finding
	for key, started := range w.starts {
		started.at = slices.DeleteFunc(started.at, func(at time.Time) bool { return reading.At.Sub(at) >= loopSpan })
		if len(started.at) == 0 {
			delete(w.starts, key)
			continue
		}
		identity := "loop/" + key
		if len(started.at) < loopStarts || w.told[identity] {
			continue
		}
		w.told[identity] = true
		latest := started.latest
		next := "peek " + latest.Owner + " and have it cap or stop what starts it again and again"
		if latest.Owner == reading.CFO {
			next = "cap or stop what starts it again and again"
		}
		findings = append(findings, Finding{Identity: identity, Owner: latest.Owner, Detail: fmt.Sprintf("start_loop: %s started %s %d times in the last %s, the last as pid %d: %s. Next: %s", ownerName(latest.Owner, reading.CFO), filepath.Base(latest.Exe), len(started.at), spanText(reading.At.Sub(started.at[0])), latest.PID, shownCommand(latest.Command), next)})
	}
	slices.SortFunc(findings, func(a, b Finding) int { return strings.Compare(a.Identity, b.Identity) })
	return findings
}

// left are the goblins that rest after their report with processes they
// started still running, once both have lasted leftAfter: each told once a
// report.
func (w *Watch) left(reading Reading) []Finding {
	byOwner := map[string][]Process{}
	for _, process := range reading.Processes {
		byOwner[process.Owner] = append(byOwner[process.Owner], process)
	}
	resting := map[string]bool{}
	var findings []Finding
	owners := make([]string, 0, len(reading.Resting))
	for owner := range reading.Resting {
		owners = append(owners, owner)
	}
	slices.Sort(owners)
	for _, owner := range owners {
		rest := reading.Resting[owner]
		identity := "left/" + owner + "/" + rest.ReportedAt.UTC().Format(time.RFC3339)
		resting[identity] = true
		since, isRested := w.rested[identity]
		if !isRested {
			w.rested[identity] = reading.At
			continue
		}
		if reading.At.Sub(since) < leftAfter || w.told[identity] {
			continue
		}
		var still []Process
		for _, process := range byOwner[owner] {
			if !process.Service && reading.At.Sub(w.seen[instance{owner, process.PID, process.Created}]) >= leftAfter {
				still = append(still, process)
			}
		}
		if len(still) == 0 {
			continue
		}
		w.told[identity] = true
		verb, _, _ := strings.Cut(rest.Report, ":")
		placed := make([]fleettree.Placed, len(still))
		for index, process := range still {
			placed[index] = process.Placed
		}
		findings = append(findings, Finding{Identity: identity, Owner: owner, Detail: fmt.Sprintf("left_running: %s reported %s at %s and rests with %s it started still running: %s. Next: tell %s to end each it no longer needs by its pid, or end a leftover by its pid yourself", owner, verb, rest.ReportedAt.UTC().Format("15:04Z"), count(len(still), "process", "processes"), Named(placed), owner)})
	}
	for identity := range w.rested {
		if !resting[identity] {
			delete(w.rested, identity)
		}
	}
	return findings
}

// Named names processes as a wake names them: each one whose parent is not
// among them, the first namedTrees, with its pid, its program and its
// command line, and the programs that run under it, the first three by
// name, so a shell left holding a server says what the server is.
func Named(processes []fleettree.Placed) string {
	byPID := map[int]fleettree.Placed{}
	children := map[int][]int{}
	for _, process := range processes {
		byPID[process.PID] = process
		children[process.ParentPID] = append(children[process.ParentPID], process.PID)
	}
	var names []string
	for _, process := range processes {
		if _, isAmong := byPID[process.ParentPID]; isAmong && process.ParentPID != process.PID {
			continue
		}
		var under []string
		for queue := slices.Clone(children[process.PID]); len(queue) > 0; queue = queue[1:] {
			if queue[0] != process.PID {
				under = append(under, fmt.Sprintf("%s pid %d", filepath.Base(byPID[queue[0]].Exe), queue[0]))
				queue = append(queue, children[queue[0]]...)
			}
		}
		name := fmt.Sprintf("pid %d %s %q", process.PID, filepath.Base(process.Exe), shownCommand(process.Command))
		switch {
		case len(under) > 3:
			name += fmt.Sprintf(" (under it %s and %d more)", strings.Join(under[:3], ", "), len(under)-3)
		case len(under) > 0:
			name += " (under it " + strings.Join(under, ", ") + ")"
		}
		names = append(names, name)
	}
	if len(names) > namedTrees {
		names = append(names[:namedTrees], fmt.Sprintf("and %d more", len(names)-namedTrees))
	}
	return strings.Join(names, ", ")
}

// namedTrees is how many trees of processes one wake names.
const namedTrees = 5

func ownerName(owner, cfo string) string {
	if owner == cfo {
		return "the CFO terminal"
	}
	return owner
}

// shownCommand is a command line as a wake may show it (proc.ShownCommand).
func shownCommand(command string) string {
	arguments, err := proc.SplitCommandLine(command)
	if err != nil || command == "" {
		return "a command line that could not be read"
	}
	return proc.ShownCommand(arguments)
}

func count(number int, one, many string) string {
	if number == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", number, many)
}

func spanText(span time.Duration) string {
	return span.Round(time.Second).String()
}
