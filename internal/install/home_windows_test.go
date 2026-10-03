//go:build windows

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
)

// An install that updates a home while its old build still runs, such as the
// supervisor the one-line install opens the board with, moves that copy
// aside instead of failing to overwrite it, says the old build still runs,
// and removes the copy on a later install once nothing runs it.
func TestInstallReplacesAHomeBuildThatIsStillRunning(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	f.install()
	// A running goblins.exe: ping, copied under that name, runs long enough
	// and needs no console.
	ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Fatal(err)
	}
	running := filepath.Join(f.root, "goblins.exe")
	if err := os.WriteFile(running, ping, 0o755); err != nil {
		t.Fatal(err)
	}
	old := exec.Command(running, "-n", "120", "127.0.0.1")
	old.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = old.Wait()
		close(exited)
	}()
	stop := func() {
		_ = old.Process.Kill()
		<-exited
	}
	t.Cleanup(stop)
	writeFile(t, f.service.Binary, "build 2")

	output := f.install()

	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		if got := readFile(t, filepath.Join(f.root, name)); got != "build 2" {
			t.Errorf("%s = %q, want the new build", name, got)
		}
	}
	aside, err := filepath.Glob(filepath.Join(f.root, "*.old"))
	if err != nil {
		t.Fatal(err)
	}
	if len(aside) != 1 || !strings.HasPrefix(filepath.Base(aside[0]), "goblins.exe.") || readFile(t, aside[0]) != string(ping) {
		t.Fatalf("copies moved aside = %v, want only the goblins.exe still running", aside)
	}
	if !strings.Contains(output, "the previous build still runs") {
		t.Errorf("the install does not say the old build still runs:\n%s", output)
	}

	stop()
	output = f.install()

	if left, _ := filepath.Glob(filepath.Join(f.root, "*.old")); len(left) != 0 {
		t.Errorf("copies left once nothing runs them: %v", left)
	}
	if strings.Contains(output, "the previous build still runs") {
		t.Errorf("the install says an old build still runs once none does:\n%s", output)
	}
}

// An install that brings a new desktop window while the old one is open moves
// the open copy aside as it does a running binary, says the previous window
// still runs, and removes the copy on a later install once it is closed.
func TestInstallReplacesADesktopWindowThatIsStillOpen(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	beside := filepath.Join(filepath.Dir(f.service.Binary), "goblins-window.exe")
	writeFile(t, beside, "window 1")
	f.install()
	// An open goblins-window.exe: ping, copied under that name.
	ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(f.root, "goblins-window.exe")
	if err := os.WriteFile(open, ping, 0o755); err != nil {
		t.Fatal(err)
	}
	old := exec.Command(open, "-n", "120", "127.0.0.1")
	old.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = old.Wait()
		close(exited)
	}()
	stop := func() {
		_ = old.Process.Kill()
		<-exited
	}
	t.Cleanup(stop)
	writeFile(t, beside, "window 2")

	output := f.install()

	if got := readFile(t, open); got != "window 2" {
		t.Errorf("goblins-window.exe = %q, want the new window", got)
	}
	aside, err := filepath.Glob(filepath.Join(f.root, "*.old"))
	if err != nil {
		t.Fatal(err)
	}
	if len(aside) != 1 || !strings.HasPrefix(filepath.Base(aside[0]), "goblins-window.exe.") || readFile(t, aside[0]) != string(ping) {
		t.Fatalf("copies moved aside = %v, want only the open goblins-window.exe", aside)
	}
	if !strings.Contains(output, "the previous window still runs") {
		t.Errorf("the install does not say the previous window still runs:\n%s", output)
	}

	stop()
	f.install()

	if left, _ := filepath.Glob(filepath.Join(f.root, "*.old")); len(left) != 0 {
		t.Errorf("copies left once the window is closed: %v", left)
	}
}

// An earlier copy of the desktop window that is open cannot be removed, so
// the install names it and leaves it, and a later install removes it once it
// is closed.
func TestInstallLeavesAnEarlierWindowThatIsStillOpen(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	earlier := earlierWindow(t, f)
	// An open goblins-window.exe: ping, copied under that name.
	ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(earlier, "goblins-window.exe")
	if err := os.WriteFile(open, ping, 0o755); err != nil {
		t.Fatal(err)
	}
	old := exec.Command(open, "-n", "120", "127.0.0.1")
	old.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = old.Wait()
		close(exited)
	}()
	stop := func() {
		_ = old.Process.Kill()
		<-exited
	}
	t.Cleanup(stop)

	// Act
	output := f.install()

	// Assert
	if got := readFile(t, open); got != string(ping) {
		t.Errorf("the open window's program was changed")
	}
	if got := readFile(t, filepath.Join(earlier, "goblins-window.png")); got != "picture" {
		t.Errorf("the open window's picture = %q, want it as it was", got)
	}
	if !strings.Contains(output, open+" is still open") || !strings.Contains(output, "quit it from its tray icon") {
		t.Errorf("the install does not say the earlier window is still open and how to end it:\n%s", output)
	}

	stop()
	f.install()

	if _, err := os.Stat(earlier); !os.IsNotExist(err) {
		t.Errorf("the earlier copy's folder is still there once the window is closed: %v", err)
	}
}
