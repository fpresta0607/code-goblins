package supervise

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
)

// deadPID runs a throwaway process to completion and returns its now-dead
// PID, the same fixture technique internal/lock's own tests use to build a
// verifiably-dead owner without going through the real acquire/steal path.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState.Pid()
}

// writeDeadLock writes a lock.Info directly to dir/name, naming pid (a
// verifiably dead process) as holder, from outside package lock: the
// unexported writeInfo used by lock's own dead-owner tests is unreachable
// here, so this marshals with encoding/json instead, exactly as the brief
// prescribes.
func writeDeadLock(t *testing.T, dir, name string, pid int) {
	t.Helper()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	info := lock.Info{PID: pid, OwnerPID: pid, Start: time.Now().Add(-time.Hour), Hostname: hostname}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeHeartbeat(t *testing.T, dir string, cycle time.Time) {
	t.Helper()
	if err := monitor.WriteHeartbeat(dir, monitor.Heartbeat{LastCycle: cycle}); err != nil {
		t.Fatal(err)
	}
}

// --- Step 1 row 1: Needed ---

func TestNeededEmptyDir(t *testing.T) {
	dir := t.TempDir()
	needed, msg, err := Needed(dir)
	if err != nil {
		t.Fatalf("Needed err = %v, want nil", err)
	}
	if needed || msg != "" {
		t.Errorf("Needed = %v, %q, want false, \"\"", needed, msg)
	}
}

func TestNeededOneMeta(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, filepath.Join(dir, "g1.meta"))
	needed, msg, err := Needed(dir)
	if err != nil {
		t.Fatalf("Needed err = %v, want nil", err)
	}
	if !needed || msg != "1 task(s) in flight" {
		t.Errorf("Needed = %v, %q, want true, \"1 task(s) in flight\"", needed, msg)
	}
}

func TestNeededUnlistableDirReturnsError(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "not-a-dir")
	touchFile(t, notADir)
	if _, _, err := Needed(notADir); err == nil {
		t.Error("Needed err = nil, want non-nil for a path naming a regular file")
	}
}

// --- Step 1 row 2: WatcherHealthy ---

func TestWatcherHealthyNoLock(t *testing.T) {
	dir := t.TempDir()
	if WatcherHealthy(dir, 300*time.Second) {
		t.Error("WatcherHealthy = true, want false with no lock")
	}
}

func TestWatcherHealthyLiveLockStaleBeat(t *testing.T) {
	dir := t.TempDir()
	if _, err := lock.AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "watch"); err != nil {
		t.Fatal(err)
	}
	writeHeartbeat(t, dir, time.Now().Add(-10*time.Minute))
	if WatcherHealthy(dir, 300*time.Second) {
		t.Error("WatcherHealthy = true, want false with a stale beat")
	}
}

func TestWatcherHealthyDeadOwnerFreshBeat(t *testing.T) {
	dir := t.TempDir()
	writeDeadLock(t, dir, ".watch.lock", deadPID(t))
	writeHeartbeat(t, dir, time.Now())
	if WatcherHealthy(dir, 300*time.Second) {
		t.Error("WatcherHealthy = true, want false with a dead lock owner")
	}
}

func TestWatcherHealthyLiveLockNoBeatFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := lock.AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "watch"); err != nil {
		t.Fatal(err)
	}
	if WatcherHealthy(dir, 300*time.Second) {
		t.Error("WatcherHealthy = true, want false with no beat file at all")
	}
}

func TestWatcherHealthyLiveLockFreshBeat(t *testing.T) {
	dir := t.TempDir()
	if _, err := lock.AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "watch"); err != nil {
		t.Fatal(err)
	}
	writeHeartbeat(t, dir, time.Now())
	if !WatcherHealthy(dir, 300*time.Second) {
		t.Error("WatcherHealthy = false, want true with a live lock and a fresh beat")
	}
}

// --- Step 1 row 3: ChargeBudget / ResetBudget / markers ---

