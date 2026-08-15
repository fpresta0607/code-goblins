package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/supervise"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// newPrimaryHome creates AGENTS.md, state/, and a plain git checkout in a
// temp dir, sets CFO_HOME to it, and returns the dir.
func newPrimaryHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# home"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("CFO_HOME", dir)
	return dir
}

func TestRunHookPretoolSubagentDeniesInPrimaryHome(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"Agent"}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	var envelope struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
		SystemMessage string `json:"systemMessage"`
	}
	trimmed := strings.TrimRight(stderr.String(), "\n")
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		t.Fatalf("stderr is not the deny envelope: %v\nstderr=%s", err, stderr.String())
	}
	if envelope.HookSpecificOutput.HookEventName != "PreToolUse" || envelope.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("envelope = %+v, want PreToolUse/deny", envelope)
	}
	if !strings.Contains(envelope.SystemMessage, `delegation-shaped on "agent"`) {
		t.Errorf("systemMessage = %q, want it to contain %q", envelope.SystemMessage, `delegation-shaped on "agent"`)
	}
}

func TestRunHookPretoolSubagentInertWithoutState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# home"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"Agent"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolSubagentEscapeHatch(t *testing.T) {
	newPrimaryHome(t)
	t.Setenv("CFO_ALLOW_SUBAGENT", "1")
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"Agent"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolSubagentMCPPassthrough(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"mcp__x__spawn"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolSubagentTransportFailure(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader("garbage"), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolArmDeniesInPrimaryHome(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-arm", strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"cfo watch &"}}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "watcher-background") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "watcher-background")
	}
}

func TestRunHookPretoolArmAllowsInPrimaryHome(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-arm", strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"git log --oneline"}}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolArmInertWithoutState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# home"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-arm", strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"cfo watch &"}}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolCdDeniesInPrimaryHome(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-cd", strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"cd C:\\"}}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "cwd-relocation") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "cwd-relocation")
	}
}

func TestRunHookPretoolCdAllowsInPrimaryHome(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-cd", strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookUnknownName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runHook("no-such", strings.NewReader(""), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown hook") {
		t.Errorf("stderr = %q, want it to mention unknown hook", stderr.String())
	}
}

// writeMetaFixture drops an empty state/<name> file, standing in for a
// goblin's task meta so supervise.Needed sees work in flight.
func writeMetaFixture(t *testing.T, state, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeHeartbeatFixture(t *testing.T, state string, lastCycle time.Time) {
	t.Helper()
	if err := monitor.WriteHeartbeat(state, monitor.Heartbeat{LastCycle: lastCycle}); err != nil {
		t.Fatal(err)
	}
}

// setSyncWait sets the default sync-wait window every turnend-guard case
// uses unless it says otherwise: short enough to keep the suite fast, long
// enough for the 100ms poll to observe a late proof.
func setSyncWait(t *testing.T, ms string) {
	t.Helper()
	t.Setenv("CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS", ms)
}

func TestRunHookTurnendGuardNotPrimary(t *testing.T) {
	dir := t.TempDir()
	// state/ exists (so the Needed/ResetBudget paths could in principle
	// run) but AGENTS.md does not and there is no git checkout, so
	// IsPrimary is false. Asserting state/ gains no files pins INERT MEANS
	// INERT at the IsPrimary gate specifically, distinct from the Needed
	// fail-open paths tested elsewhere, all of which require a primary
	// home to even reach.
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	setSyncWait(t, "500")
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("state/ gained files from a non-primary home: %v", entries)
	}
}

