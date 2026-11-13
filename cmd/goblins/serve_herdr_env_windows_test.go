package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
)

func reportEnvironment(report string) int {
	if err := os.WriteFile(report, []byte(strings.Join(os.Environ(), "\n")), 0o600); err != nil {
		return 1
	}
	return 0
}

// environmentReportVariable makes this test binary, started as a stand-in
// herdr, write the environment it was started with to the file it names and
// exit.
const environmentReportVariable = "GOBLINS_TEST_ENVIRONMENT_REPORT"

// standInHerdr puts this test binary on an otherwise empty PATH as herdr,
// reporting its environment to the returned file.
func standInHerdr(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr.exe"), data, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	report := filepath.Join(t.TempDir(), "environment.txt")
	t.Setenv(environmentReportVariable, report)
	return report
}

// waitForReport returns the environment the stand-in herdr reported.
func waitForReport(t *testing.T, report string) string {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(report); err == nil && len(data) > 0 {
			return string(data)
		}
	}
	t.Fatal("the stand-in herdr never reported its environment")
	return ""
}

// goblins serve does not care where it was started: started from a Herdr pane it
// forgets that pane before anything else, so a terminal the board opens
// carries none of the pane's variables and herdr starts, while HERDR_SESSION,
// the fleet's session, and HERDR_CONFIG_PATH, the user's Herdr configuration,
// stay.
func TestAServeStartedInAHerdrPaneOpensTerminalsWithoutThePane(t *testing.T) {
	// Arrange
	for name, value := range paneVariables {
		t.Setenv(name, value)
	}
	t.Setenv("HERDR_SESSION", "fleet")
	t.Setenv("HERDR_CONFIG_PATH", `C:\herdr\herdr.toml`)
	report := standInHerdr(t)

	// Act: serve starts as from the CEO's pane and stops at an address it
	// refuses, then the board opens a terminal.
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"serve", "--listen", "example.invalid:1"}, &stdout, &stderr); exit != 2 {
		t.Fatalf("serve exit = %d (stderr %q), want the address refused", exit, stderr.String())
	}
	stream, err := herdr.OpenTerminal(context.Background(), "fleet", "terminal-1", false, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	environment := waitForReport(t, report)

	// Assert
	for _, entry := range strings.Split(environment, "\n") {
		name, _, _ := strings.Cut(entry, "=")
		if _, pane := paneVariables[strings.ToUpper(name)]; pane {
			t.Errorf("the terminal's herdr started with %s", entry)
		}
	}
	if !strings.Contains(environment, "HERDR_SESSION=fleet") {
		t.Errorf("the terminal's herdr lost HERDR_SESSION; environment:\n%s", environment)
	}
	if !strings.Contains(environment, `HERDR_CONFIG_PATH=C:\herdr\herdr.toml`) {
		t.Errorf("the terminal's herdr lost HERDR_CONFIG_PATH; environment:\n%s", environment)
	}
}

// goblins serve started from the CEO's native terminal forgets that terminal's id
// and proof value, so no terminal the board opens, nor any process in it,
// carries the proof that a process runs in the CEO's terminal.
func TestAServeStartedInANativeTerminalOpensTerminalsWithoutItsProof(t *testing.T) {
	// Arrange
	t.Setenv(host.IDVariable, "ceo")
	t.Setenv(host.ProofVariable, "terminal-proof")
	t.Setenv("HERDR_SESSION", "fleet")
	report := standInHerdr(t)

	// Act: serve starts as from the CEO's terminal and stops at an address it
	// refuses, then the board opens a terminal.
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"serve", "--listen", "example.invalid:1"}, &stdout, &stderr); exit != 2 {
		t.Fatalf("serve exit = %d (stderr %q), want the address refused", exit, stderr.String())
	}
	stream, err := herdr.OpenTerminal(context.Background(), "fleet", "terminal-1", false, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	environment := waitForReport(t, report)

	// Assert
	if !strings.Contains(environment, "HERDR_SESSION=fleet") {
		t.Fatalf("the report is not the terminal's environment:\n%s", environment)
	}
	for _, entry := range strings.Split(environment, "\n") {
		if name, _, _ := strings.Cut(entry, "="); strings.EqualFold(name, host.IDVariable) || strings.EqualFold(name, host.ProofVariable) {
			t.Errorf("the terminal's herdr started with %s", entry)
		}
	}
}
