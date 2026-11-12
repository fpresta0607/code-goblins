package codegoblins

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/standin"
)

// TestStartHiddenServerSaysWhyItsServerDidNotAnswer starts the stand-in
// program as the hidden server the install jobs' Start-HiddenServer starts,
// with a 2-second wait. A server that answers is handed back; one that ends
// first fails the wait at once with its exit code and output; and one that
// runs but never answers fails it at the deadline with its pid, its last
// request's error and its output, and is ended. A hidden server's output
// reaches no log of its own, so these are what a failed wait in a job says.
func TestStartHiddenServerSaysWhyItsServerDidNotAnswer(t *testing.T) {
	// Arrange
	answering := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(answering.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + listener.Addr().String() + "/"
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	helper, err := filepath.Abs(filepath.Join("tests", "hidden_server.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		rule standin.Rule
		url  string
		want []string
	}{
		{
			name: "hands back a server that answers",
			rule: standin.Rule{Any: true, Hold: true},
			url:  answering.URL,
			want: []string{"answered as pid"},
		},
		{
			name: "names the exit code and output of a server that ended",
			rule: standin.Rule{Any: true, Stdout: "Serving HTTP on 127.0.0.1 port 8765", Stderr: "OSError: the port is taken", Exit: 3},
			url:  refused,
			want: []string{"the test server exited with code 3 before it answered " + refused, "Serving HTTP on 127.0.0.1 port 8765", "OSError: the port is taken"},
		},
		{
			name: "ends a server that runs and never answers, naming its pid, last error and output",
			rule: standin.Rule{Any: true, Stdout: "starting", Hold: true},
			url:  refused,
			want: []string{"the test server did not answer within 2 seconds and was ended: it still ran as pid", "its last request to " + refused + " failed: ", "its output:\nstarting"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			standin.RemoveAtCleanup(t, dir)
			server := filepath.Join(dir, "server.exe")
			standin.Put(t, server)
			script := "$ErrorActionPreference = 'Stop'\n" +
				". '" + helper + "'\n" +
				"try {\n" +
				"  $server = Start-HiddenServer -Name 'test server' -FilePath '" + server + "' -ArgumentList 'serve' -Url '" + test.url + "' -Log '" + filepath.Join(dir, "server") + "' -Seconds 2\n" +
				"  \"answered as pid $($server.Id)\"\n" +
				"  Stop-Process -Id $server.Id\n" +
				"} catch { $_.Exception.Message }\n"
			command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
			command.Env = append(os.Environ(), standin.Env("", test.rule)...)
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
			if isProcessRunning(t, pid) {
				t.Errorf("the server, pid %d, still runs after the wait ended", pid)
			}
		})
	}
}

// isProcessRunning reports whether process pid has not exited, and fails the
// test for one it cannot read.
func isProcessRunning(t *testing.T, pid int) bool {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false
	}
	if err != nil {
		t.Fatalf("read process %d: %v", pid, err)
	}
	defer windows.CloseHandle(process)
	// Stop-Process returns once the process is told to end; give it a moment
	// to be gone.
	event, err := windows.WaitForSingleObject(process, 5000)
	if err != nil {
		t.Fatalf("wait for process %d: %v", pid, err)
	}
	return event != windows.WAIT_OBJECT_0
}