func TestRunHookTurnendGuardResetsQuietBudget(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	if err := os.WriteFile(filepath.Join(state, ".turnend-claude-blocks"), []byte("session=s1\ncount=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(state, ".turnend-claude-blocks")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("budget file survives a quiet turn: stat err = %v, want ErrNotExist", err)
	}
}

func TestRunHookTurnendGuardKeepsBudgetAfterNotified(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	if err := os.WriteFile(filepath.Join(state, ".turnend-claude-blocks"), []byte("session=s1\ncount=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := supervise.MarkNotified(state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(state, ".turnend-claude-blocks"))
	if err != nil {
		t.Fatalf("budget file removed: %v", err)
	}
	if string(data) != "session=s1\ncount=2\n" {
		t.Errorf("budget file = %q, want unchanged", string(data))
	}
	if !supervise.NotifiedOnce(state) {
		t.Error("notified marker removed by a quiet turn with a pending failure episode")
	}
}

func TestRunHookTurnendGuardUnlistableStateIsSilent(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	if err := os.WriteFile(filepath.Join(state, ".turnend-claude-blocks"), []byte("session=s1\ncount=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("icacls", state, "/deny", os.Getenv("USERNAME")+":(RD)")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("icacls deny: %v\n%s", err, out)
	}
	// Registered after t.TempDir() (inside newPrimaryHome above), so LIFO
	// cleanup ordering runs this restore before TempDir's own RemoveAll;
	// without it RemoveAll cannot list state\ and the fixture leaks.
	t.Cleanup(func() {
		exec.Command("icacls", state, "/remove:d", os.Getenv("USERNAME")).Run()
	})
	if _, rerr := os.ReadDir(state); rerr == nil {
		t.Skip("icacls deny had no effect (elevated session?)")
	}

	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}

	// Restore listing to verify the budget file directly, before this
	// test's own cleanup removes the ACL again (Cleanup is idempotent).
	exec.Command("icacls", state, "/remove:d", os.Getenv("USERNAME")).Run()
	data, err := os.ReadFile(filepath.Join(state, ".turnend-claude-blocks"))
	if err != nil {
		t.Fatalf("budget file missing after an unlistable-dir guard call: %v", err)
	}
	if string(data) != "session=s1\ncount=2\n" {
		t.Errorf("budget file = %q, want left exactly as written", string(data))
	}
}

func TestRunHookTurnendGuardHealthyWatcherExitsClean(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if _, err := lock.AcquireNamedOwner(state, ".watch.lock", os.Getpid(), "watch"); err != nil {
		t.Fatal(err)
	}
	writeHeartbeatFixture(t, state, time.Now())
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookTurnendGuardBlocksBlindTurn(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr.String(), "TURN WOULD END BLIND") {
		t.Errorf("stderr = %q, want it to contain TURN WOULD END BLIND", stderr.String())
	}
	if !strings.Contains(stderr.String(), "1 task(s) in flight") {
		t.Errorf("stderr = %q, want it to contain 1 task(s) in flight", stderr.String())
	}
}

func TestRunHookTurnendGuardAllowsFreshRewakeProof(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	n, err := supervise.NextEpoch(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervise.SetOutcome(state, n, "rewake"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
}

func TestRunHookTurnendGuardAlarmFiresOnceThenBlocks(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if err := os.WriteFile(filepath.Join(state, ".turnend-claude-blocks"), []byte("session=s1\ncount=3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := supervise.MarkNotified(state); err != nil {
		t.Fatal(err)
	}

	var stdout1, stderr1 bytes.Buffer
	exit1 := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout1, &stderr1)
	if exit1 != 0 {
		t.Fatalf("first call exit = %d, want 0; stderr=%s", exit1, stderr1.String())
	}
	if !strings.Contains(stdout1.String(), "GENUINELY DOWN") {
		t.Errorf("stdout = %q, want it to contain GENUINELY DOWN", stdout1.String())
	}
	if !supervise.AlarmFired(state) {
		t.Error("alarmed marker not created")
	}
	data, err := os.ReadFile(filepath.Join(state, ".turnend-claude-blocks"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "count=4") {
		t.Errorf("budget file = %q, want count=4", string(data))
	}

	var stdout2, stderr2 bytes.Buffer
	exit2 := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout2, &stderr2)
	if exit2 != 2 {
		t.Fatalf("second call exit = %d, want 2 (the alarm message is one-time, the block is not)", exit2)
	}
	if !strings.Contains(stderr2.String(), "TURN WOULD END BLIND") {
		t.Errorf("second call stderr = %q, want the blind-turn banner", stderr2.String())
	}
}

func TestRunHookTurnendGuardRendersBeatAge(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	aged := time.Now().Add(-10 * time.Minute)
	writeHeartbeatFixture(t, state, aged)
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr.String(), "last beat:") {
		t.Errorf("stderr = %q, want it to contain \"last beat:\"", stderr.String())
	}
	if strings.Contains(stderr.String(), "last beat: never") {
		t.Errorf("stderr = %q, want an age token rather than never", stderr.String())
	}
}

func TestRunHookTurnendGuardPollsForLateProof(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "500")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(150 * time.Millisecond)
		n, err := supervise.NextEpoch(state)
		if err != nil {
			return
		}
		_ = supervise.SetOutcome(state, n, "rewake")
	}()
	defer func() { <-done }()

	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (the poll should observe the late proof); stderr=%s", exit, stderr.String())
	}
}

func TestRunHookTurnendGuardZeroWindowDoesNotWait(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "0")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2 (a zero sync window must not wait for the late proof)", exit)
	}
	// A proof landing after the fact must be unobservable to a zero-window
	// guard. Writing it after the hook returns is deterministic; the previous
	// form raced a 150ms goroutine against the hook start and flaked whenever
	// a loaded host scheduled the goroutine first.
	n, err := supervise.NextEpoch(state)
	if err != nil {
		t.Fatalf("NextEpoch after zero-window exit: %v", err)
	}
	if err := supervise.SetOutcome(state, n, "rewake"); err != nil {
		t.Fatalf("SetOutcome after zero-window exit: %v", err)
	}
}

