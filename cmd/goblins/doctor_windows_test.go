package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/standin"
)

// A harness behind its newest version waits while anything runs from its
// install, and nothing is staged or installed meanwhile.
func TestRunDoctorFixWaitsWhileSomethingRunsFromTheInstall(t *testing.T) {
	// Arrange
	bin := doctorBin(t)
	harnessReleaseServer(t, "1.0.0", "1.2.0", "1.0.0")
	root := filepath.Join(t.TempDir(), "npm", "node_modules")
	log := filepath.Join(t.TempDir(), "npm.log")
	npm := "@echo off\r\necho %* >> \"" + log + "\"\r\nif \"%1\"==\"root\" echo " + root + "\r\nexit /b 0\r\n"
	if err := os.WriteFile(filepath.Join(bin, "npm.cmd"), []byte(npm), 0o700); err != nil {
		t.Fatal(err)
	}
	vendor := filepath.Join(root, "@openai", "codex", "vendor")
	if err := os.MkdirAll(vendor, 0o700); err != nil {
		t.Fatal(err)
	}
	standin.RemoveAtCleanup(t, vendor)
	ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(vendor, "codex.exe")
	if err := os.WriteFile(program, ping, 0o700); err != nil {
		t.Fatal(err)
	}
	running := exec.Command(program, "-n", "120", "127.0.0.1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	standin.Hold(t, running.Process.Pid)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"doctor", "--fix"}, &stdout, &stderr)

	// Assert
	want := fmt.Sprintf("fix      codex      waits: 1 processes run from %s (pid %d)", filepath.Join(root, "@openai", "codex"), running.Process.Pid)
	if exit != 0 || !strings.Contains(stdout.String(), want) {
		t.Fatalf("exit = %d, want 0 and %q\n%s", exit, want, stdout.String())
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "install") {
		t.Fatalf("npm installed while codex ran from its install: %s", calls)
	}
}
