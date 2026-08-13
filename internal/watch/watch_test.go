package watch

import (
	"context"
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
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
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
	seenPath := filepath.Join(dir, SeenName("a.status"))

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

// TestScanSignalsDistinguishesSanitizeCollisions proves the FNV-1a hash
// suffix in SeenName does the job the doc comments on Sanitize and SeenName
// claim: two distinct, valid-on-NTFS filenames that sanitize to the same
// string must still get distinct signature files. Without the hash suffix
// (i.e. if SeenName reverted to seenPrefix+Sanitize(name)), both files would
// share one .seen-* file: CommitSignatures for whichever committed last
// would leave the other permanently stale, so the second scan below would
// keep reporting one of them changed forever instead of reading quiet. This
// test fails under that mutation.
func TestScanSignalsDistinguishesSanitizeCollisions(t *testing.T) {
	dir := t.TempDir()
	// Both are legal Windows filenames (":" is not, so the collision fixture
	// cannot use it); both sanitize to "a_b_status".
	nameA := "a.b.status"
	nameB := "a_b.status"
	if got := Sanitize(nameA); got != Sanitize(nameB) {
		t.Fatalf("fixture invalid: Sanitize(%q) = %q, Sanitize(%q) = %q, want them equal so this test proves something", nameA, got, nameB, Sanitize(nameB))
	}
	if SeenName(nameA) == SeenName(nameB) {
		t.Fatalf("SeenName(%q) and SeenName(%q) collide: %q", nameA, nameB, SeenName(nameA))
	}

	if err := os.WriteFile(filepath.Join(dir, nameA), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nameB), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	changes, err := ScanSignals(dir)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("first scan = %+v, want both colliding files reported", changes)
	}
	if err := CommitSignatures(dir, changes); err != nil {
		t.Fatalf("CommitSignatures: %v", err)
	}

	// If the two files shared one signature file, whichever committed last
	// (alphabetically nameB, since ScanSignals/CommitSignatures process in
	// os.ReadDir order) would have overwritten the other's signature, and
	// this scan would report nameA changed again.
	changes2, err := ScanSignals(dir)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(changes2) != 0 {
		t.Fatalf("second scan after committing both = %+v, want none (signature files must not collide)", changes2)
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

type missingProbe struct{}

func (missingProbe) Inspect(context.Context, state.TaskMeta) (monitor.EndpointSample, error) {
	return monitor.EndpointSample{Verdict: monitor.ProbeMissing, Detail: "pane missing"}, nil
}

func monitoringService(t *testing.T, dir, id string) *monitor.Service {
	t.Helper()
	if err := state.WriteTaskMeta(dir, state.TaskMeta{
		ID:               id,
		Worktree:         `C:\work\` + id,
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "ws",
		HerdrTabID:       "tab-" + id,
		HerdrPaneID:      "pane-" + id,
	}); err != nil {
		t.Fatal(err)
	}
	return &monitor.Service{
		StateDir:            dir,
		Probe:               missingProbe{},
		Now:                 time.Now,
		StaleEscalateAfter:  time.Minute,
		BusyTurnMax:         time.Hour,
		PauseResurfaceAfter: time.Hour,
		Heartbeat:           time.Minute,
		HeartbeatMax:        time.Hour,
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
		if _, err := os.Stat(filepath.Join(dir, SeenName(name))); err != nil {
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

func TestRunContinuesWhenPostGraceRescanIsEmpty(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.status")
	if err := os.WriteFile(aPath, []byte("a1"), 0o644); err != nil {
		t.Fatal(err)
	}

	sleepCalls := 0
	cfg := baseConfig(dir)
	cfg.Monitor = monitoringService(t, dir, "g1")
	cfg.Sleep = func(time.Duration) {
		sleepCalls++
		if sleepCalls == 1 {
			// The file that triggered this cycle is gone by the time the
			// post-grace rescan runs.
			if err := os.Remove(aPath); err != nil {
				t.Fatal(err)
			}
		}
	}

	reason, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(reason, "stale:") {
		t.Fatalf("reason = %q, want the monitor event after the empty rescan", reason)
	}
	if sleepCalls < 1 {
		t.Fatalf("sleepCalls = %d, want at least 1", sleepCalls)
	}

	records, err := wake.Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "stale" {
		t.Fatalf("records = %+v, want exactly one monitor stale record", records)
	}

	ep, err := wake.ReadEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 1 {
		t.Fatalf("episode = %+v, want pending:1 (the empty rescan must not publish its own episode)", ep)
	}
}

func TestRunClosesOnMonitorEvent(t *testing.T) {
	dir := t.TempDir()
	cfg := baseConfig(dir)
	cfg.Monitor = monitoringService(t, dir, "g1")

	reason, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(reason, "stale:endpoint_missing") {
		t.Fatalf("reason = %q, want endpoint-missing monitor event", reason)
	}

	records, err := wake.Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "stale" {
		t.Fatalf("records = %+v, want one stale record", records)
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

	// The load-bearing half of this scenario: the deferred
	// lock.ReleaseNamed must refuse to remove a lock it no longer holds.
	// ReleaseNamed's own self-check is what makes that refusal happen (Run
	// defers it unconditionally), so assert directly on the lock file
	// rather than trusting that behavior implicitly via another package's
	// test suite.
	holder, err := lock.ReadNamed(dir, ".watch.lock")
	if err != nil {
		t.Fatalf("lock file gone after Run's deferred release: %v", err)
	}
	if holder.PID != cmd.Process.Pid {
		t.Errorf("lock holder PID = %d, want the successor's %d (Run's deferred release must not have torn down the successor's lock)", holder.PID, cmd.Process.Pid)
	}
}

func TestRunWritesTypedHeartbeat(t *testing.T) {
	dir := t.TempDir()
	cfg := baseConfig(dir)
	cfg.Monitor = monitoringService(t, dir, "g1")

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	heartbeat, err := monitor.ReadHeartbeat(dir)
	if err != nil {
		t.Fatalf("ReadHeartbeat: %v", err)
	}
	if heartbeat.LastCycle.IsZero() {
		t.Error("typed heartbeat LastCycle is zero")
	}
}
