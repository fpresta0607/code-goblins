package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// custodyAudit is the file in the state folder that records each time the
// CFO's own session took the session lock over from another live holder.
const custodyAudit = "custody.audit"

// stopWaits makes a Stop hook's watcher notice a goblin's report within a
// second or two, and leaves its heartbeat at its ten minutes so only a report
// ends the wait.
var stopWaits = map[string]string{"CFO_POLL": "1", "CFO_SIGNAL_GRACE": "1", "CFO_CLAUDE_AUTOARM_ATTEMPTS": "1"}

// stateFiles lists what the state folder holds, for a test that nothing in it
// was created or removed.
func stateFiles(t *testing.T, state string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(state, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if relative, _ := filepath.Rel(state, path); relative != "." {
			names = append(names, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// deadHolder leaves the session lock as a restart leaves it: recorded for a
// process that no longer runs.
func deadHolder(t *testing.T, state string) {
	t.Helper()
	ended := exec.Command("cmd", "/c", "exit")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Hour).UTC()
	data, err := json.Marshal(lock.Info{PID: ended.Process.Pid, OwnerPID: ended.Process.Pid, Session: "before-the-restart", Start: before, Hostname: hostname, Acquired: before})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, ".lock"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// holderPID is the process the session lock names, or 0 while nobody holds it.
func holderPID(t *testing.T, state string) int {
	t.Helper()
	holder, err := lock.Read(state)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return holder.PID
}

// registeredPID is the process primary.json registers as the CFO, or 0 while
// nothing is registered.
func registeredPID(t *testing.T, state string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "primary.json"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	var primary struct {
		Process struct {
			PID int `json:"pid"`
		} `json:"process"`
	}
	if err := json.Unmarshal(data, &primary); err != nil {
		t.Fatal(err)
	}
	return primary.Process.PID
}

// goblinReports makes goblin g1 report, which a watcher takes for a signal
// that needs the CFO.
func goblinReports(t *testing.T, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, "g1.status"), []byte("needs-decision: which way\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A Claude Code session that is not the CFO's own, one in no native terminal
// as every session the Overlord opens himself is, gets nothing from any of
// the CFO's hooks: no digest, no lock, no guard on its tools and no held
// turn. `cfo install` puts the hooks in the user's settings, so they run in
// every session on the machine.
func TestEveryHookLeavesASessionThatIsNotTheCFOsAlone(t *testing.T) {
	for _, c := range []struct {
		name, hook, payload string
		hasGoblin           bool
	}{
		{"its start", "session-start", hookPayload(t, "desktop-session", "startup", "", ""), false},
		{"its resume", "session-start", hookPayload(t, "desktop-session", "resume", "", ""), false},
		{"its own delegation tool", "pretool-subagent", hookPayload(t, "desktop-session", "", "Agent", ""), false},
		{"its own question prompt", "pretool-subagent", hookPayload(t, "desktop-session", "", "AskUserQuestion", ""), false},
		{"a cd in its shell", "pretool-bash", hookPayload(t, "desktop-session", "", "Bash", `cd C:\`), false},
		{"its turn ending while a goblin works and no watcher runs", "turnend-guard", hookPayload(t, "desktop-session", "", "", ""), true},
		{"its compaction", "pre-compact", hookPayload(t, "desktop-session", "", "", ""), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			root := newPrimaryHome(t)
			state := filepath.Join(root, "state")
			if c.hasGoblin {
				writeMetaFixture(t, state, "g1.meta")
			}
			setSyncWait(t, "1")
			before := stateFiles(t, state)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runHook(c.hook, strings.NewReader(c.payload), &stdout, &stderr)

			// Assert
			if exit != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("exit = %d, stdout = %q, stderr = %q, want the hook silent", exit, stdout.String(), stderr.String())
			}
			if after := stateFiles(t, state); !slices.Equal(before, after) {
				t.Errorf("the hook changed the state folder from %q to %q", before, after)
			}
		})
	}
}

// 2026-10-09: the machine restarted, and 22 seconds before the comeback
// brought the CFO back in native terminal cfo, the Overlord's desktop app
// reopened a Claude Code session of his own in a PrecisionDocs worktree. Its
// SessionStart hook found the lock's holder dead and took the lock, its Stop
// hook was rewoken with the fleet's wakes, and it drained and acknowledged
// them while the CFO read "THIS SESSION DOES NOT HOLD THE HOME".
func TestTheCFOComingBackAfterARestartHoldsTheHomeThoughAnotherSessionStartedFirst(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	deadHolder(t, h.State)
	writeMetaFixture(t, h.State, "g1.meta")
	other := startOtherSession(t, h)

	// Act: the other session starts first, then the CFO's.
	first := other.hook(t, "session-start", hookPayload(t, "desktop-session", "startup", "", ""))
	cfo := startCFOSession(t, h)
	digest := cfo.hook(t, "session-start", hookPayload(t, "cfo-session", "resume", "", ""))

	// Assert
	if first.Exit != 0 || first.Stdout != "" || first.Stderr != "" {
		t.Errorf("the other session's start: exit = %d, stdout = %q, stderr = %q, want nothing", first.Exit, first.Stdout, first.Stderr)
	}
	if holder := holderPID(t, h.State); holder != cfo.pid {
		t.Fatalf("the session lock names pid %d, want the CFO's harness pid %d (the other session's is %d)", holder, cfo.pid, other.pid)
	}
	if want := fmt.Sprintf("SESSION LOCK: held by pid %d", cfo.pid); !strings.Contains(digest.Stdout, want) || strings.Contains(digest.Stdout, "READ-ONLY") {
		t.Errorf("the CFO's digest lacks %q or is read-only:\n%s", want, digest.Stdout)
	}
	if registered := registeredPID(t, h.State); registered != cfo.pid {
		t.Errorf("primary.json registers pid %d, want the CFO's harness pid %d", registered, cfo.pid)
	}

	// Act: both sessions end a turn, then a goblin reports.
	stop := hookPayload(t, "desktop-session", "", "", "")
	otherStop := other.begin(t, stopWaits, stop, "hook", "stop-autoarm")
	cfoStop := cfo.begin(t, stopWaits, hookPayload(t, "cfo-session", "", "", ""), "hook", "stop-autoarm")
	otherEnded, isEnded := otherStop(30 * time.Second)
	goblinReports(t, h.State)
	rewake, isRewoken := cfoStop(60 * time.Second)

	// Assert: the wake reaches the CFO and nothing reaches the other session.
	if !isEnded || otherEnded.Exit != 0 || otherEnded.Stdout != "" || otherEnded.Stderr != "" {
		t.Errorf("the other session's Stop hook: ended = %t, exit = %d, stdout = %q, stderr = %q, want it over at once with nothing", isEnded, otherEnded.Exit, otherEnded.Stdout, otherEnded.Stderr)
	}
	if !isRewoken || rewake.Exit != 2 || !strings.Contains(rewake.Stderr, "cfo watcher wake") || !strings.Contains(rewake.Stderr, "signal:") {
		t.Errorf("the CFO's Stop hook: ended = %t, exit = %d, stderr = %q, want it rewoken with the goblin's signal", isRewoken, rewake.Exit, rewake.Stderr)
	}
	if guard := other.hook(t, "turnend-guard", stop); guard.Exit != 0 || guard.Stdout != "" || guard.Stderr != "" {
		t.Errorf("the other session's turn-end guard: exit = %d, stdout = %q, stderr = %q, want nothing", guard.Exit, guard.Stdout, guard.Stderr)
	}
	if holder := holderPID(t, h.State); holder != cfo.pid {
		t.Errorf("after both sessions' Stop hooks the session lock names pid %d, want the CFO's harness pid %d", holder, cfo.pid)
	}
}

// A session that is not the CFO's own and already holds the lock, as the
// desktop app's session did on 2026-10-09 under the build before this rule,
// loses it to the CFO at the CFO's next hook, with no hand edit of the state
// folder. The takeover is audited, the CFO is told at once, and the other
// session's hooks never take the lock back.
func TestTheCFOTakesTheHomeOverFromASessionThatIsNotItsOwn(t *testing.T) {
	stop := hookPayload(t, "cfo-session", "", "", "")
	for _, c := range []struct {
		name string
		// next is the CFO's next hook, and told what it must say about the
		// takeover.
		next func(t *testing.T, cfo *standInSession) sessionResponse
		told func(response sessionResponse) string
	}{
		{"at its next Stop", func(t *testing.T, cfo *standInSession) sessionResponse {
			return cfo.run(t, stopWaits, stop, "hook", "stop-autoarm")
		}, func(response sessionResponse) string { return response.Stderr }},
		{"at its next start", func(t *testing.T, cfo *standInSession) sessionResponse {
			return cfo.hook(t, "session-start", hookPayload(t, "cfo-session", "resume", "", ""))
		}, func(response sessionResponse) string { return response.Stdout }},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h := scratchHome(t)
			writeMetaFixture(t, h.State, "g1.meta")
			other := startOtherSession(t, h)
			cfo := startCFOSession(t, h)
			if _, err := lock.AcquireOwner(h.State, other.pid, "desktop-session"); err != nil {
				t.Fatal(err)
			}

			// Act
			response := c.next(t, cfo)

			// Assert
			if holder := holderPID(t, h.State); holder != cfo.pid {
				t.Fatalf("after the CFO's hook the session lock names pid %d, want the CFO's harness pid %d (the other session's is %d)", holder, cfo.pid, other.pid)
			}
			from := fmt.Sprintf("from pid %d", other.pid)
			if told := c.told(response); !strings.Contains(told, from) {
				t.Errorf("the CFO's hook said %q, want it to say it took the lock over %s", told, from)
			}
			audit, err := os.ReadFile(filepath.Join(h.State, custodyAudit))
			if err != nil || !strings.Contains(string(audit), strconv.Itoa(other.pid)) || !strings.Contains(string(audit), strconv.Itoa(cfo.pid)) || !strings.Contains(string(audit), "desktop-session") {
				t.Errorf("state\\%s = %q (%v), want one record naming pid %d, its session and pid %d", custodyAudit, audit, err, other.pid, cfo.pid)
			}
			drained := cfo.run(t, nil, "", "drain")
			if !strings.Contains(drained.Stdout, from) {
				t.Errorf("cfo drain in the CFO's session shows %q, want the wake that says it took the lock over %s", drained.Stdout, from)
			}
			// It could not register while the other session held the home.
			if registered := registeredPID(t, h.State); registered != cfo.pid {
				t.Errorf("primary.json registers pid %d, want the CFO's harness pid %d", registered, cfo.pid)
			}

			// Act: the other session starts again and ends a turn.
			started := other.hook(t, "session-start", hookPayload(t, "desktop-session", "resume", "", ""))
			ended := other.run(t, stopWaits, hookPayload(t, "desktop-session", "", "", ""), "hook", "stop-autoarm")

			// Assert
			for name, quiet := range map[string]sessionResponse{"start": started, "Stop": ended} {
				if quiet.Exit != 0 || quiet.Stdout != "" || quiet.Stderr != "" {
					t.Errorf("the other session's %s hook: exit = %d, stdout = %q, stderr = %q, want nothing", name, quiet.Exit, quiet.Stdout, quiet.Stderr)
				}
			}
			if holder := holderPID(t, h.State); holder != cfo.pid {
				t.Errorf("after the other session's hooks the session lock names pid %d, want the CFO's harness pid %d", holder, cfo.pid)
			}
		})
	}
}

// The other session's Stop hook, started by the build before this rule, still
// waits on the wake queue for up to eight hours and holds the lock that lets
// one Stop hook arm at a time. The CFO's Stop hook arms all the same, so a
// goblin's report still rewakes the CFO.
func TestTheCFOsStopHookArmsWhileAnotherSessionsStopHookStillWaits(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	writeMetaFixture(t, h.State, "g1.meta")
	other := startOtherSession(t, h)
	cfo := startCFOSession(t, h)
	cfo.hook(t, "session-start", hookPayload(t, "cfo-session", "startup", "", ""))
	if _, err := lock.AcquireNamedOwner(h.State, autoarmLockName, other.pid, autoarmSession); err != nil {
		t.Fatal(err)
	}

	// Act
	cfoStop := cfo.begin(t, stopWaits, hookPayload(t, "cfo-session", "", "", ""), "hook", "stop-autoarm")
	if early, isOver := cfoStop(3 * time.Second); isOver {
		t.Fatalf("the CFO's Stop hook ended before any goblin reported: exit = %d, stderr = %q, want it armed and waiting", early.Exit, early.Stderr)
	}
	goblinReports(t, h.State)
	rewake, isRewoken := cfoStop(60 * time.Second)

	// Assert
	if !isRewoken || rewake.Exit != 2 || !strings.Contains(rewake.Stderr, "signal:") {
		t.Errorf("the CFO's Stop hook: ended = %t, exit = %d, stderr = %q, want it rewoken with the goblin's signal", isRewoken, rewake.Exit, rewake.Stderr)
	}
}

// Acknowledging the fleet's wakes, steering a goblin and dispatching one are
// the CFO's. An agent's session that is not the CFO's own is refused each,
// and the CFO's own session does each.
func TestOnlyTheCFOsOwnSessionActsAsTheCFO(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	other := startOtherSession(t, h)
	cfo := startCFOSession(t, h)
	cfo.hook(t, "session-start", hookPayload(t, "cfo-session", "startup", "", ""))
	queued, err := wake.Append(h.State, "check", "probe", "a wake only the CFO acknowledges")
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	acknowledge := []string{"drain", "--ack-through", strconv.Itoa(queued.Seq)}
	steer := []string{"send", "g1", "merge", "main", "first"}
	dispatch := func(id string) []string {
		return []string{"spawn", id, "--project", project, "--brief", briefFile(t), "--harness", "claude"}
	}
	acted := func() string {
		data, err := os.ReadFile(filepath.Join(h.Root, actedLog))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return string(data)
	}
	pending := func() int {
		records, err := wake.Pending(h.State)
		if err != nil {
			t.Fatal(err)
		}
		return len(records)
	}

	// Act: the other session tries each.
	refused := map[string]sessionResponse{
		"drain --ack-through": other.run(t, nil, "", acknowledge...),
		"send":                other.run(t, nil, "", steer...),
		"spawn":               other.run(t, nil, "", dispatch("g8")...),
	}

	// Assert
	for name, response := range refused {
		if response.Exit != 1 || !strings.Contains(response.Stderr, "not the CFO's own session") {
			t.Errorf("cfo %s in the other session: exit = %d, stderr = %q, want it refused as not the CFO's own session", name, response.Exit, response.Stderr)
		}
	}
	if pending() != 1 {
		t.Errorf("the other session acknowledged the wake: %d pending, want 1", pending())
	}
	if did := acted(); did != "" {
		t.Errorf("the other session acted as the CFO: %q, want nothing sent or dispatched", did)
	}

	// Act: a goblin's command under the same harness, as a proof a goblin
	// runs gives the commands it runs itself.
	asGoblin := other.run(t, map[string]string{harness.RoleVariable: harness.RoleGoblin}, "", "send", "g2", "carry", "on")

	// Assert
	if asGoblin.Exit != 0 || acted() != "send g2 carry on\n" {
		t.Errorf("cfo send from a goblin's process: exit = %d, stderr = %q, acted %q, want it sent: a goblin keeps the commands it has", asGoblin.Exit, asGoblin.Stderr, acted())
	}

	// Act: the CFO's own session does each.
	allowed := map[string]sessionResponse{
		"drain --ack-through": cfo.run(t, nil, "", acknowledge...),
		"send":                cfo.run(t, nil, "", steer...),
		"spawn":               cfo.run(t, nil, "", dispatch("g9")...),
	}

	// Assert
	for name, response := range allowed {
		if response.Exit != 0 {
			t.Errorf("cfo %s in the CFO's own session: exit = %d, stderr = %q, want it done", name, response.Exit, response.Stderr)
		}
	}
	if pending() != 0 {
		t.Errorf("the CFO's own session left %d wake(s) pending, want none", pending())
	}
	if did, want := acted(), "send g2 carry on\nsend g1 merge main first\nspawn g9\n"; did != want {
		t.Errorf("the CFO's own session acted %q, want %q", did, want)
	}
}

// The digest printed by hand takes the session lock only in the CFO's own
// session. Any other session reads it and takes nothing, whoever holds the
// lock or nobody.
func TestTheDigestPrintedByHandTakesTheLockOnlyInTheCFOsOwnSession(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	other := startOtherSession(t, h)

	// Act
	printed := other.run(t, nil, "", "session-start")

	// Assert
	if printed.Exit != 0 || !strings.Contains(printed.Stdout, "READ-ONLY DIGEST") {
		t.Errorf("cfo session-start in the other session: exit = %d, want the read-only digest, got:\n%s", printed.Exit, firstLines(printed.Stdout, 8))
	}
	if holder := holderPID(t, h.State); holder != 0 {
		t.Errorf("cfo session-start in the other session took the session lock for pid %d (the session's is %d), want nobody holding it", holder, other.pid)
	}

	// Act
	cfo := startCFOSession(t, h)
	printed = cfo.run(t, nil, "", "session-start")

	// Assert
	if printed.Exit != 0 || strings.Contains(printed.Stdout, "READ-ONLY DIGEST") {
		t.Errorf("cfo session-start in the CFO's own session: exit = %d, want the digest under its lock, got:\n%s", printed.Exit, firstLines(printed.Stdout, 8))
	}
	if holder := holderPID(t, h.State); holder != cfo.pid {
		t.Errorf("cfo session-start in the CFO's own session left the lock with pid %d, want the CFO's harness pid %d", holder, cfo.pid)
	}
}

// firstLines is the start of a long output, for a failure's message.
func firstLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	return strings.Join(lines[:min(count, len(lines))], "\n")
}

// scratchHome's stand-in cfo refuses a home no test marked, so a stand-in
// session that lost its environment cannot reach another home.
func TestTheStandInCFORunsOnlyAgainstAMarkedScratchHome(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	if err := os.Remove(filepath.Join(h.Root, scratchHomeMarker)); err != nil {
		t.Fatal(err)
	}
	other := startOtherSession(t, home.Home{Root: h.Root, State: h.State})

	// Act
	response := other.run(t, nil, "", "drain")

	// Assert
	if response.Exit != 97 {
		t.Errorf("the stand-in cfo ran against an unmarked home: exit = %d, stdout = %q", response.Exit, response.Stdout)
	}
}
