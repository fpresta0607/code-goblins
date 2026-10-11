// Package startwatch watches what each goblin and the CFO's terminal start,
// placed under its owner the way the family tree places processes, and says
// once what the CFO should know of it: a process that shows on the
// Overlord's screen, one command started again and again, and a goblin that
// rests after its report with something it started still running. It reads
// what it is given and ends nothing: ending stays with the sweeps.
package startwatch

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
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
	return nil
}

// Named names processes as a wake names them.
func Named(processes []fleettree.Placed) string {
	return ""
}