// TestRunHookTurnendGuardChargeBudgetErrorEscalates exercises the failure
// posture the brief prescribes for a genuine ChargeBudget error (distinct
// from the icacls unlistable-Needed case above, which is caught earlier at
// step 2 and never reaches ChargeBudget at all): the ladder cannot run, so
// the guard skips straight to the attended fail-open instead of blocking
// every Stop forever on a counter that can never advance. Making the budget
// file's own path a directory forces os.ReadFile to fail with a genuine I/O
// error rather than ErrNotExist.
func TestRunHookTurnendGuardChargeBudgetErrorEscalates(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "50")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if err := os.MkdirAll(filepath.Join(state, ".turnend-claude-blocks"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "GENUINELY DOWN") {
		t.Errorf("stdout = %q, want it to contain GENUINELY DOWN", stdout.String())
	}
	// attendedFailOpen must NOT mark the alarm (C2 fix): this branch has
	// nothing to do with the ladder's own one-shot escalation, and marking
	// it here would permanently disarm that escalation for whatever
	// genuine failure episode comes next. See
	// TestRunHookTurnendGuardBudgetErrorDoesNotConsumeLadderAlarm below for
	// the end-to-end regression.
	if supervise.AlarmFired(state) {
		t.Error("attendedFailOpen must not mark the alarm; it would consume the ladder's one-shot escalation")
	}
}

// TestRunHookTurnendGuardCeilingTerminatesWithoutNotified is the C1 hard-
// ceiling regression: NotifiedOnce has exactly one intended writer, Task
// 11's stop-autoarm hook. If that hook is never registered (or dies before
// ever calling MarkNotified), the normal ladder arm's NotifiedOnce conjunct
// can never be true, so without the ceiling arm this fixture would block
// forever. blockBudget defaults to 3, so the ceiling (escalationCeilingMultiplier
// * blockBudget) is 9: the first 9 charges (count 1..9) must still block,
// and the 10th (count 10) must cross the ceiling and let the turn end.
func TestRunHookTurnendGuardCeilingTerminatesWithoutNotified(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "0") // nothing in this fixture is ever a proof; skip the wait
	// Pin the block budget so an ambient CFO_CLAUDE_TURNEND_BLOCK_BUDGET
	// override in a developer shell cannot break the 9/10 boundary asserted
	// below.
	t.Setenv("CFO_CLAUDE_TURNEND_BLOCK_BUDGET", "3")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	for i := 1; i <= 9; i++ {
		var stdout, stderr bytes.Buffer
		exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
		if exit != 2 {
			t.Fatalf("call #%d: exit = %d, want 2 (blocked, ceiling not yet reached); stdout=%s stderr=%s", i, exit, stdout.String(), stderr.String())
		}
	}

	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("call #10: exit = %d, want 0 (hard ceiling reached); stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ESCALATION LADDER NEVER ESCALATED") {
		t.Errorf("call #10 stdout = %q, want the ceiling message", stdout.String())
	}
}

