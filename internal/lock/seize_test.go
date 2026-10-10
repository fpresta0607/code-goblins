package lock

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// liveProcess starts a process that outlives the test's acts and returns its
// pid.
func liveProcess(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd.Process.Pid
}

// takeovers reads every record of dir's custody audit.
func takeovers(t *testing.T, dir string) []Takeover {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, AuditFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var records []Takeover
	lines := bufio.NewScanner(bytes.NewReader(data))
	for lines.Scan() {
		var record Takeover
		if err := json.Unmarshal(lines.Bytes(), &record); err != nil {
			t.Fatalf("custody audit line %q: %v", lines.Text(), err)
		}
		records = append(records, record)
	}
	return records
}

func TestSeizeOwnerTakesTheLockFromALiveHolderAndAuditsIt(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	other := liveProcess(t)
	held, err := AcquireOwner(dir, other, "desktop-session")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	info, replaced, err := SeizeOwner(dir, os.Getpid(), "cfo-session", "cfo hook session-start")

	// Assert
	if err != nil {
		t.Fatalf("SeizeOwner: %v", err)
	}
	if info.PID != os.Getpid() || !HeldBy(dir, os.Getpid()) {
		t.Errorf("the lock names %+v, want this process", info)
	}
	if replaced == nil || replaced.PID != other || replaced.Session != "desktop-session" {
		t.Fatalf("replaced = %+v, want the holder it took the lock from, pid %d", replaced, other)
	}
	records := takeovers(t, dir)
	if len(records) != 1 {
		t.Fatalf("the custody audit holds %d records, want 1: %+v", len(records), records)
	}
	record := records[0]
	if record.Lock != ".lock" || record.From.PID != other || record.From.Session != "desktop-session" || !record.From.Acquired.Equal(held.Acquired) || record.To.PID != os.Getpid() || record.To.Session != "cfo-session" || record.Why != "cfo hook session-start" || record.At.IsZero() {
		t.Errorf("the custody audit records %+v, want the takeover of .lock from pid %d to pid %d with its reason and time", record, other, os.Getpid())
	}
}

func TestSeizeOwnerAuditsNothingWhereItTakesTheLockFromNoLiveHolder(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, dir string){
		"a free lock": func(*testing.T, string) {},
		"a lock this owner holds": func(t *testing.T, dir string) {
			if _, err := AcquireOwner(dir, os.Getpid(), "earlier"); err != nil {
				t.Fatal(err)
			}
		},
		"a lock a dead process left": func(t *testing.T, dir string) { deadHoldersRecord(t, dir, ".lock") },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			arrange(t, dir)

			// Act
			info, replaced, err := SeizeOwner(dir, os.Getpid(), "cfo-session", "cfo register")

			// Assert
			if err != nil || info.PID != os.Getpid() || !HeldBy(dir, os.Getpid()) {
				t.Fatalf("SeizeOwner = %+v, %v, want this process holding the lock", info, err)
			}
			if replaced != nil {
				t.Errorf("replaced = %+v, want no holder replaced", replaced)
			}
			if records := takeovers(t, dir); len(records) != 0 {
				t.Errorf("the custody audit holds %+v, want nothing", records)
			}
		})
	}
}

// A takeover that cannot be recorded is not made.
func TestSeizeOwnerLeavesTheHolderWhenTheTakeoverCannotBeAudited(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	other := liveProcess(t)
	if _, err := AcquireOwner(dir, other, "desktop-session"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, AuditFile), 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	_, replaced, err := SeizeOwner(dir, os.Getpid(), "cfo-session", "cfo register")

	// Assert
	if err == nil || replaced != nil {
		t.Fatalf("SeizeOwner = %+v, %v, want it refused with nobody replaced", replaced, err)
	}
	if !HeldBy(dir, other) {
		t.Error("the holder lost the lock to a takeover no audit recorded")
	}
}

func TestSeizeOwnerRefusesADeadOwner(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	other := liveProcess(t)
	if _, err := AcquireOwner(dir, other, "desktop-session"); err != nil {
		t.Fatal(err)
	}
	ended := exec.Command("cmd", "/c", "exit 0")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}

	// Act
	_, _, err := SeizeOwner(dir, ended.ProcessState.Pid(), "cfo-session", "cfo register")

	// Assert
	if !errors.Is(err, ErrOwnerDead) {
		t.Errorf("err = %v, want ErrOwnerDead", err)
	}
	if !HeldBy(dir, other) || len(takeovers(t, dir)) != 0 {
		t.Error("a dead owner's takeover changed the lock or its audit")
	}
}

// A named lock is taken from its live holder only where the caller says that
// holder may be replaced.
func TestSeizeNamedOwnerTakesOnlyFromAHolderTheCallerNames(t *testing.T) {
	for _, c := range []struct {
		name          string
		isReplaceable bool
	}{{"a holder the caller may replace", true}, {"a holder the caller may not replace", false}} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			other := liveProcess(t)
			if _, err := AcquireNamedOwner(dir, ".autoarm.lock", other, "autoarm"); err != nil {
				t.Fatal(err)
			}
			var asked *Info

			// Act
			_, replaced, err := SeizeNamedOwner(dir, ".autoarm.lock", os.Getpid(), "autoarm", "cfo hook stop-autoarm", func(holder *Info) bool {
				asked = holder
				return c.isReplaceable
			})

			// Assert
			if asked == nil || asked.PID != other {
				t.Fatalf("the caller was asked about %+v, want the live holder pid %d", asked, other)
			}
			if c.isReplaceable {
				if err != nil || replaced == nil || replaced.PID != other || !HeldByNamed(dir, ".autoarm.lock", os.Getpid()) {
					t.Errorf("SeizeNamedOwner = %+v, %v, want the lock taken from pid %d", replaced, err, other)
				}
				if records := takeovers(t, dir); len(records) != 1 || records[0].Lock != ".autoarm.lock" {
					t.Errorf("the custody audit holds %+v, want the one takeover of .autoarm.lock", records)
				}
				return
			}
			if !errors.Is(err, ErrHeld) || replaced != nil || !HeldByNamed(dir, ".autoarm.lock", other) {
				t.Errorf("SeizeNamedOwner = %+v, %v, want ErrHeld and the holder left", replaced, err)
			}
			if records := takeovers(t, dir); len(records) != 0 {
				t.Errorf("the custody audit holds %+v, want nothing", records)
			}
		})
	}
}

// Two hooks of one session fire on the same Stop, so two takeovers of the
// same holder for the same owner can run at once: the lock ends with that
// owner and the holder is recorded as replaced once.
func TestSeizeOwnerAtOnceForOneOwnerReplacesTheHolderOnce(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	other := liveProcess(t)
	if _, err := AcquireOwner(dir, other, "desktop-session"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		replaced *Info
		err      error
	}
	results := make(chan result, 4)

	// Act
	for range cap(results) {
		go func() {
			_, replaced, err := SeizeOwner(dir, os.Getpid(), "cfo-session", "cfo hook stop-autoarm")
			results <- result{replaced, err}
		}()
	}

	// Assert
	replacements := 0
	for range cap(results) {
		select {
		case got := <-results:
			if got.err != nil {
				t.Errorf("SeizeOwner: %v", got.err)
			}
			if got.replaced != nil {
				replacements++
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a takeover never returned")
		}
	}
	if !HeldBy(dir, os.Getpid()) {
		t.Error("the lock does not name the owner every takeover asked for")
	}
	if records := takeovers(t, dir); replacements != 1 || len(records) != 1 {
		t.Errorf("%d takeovers reported a holder replaced and the audit holds %d records, want 1 and 1", replacements, len(records))
	}
}
