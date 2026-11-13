//go:build linux || darwin

package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ticketOf is the first ticket pid, a process that runs, takes in a line.
func ticketOf(t *testing.T, pid int) string {
	t.Helper()
	instance, status := readInstance(pid)
	if status != statusAlive {
		t.Fatalf("process %d could not be read to write its ticket", pid)
	}
	return fmt.Sprintf("%d %s 1", pid, instance)
}

// staleTickets are the tickets no running process stands behind. Windows
// drops a dead waiter's place by itself, and here the next look at the line
// has to strike it.
var staleTickets = []struct {
	name   string
	ticket func(t *testing.T) string
}{
	{name: "its process ended", ticket: func(t *testing.T) string {
		waiter := lastingProgram(30)
		if err := waiter.Start(); err != nil {
			t.Fatal(err)
		}
		ticket := ticketOf(t, waiter.Process.Pid)
		_ = waiter.Process.Kill()
		_ = waiter.Wait()
		return ticket
	}},
	{name: "another process has its ID now", ticket: func(t *testing.T) string {
		pid := liveProcess(t)
		instance, _ := readInstance(pid)
		boot, ticks := cutInstance(t, instance)
		return fmt.Sprintf("%d %s/%d 1", pid, boot, ticks+1)
	}},
}

// writeLine puts tickets, in order, in the line of waiters at path.
func writeLine(t *testing.T, path string, tickets ...string) {
	t.Helper()
	text := ""
	for _, ticket := range tickets {
		text += ticket + "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A waiter that was killed leaves its ticket at the head of the line. The
// lock passes to the next waiter all the same, not to nobody until every
// caller has waited its whole wait out.
func TestATicketNoRunningProcessStandsBehindDoesNotHoldTheLine(t *testing.T) {
	for _, stale := range staleTickets {
		t.Run(stale.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			writeLine(t, filepath.Join(dir, takerLock+waitersSuffix), stale.ticket(t))

			// Act
			_, err := AcquireNamedOwnerWithin(dir, takerLock, os.Getpid(), "wake", 5*time.Second)

			// Assert
			if err != nil {
				t.Fatalf("a waiter behind a ticket nobody stands behind was refused: %v", err)
			}
			if err := ReleaseNamed(dir, takerLock); err != nil {
				t.Errorf("the waiter could not let the lock go: %v", err)
			}
		})
	}
}

// A sign whose file holds only such a ticket is nobody's: it does not read
// as shown, and the next process to show it gets it.
func TestASignNoRunningProcessHoldsIsNotShownAndCanBeShown(t *testing.T) {
	for _, stale := range staleTickets {
		t.Run(stale.name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), ".claude-autoarm-arming")
			writeLine(t, path, stale.ticket(t))

			// Act
			isShown := Shown(path)
			hide, err := Show(path, 50*time.Millisecond)

			// Assert
			if isShown {
				t.Error("a sign no running process holds reads as shown")
			}
			if err != nil {
				t.Fatalf("a sign no running process holds could not be shown: %v", err)
			}
			if !Shown(path) {
				t.Error("the sign this process then showed does not read as shown")
			}
			hide()
		})
	}
}

// A ticket names its process by its instance and carries no start, so the
// ticket of a process that still runs holds its place whatever the clock
// was stepped to since it was written.
func TestATicketOfARunningProcessHoldsTheLine(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, takerLock+waitersSuffix)
	writeLine(t, path, ticketOf(t, liveProcess(t)))

	// Act
	_, err := AcquireNamedOwnerWithin(dir, takerLock, os.Getpid(), "wake", 300*time.Millisecond)

	// Assert
	if !errors.Is(err, ErrHeld) {
		t.Errorf("a waiter behind a running process's ticket got %v, want ErrHeld after its wait", err)
	}
	if !Shown(path) {
		t.Error("a line a running process heads does not read as held")
	}
}
