//go:build linux || darwin

package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The shared machine config is readable by every account running a pipeline
// on the machine, so applying owned fields must not carry the staged file's
// owner-only mode onto it.
func TestApplyKeepsLiveConfigModeAndDropsDrift(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	before := "agent: [pi]\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{Path: path, Policy: testPolicy(t), Idle: func(context.Context) (func() error, error) {
		return func() error { return nil }, nil
	}}

	// Act
	result, err := c.Apply(context.Background())

	// Assert
	if err != nil || result.Backup == "" {
		t.Fatalf("apply: %+v %v", result, err)
	}
	now, err := os.ReadFile(path)
	if err != nil || string(now) == before {
		t.Fatalf("apply left the configuration %q, %v", now, err)
	}
	if _, drift, err := Render(now, testPolicy(t)); err != nil || len(drift) != 0 {
		t.Fatalf("applied drift: %v %v", drift, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the applied config = %v, %v; want the live mode 0644 kept", info.Mode(), err)
	}
	// Without this the check above would pass for any implementation, because
	// the restriction the live file must not take would not exist.
	if info, err := os.Stat(result.Backup); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the backup = %v, %v; want it kept to its owner", info.Mode(), err)
	}
	staged, err := filepath.Glob(filepath.Join(dir, ".pipeline-config-*"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("the apply left its staged copy: %v, %v", staged, err)
	}
}
