package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// standInCLIVariable names the file a stand-in CLI writes its pid to. Set, it
// makes TestProbeStandInCLI the CLI an npm shim starts.
const standInCLIVariable = "AUTH_PROBE_STAND_IN_PID"

// TestProbeStandInCLI is not a test: it is the CLI behind the npm shim
// TestATimedOutProbeEndsTheCLIBehindItsShim probes. Like vercel whoami on a
// bad day, it prints its answer and then does not exit.
func TestProbeStandInCLI(t *testing.T) {
	pidFile := os.Getenv(standInCLIVariable)
	if pidFile == "" {
		return
	}
	fmt.Println("stand-in-user")
	if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(1)
	}
	if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
		os.Exit(1)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

// A CLI installed with npm runs behind a .cmd shim, so a probe of it starts
// cmd.exe, which starts the CLI. A probe that runs out of time must end the
// CLI as well as the shim, and must say what the CLI printed before it ran
// out, since a CLI that answered and then hung is a different fault from one
// that never answered.
func TestATimedOutProbeEndsTheCLIBehindItsShim(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	shim := filepath.Join(dir, "stand-in-cli.cmd")
	script := fmt.Sprintf("@ECHO off\r\n\"%s\" -test.run=^TestProbeStandInCLI$ %%*\r\n", os.Args[0])
	if err := os.WriteFile(shim, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "cli.pid")
	t.Setenv(standInCLIVariable, pidFile)
	checker := Checker{Store: newMemoryStore(nil), Runner: execx.OSRunner{}, Timeout: 3 * time.Second}
	manifest := Manifest{Services: []Service{{Name: "stand-in", Method: MethodCLI, Probe: []string{shim, "whoami"}}}}

	// Act
	type outcome struct {
		report Report
		err    error
	}
	checked := make(chan outcome, 1)
	go func() {
		report, err := checker.Check(context.Background(), manifest)
		checked <- outcome{report, err}
	}()
	cli := openStandInCLI(t, pidFile)
	result := <-checked

	// Assert
	if result.err != nil {
		t.Fatal(result.err)
	}
	status := result.report.Statuses[0]
	if status.State != StateUnreachable {
		t.Fatalf("state = %s (%s), want %s", status.State, status.Detail, StateUnreachable)
	}
	if !strings.Contains(status.Detail, "stand-in-user") {
		t.Errorf("detail = %q, want what the CLI printed before the probe ran out of time", status.Detail)
	}
	if event, err := windows.WaitForSingleObject(cli, 5000); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Errorf("the CLI behind the shim still runs after its probe ran out of time (wait = %#x, %v)", event, err)
	}
}

// openStandInCLI waits for the stand-in CLI to report its pid and holds a
// handle to it, so the process is judged by that handle and not by a pid
// Windows may have given another process since. The CLI is ended when the
// test ends, whatever the probe left running.
func openStandInCLI(t *testing.T, pidFile string) windows.Handle {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			t.Fatalf("the stand-in CLI reported pid %q", data)
		}
		cli, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
		if err != nil {
			t.Fatalf("open the stand-in CLI: %v", err)
		}
		t.Cleanup(func() {
			_ = windows.TerminateProcess(cli, 1)
			_, _ = windows.WaitForSingleObject(cli, 5000)
			_ = windows.CloseHandle(cli)
		})
		return cli
	}
	t.Fatal("the stand-in CLI never started")
	return 0
}
