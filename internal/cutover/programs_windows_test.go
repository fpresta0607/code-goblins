//go:build windows

package cutover

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SLEEP_VARIABLE makes this test binary wait instead of testing, so a copy of
// it can stand in for a program that runs from a home's bin.
const SLEEP_VARIABLE = "CUTOVER_TEST_SLEEP"

func TestMain(m *testing.M) {
	if os.Getenv(SLEEP_VARIABLE) == "1" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestTheProgramReaderFindsAProgramRunningFromTheHomesBin(t *testing.T) {
	// Arrange
	root := filepath.Join(t.TempDir(), "home")
	image := filepath.Join(root, "bin", "goblins-window.exe")
	if err := os.MkdirAll(filepath.Dir(image), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, testBinary(t), image)
	program := exec.Command(image)
	program.Env = append(os.Environ(), SLEEP_VARIABLE+"=1")
	if err := program.Start(); err != nil {
		t.Fatal(err)
	}
	isEnded := false
	end := func() {
		if !isEnded {
			isEnded = true
			_ = program.Process.Kill()
			_ = program.Wait()
		}
	}
	t.Cleanup(end)
	machine := Machine{Root: root, Programs: RunningPrograms{}}

	// Act
	running, err := machine.RunningFromBin()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0].PID != program.Process.Pid || !strings.EqualFold(running[0].Image, image) {
		t.Fatalf("running from bin: %+v; want only pid %d at %s", running, program.Process.Pid, image)
	}
	end()
	if running, err := machine.RunningFromBin(); err != nil || len(running) != 0 {
		t.Fatalf("after the program ended: %+v, %v; want nothing", running, err)
	}
}

func testBinary(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	source, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}
