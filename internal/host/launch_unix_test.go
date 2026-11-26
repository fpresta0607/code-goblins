//go:build linux || darwin

package host

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/localsocket"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// hostRole tells this test binary, started again, what to be: a host Launch
// started, or a launcher that launches one and exits.
const hostRole = "HOST_TEST_ROLE"

// launcherScript is what the terminal of the host a launcher launches runs.
const launcherScript = `read line; echo "got $line"; sleep 600`

func TestMain(m *testing.M) {
	switch os.Getenv(hostRole) {
	case "host":
		if err := RunArgs(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "launch":
		record, err := Launch(os.Args[1], []string{os.Args[0]}, hostEnvironment(), Spec{ID: hostID, Args: []string{"sh", "-c", launcherScript}, Cols: 80, Rows: 24})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(record.HostPID)
	default:
		os.Exit(m.Run())
	}
}

// hostEnvironment is this process's environment, with this test binary
// started again as a host.
func hostEnvironment() []string {
	env := []string{hostRole + "=host"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, hostRole+"=") {
			env = append(env, entry)
		}
	}
	return env
}

// launchHost launches a host running script under sh with env, under a fresh
// home, and closes it when the test ends.
func launchHost(t *testing.T, env []string, script string) (string, Record) {
	t.Helper()
	stateDir := t.TempDir()
	t.Cleanup(func() { _ = os.Remove(localsocket.Folder(stateDir)) })
	record, err := Launch(stateDir, []string{os.Args[0]}, env, Spec{ID: hostID, Args: []string{"sh", "-c", script}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	closeWhenDone(t, stateDir, record)
	return stateDir, record
}

// closeWhenDone closes the host record names when the test ends, and ends it
// if it does not close.
func closeWhenDone(t *testing.T, stateDir string, record Record) {
	t.Cleanup(func() {
		if err := Close(stateDir, record, 10*time.Second); err != nil {
			t.Errorf("Close: %v", err)
			_ = unix.Kill(record.HostPID, unix.SIGKILL)
		}
	})
}

func TestALaunchedUnixHostServesItsTerminalOutsideItsLaunchersSessionAndTree(t *testing.T) {
	// Arrange
	env := append(hostEnvironment(), execx.TreeVariable+"=launchers-tree")

	// Act
	_, record := launchHost(t, env, `echo "tree [$GOBLINS_TREE]"; read line; echo "got $line"; sleep 600`)

	// Assert
	if record.HostPID == os.Getpid() {
		t.Fatal("the host runs in the launching process")
	}
	if instance, err := proc.Instance(record.HostPID); err != nil || instance != record.HostInstance {
		t.Fatalf("the record names the host as %q, and the host reads as %q, %v", record.HostInstance, instance, err)
	}
	if !Running(record) {
		t.Fatal("Running reported the launched host as ended")
	}
	if session, err := unix.Getsid(record.HostPID); err != nil || session != record.HostPID {
		t.Fatalf("the host is in session %d (%v), not one of its own", session, err)
	}
	viewer := watch(t, record)
	if tree := viewer.await(t, `tree \[([^\]]*)\]`)[1]; strings.Contains(tree, "launchers-tree") {
		t.Fatalf("the terminal carries its launcher's tree mark: %q", tree)
	}
	if err := viewer.client.Input([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	viewer.await(t, `got hello`)
}

func TestAUnixHostOutlivesItsLauncher(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	t.Cleanup(func() { _ = os.Remove(localsocket.Folder(stateDir)) })
	launcher := exec.Command(os.Args[0], stateDir)
	launcher.Env = append(os.Environ(), hostRole+"=launch")

	// Act
	output, err := launcher.Output()

	// Assert
	if err != nil {
		t.Fatalf("the launcher failed: %v", err)
	}
	hostPID, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("the launcher printed %q, want the host's pid", output)
	}
	record, err := ReadRecord(stateDir, hostID)
	if err != nil || record.HostPID != hostPID {
		t.Fatalf("ReadRecord = %+v, %v; want the launched host, pid %d", record, err, hostPID)
	}
	closeWhenDone(t, stateDir, record)
	if !Running(record) {
		t.Fatal("the host ended with its launcher")
	}
	viewer := watch(t, record)
	if err := viewer.client.Input([]byte("still here\n")); err != nil {
		t.Fatal(err)
	}
	viewer.await(t, `got still here`)
}

func TestCloseEndsALaunchedUnixHostAndItsTerminal(t *testing.T) {
	// Arrange
	stateDir, record := launchHost(t, hostEnvironment(), `sleep 600`)

	// Act
	err := Close(stateDir, record, 10*time.Second)

	// Assert
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if Running(record) {
		t.Fatal("Running reported the closed host as running")
	}
	if _, err := ReadRecord(stateDir, hostID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the closed host's record is still there: %v", err)
	}
	if end, err := ReadEnd(stateDir, hostID); err != nil || !end.Closed || end.HostPID != record.HostPID {
		t.Fatalf("the host recorded its end as %+v, %v; want closed by host pid %d", end, err, record.HostPID)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := proc.Instance(record.ChildPID); errors.Is(err, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal's program, pid %d, still runs 30 seconds after its host closed", record.ChildPID)
		}
	}
}

func TestALaunchOfAUnixHostWhoseTerminalCannotStartFailsWithItsLog(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	missing := filepath.Join(stateDir, "no-such-folder")

	// Act
	_, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), Spec{ID: hostID, Args: []string{"sh", "-c", "exit 0"}, Dir: missing, Cols: 80, Rows: 24})

	// Assert
	if err == nil {
		t.Fatal("Launch reported a host whose terminal could not start")
	}
	if !strings.Contains(err.Error(), "the host ended before it served") || !strings.Contains(err.Error(), "pty: start sh: ") {
		t.Fatalf("Launch failed with %v, not the host's end and its log's failed start of the terminal", err)
	}
}

func TestRunningReadsAUnixHostByItsInstance(t *testing.T) {
	// Arrange
	instance, err := proc.Instance(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	ended := exec.Command("true")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		record    Record
		isRunning bool
	}{
		{name: "the process the record names", record: Record{HostPID: os.Getpid(), HostInstance: instance}, isRunning: true},
		{name: "another run under the record's pid", record: Record{HostPID: os.Getpid(), HostInstance: instance + "0"}, isRunning: false},
		{name: "a record that names no instance", record: Record{HostPID: os.Getpid()}, isRunning: false},
		{name: "a process that has ended", record: Record{HostPID: ended.Process.Pid, HostInstance: instance}, isRunning: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			isRunning := Running(test.record)

			// Assert
			if isRunning != test.isRunning {
				t.Fatalf("Running = %t, want %t", isRunning, test.isRunning)
			}
		})
	}
}