// TestRunHookTurnendGuardBudgetErrorDoesNotConsumeLadderAlarm is the C2
// regression: a budget I/O error unrelated to any failure episode (here, a
// quiet-turn ResetBudget failure) must not consume the ladder's one-shot
// GENUINELY DOWN escalation. If attendedFailOpen still called MarkAlarm,
// step B below would see AlarmFired already true and skip straight to a
// permanent block instead of getting its own attended fail-open.
func TestRunHookTurnendGuardBudgetErrorDoesNotConsumeLadderAlarm(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "0")
	state := filepath.Join(dir, "state")

	// Step A: force a genuine ResetBudget I/O error on a quiet turn (no
	// g1.meta) by making the budget file's own path a NON-EMPTY directory,
	// so os.Remove fails "directory not empty" through every bounded
	// retry (a plain empty directory would not reproduce this: os.Remove
	// succeeds on an empty directory).
	blockerDir := filepath.Join(state, ".turnend-claude-blocks")
	if err := os.MkdirAll(blockerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockerDir, "blocker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdoutA, stderrA bytes.Buffer
	exitA := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdoutA, &stderrA)
	if exitA != 0 {
		t.Fatalf("step A exit = %d, want 0; stderr=%s", exitA, stderrA.String())
	}
	if !strings.Contains(stdoutA.String(), "GENUINELY DOWN") {
		t.Fatalf("step A stdout = %q, want the attended fail-open message", stdoutA.String())
	}
	if supervise.AlarmFired(state) {
		t.Fatal("a quiet-turn budget I/O error must not consume the ladder's one-shot alarm")
	}

	// Step B: clear the obstruction, then arrange a genuine failure episode
	// (goblin in flight, already notified, budget already at the block
	// threshold) and confirm it still gets ITS OWN attended fail-open
	// rather than being routed straight to a permanent block because
	// AlarmFired was wrongly left set by step A.
	if err := os.RemoveAll(blockerDir); err != nil {
		t.Fatal(err)
	}
	writeMetaFixture(t, state, "g1.meta")
	if err := os.WriteFile(filepath.Join(state, ".turnend-claude-blocks"), []byte("session=s1\ncount=3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := supervise.MarkNotified(state); err != nil {
		t.Fatal(err)
	}
	var stdoutB, stderrB bytes.Buffer
	exitB := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdoutB, &stderrB)
	if exitB != 0 {
		t.Fatalf("step B exit = %d, want 0 (the genuine episode's own attended fail-open); stderr=%s", exitB, stderrB.String())
	}
	if !strings.Contains(stdoutB.String(), "GENUINELY DOWN") {
		t.Errorf("step B stdout = %q, want the GENUINELY DOWN message", stdoutB.String())
	}
	if !supervise.AlarmFired(state) {
		t.Error("step B's own ladder arm must set the alarmed marker")
	}
}

// TestRunHookTurnendGuardChargeBudgetErrorMarksAlarmWhenAlreadyNotified is
// FIX 2's regression: a ChargeBudget error that persists across every Stop
// (here, the budget path shadowed by a directory) must not wedge stop-
// autoarm's repeat-failure arm behind an alarm that can never fire, because
// AlarmFired is set from ONLY inside the normal ladder arm, which a
// persistently erroring ChargeBudget can never reach. With NotifiedOnce
// already true, the episode is a genuine reported failure, so the
// ChargeBudget error branch marks the alarm itself, and a subsequent
// stop-autoarm repeat-failure firing observes it and exits 0 silently
// instead of looping exit 2 forever.
func TestRunHookTurnendGuardChargeBudgetErrorMarksAlarmWhenAlreadyNotified(t *testing.T) {
	dir := newPrimaryHome(t)
	setSyncWait(t, "50")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if err := os.MkdirAll(filepath.Join(state, ".turnend-claude-blocks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := supervise.MarkNotified(state); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := runHook("turnend-guard", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "GENUINELY DOWN") {
		t.Errorf("stdout = %q, want it to contain GENUINELY DOWN", stdout.String())
	}
	if !supervise.AlarmFired(state) {
		t.Fatal("a persistently-failing ChargeBudget after NotifiedOnce must mark the alarm, or the sibling stop-autoarm repeat-failure arm can never release")
	}

	// Confirm the alarm actually unwedges stop-autoarm's repeat-failure arm:
	// a subsequent firing must exit 0 silently rather than 2.
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	foreign := startLiveForeignProcess(t)
	if _, err := lock.AcquireNamedOwner(state, ".watch.lock", foreign.Process.Pid, "watch"); err != nil {
		t.Fatal(err)
	}
	// No fresh beat: the attempt loop can only fail (ErrHeld, not healthy).

	var stdoutAutoarm, stderrAutoarm bytes.Buffer
	exitAutoarm := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdoutAutoarm, &stderrAutoarm)
	if exitAutoarm != 0 {
		t.Fatalf("stop-autoarm exit = %d, want 0; stdout=%s stderr=%s", exitAutoarm, stdoutAutoarm.String(), stderrAutoarm.String())
	}
	if stdoutAutoarm.Len() != 0 || stderrAutoarm.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdoutAutoarm.String(), stderrAutoarm.String())
	}
}

