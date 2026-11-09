package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
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
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// custodyAudit is the file in the state folder that records each time the
// CEO's own session took the session lock over from another live holder.
const custodyAudit = "custody.audit"

// stopWaits makes a Stop hook's watcher notice a goblin's report within a
// second or two, and leaves its heartbeat at its ten minutes so only a report
// ends the wait.
var stopWaits = map[string]string{"CEO_POLL": "1", "CEO_SIGNAL_GRACE": "1", "CEO_CLAUDE_AUTOARM_ATTEMPTS": "1"}

// sweptStopWaits is stopWaits for a scratch home whose watcher must report
// nothing but what its test does. A watcher's first cycle sweeps the whole
// machine for orphans, and to a scratch home a stand-in harness another
// goblin's test runs beside it is one: its wake reached the CEO before the
// goblin's report in two runs of two while such a test ran. The home is
// recorded as swept, and its watcher is not due to sweep again for a day.
func sweptStopWaits(t *testing.T, state string) map[string]string {
	t.Helper()
	if err := reap.WriteRecord(state, reap.Record{Time: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	waits := maps.Clone(stopWaits)
	waits["CEO_REAP_EVERY"] = "86400"
	return waits
}

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

// registeredPID is the process primary.json registers as the CEO, or 0 while
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
// that needs the CEO.
func goblinReports(t *testing.T, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, "g1.status"), []byte("needs-decision: which way\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A Claude Code session that is not the CEO's own, one in no native terminal
// as every session the Overlord opens himself is, gets nothing from any of
// the CEO's hooks: no digest, no lock, no guard on its tools and no held
// turn. `ceo install` puts the hooks in the user's settings, so they run in
// every session on the machine.
func TestEveryHookLeavesASessionThatIsNotTheCEOsAlone(t *testing.T) {
	for _, c := range []struct {
		name, hook, payload string
		hasGoblin           bool
	}{
		{"its start", "session-start", hookPayload(t, "desktop-session", "startup", "", ""), false},
		{"its resume", "session-start", hookPayload(t, "desktop-session", "resume", "", ""), false},
		{"its own delegation tool", "pretool-subagent", hookPayload(t, "desktop-session", "", "Agent", ""), false},
		{"its own question prompt", "pretool-subagent", hookPayload(t, "desktop-session", "", "AskUserQuestion", ""), false},
		{"a cd in its shell", "pretool-bash", hookPayload(t, "desktop-session", "", "Bash", `cd C:\`), false},
		{"a cd in its PowerShell", "pretool-powershell", hookPayload(t, "desktop-session", "", "PowerShell", `cd C:\`), false},
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
// brought the CEO back in native terminal ceo, the Overlord's desktop app
// reopened a Claude Code session of his own in a PrecisionDocs worktree. Its
// SessionStart hook found the lock's holder dead and took the lock, its Stop
// hook was rewoken with the fleet's wakes, and it drained and acknowledged
// them while the CEO read "THIS SESSION DOES NOT HOLD THE HOME".
func TestTheCEOComingBackAfterARestartHoldsTheHomeThoughAnotherSessionStartedFirst(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	deadHolder(t, h.State)
	writeMetaFixture(t, h.State, "g1.meta")
	other := startOtherSession(t, h)

	// Act: the other session starts first, then the CEO's.
	first := other.hook(t, "session-start", hookPayload(t, "desktop-session", "startup", "", ""))
	ceo := startCEOSession(t, h)
	digest := ceo.hook(t, "session-start", hookPayload(t, "ceo-session", "resume", "", ""))

	// Assert
	if first.Exit != 0 || first.Stdout != "" || first.Stderr != "" {
		t.Errorf("the other session's start: exit = %d, stdout = %q, stderr = %q, want nothing", first.Exit, first.Stdout, first.Stderr)
	}
	if holder := holderPID(t, h.State); holder != ceo.pid {
		t.Fatalf("the session lock names pid %d, want the CEO's harness pid %d (the other session's is %d)", holder, ceo.pid, other.pid)
	}
	if want := fmt.Sprintf("SESSION LOCK: held by pid %d", ceo.pid); !strings.Contains(digest.Stdout, want) || strings.Contains(digest.Stdout, "READ-ONLY") {
		t.Errorf("the CEO's digest lacks %q or is read-only:\n%s", want, digest.Stdout)
	}
	if registered := registeredPID(t, h.State); registered != ceo.pid {
		t.Errorf("primary.json registers pid %d, want the CEO's harness pid %d", registered, ceo.pid)
	}

	// Act: both sessions end a turn, then a goblin reports.
	stop := hookPayload(t, "desktop-session", "", "", "")
	otherStop := other.begin(t, stopWaits, stop, "hook", "stop-autoarm")
	ceoStop := ceo.begin(t, stopWaits, hookPayload(t, "ceo-session", "", "", ""), "hook", "stop-autoarm")
	otherEnded, isEnded := otherStop(30 * time.Second)
	goblinReports(t, h.State)
	rewake, isRewoken := ceoStop(60 * time.Second)

	// Assert: the wake reaches the CEO and nothing reaches the other session.
	if !isEnded || otherEnded.Exit != 0 || otherEnded.Stdout != "" || otherEnded.Stderr != "" {
		t.Errorf("the other session's Stop hook: ended = %t, exit = %d, stdout = %q, stderr = %q, want it over at once with nothing", isEnded, otherEnded.Exit, otherEnded.Stdout, otherEnded.Stderr)
	}
	if !isRewoken || rewake.Exit != 2 || !strings.Contains(rewake.Stderr, "ceo watcher wake") || !strings.Contains(rewake.Stderr, "signal:") {
		t.Errorf("the CEO's Stop hook: ended = %t, exit = %d, stderr = %q, want it rewoken with the goblin's signal", isRewoken, rewake.Exit, rewake.Stderr)
	}
	if guard := other.hook(t, "turnend-guard", stop); guard.Exit != 0 || guard.Stdout != "" || guard.Stderr != "" {
		t.Errorf("the other session's turn-end guard: exit = %d, stdout = %q, stderr = %q, want nothing", guard.Exit, guard.Stdout, guard.Stderr)
	}
	if holder := holderPID(t, h.State); holder != ceo.pid {
		t.Errorf("after both sessions' Stop hooks the session lock names pid %d, want the CEO's harness pid %d", holder, ceo.pid)
	}
}

// A session that is not the CEO's own and already holds the lock, as the
// desktop app's session did on 2026-10-09 under the build before this rule,
// loses it to the CEO at the CEO's next hook, with no hand edit of the state
// folder. The takeover is audited, the CEO is told at once, and the other
// session's hooks never take the lock back.
func TestTheCEOTakesTheHomeOverFromASessionThatIsNotItsOwn(t *testing.T) {
	stop := hookPayload(t, "ceo-session", "", "", "")
	for _, c := range []struct {
		name string
		// next is the CEO's next hook, and told what it must say about the
		// takeover.
		next func(t *testing.T, ceo *standInSession) sessionResponse
		told func(response sessionResponse) string
	}{
		{"at its next Stop", func(t *testing.T, ceo *standInSession) sessionResponse {
			return ceo.run(t, stopWaits, stop, "hook", "stop-autoarm")
		}, func(response sessionResponse) string { return response.Stderr }},
		{"at its next start", func(t *testing.T, ceo *standInSession) sessionResponse {
			return ceo.hook(t, "session-start", hookPayload(t, "ceo-session", "resume", "", ""))
		}, func(response sessionResponse) string { return response.Stdout }},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h := scratchHome(t)
			writeMetaFixture(t, h.State, "g1.meta")
			other := startOtherSession(t, h)
			ceo := startCEOSession(t, h)
			if _, err := lock.AcquireOwner(h.State, other.pid, "desktop-session"); err != nil {
				t.Fatal(err)
			}

			// Act
			response := c.next(t, ceo)

			// Assert
			if holder := holderPID(t, h.State); holder != ceo.pid {
				t.Fatalf("after the CEO's hook the session lock names pid %d, want the CEO's harness pid %d (the other session's is %d)", holder, ceo.pid, other.pid)
			}
			from := fmt.Sprintf("from pid %d", other.pid)
			if told := c.told(response); !strings.Contains(told, from) {
				t.Errorf("the CEO's hook said %q, want it to say it took the lock over %s", told, from)
			}
			audit, err := os.ReadFile(filepath.Join(h.State, custodyAudit))
			if err != nil || !strings.Contains(string(audit), strconv.Itoa(other.pid)) || !strings.Contains(string(audit), strconv.Itoa(ceo.pid)) || !strings.Contains(string(audit), "desktop-session") {
				t.Errorf("state\\%s = %q (%v), want one record naming pid %d, its session and pid %d", custodyAudit, audit, err, other.pid, ceo.pid)
			}
			drained := ceo.run(t, nil, "", "drain")
			if !strings.Contains(drained.Stdout, from) {
				t.Errorf("ceo drain in the CEO's session shows %q, want the wake that says it took the lock over %s", drained.Stdout, from)
			}
			// It could not register while the other session held the home.
			if registered := registeredPID(t, h.State); registered != ceo.pid {
				t.Errorf("primary.json registers pid %d, want the CEO's harness pid %d", registered, ceo.pid)
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
			if holder := holderPID(t, h.State); holder != ceo.pid {
				t.Errorf("after the other session's hooks the session lock names pid %d, want the CEO's harness pid %d", holder, ceo.pid)
			}
		})
	}
}

// The other session's Stop hook, started by the build before this rule, still
// waits on the wake queue for up to eight hours and holds the lock that lets
// one Stop hook arm at a time. The CEO's Stop hook arms all the same, so a
// goblin's report still rewakes the CEO.
func TestTheCEOsStopHookArmsWhileAnotherSessionsStopHookStillWaits(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	writeMetaFixture(t, h.State, "g1.meta")
	other := startOtherSession(t, h)
	ceo := startCEOSession(t, h)
	ceo.hook(t, "session-start", hookPayload(t, "ceo-session", "startup", "", ""))
	if _, err := lock.AcquireNamedOwner(h.State, autoarmLockName, other.pid, autoarmSession); err != nil {
		t.Fatal(err)
	}

	// Act
	ceoStop := ceo.begin(t, stopWaits, hookPayload(t, "ceo-session", "", "", ""), "hook", "stop-autoarm")
	if early, isOver := ceoStop(3 * time.Second); isOver {
		t.Fatalf("the CEO's Stop hook ended before any goblin reported: exit = %d, stderr = %q, want it armed and waiting", early.Exit, early.Stderr)
	}
	goblinReports(t, h.State)
	rewake, isRewoken := ceoStop(60 * time.Second)

	// Assert
	if !isRewoken || rewake.Exit != 2 || !strings.Contains(rewake.Stderr, "signal:") {
		t.Errorf("the CEO's Stop hook: ended = %t, exit = %d, stderr = %q, want it rewoken with the goblin's signal", isRewoken, rewake.Exit, rewake.Stderr)
	}
}

// Acknowledging the fleet's wakes, steering a goblin and dispatching one are
// the CEO's. An agent's session that is not the CEO's own is refused each,
// and the CEO's own session does each.
func TestOnlyTheCEOsOwnSessionActsAsTheCEO(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	other := startOtherSession(t, h)
	ceo := startCEOSession(t, h)
	ceo.hook(t, "session-start", hookPayload(t, "ceo-session", "startup", "", ""))
	queued, err := wake.Append(h.State, "check", "probe", "a wake only the CEO acknowledges")
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
		if response.Exit != 1 || !strings.Contains(response.Stderr, "not the CEO's own session") {
			t.Errorf("ceo %s in the other session: exit = %d, stderr = %q, want it refused as not the CEO's own session", name, response.Exit, response.Stderr)
		}
	}
	if pending() != 1 {
		t.Errorf("the other session acknowledged the wake: %d pending, want 1", pending())
	}
	if did := acted(); did != "" {
		t.Errorf("the other session acted as the CEO: %q, want nothing sent or dispatched", did)
	}

	// Act: a goblin's command under the same harness, as a proof a goblin
	// runs gives the commands it runs itself.
	asGoblin := other.run(t, map[string]string{harness.RoleVariable: harness.RoleGoblin}, "", "send", "g2", "carry", "on")

	// Assert
	if asGoblin.Exit != 0 || acted() != "send g2 carry on\n" {
		t.Errorf("ceo send from a goblin's process: exit = %d, stderr = %q, acted %q, want it sent: a goblin keeps the commands it has", asGoblin.Exit, asGoblin.Stderr, acted())
	}

	// Act: the CEO's own session does each.
	allowed := map[string]sessionResponse{
		"drain --ack-through": ceo.run(t, nil, "", acknowledge...),
		"send":                ceo.run(t, nil, "", steer...),
		"spawn":               ceo.run(t, nil, "", dispatch("g9")...),
	}

	// Assert
	for name, response := range allowed {
		if response.Exit != 0 {
			t.Errorf("ceo %s in the CEO's own session: exit = %d, stderr = %q, want it done", name, response.Exit, response.Stderr)
		}
	}
	if pending() != 0 {
		t.Errorf("the CEO's own session left %d wake(s) pending, want none", pending())
	}
	if did, want := acted(), "send g2 carry on\nsend g1 merge main first\nspawn g9\n"; did != want {
		t.Errorf("the CEO's own session acted %q, want %q", did, want)
	}
}

// The digest printed by hand takes the session lock only in the CEO's own
// session. Any other session reads it and takes nothing, whoever holds the
// lock or nobody.
func TestTheDigestPrintedByHandTakesTheLockOnlyInTheCEOsOwnSession(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	other := startOtherSession(t, h)

	// Act
	printed := other.run(t, nil, "", "session-start")

	// Assert
	if printed.Exit != 0 || !strings.Contains(printed.Stdout, "READ-ONLY DIGEST") {
		t.Errorf("ceo session-start in the other session: exit = %d, want the read-only digest, got:\n%s", printed.Exit, firstLines(printed.Stdout, 8))
	}
	if holder := holderPID(t, h.State); holder != 0 {
		t.Errorf("ceo session-start in the other session took the session lock for pid %d (the session's is %d), want nobody holding it", holder, other.pid)
	}

	// Act
	ceo := startCEOSession(t, h)
	printed = ceo.run(t, nil, "", "session-start")

	// Assert
	if printed.Exit != 0 || strings.Contains(printed.Stdout, "READ-ONLY DIGEST") {
		t.Errorf("ceo session-start in the CEO's own session: exit = %d, want the digest under its lock, got:\n%s", printed.Exit, firstLines(printed.Stdout, 8))
	}
	if holder := holderPID(t, h.State); holder != ceo.pid {
		t.Errorf("ceo session-start in the CEO's own session left the lock with pid %d, want the CEO's harness pid %d", holder, ceo.pid)
	}
}

// firstLines is the start of a long output, for a failure's message.
func firstLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	return strings.Join(lines[:min(count, len(lines))], "\n")
}

// scratchHome's stand-in ceo refuses a home no test marked, so a stand-in
// session that lost its environment cannot reach another home.
func TestTheStandInCEORunsOnlyAgainstAMarkedScratchHome(t *testing.T) {
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
		t.Errorf("the stand-in ceo ran against an unmarked home: exit = %d, stdout = %q", response.Exit, response.Stdout)
	}
}
