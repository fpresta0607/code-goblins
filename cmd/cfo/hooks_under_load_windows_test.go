package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervise"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const (
	// loadSessions is how many Claude Code sessions fire their hooks at once:
	// the CFO's own and eleven the Overlord opened himself.
	loadSessions = 12
	// loadRounds is how many turns every session takes.
	loadRounds = 3
	// loadTools is how many times a session's turn runs each tool whose
	// PreToolUse hook is the CFO's.
	loadTools = 3
	// loadAcknowledged is how many records arrive for the CFO to acknowledge
	// at once in the first round, as many as the drain of 2026-10-09 14:20Z
	// retired, and loadWait how long a session waits for one command, that
	// acknowledgement included: it took over a minute then.
	loadAcknowledged = 55
	loadWait         = 4 * time.Minute
)

// hookLoad collects how long each session's harness waited for each hook and
// command it ran, by what it was, and everything that went wrong.
type hookLoad struct {
	sync.Mutex
	took     map[string][]time.Duration
	problems []string
}

func (l *hookLoad) record(what string, took time.Duration) {
	l.Lock()
	defer l.Unlock()
	l.took[what] = append(l.took[what], took)
}

func (l *hookLoad) problem(format string, args ...any) {
	l.Lock()
	defer l.Unlock()
	l.problems = append(l.problems, fmt.Sprintf(format, args...))
}

// fire has session run cfo with args and waits for it to end, recording how
// long its harness waited under what.
func (l *hookLoad) fire(session *standInSession, what string, env map[string]string, stdin string, args ...string) sessionResponse {
	wait, err := session.ask(env, stdin, args...)
	if err != nil {
		l.problem("%s: %v", what, err)
		return sessionResponse{Exit: -1}
	}
	response, ended, err := wait(loadWait)
	if err != nil || !ended {
		l.problem("%s: ended = %t, %v", what, ended, err)
		return sessionResponse{Exit: -1}
	}
	l.record(what, response.Took)
	return response
}

// quiet fires a hook in a session that is not the CFO's own, which must get
// nothing from it.
func (l *hookLoad) quiet(session *standInSession, hook string, env map[string]string, payload string) {
	response := l.fire(session, "another session's "+hook, env, payload, "hook", hook)
	if response.Exit != 0 || response.Stdout != "" || response.Stderr != "" {
		l.problem("another session's %s: exit = %d, stdout = %q, stderr = %q, want nothing", hook, response.Exit, response.Stdout, response.Stderr)
	}
}

// table is every latency recorded: how many times each ran, its median and
// its worst.
func (l *hookLoad) table() string {
	names := make([]string, 0, len(l.took))
	for name := range l.took {
		names = append(names, name)
	}
	slices.Sort(names)
	var lines strings.Builder
	for _, name := range names {
		took := slices.Sorted(slices.Values(l.took[name]))
		fmt.Fprintf(&lines, "%-58s n=%-4d median %-8s worst %s\n", name, len(took), took[len(took)/2].Round(time.Millisecond), took[len(took)-1].Round(time.Millisecond))
	}
	return lines.String()
}

// drainedLine matches one record of a drain's listing, and requiredAck the
// command it ends with.
var (
	drainedLine = regexp.MustCompile(`(?m)^  (\d+)  \S+`)
	requiredAck = regexp.MustCompile(`(?m)^WAKE_ACK_REQUIRED: cfo (drain .+)$`)
)

