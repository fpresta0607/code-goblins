package codegoblins

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/standin"
)

// TestStartHiddenServerSaysWhyItsServerDidNotAnswer starts Python servers as
// the install jobs' Start-HiddenServer starts theirs. A server that answers is
// handed back; one that ends first fails the wait at once with its exit code
// and output; and one that runs but never answers fails it at the deadline
// with its pid, its last request's error and its output, and is ended. A
// hidden server's output reaches no log of its own, so these are what a failed
// wait in a job says. Only the server that never answers waits out its
// deadline, so only it has a short one, and as Python can take longer than
// that to start on a busy runner, its case reads nothing Python prints.
func TestStartHiddenServerSaysWhyItsServerDidNotAnswer(t *testing.T) {
	// Arrange
	port := freePort(t)
	refused := "http://127.0.0.1:" + strconv.Itoa(freePort(t)) + "/"
	for _, test := range []struct {
		name      string
		script    string
		arguments string
		url       string
		seconds   int
		want      []string
	}{
		{
			name:      "hands back a server that answers",
			arguments: "'-m', 'http.server', '" + strconv.Itoa(port) + "', '--bind', '127.0.0.1'",
			url:       "http://127.0.0.1:" + strconv.Itoa(port) + "/",
			seconds:   30,
			want:      []string{"answered as pid"},
		},
		{
			name:    "names the exit code and output of a server that ended",
			script:  "print('Serving HTTP on 127.0.0.1 port 8765')\nraise OSError('the port is taken')\n",
			url:     refused,
			seconds: 30,
			want:    []string{"the test server exited with code 1 before it answered " + refused, "its output:\nServing HTTP on 127.0.0.1 port 8765", "OSError: the port is taken"},
		},
		{
			name:    "ends a server that runs and never answers, naming its pid, last error and output",
			script:  "import time\ntime.sleep(3600)\n",
			url:     refused,
			seconds: 2,
			want:    []string{"the test server did not answer within 2 seconds and was ended: it still ran as pid", "its last request to " + refused + " failed: ", "its output:\n", "its errors:\n"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			standin.RemoveAtCleanup(t, dir)
			arguments := test.arguments
			if test.script != "" {
				server := filepath.Join(dir, "server.py")
				if err := os.WriteFile(server, []byte(test.script), 0o644); err != nil {
					t.Fatal(err)
				}
				arguments = "'" + server + "'"
			}
			script := "$ErrorActionPreference = 'Stop'\n" +
				". '" + hiddenServerScript(t) + "'\n" +
				"try {\n" +
				"  $server = Start-HiddenServer -Name 'test server' -ArgumentList " + arguments + " -Url '" + test.url + "' -Log '" + filepath.Join(dir, "server") + "' -Seconds " + strconv.Itoa(test.seconds) + "\n" +
				"  \"answered as pid $($server.Id)\"\n" +
				"  Stop-Process -Id $server.Id\n" +
				"} catch { $_.Exception.Message }\n"
			command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
			command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

			// Act
			output, err := command.CombinedOutput()

			// Assert
			if err != nil {
				t.Fatalf("powershell failed: %v\n%s", err, output)
			}
			text := strings.ReplaceAll(string(output), "\r\n", "\n")
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Errorf("the wait said nothing of %q:\n%s", want, text)
				}
			}
			found := regexp.MustCompile(`pid (\d+)`).FindStringSubmatch(text)
			if found == nil {
				return
			}
			pid, err := strconv.Atoi(found[1])
			if err != nil {
				t.Fatal(err)
			}
			if !hasProcessEnded(t, pid) {
				t.Errorf("the server, pid %d, still runs after the wait ended", pid)
			}
		})
	}
}

// TestStartHiddenServerKeepsTheShellsOutputFromItsServer starts a server
// through Start-HiddenServer from a shell whose output is a pipe, as a job's
// step is, and lets the shell exit with the server running. A job's runner
// waits for a step's output to close and, 5 seconds after its shell exits,
// ends every process of the step, so a server that holds the shell's output
// keeps that pipe open and is gone when a later step asks it for a release.
// This one leaves the pipe to close as the shell exits, and outlives its
// shell.
func TestStartHiddenServerKeepsTheShellsOutputFromItsServer(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	standin.RemoveAtCleanup(t, dir)
	port := strconv.Itoa(freePort(t))
	script := ". '" + hiddenServerScript(t) + "'\n" +
		"$server = Start-HiddenServer -Name 'test server' -ArgumentList '-m', 'http.server', '" + port + "', '--bind', '127.0.0.1' -Url 'http://127.0.0.1:" + port + "/' -Log '" + filepath.Join(dir, "server") + "'\n" +
		"\"started pid $($server.Id)\"\n"
	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	// The output a server holds never closes, and Run then returns
	// ErrWaitDelay once this has passed since the shell exited.
	command.WaitDelay = 30 * time.Second

	// Act
	err := command.Run()

	// Assert
	found := regexp.MustCompile(`started pid (\d+)`).FindStringSubmatch(output.String())
	if found == nil {
		t.Fatalf("the shell started no server: %v\n%s", err, output.String())
	}
	pid, convertErr := strconv.Atoi(found[1])
	if convertErr != nil {
		t.Fatal(convertErr)
	}
	t.Cleanup(func() {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
			if !hasProcessEnded(t, pid) {
				t.Errorf("the server, pid %d, did not end when it was killed", pid)
			}
		}
	})
	if errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("the shell's output stayed open after it exited, held by the server it started:\n%s", output.String())
	}
	if err != nil {
		t.Fatalf("powershell failed: %v\n%s", err, output.String())
	}
	if !isProcessRunning(t, pid) {
		t.Errorf("the server, pid %d, did not outlive the shell that started it", pid)
	}
}

// hiddenServerScript is the absolute path of the script that defines
// Start-HiddenServer.
func hiddenServerScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("tests", "hidden_server.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// freePort is a port on 127.0.0.1 that nothing listened on a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// isProcessRunning reports whether process pid has not exited, and fails the
// test for one it cannot read.
func isProcessRunning(t *testing.T, pid int) bool {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false
	}
	if err != nil {
		t.Fatalf("read process %d: %v", pid, err)
	}
	defer windows.CloseHandle(process)
	const stillActive = 259
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		t.Fatalf("read process %d: %v", pid, err)
	}
	return code == stillActive
}

// hasProcessEnded reports whether process pid has ended or ends soon after,
// and fails the test for one it cannot read. Stop-Process and Kill return once
// a process is told to end, which it does a moment later.
func hasProcessEnded(t *testing.T, pid int) bool {
	t.Helper()
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return true
	}
	if err != nil {
		t.Fatalf("read process %d: %v", pid, err)
	}
	defer windows.CloseHandle(process)
	event, err := windows.WaitForSingleObject(process, 30000)
	if err != nil {
		t.Fatalf("wait for process %d: %v", pid, err)
	}
	return event == windows.WAIT_OBJECT_0
}
