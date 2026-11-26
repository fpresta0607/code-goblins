package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestTheDefaultRuntimeReadsAReceiptsFileHashAndDaemonProcessImage(t *testing.T) {
	// Arrange
	program := filepath.Join(t.TempDir(), "tool.exe")
	if err := os.WriteFile(program, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	prior := identifyProcess
	identifyProcess = func(pid int, at time.Time) (proc.Identity, error) {
		if pid != 7 || !at.Equal(started) {
			t.Errorf("identified pid %d created at %s", pid, at)
		}
		return proc.Identity{Image: "daemon-image", Arguments: []string{"daemon-argument"}, Directory: "daemon-directory"}, nil
	}
	t.Cleanup(func() { identifyProcess = prior })
	installation := defaultCommandRuntime().installation

	// Act
	digest, hashErr := installation.Hash(program)
	image, imageErr := installation.DaemonImage(7, started)

	// Assert: the digest is FIPS 180-2's published SHA-256 of "abc".
	if hashErr != nil || digest != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("Hash = %q, %v", digest, hashErr)
	}
	if imageErr != nil || image != "daemon-image" {
		t.Errorf("DaemonImage = %q, %v; want the process image", image, imageErr)
	}
}