// --- stop-autoarm ---

// setAncestorPID pins the stop-autoarm identity gate to pid via
// CFO_TEST_ANCESTOR_PID: the ambient proc.FindAncestor walk cannot be
// asserted from inside this repo's own test suite, because a go test binary
// launched from a Claude Code session has claude.exe about five hops up its
// own ancestry, well inside maxHops 16, so it always finds a real ancestor
// no fixture here controls.
func setAncestorPID(t *testing.T, pid int) {
	t.Helper()
	t.Setenv("CFO_TEST_ANCESTOR_PID", strconv.Itoa(pid))
}

// setTinyAutoarmIntervals sets the tiny intervals every stop-autoarm case
// uses unless it says otherwise: CFO_POLL/CFO_SIGNAL_GRACE/CFO_HEARTBEAT
// clamp to 1s regardless (watch.ConfigFromEnv floors every interval at 1s),
// so "tiny" here means fast relative to the multi-minute production
// defaults, not literally sub-second. CFO_CLAUDE_AUTOARM_ATTEMPTS=1 keeps
// the attempt loop to a single watch.Run call per firing.
func setTinyAutoarmIntervals(t *testing.T) {
	t.Helper()
	t.Setenv("CFO_POLL", "1")
	t.Setenv("CFO_SIGNAL_GRACE", "1")
	t.Setenv("CFO_HEARTBEAT", "1")
	t.Setenv("CFO_CLAUDE_AUTOARM_ATTEMPTS", "1")
}

// startLiveForeignProcess spawns a throwaway child process that stays alive
// for the fixture's duration (never Waited on until cleanup), for tests that
// need a live foreign PID to pre-hold a lock. Mirrors the ping-child pattern
// internal/lock, internal/watch and internal/proc's own tests already use.
func startLiveForeignProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func TestAutoarmInertWithoutHarnessAncestor(t *testing.T) {
	dir := newPrimaryHome(t)
	setTinyAutoarmIntervals(t) // regression guard: if the gate ever stops firing, watch.Run must not fall through to production-length waits and hang this test
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	exited := exec.Command("cmd", "/c", "exit 0")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	setAncestorPID(t, exited.ProcessState.Pid())

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestAutoarmExitsAtNeedGate(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	state := filepath.Join(dir, "state")

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
	epoch, err := supervise.ReadEpoch(state)
	if err != nil {
		t.Fatal(err)
	}
	if epoch.N != 0 {
		t.Errorf("epoch.N = %d, want 0 (the need gate exits before the epoch is ever taken)", epoch.N)
	}
}

func TestAutoarmCleanWhenNeedVanishes(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Wait until the hook has passed the need gate (Step 3) and taken
		// the epoch ledger (Step 5) before removing the need. Polling the
		// ledger is deterministic: NextEpoch runs only after the need gate
		// and single-flight lock have succeeded, and always before the
		// attempt loop (Step 6) and the need-vanished re-check (Step 7). A
		// fixed sleep here races the need gate under load: if the remove
		// lands first the hook exits at Step 3 and never records an outcome.
		deadline := time.Now().Add(10 * time.Second)
		for {
			epoch, err := supervise.ReadEpoch(state)
			if err == nil && epoch.N >= 1 {
				break
			}
			if time.Now().After(deadline) {
				return
			}
			time.Sleep(time.Millisecond)
		}
		_ = os.WriteFile(filepath.Join(state, "g1.status"), []byte("done\n"), 0o644)
		_ = os.Remove(filepath.Join(state, "g1.meta"))
	}()
	defer func() { <-done }()

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (need vanished wins over whatever the loop found); stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
	epoch, err := supervise.ReadEpoch(state)
	if err != nil {
		t.Fatal(err)
	}
	if epoch.Outcome != "clean" {
		t.Errorf("epoch outcome = %q, want clean", epoch.Outcome)
	}
}

