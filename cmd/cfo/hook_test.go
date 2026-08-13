package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervise"
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

// setSyncWait sets the default sync-wait window every turnend-guard case
// uses unless it says otherwise: short enough to keep the suite fast, long
// enough for the 100ms poll to observe a late proof.
func setSyncWait(t *testing.T, ms string) {
	t.Helper()
	t.Setenv("CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS", ms)
}

func TestRunHookTurnendGuardNotPrimary(t *testing.T) {
	dir := t.TempDir()
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
	if err := os.WriteFile(filepath.Join(state, ".last-watcher-beat"), nil, 0o644); err != nil {
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
	beatPath := filepath.Join(state, ".last-watcher-beat")
	if err := os.WriteFile(beatPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(beatPath, aged, aged); err != nil {
		t.Fatal(err)
	}
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
	if exit != 2 {
		t.Fatalf("exit = %d, want 2 (a zero sync window must not wait for the late proof)", exit)
	}
}

// TestRunHookTurnendGuardChargeBudgetErrorEscalates exercises the failure
// posture the brief prescribes for a genuine ChargeBudget error (distinct
// from the icacls unlistable-Needed case above, which is caught earlier at
// step 2 and never reaches ChargeBudget at all): the ladder cannot run, so
// the guard skips straight to step 5's attended fail-open instead of
// blocking every Stop forever on a counter that can never advance. Making
// the budget file's own path a directory forces os.ReadFile to fail with a
// genuine I/O error rather than ErrNotExist.
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
	if !supervise.AlarmFired(state) {
		t.Error("attended fail-open must best-effort MarkAlarm")
	}
}