// The Overlord asked on 2026-10-09 whether the hooks handle many sessions
// firing them at once, safely and efficiently. A dozen sessions take three
// turns together: each starts, runs its tools, and ends its turn while a
// goblin reports, and in the first turn 55 more records wait to be
// acknowledged. Every wake reaches the CFO once and no other session, nothing
// is lost or shown twice, the home stays the CFO's, and a notify from another
// process lands while the CFO acknowledges. The latencies are logged for
// data/cg-cfo-custody/hooks-under-load.md.
func TestEveryWakeReachesTheCFOOnceWhileADozenSessionsFireTheirHooks(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	writeMetaFixture(t, h.State, "g1.meta")
	waits := sweptStopWaits(t, h.State)
	others := make([]*standInSession, loadSessions-1)
	for at := range others {
		others[at] = startOtherSession(t, h)
	}
	cfo := startCFOSession(t, h)
	load := &hookLoad{took: map[string][]time.Duration{}}
	// A program just written starts slowly once, while it is scanned, and
	// every stand-in session runs the same cfo.exe: that start is timed apart
	// from the hooks.
	load.fire(cfo, "a cfo.exe just written, its first start", nil, "", "version")
	// What one small state file costs to write here: every lock taken and
	// every record queued or acknowledged pays it a few times over.
	probe := filepath.Join(t.TempDir(), "probe")
	for range 20 {
		started := time.Now()
		if err := fsx.AtomicWriteFile(probe, []byte("probe")); err != nil {
			t.Fatal(err)
		}
		load.record("one small state file written", time.Since(started))
	}
	// Each session's payloads, made here since a session's goroutine may not
	// end the test.
	payloads := func(session string) map[string]string {
		return map[string]string{
			"startup": hookPayload(t, session, "startup", "", ""),
			"resume":  hookPayload(t, session, "resume", "", ""),
			"bash":    hookPayload(t, session, "", "Bash", "git status"),
			"agent":   hookPayload(t, session, "", "Agent", ""),
			"stop":    hookPayload(t, session, "", "", ""),
		}
	}
	own, other := payloads("cfo-session"), payloads("desktop-session")
	everyOther := func(act func(session *standInSession)) *sync.WaitGroup {
		var group sync.WaitGroup
		for _, session := range others {
			group.Go(func() { act(session) })
		}
		return &group
	}
	appended, listed := map[int]bool{}, map[int]int{}
	reports, reopened := 0, 0
	// The latencies are logged however the test ends.
	t.Cleanup(func() {
		t.Logf("%d sessions, %d rounds, the CFO's turn reopened by its turn-end guard %d time(s):\n%s", loadSessions, loadRounds, reopened, load.table())
	})

	// Act
	for round := range loadRounds {
		// Every session starts or resumes and runs its tools.
		source := "resume"
		if round == 0 {
			source = "startup"
		}
		working := everyOther(func(session *standInSession) {
			load.quiet(session, "session-start", nil, other[source])
			for range loadTools {
				load.quiet(session, "pretool-bash", nil, other["bash"])
				load.quiet(session, "pretool-subagent", nil, other["agent"])
			}
			load.fire(session, "another session's version, a process that only starts", nil, "", "version")
		})
		load.fire(cfo, "the CFO's session-start ("+source+")", nil, own[source], "hook", "session-start")
		for range loadTools {
			load.fire(cfo, "the CFO's pretool-bash", nil, own["bash"], "hook", "pretool-bash")
			load.fire(cfo, "the CFO's pretool-subagent", nil, own["agent"], "hook", "pretool-subagent")
		}
		load.fire(cfo, "the CFO's version, a process that only starts", nil, "", "version")
		working.Wait()

		// Every session ends its turn, which fires both Stop hooks at once.
		ended := time.Now()
		guard := cfo.begin(t, waits, own["stop"], "hook", "turnend-guard")
		armed := cfo.begin(t, waits, own["stop"], "hook", "stop-autoarm")
		// The hook shows it is arming first, and is armed once it holds the
		// auto-arm lock: the guard waits for the first and decides on the
		// second.
		arming := make(chan time.Duration, 1)
		go func() {
			shown := time.Duration(0)
			for deadline := ended.Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if shown == 0 && supervise.AutoarmArming(h.State) {
					shown = time.Since(ended)
				}
				if _, err := lock.ReadNamed(h.State, autoarmLockName); err == nil {
					if shown == 0 {
						shown = time.Since(ended)
					}
					load.record("the CFO's stop-autoarm, from the turn's end to showing it arms", shown)
					arming <- time.Since(ended)
					return
				}
			}
			arming <- 0
		}()
		everyOther(func(session *standInSession) {
			load.quiet(session, "turnend-guard", waits, other["stop"])
			load.quiet(session, "stop-autoarm", waits, other["stop"])
		}).Wait()
		if guarded, isOver := guard(loadWait); !isOver {
			t.Fatalf("round %d: the CFO's turn-end guard did not end within %s", round, loadWait)
		} else {
			load.record("the CFO's turnend-guard", guarded.Took)
			if guarded.Exit != 0 {
				reopened++
			}
		}
		if took := <-arming; took == 0 {
			t.Fatalf("round %d: the CFO's Stop hook did not arm within 30s", round)
		} else {
			load.record("the CFO's stop-autoarm, from the turn's end to armed", took)
		}

		// The CFO's own Stop fires a second time while the first waits, then
		// a goblin reports while every other session ends another turn.
		if second := load.fire(cfo, "the CFO's stop-autoarm, a second firing while the first waits", waits, own["stop"], "hook", "stop-autoarm"); second.Exit != 0 || second.Stderr != "" {
			t.Errorf("round %d: the CFO's second Stop firing: exit = %d, stderr = %q, want it over with nothing while the first waits", round, second.Exit, second.Stderr)
		}
		if err := os.WriteFile(filepath.Join(h.State, "g1.status"), fmt.Appendf(nil, "needs-decision: which way, round %d\n", round), 0o644); err != nil {
			t.Fatal(err)
		}
		reported := time.Now()
		reports++
		ending := everyOther(func(session *standInSession) {
			load.quiet(session, "stop-autoarm", waits, other["stop"])
		})
		rewake, isRewoken := armed(loadWait)
		load.record("a goblin's report to the CFO's rewake (poll 1s, grace 1s)", time.Since(reported))
		ending.Wait()
		if !isRewoken || rewake.Exit != 2 || !strings.Contains(rewake.Stderr, "signal:") {
			t.Fatalf("round %d: the CFO's Stop hook: ended = %t, exit = %d, stderr = %q, want it rewoken with the goblin's signal", round, isRewoken, rewake.Exit, rewake.Stderr)
		}

		// More records arrive, and the CFO drains and acknowledges until the
		// queue is empty while another process keeps appending.
		for at := 0; round == 0 && at < loadAcknowledged; at++ {
			record, err := wake.AppendOnce(h.State, fmt.Sprintf("load/%d/%d", round, at), "notify", "g1", "working: a record to acknowledge")
			if err != nil {
				t.Fatal(err)
			}
			appended[record.Seq] = true
		}
		for pass := 0; ; pass++ {
			drained := load.fire(cfo, "the CFO's drain", nil, "", "drain")
			for _, line := range drainedLine.FindAllStringSubmatch(drained.Stdout, -1) {
				seq, _ := strconv.Atoi(line[1])
				listed[seq]++
			}
			ack := requiredAck.FindStringSubmatch(drained.Stdout)
			if ack == nil {
				break
			}
			if pass == 5 {
				t.Fatalf("round %d: the queue was not empty after 5 acknowledgements:\n%s", round, drained.Stdout)
			}
			acknowledged := make(chan sessionResponse, 1)
			go func() {
				acknowledged <- load.fire(cfo, fmt.Sprintf("the CFO's drain --ack-through, %d records", len(drainedLine.FindAllString(drained.Stdout, -1))), nil, "", strings.Fields(ack[1])...)
			}()
			for isAcknowledging := pass == 0; isAcknowledging; {
				started := time.Now()
				record, err := wake.Append(h.State, "notify", "g1", "working: a notify during the acknowledgement")
				if err != nil {
					t.Errorf("round %d: a notify during the CFO's acknowledgement was refused after %s: %v", round, time.Since(started).Round(time.Millisecond), err)
				} else {
					load.record("another process's notify during the acknowledgement", time.Since(started))
					appended[record.Seq] = true
				}
				select {
				case response := <-acknowledged:
					acknowledged <- response
					isAcknowledging = false
				// One notify follows another 10 ms apart. That starved the
				// acknowledgement for its whole five seconds while a waiter
				// only looked for the lock again every so often: it queues
				// now, so it is next after the notify it found filing.
				case <-time.After(10 * time.Millisecond):
				}
			}
			if response := <-acknowledged; response.Exit != 0 {
				t.Fatalf("round %d: cfo %s: exit = %d, stderr = %q", round, ack[1], response.Exit, response.Stderr)
			}
		}
	}

	// Assert
	for _, problem := range load.problems {
		t.Error(problem)
	}
	signals := 0
	for seq, times := range listed {
		if times != 1 {
			t.Errorf("wake %d was shown to the CFO %d times, want once", seq, times)
		}
		if !appended[seq] {
			signals++
		}
	}
	for seq := range appended {
		if listed[seq] == 0 {
			t.Errorf("wake %d was never shown to the CFO", seq)
		}
	}
	if signals != reports {
		t.Errorf("the CFO was shown %d wake(s) for %d report(s) of the goblin, want one each", signals, reports)
	}
	if pending, err := wake.Pending(h.State); err != nil || len(pending) != 0 {
		t.Errorf("the queue holds %d record(s) after the last acknowledgement, %v, want none", len(pending), err)
	}
	if holder := holderPID(t, h.State); holder != cfo.pid {
		t.Errorf("the session lock names pid %d, want the CFO's harness pid %d", holder, cfo.pid)
	}
	if registered := registeredPID(t, h.State); registered != cfo.pid {
		t.Errorf("primary.json registers pid %d, want the CFO's harness pid %d", registered, cfo.pid)
	}
	if _, err := os.Stat(filepath.Join(h.State, custodyAudit)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state\\%s is there (%v): the lock changed hands, and no session but the CFO's took it", custodyAudit, err)
	}
}