func TestAutoarmRewakeOnSignal(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(state, "g1.status"), []byte("done\n"), 0o644)
	}()
	defer func() { <-done }()

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cfo watcher wake") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "cfo watcher wake")
	}
	if !strings.Contains(stderr.String(), "signal:") {
		t.Errorf("stderr = %q, want it to contain a signal: reason line", stderr.String())
	}
	epoch, err := supervise.ReadEpoch(state)
	if err != nil {
		t.Fatal(err)
	}
	if epoch.Outcome != "rewake" {
		t.Errorf("epoch outcome = %q, want rewake", epoch.Outcome)
	}
	pending, err := wake.Pending(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("pending wake records = %d, want exactly 1", len(pending))
	}
	heartbeat, err := monitor.ReadHeartbeat(state)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.LastCycle.IsZero() {
		t.Error("watch did not record a typed heartbeat before the signal")
	}
}

func TestAutoarmSingleFlight(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t) // regression guard: if single-flight ever stops firing, watch.Run must not fall through to production-length waits and hang this test
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	foreign := startLiveForeignProcess(t)
	if _, err := lock.AcquireNamedOwner(state, ".claude-autoarm.lock", foreign.Process.Pid, "autoarm"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, want a fast single-flight exit rather than falling through to the attempt loop", elapsed)
	}
}

func TestAutoarmFailureEpisode(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")

	foreign := startLiveForeignProcess(t)
	if _, err := lock.AcquireNamedOwner(state, ".watch.lock", foreign.Process.Pid, "watch"); err != nil {
		t.Fatal(err)
	}
	// No typed heartbeat: a fresh beat would make the ErrHeld
	// return read as HEALTHY instead of a strike, which is exactly what
	// TestAutoarmHealthyAfterSteal arranges.

	var stdout1, stderr1 bytes.Buffer
	exit1 := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout1, &stderr1)
	if exit1 != 2 {
		t.Fatalf("first run exit = %d, want 2; stderr=%s", exit1, stderr1.String())
	}
	if !strings.Contains(stderr1.String(), "FAILED after 1 attempt(s)") {
		t.Errorf("first run stderr = %q, want it to contain FAILED after 1 attempt(s)", stderr1.String())
	}
	if !supervise.NotifiedOnce(state) {
		t.Error("notified marker not created after the first failure")
	}
	epoch1, err := supervise.ReadEpoch(state)
	if err != nil {
		t.Fatal(err)
	}
	if epoch1.Outcome != "failed" {
		t.Errorf("first run epoch outcome = %q, want failed", epoch1.Outcome)
	}
	// Requirement 6 regression: this fixture is the DOMINANT watcher-down
	// case (a live-but-wedged foreign holder of .watch.lock with no fresh
	// beat), which fails via wrapped lock.ErrHeld. A publish scoped to
	// exclude ErrHeld (the pre-fix code) never reaches this fixture at
	// all, so cfo drain would show no recovery episode despite the FAILED
	// banner telling the operator supervision is down.
	episode1, err := wake.ReadEpisode(state)
	if err != nil {
		t.Fatal(err)
	}
	if !episode1.Pending || episode1.Gen != 1 {
		t.Errorf("episode after first failure = %+v, want pending at generation 1", episode1)
	}

	var stdout2, stderr2 bytes.Buffer
	exit2 := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout2, &stderr2)
	if exit2 != 2 {
		t.Fatalf("second run exit = %d, want 2; stdout=%s", exit2, stdout2.String())
	}
	if stderr2.Len() != 0 {
		t.Errorf("second run stderr = %q, want empty", stderr2.String())
	}
	epoch2, err := supervise.ReadEpoch(state)
	if err != nil {
		t.Fatal(err)
	}
	if epoch2.Outcome != "failed-suppressed" {
		t.Errorf("second run epoch outcome = %q, want failed-suppressed", epoch2.Outcome)
	}
	// Each failing firing publishes its own episode exactly once: the
	// second firing is a genuinely new failure to hold the home, so the
	// generation advances again rather than staying pinned at 1.
	episode2, err := wake.ReadEpisode(state)
	if err != nil {
		t.Fatal(err)
	}
	if !episode2.Pending || episode2.Gen != 2 {
		t.Errorf("episode after second failure = %+v, want pending at generation 2", episode2)
	}
}