func TestChargeBudgetCountsAndResetsOnSessionChange(t *testing.T) {
	dir := t.TempDir()
	for i, want := range []int{1, 2, 3} {
		count, err := ChargeBudget(dir, "s1")
		if err != nil {
			t.Fatalf("ChargeBudget #%d: %v", i, err)
		}
		if count != want {
			t.Errorf("ChargeBudget #%d = %d, want %d", i, count, want)
		}
	}
	count, err := ChargeBudget(dir, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("ChargeBudget after session change = %d, want 1", count)
	}

	if err := MarkNotified(dir); err != nil {
		t.Fatal(err)
	}
	if err := MarkAlarm(dir); err != nil {
		t.Fatal(err)
	}
	if !NotifiedOnce(dir) {
		t.Error("NotifiedOnce = false, want true after MarkNotified")
	}
	if !AlarmFired(dir) {
		t.Error("AlarmFired = false, want true after MarkAlarm")
	}

	if err := ResetBudget(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".turnend-claude-blocks")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("budget file stat err = %v, want ErrNotExist after ResetBudget", err)
	}
	if NotifiedOnce(dir) {
		t.Error("NotifiedOnce = true, want false after ResetBudget")
	}
	if AlarmFired(dir) {
		t.Error("AlarmFired = true, want false after ResetBudget")
	}
}

// --- Step 1 row 4: epoch ledger ---

func TestEpochLedgerNextAndSetOutcome(t *testing.T) {
	dir := t.TempDir()
	n1, err := NextEpoch(dir)
	if err != nil || n1 != 1 {
		t.Fatalf("NextEpoch #1 = %d, %v, want 1, nil", n1, err)
	}
	n2, err := NextEpoch(dir)
	if err != nil || n2 != 2 {
		t.Fatalf("NextEpoch #2 = %d, %v, want 2, nil", n2, err)
	}
	if err := SetOutcome(dir, 2, "rewake"); err != nil {
		t.Fatal(err)
	}
	epoch, err := ReadEpoch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if epoch.N != 2 {
		t.Errorf("epoch.N = %d, want 2", epoch.N)
	}
	if epoch.Outcome != "rewake" {
		t.Errorf("epoch.Outcome = %q, want rewake", epoch.Outcome)
	}
	if epoch.OwnerPID != os.Getpid() {
		t.Errorf("epoch.OwnerPID = %d, want %d", epoch.OwnerPID, os.Getpid())
	}
	if diff := time.Since(epoch.UpdatedAt); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("epoch.UpdatedAt = %v, want within 2s of now", epoch.UpdatedAt)
	}
}

func TestSetOutcomeRefusesStaleEpoch(t *testing.T) {
	dir := t.TempDir()
	n, err := NextEpoch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NextEpoch(dir); err != nil {
		t.Fatal(err)
	}
	// n is now stale: the ledger has moved on to n+1.
	if err := SetOutcome(dir, n, "rewake"); !errors.Is(err, ErrStaleEpoch) {
		t.Errorf("SetOutcome with a stale epoch = %v, want ErrStaleEpoch", err)
	}
}

func TestSetOutcomeRefusesAnEpochWithNoLedgerAtAll(t *testing.T) {
	dir := t.TempDir()
	if err := SetOutcome(dir, 1, "rewake"); !errors.Is(err, ErrStaleEpoch) {
		t.Errorf("SetOutcome against a never-written ledger = %v, want ErrStaleEpoch", err)
	}
}

// --- Step 1 row 5: AutoarmOwnsRecovery ---

