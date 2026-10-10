package custody

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// heldByAnother leaves dir's session lock with a live process that is not
// this one, under session, and returns that process.
func heldByAnother(t *testing.T, dir, session string) int {
	t.Helper()
	other := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
	if _, err := lock.AcquireOwner(dir, other.Process.Pid, session); err != nil {
		t.Fatal(err)
	}
	return other.Process.Pid
}

func TestTakeTellsTheCFOItTookTheLockFromALiveHolder(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	other := heldByAnother(t, dir, "desktop-session")

	// Act
	holder, replaced, err := Take(dir, os.Getpid(), "cfo-session", "cfo hook stop-autoarm")

	// Assert
	if err != nil || holder.PID != os.Getpid() || !lock.HeldBy(dir, os.Getpid()) {
		t.Fatalf("Take = %+v, %v, want this process holding the lock", holder, err)
	}
	if replaced == nil || replaced.PID != other {
		t.Fatalf("replaced = %+v, want the holder pid %d", replaced, other)
	}
	pending, err := wake.Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Kind != "check" || pending[0].Key != "custody" {
		t.Fatalf("the wake queue holds %+v, want one check wake keyed custody", pending)
	}
	for _, want := range []string{fmt.Sprintf("from pid %d", other), "desktop-session", lock.AuditFile} {
		if !strings.Contains(pending[0].Detail, want) {
			t.Errorf("the wake says %q, want it to name %q", pending[0].Detail, want)
		}
	}
	if pending[0].Detail != Notice(*replaced) {
		t.Errorf("the wake says %q, want the notice %q", pending[0].Detail, Notice(*replaced))
	}
}

// Taking the lock again under the same custody, as every later hook of the
// session does, tells the CFO nothing more.
func TestTakeTellsTheCFOOfOneTakeoverOnce(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	heldByAnother(t, dir, "desktop-session")
	if _, _, err := Take(dir, os.Getpid(), "cfo-session", "cfo hook stop-autoarm"); err != nil {
		t.Fatal(err)
	}

	// Act
	_, replaced, err := Take(dir, os.Getpid(), "cfo-session", "cfo hook session-start")

	// Assert
	if err != nil || replaced != nil {
		t.Fatalf("the second Take = %+v, %v, want the lock kept with nobody replaced", replaced, err)
	}
	if pending, err := wake.Pending(dir); err != nil || len(pending) != 1 {
		t.Errorf("the wake queue holds %+v (%v), want the one wake of the takeover", pending, err)
	}
}

func TestTakeQueuesNoWakeWhereNobodyHeldTheLock(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	holder, replaced, err := Take(dir, os.Getpid(), "cfo-session", "cfo register")

	// Assert
	if err != nil || holder.PID != os.Getpid() || replaced != nil {
		t.Fatalf("Take = %+v, %+v, %v, want the free lock taken with nobody replaced", holder, replaced, err)
	}
	if pending, err := wake.Pending(dir); err != nil || len(pending) != 0 {
		t.Errorf("the wake queue holds %+v (%v), want nothing", pending, err)
	}
}