func TestAutoarmHealthyAfterSteal(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if err := os.WriteFile(filepath.Join(state, ".turnend-claude-blocks"), []byte("session=s1\ncount=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	foreign := startLiveForeignProcess(t)
	if _, err := lock.AcquireNamedOwner(state, ".watch.lock", foreign.Process.Pid, "watch"); err != nil {
		t.Fatal(err)
	}
	writeHeartbeatFixture(t, state, time.Now())

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	epoch, err := supervise.ReadEpoch(state)
	if err != nil {
		t.Fatal(err)
	}
	if epoch.Outcome != "clean" {
		t.Errorf("epoch outcome = %q, want clean", epoch.Outcome)
	}
	if _, err := os.Stat(filepath.Join(state, ".turnend-claude-blocks")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("budget file survives a HEALTHY outcome: stat err = %v, want ErrNotExist", err)
	}
	if supervise.NotifiedOnce(state) {
		t.Error("notified marker created on a HEALTHY outcome")
	}
}

func TestAutoarmYieldsToAlarm(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if err := supervise.MarkNotified(state); err != nil {
		t.Fatal(err)
	}
	if err := supervise.MarkAlarm(state); err != nil {
		t.Fatal(err)
	}

	foreign := startLiveForeignProcess(t)
	if _, err := lock.AcquireNamedOwner(state, ".watch.lock", foreign.Process.Pid, "watch"); err != nil {
		t.Fatal(err)
	}
	// No fresh beat: the attempt loop can only fail (ErrHeld, not healthy).

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

// TestAutoarmRepeatFailureWaitsForLateAlarm is Important 2's regression
// test: this hook's own repeat-failure decision (identity gate, session
// custody, need gate, single-flight, epoch write, one failing watch.Run
// attempt against a wedged foreign .watch.lock holder, then the outcome
// bookkeeping) settles in roughly 150-200ms end to end, measured directly
// against this exact fixture. The sibling turnend-guard's MarkAlarm call on
// the SAME Stop only lands after its own up-to-CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS
// poll for autoarm proof, which can be materially longer. A single immediate
// AlarmFired read races ahead of that write whenever the guard's poll runs
// past this hook's own ~200ms. The goroutine below stands in for the
// guard's concurrent, later MarkAlarm call at 400ms - comfortably past this
// hook's own measured decision latency, and comfortably inside the 700ms
// sync-wait budget below - so a correct implementation must still catch it.
func TestAutoarmRepeatFailureWaitsForLateAlarm(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	t.Setenv("CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS", "700")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if err := supervise.MarkNotified(state); err != nil {
		t.Fatal(err)
	}

	foreign := startLiveForeignProcess(t)
	if _, err := lock.AcquireNamedOwner(state, ".watch.lock", foreign.Process.Pid, "watch"); err != nil {
		t.Fatal(err)
	}
	// No fresh beat: the attempt loop can only fail (ErrHeld, not healthy).

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(400 * time.Millisecond)
		_ = supervise.MarkAlarm(state)
	}()
	defer func() { <-done }()

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (the poll should observe the late alarm mark rather than racing ahead of it); stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

// TestAutoarmPublishesEpisodeOnGenuineRunError exercises inherited
// requirement 6 (the arm side owns publishing the watcher-down episode)
// against a genuine internal watch.Run failure, distinct from the lock
// contention TestAutoarmFailureEpisode exercises: a directory in place of
// state\.watch.lock makes lock.AcquireNamedOwner's create-exclusive write
// fail on every retry inside package lock, exhausting to "lock: failed to
// acquire after 10 attempts" rather than ErrHeld.
//
// The fault injection is load-bearing but not self-verifying: it relies on
// Windows mapping O_CREATE|O_EXCL against an existing directory to
// something other than os.ErrExist. If that mapping ever changed, the
// lock package's unreadable-holder grace period would instead remove the
// empty directory and let a later attempt take the lock cleanly, closing
// on a heartbeat (still exit 2, still a published episode at generation 1
// under the post-fix unconditional publish) for an entirely different
// reason than this test claims to cover. The stderr assertion below
// catches that: only the FAILURE arm's banner says "FAILED after 1
// attempt(s)", while a heartbeat close would emit the rewake banner
// instead.
// --- session-start ---

func TestRunHookSessionStartNotPrimary(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	exit := runHook("session-start", strings.NewReader(`{"session_id":"s1","source":"startup"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookSessionStartFullComposeOnStartup(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("session-start", strings.NewReader(`{"session_id":"s1","source":"startup"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "== SESSION LOCK ==") {
		t.Errorf("stdout = %q, want it to contain == SESSION LOCK ==", stdout.String())
	}
}

// TestRunHookSessionStartDegradesWakeReadErrorInline is the end-to-end leg
// of Important 1's fix: a corrupt state\.wake-queue line must not turn the
// SessionStart hook's output into a truncated four-line digest. The hook
// must still exit 0 with the full seven-header digest and the inline
// degrade line, never the SESSION START DEGRADED text (that text is
// reserved for a genuine Compose-level failure, which a scoped read error
// is not).
func TestRunHookSessionStartDegradesWakeReadErrorInline(t *testing.T) {
	dir := newPrimaryHome(t)
	state := filepath.Join(dir, "state")
	if err := os.WriteFile(filepath.Join(state, ".wake-queue"), []byte("not valid json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := runHook("session-start", strings.NewReader(`{"session_id":"s1","source":"startup"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	out := stdout.String()
	for _, header := range []string{
		"== SESSION LOCK ==", "== WAKE QUEUE ==", "== SUPERVISION OPERATING INSTRUCTIONS ==",
		"== READ-ONCE CONTRACT ==", "== FLEET STATE ==", "== CONTEXT ==", "== NEXT STEP ==",
	} {
		if !strings.Contains(out, header) {
			t.Errorf("stdout missing header %q:\n%s", header, out)
		}
	}
	if !strings.Contains(out, "WAKE QUEUE: UNREADABLE") {
		t.Errorf("stdout missing the inline WAKE QUEUE degrade line:\n%s", out)
	}
	if strings.Contains(out, "SESSION START DEGRADED") {
		t.Errorf("stdout unexpectedly contains SESSION START DEGRADED for a section-scoped read error:\n%s", out)
	}
}

func TestRunHookSessionStartResumeNoMarkerFallsThrough(t *testing.T) {
	newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	var stdout, stderr bytes.Buffer
	exit := runHook("session-start", strings.NewReader(`{"session_id":"s1","source":"resume"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "== SESSION LOCK ==") {
		t.Errorf("stdout = %q, want the fall-through full compose (== SESSION LOCK ==)", stdout.String())
	}
}

const sessionStartNudge = "CFO: operational input may be waiting; run cfo drain if supervision was active.\n"

// TestRunHookSessionStartRouting pins the owner pid via CFO_TEST_ANCESTOR_PID
// to a live foreign process (the ping-child pattern; the ambient
// proc.FindAncestor walk cannot be asserted from inside this repo's own test
// suite, same reasoning as setAncestorPID's other callers), pre-writes the
// session lock and completion marker to match it, then exercises every
// SessionStart source against that one fixed custody window.
func TestRunHookSessionStartRouting(t *testing.T) {
	dir := newPrimaryHome(t)
	state := filepath.Join(dir, "state")

	foreign := startLiveForeignProcess(t)
	ownerPID := foreign.Process.Pid
	setAncestorPID(t, ownerPID)

	if _, err := lock.AcquireOwner(state, ownerPID, "s0"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, ".session-start-complete"), []byte(strconv.Itoa(ownerPID)), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, source := range []string{"resume", "reload", "fork"} {
		t.Run(source+" with marker prints the nudge", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := runHook("session-start", strings.NewReader(`{"session_id":"s1","source":"`+source+`"}`), &stdout, &stderr)
			if exit != 0 {
				t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
			}
			if stdout.String() != sessionStartNudge {
				t.Errorf("stdout = %q, want exactly %q", stdout.String(), sessionStartNudge)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}

	t.Run("unrecognized source runs full compose", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		exit := runHook("session-start", strings.NewReader(`{"session_id":"s1","source":"banana"}`), &stdout, &stderr)
		if exit != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
		}
		if !strings.Contains(stdout.String(), "== SESSION LOCK ==") {
			t.Errorf("stdout = %q, want the full compose (== SESSION LOCK ==)", stdout.String())
		}
	})
}

func TestAutoarmPublishesEpisodeOnGenuineRunError(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	if err := os.MkdirAll(filepath.Join(state, ".watch.lock"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := runHook("stop-autoarm", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "FAILED after 1 attempt(s)") {
		t.Errorf("stderr = %q, want it to contain FAILED after 1 attempt(s) (proves the fault injection landed on the FAILURE arm, not a heartbeat close)", stderr.String())
	}
	episode, err := wake.ReadEpisode(state)
	if err != nil {
		t.Fatal(err)
	}
	if !episode.Pending || episode.Gen != 1 {
		t.Errorf("episode = %+v, want pending at generation 1", episode)
	}
}