func TestAutoarmOwnsRecovery(t *testing.T) {
	const grace = 300 * time.Second
	const epochFresh = 15 * time.Second

	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{
			name: "healthy watcher",
			setup: func(t *testing.T, dir string) {
				if _, err := lock.AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "watch"); err != nil {
					t.Fatal(err)
				}
				writeHeartbeat(t, dir, time.Now())
			},
			want: true,
		},
		{
			name: "live autoarm lock, no notified marker",
			setup: func(t *testing.T, dir string) {
				if _, err := lock.AcquireNamedOwner(dir, ".claude-autoarm.lock", os.Getpid(), "autoarm"); err != nil {
					t.Fatal(err)
				}
			},
			want: true,
		},
		{
			name: "fresh rewake epoch owned by this live process",
			setup: func(t *testing.T, dir string) {
				n, err := NextEpoch(dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := SetOutcome(dir, n, "rewake"); err != nil {
					t.Fatal(err)
				}
			},
			want: true,
		},
		{
			name:  "(a) no proofs at all",
			setup: func(t *testing.T, dir string) {},
			want:  false,
		},
		{
			name: "(b) autoarm lock with empty session",
			setup: func(t *testing.T, dir string) {
				if _, err := lock.AcquireNamedOwner(dir, ".claude-autoarm.lock", os.Getpid(), ""); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "(c) autoarm lock with autoarm session but already notified",
			setup: func(t *testing.T, dir string) {
				if _, err := lock.AcquireNamedOwner(dir, ".claude-autoarm.lock", os.Getpid(), "autoarm"); err != nil {
					t.Fatal(err)
				}
				if err := MarkNotified(dir); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "(d) rewake epoch older than epochFresh",
			setup: func(t *testing.T, dir string) {
				e := Epoch{N: 1, OwnerPID: os.Getpid(), Outcome: "rewake", UpdatedAt: time.Now().Add(-60 * time.Second)}
				if err := writeEpochFile(dir, e); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "(e) fresh epoch outcome failed",
			setup: func(t *testing.T, dir string) {
				e := Epoch{N: 1, OwnerPID: os.Getpid(), Outcome: "failed", UpdatedAt: time.Now()}
				if err := writeEpochFile(dir, e); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "(e) fresh epoch outcome failed-suppressed",
			setup: func(t *testing.T, dir string) {
				e := Epoch{N: 1, OwnerPID: os.Getpid(), Outcome: "failed-suppressed", UpdatedAt: time.Now()}
				if err := writeEpochFile(dir, e); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "(e) fresh epoch outcome arming",
			setup: func(t *testing.T, dir string) {
				e := Epoch{N: 1, OwnerPID: os.Getpid(), Outcome: "arming", UpdatedAt: time.Now()}
				if err := writeEpochFile(dir, e); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "(f) fresh rewake owned by a dead pid",
			setup: func(t *testing.T, dir string) {
				e := Epoch{N: 1, OwnerPID: deadPID(t), Outcome: "rewake", UpdatedAt: time.Now()}
				if err := writeEpochFile(dir, e); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(t, dir)
			if got := AutoarmOwnsRecovery(dir, grace, epochFresh); got != c.want {
				t.Errorf("AutoarmOwnsRecovery = %v, want %v", got, c.want)
			}
		})
	}
}

// --- Step 1 row 6: markers ---

func TestMarkNotifiedIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if NotifiedOnce(dir) {
		t.Error("NotifiedOnce = true, want false before MarkNotified")
	}
	if err := MarkNotified(dir); err != nil {
		t.Fatal(err)
	}
	if !NotifiedOnce(dir) {
		t.Error("NotifiedOnce = false, want true after MarkNotified")
	}
	if err := MarkNotified(dir); err != nil {
		t.Errorf("second MarkNotified = %v, want idempotent nil", err)
	}
	if !NotifiedOnce(dir) {
		t.Error("NotifiedOnce = false, want true after second MarkNotified")
	}
}

// --- Step 1 row 7: ChargeBudget steals a dead lock holder ---

func TestChargeBudgetStealsDeadLockHolder(t *testing.T) {
	dir := t.TempDir()
	writeDeadLock(t, dir, ".turnend-claude-blocks.lock", deadPID(t))
	count, err := ChargeBudget(dir, "s1")
	if err != nil {
		t.Fatalf("ChargeBudget with a dead lock holder: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}
