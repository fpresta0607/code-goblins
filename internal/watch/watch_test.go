package watch

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestSanitize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"g1.status", "g1_status"},
		{"w:1/2", "w_1_2"},
	}
	for _, tt := range tests {
		if got := Sanitize(tt.in); got != tt.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScanSignalsDetectsNewAndChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.status")
	if err := os.WriteFile(path, []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changes, err := ScanSignals(dir)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(changes) != 1 || changes[0].Name != "a.status" {
		t.Fatalf("first scan = %+v, want one change naming a.status", changes)
	}

	if err := CommitSignatures(dir, changes); err != nil {
		t.Fatalf("CommitSignatures: %v", err)
	}

	changes, err = ScanSignals(dir)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("second scan = %+v, want none", changes)
	}

	appendFile(t, path, "line2\n")

	changes, err = ScanSignals(dir)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if len(changes) != 1 || changes[0].Name != "a.status" {
		t.Fatalf("third scan = %+v, want one change naming a.status again", changes)
	}
}

func TestScanSignalsCommitIsTheOnlyCommitment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.status")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	seenPath := filepath.Join(dir, ".seen-a_status")

	changes, err := ScanSignals(dir)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("first scan = %+v, want one change", changes)
	}
	if _, err := os.Stat(seenPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("seen file exists before any commit: stat err = %v", err)
	}

	changes2, err := ScanSignals(dir)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(changes2) != 1 {
		t.Fatalf("second scan without commit = %+v, want one change again", changes2)
	}

	if err := CommitSignatures(dir, changes); err != nil {
		t.Fatalf("CommitSignatures: %v", err)
	}
	if _, err := os.Stat(seenPath); err != nil {
		t.Fatalf("seen file missing after commit: %v", err)
	}

	changes3, err := ScanSignals(dir)
	if err != nil {
		t.Fatalf("scan after commit: %v", err)
	}
	if len(changes3) != 0 {
		t.Fatalf("scan after commit = %+v, want none", changes3)
	}

	if err := os.Remove(seenPath); err != nil {
		t.Fatal(err)
	}
	changes4, err := ScanSignals(dir)
	if err != nil {
		t.Fatalf("scan after deleting seen file: %v", err)
	}
	if len(changes4) != 1 {
		t.Fatalf("scan after deleting seen file = %+v, want one change (on-disk signature is sole truth)", changes4)
	}
}

func baseConfig(dir string) Config {
	return Config{
		Home:         home.Home{State: dir},
		Poll:         time.Millisecond,
		SignalGrace:  time.Millisecond,
		Heartbeat:    time.Hour,
		HeartbeatMax: time.Hour,
		Sleep:        func(time.Duration) {},
	}
}

func TestRunClosesOnSignal(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.status")
	bPath := filepath.Join(dir, "b.status")
	if err := os.WriteFile(aPath, []byte("a1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("b1"), 0o644); err != nil {
		t.Fatal(err)
	}

	sleepCalls := 0
	cfg := baseConfig(dir)
	cfg.Sleep = func(time.Duration) {
		sleepCalls++
		if sleepCalls == 1 {
			appendFile(t, aPath, "a2")
		}
	}

	reason, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(reason, "signal:") {
		t.Fatalf("reason = %q, want prefix signal:", reason)
	}
	if !strings.Contains(reason, "a.status") || !strings.Contains(reason, "b.status") {
		t.Errorf("reason = %q, want both files named", reason)
	}

	records, err := wake.Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %+v, want 2", records)
	}
	keys := map[string]bool{}
	for _, r := range records {
		if r.Kind != "signal" {
			t.Errorf("record kind = %q, want signal", r.Kind)
		}
		if r.Detail != reason {
			t.Errorf("record detail = %q, want %q", r.Detail, reason)
		}
		keys[r.Key] = true
	}
	if !keys["a.status"] || !keys["b.status"] {
		t.Errorf("keys = %+v, want distinct a.status and b.status", keys)
	}

	for _, name := range []string{"a.status", "b.status"} {
		if _, err := os.Stat(filepath.Join(dir, ".seen-"+Sanitize(name))); err != nil {
			t.Errorf("seen file missing for %s: %v", name, err)
		}
	}

	ep, err := wake.ReadEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 1 {
		t.Errorf("episode = %+v, want pending:1", ep)
	}
}

func TestRunClosesOnHeartbeat(t *testing.T) {
	dir := t.TempDir()
	heartbeat := 50 * time.Millisecond
	heartbeatPath := filepath.Join(dir, ".last-heartbeat")
	if err := os.WriteFile(heartbeatPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-heartbeat)
	if err := os.Chtimes(heartbeatPath, aged, aged); err != nil {
		t.Fatal(err)
	}

	cfg := baseConfig(dir)
	cfg.Heartbeat = heartbeat

	reason, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reason != "heartbeat" {
		t.Fatalf("reason = %q, want heartbeat", reason)
	}

	records, err := wake.Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "heartbeat" {
		t.Fatalf("records = %+v, want one heartbeat record", records)
	}

	streak, err := os.ReadFile(filepath.Join(dir, ".heartbeat-streak"))
	if err != nil {
		t.Fatalf("streak file missing: %v", err)
	}
	if strings.TrimSpace(string(streak)) != "1" {
		t.Errorf("streak = %q, want 1", streak)
	}
}

