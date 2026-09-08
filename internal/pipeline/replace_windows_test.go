package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// acl reports a file's DACL as icacls prints it, with the leading path removed
// so two files' entries compare directly.
func acl(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %s: %s %v", path, out, err)
	}
	return strings.TrimSpace(strings.ReplaceAll(string(out), path, ""))
}

// grantRead gives a non-owner principal read access, so the fixture states the
// permissions the replacement must keep instead of depending on whatever the
// temp directory hands out: a hosted runner's temp tree has no inheritable
// entries, and a new file there gets only the creating token's default DACL.
// BUILTIN\Users is spelled by SID so the grant works on any machine language.
func grantRead(t *testing.T, path string) {
	t.Helper()
	out, err := exec.Command("icacls", path, "/grant", "*S-1-5-32-545:(R)").CombinedOutput()
	if err != nil {
		t.Fatalf("icacls /grant %s: %s %v", path, out, err)
	}
}

// The shared machine config is readable by every principal running a pipeline
// on the box, so applying owned fields must not carry the staged file's
// owner-only restriction onto it.
func TestApplyKeepsLiveConfigPermissionsAndDropsDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	before := "agent: [pi]\n"
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	created := acl(t, path)
	grantRead(t, path)
	live := acl(t, path)
	if live == created {
		t.Fatalf("fixture gained no shared entry to lose: %s", live)
	}
	c := Config{Path: path, Policy: testPolicy(t), Idle: func(context.Context) (func() error, error) {
		return func() error { return nil }, nil
	}}
	result, err := c.Apply(context.Background())
	if err != nil || result.Backup == "" {
		t.Fatalf("apply: %+v %v", result, err)
	}
	now, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(now) == before {
		t.Fatal("apply left the configuration unchanged")
	}
	_, drift, err := Render(now, testPolicy(t))
	if err != nil || len(drift) != 0 {
		t.Fatalf("applied drift: %v %v", drift, err)
	}
	if applied := acl(t, path); applied != live {
		t.Fatalf("replacement changed live permissions:\nbefore %s\nafter %s", live, applied)
	}
	// Without this the check above would pass for any implementation, because
	// the restriction the live file must not inherit would not exist.
	if restricted := acl(t, result.Backup); restricted == live {
		t.Fatalf("backup was never owner-restricted: %s", restricted)
	}
}
