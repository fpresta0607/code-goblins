package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// programIsThisProcess makes record name this test process as its terminal's
// program, by pid and creation time, as a host names the harness it started.
func programIsThisProcess(t *testing.T, record *host.Record) {
	t.Helper()
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("this process has no start time")
	}
	record.ChildPID, record.ChildStart = os.Getpid(), started
}

func TestRunHookTurnendGuardUnlistableStateIsSilent(t *testing.T) {
	dir := newCEOHome(t)
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