func TestHeartbeatBackoffDoublesAndResets(t *testing.T) {
	dir := t.TempDir()
	// heartbeat is seconds, not milliseconds, deliberately: Run's Sleep is
	// faked (no real time.Sleep call happens, so this does not slow the
	// test down), but the assertion that the first pass does NOT close
	// races real wall-clock scheduling between the os.Chtimes below and
	// Run's first heartbeatDue check. A small interval leaves too thin a
	// margin under a loaded `go test ./...` run, where scheduler jitter
	// alone can eat tens of milliseconds and flip the race.
	heartbeat := 2 * time.Second
	heartbeatPath := filepath.Join(dir, ".last-heartbeat")
	if err := os.WriteFile(heartbeatPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-heartbeat)
	if err := os.Chtimes(heartbeatPath, aged, aged); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".heartbeat-streak"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	sleepCalls := 0
	cfg := baseConfig(dir)
	cfg.Heartbeat = heartbeat
	cfg.Sleep = func(time.Duration) {
		sleepCalls++
		if sleepCalls == 1 {
			older := time.Now().Add(-2 * heartbeat)
			if err := os.Chtimes(heartbeatPath, older, older); err != nil {
				t.Fatal(err)
			}
		}
	}

	reason, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reason != "heartbeat" {
		t.Fatalf("reason = %q, want heartbeat", reason)
	}
	if sleepCalls < 1 {
		t.Fatalf("sleepCalls = %d, want at least 1 (proves the pass before it did not close on one Heartbeat)", sleepCalls)
	}

	streak, err := os.ReadFile(filepath.Join(dir, ".heartbeat-streak"))
	if err != nil {
		t.Fatalf("streak file missing: %v", err)
	}
	if strings.TrimSpace(string(streak)) != "2" {
		t.Errorf("streak = %q, want 2", streak)
	}
}

func TestRunSingletonExcludes(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	if _, err := lock.AcquireNamedOwner(dir, ".watch.lock", cmd.Process.Pid, "watch"); err != nil {
		t.Fatal(err)
	}

	_, err := Run(baseConfig(dir))
	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("err = %v, want wrapping lock.ErrHeld", err)
	}
}

// liveForeignInfo probes AcquireNamedOwner on a throwaway directory for pid to
// obtain its verifiable creation time, then hands back an Info a test can
// write directly over a lock file to simulate a successor's takeover, without
// going through the real acquire/contention path (which would correctly
// refuse to steal from this test's own still-live process).
func liveForeignInfo(t *testing.T, pid int) lock.Info {
	t.Helper()
	probeDir := t.TempDir()
	if _, err := lock.AcquireNamedOwner(probeDir, ".probe.lock", pid, "probe"); err != nil {
		t.Fatalf("probe acquire: %v", err)
	}
	info, err := lock.ReadNamed(probeDir, ".probe.lock")
	if err != nil {
		t.Fatalf("probe read: %v", err)
	}
	return *info
}

func TestRunReturnsQuietlyWhenSingletonStolen(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	sleepCalls := 0
	cfg := baseConfig(dir)
	cfg.Sleep = func(time.Duration) {
		sleepCalls++
		if sleepCalls == 1 {
			foreign := liveForeignInfo(t, cmd.Process.Pid)
			data, err := json.Marshal(foreign)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".watch.lock"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	reason, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty (lock lost to a successor)", reason)
	}
	if sleepCalls < 1 {
		t.Fatalf("sleepCalls = %d, want at least 1", sleepCalls)
	}

	records, err := wake.Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("records = %+v, want none", records)
	}
	if _, err := os.Stat(filepath.Join(dir, ".watcher-down")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".watcher-down exists after a lock-lost return: stat err = %v", err)
	}
}

func TestRunTouchesBeat(t *testing.T) {
	dir := t.TempDir()
	heartbeat := 20 * time.Millisecond
	heartbeatPath := filepath.Join(dir, ".last-heartbeat")
	if err := os.WriteFile(heartbeatPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-heartbeat)
	if err := os.Chtimes(heartbeatPath, aged, aged); err != nil {
		t.Fatal(err)
	}

	cfg := baseConfig(dir)
	cfg.Heartbeat = heartbeat

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dir, ".last-watcher-beat"))
	if err != nil {
		t.Fatalf("beat file missing: %v", err)
	}
	if age := time.Since(fi.ModTime()); age > 30*time.Second {
		t.Errorf("beat age = %v, want under 30s", age)
	}
}
