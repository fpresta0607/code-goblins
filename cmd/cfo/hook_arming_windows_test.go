package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// On 2026-10-09 the turn-end guard reopened the CFO's turn with "TURN WOULD
// END BLIND - SUPERVISION IS OFF" in 1 turn of 3 on a loaded machine. Both
// Stop hooks fire at a turn's end: the auto-arm hook needed 1.7 s to hold
// recovery, 3.3 s at worst, and the guard waited 800 ms for that. The
// auto-arm hook shows it is arming before anything slow, here before its
// harness has handed it its payload, and the guard waits for a hook that is
// arming rather than calling the turn blind.
func TestTheTurnEndGuardWaitsForAnAutoArmHookThatIsStillArming(t *testing.T) {
	// Arrange
	h := scratchHome(t)
	writeMetaFixture(t, h.State, "g1.meta")
	cfo := startCFOSession(t, h)
	cfo.hook(t, "session-start", hookPayload(t, "cfo-session", "startup", "", ""))
	stop := hookPayload(t, "cfo-session", "", "", "")
	waits := sweptStopWaits(t, h.State)

	// Act: the turn ends, and the auto-arm hook gets its payload 2 s late.
	wait, err := cfo.request(sessionRequest{Args: []string{"hook", "stop-autoarm"}, Stdin: stop, Env: waits, StdinAfter: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	guarded := cfo.run(t, waits, stop, "hook", "turnend-guard")

	// Assert
	if guarded.Exit != 0 || strings.Contains(guarded.Stderr, "TURN WOULD END BLIND") {
		t.Errorf("the guard reopened the turn while the auto-arm hook was arming: exit = %d after %s, stderr = %q", guarded.Exit, guarded.Took.Round(time.Millisecond), guarded.Stderr)
	}
	// The hook that armed late is the watcher: a goblin's report rewakes it.
	if err := os.WriteFile(filepath.Join(h.State, "g1.status"), []byte("needs-decision: which way\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rewake, isOver, err := wait(time.Minute)
	if err != nil || !isOver || rewake.Exit != 2 || !strings.Contains(rewake.Stderr, "signal:") {
		t.Errorf("the auto-arm hook: ended = %t, exit = %d, stderr = %q, %v, want it rewoken with the goblin's signal", isOver, rewake.Exit, rewake.Stderr, err)
	}
}
