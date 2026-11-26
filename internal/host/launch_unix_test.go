//go:build linux || darwin

package host

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
// started, or a launcher that launches one and exits, or one that launches
// one and keeps running.
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
	case "launch", "launch and stay":
		record, err := Launch(os.Args[1], []string{os.Args[0]}, hostEnvironment(), Spec{ID: hostID, Args: []string{"sh", "-c", launcherScript}, Cols: 80, Rows: 24})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(record.HostPID)
		if os.Getenv(hostRole) == "launch and stay" {
			time.Sleep(10 * time.Minute)
		}
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

// A host whose launcher runs in a tree leaves that tree as it starts, as a
// launch from a goblin's terminal does: ending the tree while the launcher
// still runs ends the launcher and spares the host and its terminal.
func TestEndingARunningLaunchersTreeSparesTheUnixHostItLaunched(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	t.Cleanup(func() { _ = os.Remove(localsocket.Folder(stateDir)) })
	launcher := execx.Command(os.Args[0], stateDir)
	launcher.Env = append(os.Environ(), hostRole+"=launch and stay")
	output, err := launcher.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := execx.StartTree(launcher)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End(); _ = launcher.Wait() })
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		t.Fatalf("the launcher printed %q, not the host's pid: %v", line, err)
	}
	hostPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("the launcher printed %q, want the host's pid", line)
	}
	record, err := ReadRecord(stateDir, hostID)
	if err != nil || record.HostPID != hostPID {
		t.Fatalf("ReadRecord = %+v, %v; want the launched host, pid %d", record, err, hostPID)
	}
	closeWhenDone(t, stateDir, record)
	if _, err := proc.Lookup(launcher.Process.Pid); err != nil {
		t.Fatalf("the premise failed: the launcher no longer runs: %v", err)
	}

	// Act
	ended, err := tree.End()

	// Assert
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	isEnded := func(pid int) bool {
		return slices.ContainsFunc(ended, func(process execx.EndedProcess) bool { return process.PID == pid })
	}
	if !isEnded(launcher.Process.Pid) {
		t.Fatalf("the tree's end ended %v, without the running launcher %d", ended, launcher.Process.Pid)
	}
	if isEnded(record.HostPID) || isEnded(record.ChildPID) {
		t.Fatalf("the tree's end ended %v, the host %d or its terminal %d among them", ended, record.HostPID, record.ChildPID)
	}
	_ = launcher.Wait()
	if !Running(record) {
		t.Fatal("the host ended with its launcher's tree")
	}
	viewer := watch(t, record)
	if err := viewer.client.Input([]byte("still here\n")); err != nil {
		t.Fatal(err)
	}
	viewer.await(t, `got still here`)
	if err := Close(stateDir, record, 10*time.Second); err != nil {
		t.Fatalf("Close: %v", err)
	}
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

// A host told /exit closes its end of the socket as it ends, so a Close that
// reaches it in that moment has its request refused. The host ending is the
// close Close asked for: it reports nothing wrong and the record goes.
func TestCloseTakesAUnixHostEndingAsItsRequestIsRefusedAsEnded(t *testing.T) {
	// Arrange
	stateDir, record := launchHost(t, hostEnvironment(), `sleep 600`)
	var refused error
	setCloseTerminal(t, func(client *Client) error {
		if err := client.CloseTerminal(); err != nil {
			return err
		}
		for deadline := time.Now().Add(time.Minute); Running(record) && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		refused = client.CloseTerminal()
		return refused
	})

	// Act
	err := Close(stateDir, record, 10*time.Second)

	// Assert
	if !pipeClosing(refused) {
		t.Fatalf("the premise failed: a request to the ended host was refused with %v, not a closing socket", refused)
	}
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if Running(record) {
		t.Error("the host still runs after its terminal closed")
	}
	if _, err := ReadRecord(stateDir, hostID); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the record is still there: %v", err)
	}
}

// A refused request proves nothing by itself: a host whose socket refused it
// and that keeps running is still reported, once the wait for its end runs
// out, even with its record gone, since a host removes its record while it
// winds down and only its end proves the close; any other failure is
// reported at once.
func TestCloseReportsARefusedRequestWhoseUnixHostKeepsRunning(t *testing.T) {
	closing := &net.OpError{Op: "write", Net: "unix", Err: os.NewSyscallError("write", unix.EPIPE)}
	for _, test := range []struct {
		name            string
		refusal         error
		isRecordRemoved bool
		isWait          bool
	}{
		{"a closing socket", closing, false, true},
		{"a closing socket with the record gone", closing, true, true},
		{"any other failure", errors.New("host: the close request was not understood"), false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir, record := launchHost(t, hostEnvironment(), `sleep 600`)
			previousWait := closeEndWait
			closeEndWait = time.Minute
			if test.isWait {
				closeEndWait = time.Second
			}
			t.Cleanup(func() { closeEndWait = previousWait })
			setCloseTerminal(t, func(*Client) error {
				if test.isRecordRemoved {
					if err := os.Remove(recordPath(stateDir, record.ID)); err != nil {
						t.Error(err)
					}
					// The record is put back for the close at the test's end.
					t.Cleanup(func() { _ = writeRecord(stateDir, record) })
				}
				return test.refusal
			})
			began := time.Now()

			// Act
			err := Close(stateDir, record, 10*time.Second)
			took := time.Since(began)

			// Assert
			if !errors.Is(err, test.refusal) {
				t.Fatalf("Close = %v, want the refusal %v", err, test.refusal)
			}
			if test.isWait && took < closeEndWait {
				t.Errorf("Close reported after %s, before the host's %s to end", took, closeEndWait)
			}
			if !test.isWait && took >= closeEndWait {
				t.Errorf("Close reported after %s, want at once", took)
			}
			if !Running(record) {
				t.Error("the host ended, but nothing asked it to")
			}
		})
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

// A process that cannot be read, as another user's may not be, may still be
// the host, so only one read as gone reads as ended.
func TestRunningTakesAUnixHostThatCannotBeReadAsRunning(t *testing.T) {
	for _, test := range []struct {
		name      string
		readErr   error
		isRunning bool
	}{
		{"a process that cannot be read", fs.ErrPermission, true},
		{"a process that is gone", fs.ErrNotExist, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			previous := readInstance
			readInstance = func(pid int) (string, error) {
				return "", fmt.Errorf("proc: read process %d: %w", pid, test.readErr)
			}
			t.Cleanup(func() { readInstance = previous })

			// Act
			isRunning := Running(Record{HostPID: os.Getpid(), HostInstance: "boot/1"})

			// Assert
			if isRunning != test.isRunning {
				t.Fatalf("Running = %t, want %t", isRunning, test.isRunning)
			}
		})
	}
}
