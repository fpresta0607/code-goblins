package supervisor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// runHostVariable makes this test binary, started as "host", host a run
// item's terminal as cfo host does.
const runHostVariable = "CFO_TEST_RUN_HOST"

// runHostCommand is the command a run item's launcher hosts its terminal
// with: this test binary, in place of cfo.exe host.
func runHostCommand(t *testing.T) []string {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(runHostVariable, "1")
	return []string{program, "host"}
}

// commandRun records a run item the way cfo run-request does, with its
// command as its script on disk.
func commandRun(t *testing.T, store *Store, identity, id, shell, command string, admin bool) Run {
	t.Helper()
	r := Run{ID: id, Identity: identity, By: "cfo", Title: "Run " + id, Shell: shell, Admin: admin, Command: command, Cwd: store.Home.Root, CreatedAt: time.Now().UTC()}
	name, script := runScript(r)
	dir := runDir(store.Home.State, r)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), script, 0o600); err != nil {
		t.Fatal(err)
	}
	r.ScriptSum = runDigest(script)
	if err := store.acceptRun(r); err != nil {
		t.Fatal(err)
	}
	return r
}

// runBoard serves s's board, as the Command Center's cards reach it.
func runBoard(t *testing.T, s *Service) *httptest.Server {
	t.Helper()
	board := NewHTTP(s, "", nil)
	server := httptest.NewServer(board)
	board.Host = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(server.Close)
	return server
}

// runEnded checks on the item as the supervisor's loop does until it ends,
// and once more, as the loop goes on.
func runEnded(t *testing.T, s *Service, id string) Run {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if err := s.finishRuns(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, r := range s.Store.Snapshot().Runs {
			if r.ID == id && r.State != "running" {
				if err := s.finishRuns(context.Background()); err != nil {
					t.Fatal(err)
				}
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never ended", id)
		}
	}
}

// terminalGone waits for the item's terminal to end by itself.
func terminalGone(t *testing.T, dir string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := host.ReadRecord(dir, runTerminal); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the item's terminal was still running after its command ended")
		}
	}
}

func gitBash(t *testing.T) {
	t.Helper()
	exists := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	if _, err := runShellPath("bash", exec.LookPath, exists, os.Getenv("SystemRoot")); err != nil {
		t.Skip(err)
	}
}

// The Overlord, 2026-10-08: "why can we not render terminal right in command
// center in place of the notification", then of a command that opened its own
// window: "i want powershell commands terminals to not open up the separate
// tab, stay in command center". Run starts the item in a native terminal the
// supervisor hosts, which its card shows: what he types there reaches the
// command, and when the command ends the item records its exit code and the
// end of what its terminal showed, the terminal closes by itself with nothing
// left waiting for Enter, and the CFO hears how it ended.
func TestARunItemRunsInATerminalOnItsCard(t *testing.T) {
	for _, test := range []struct {
		name, shell, command string
		code                 int
		state                string
	}{
		{"powershell that fails", "powershell", "$name = Read-Host 'Your name'\nWrite-Output \"hello $name\"\nexit 7\n", 7, "failed"},
		{"powershell that succeeds", "powershell", "$name = Read-Host 'Your name'\nWrite-Output \"hello $name\"\n", 0, "succeeded"},
		{"git bash", "bash", "read -r -p 'Your name: ' name\necho \"hello $name\"\nexit 3\n", 3, "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.shell == "bash" {
				gitBash(t)
			}
			// Arrange
			store, h := testStore(t)
			_, identity, cfo, connection := primaryFixture(t, store)
			s := &Service{Store: store, Instance: "instance", done: make(chan struct{}), Options: Options{CFO: connection, Runs: OSRunLauncher{HostCommand: runHostCommand(t)}}}
			server := runBoard(t, s)
			r := commandRun(t, store, identity, "sign-in-1", test.shell, test.command, false)
			dir := runDir(h.State, r)

			// Act: he presses Run, answers the prompt on the card, and the
			// command ends.
			pressRun(t, s, r, "press-sign-in")
			started := store.Snapshot().Runs[0]
			view := openNativeView(t, server, "run="+r.ID+"&token=instance")
			view.ackAll()
			view.waitFor(t, "Your name")
			view.send(t, websocket.MessageBinary, "Overlord\r")
			view.waitFor(t, "hello Overlord")
			ended := runEnded(t, s, r.ID)

			// Assert
			if started.State != "running" || !started.Terminal {
				t.Fatalf("after Run the item = %s with terminal %v, want it running in a terminal on its card", started.State, started.Terminal)
			}
			if ended.State != test.state || ended.ExitCode == nil || *ended.ExitCode != test.code {
				t.Fatalf("run = %s exit %v, want %s with exit code %d", ended.State, ended.ExitCode, test.state, test.code)
			}
			if !strings.Contains(ended.Output, "hello Overlord") {
				t.Errorf("the item kept %q, want the end of what its terminal showed", ended.Output)
			}
			terminalGone(t, dir)
			view.waitForClose(t)
			if typed := cfo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "exit code "+strconv.Itoa(test.code)) || !strings.Contains(typed[0], "hello Overlord") {
				t.Errorf("the CFO got %q, want the exit code and the end of the output once", typed)
			}
		})
	}
}

// The card keeps a way to stop the command: Stop keeps what its terminal
// showed and the item reads stopped, not failed, since he chose it; the CFO
// hears it did not finish, and the supervisor's next pass ends the terminal
// and everything in it.
func TestStoppingARunEndsItsTerminal(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, identity, cfo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Instance: "instance", done: make(chan struct{}), Options: Options{CFO: connection, Runs: OSRunLauncher{HostCommand: runHostCommand(t)}}}
	server := runBoard(t, s)
	r := commandRun(t, store, identity, "long-copy-1", "powershell", "Write-Output 'copying'\nStart-Sleep -Seconds 300\n", false)
	pressRun(t, s, r, "press-copy")
	view := openNativeView(t, server, "run="+r.ID+"&token=instance")
	view.ackAll()
	view.waitFor(t, "copying")

	// Act
	if _, err := store.Queue(Action{ID: "stop-copy", Kind: "run_stop", RunID: r.ID, Generation: r.Identity}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Runs[0]
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Assert
	if got.State != "stopped" || got.ExitCode != nil || got.Reason != "the Overlord stopped it" || !strings.Contains(got.Output, "copying") {
		t.Fatalf("run = %+v, want it stopped with no exit code, its reason and what it showed", got)
	}
	terminalGone(t, runDir(h.State, r))
	view.waitForClose(t)
	if typed := cfo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "did not finish: the Overlord stopped it") {
		t.Errorf("the CFO got %q, want to hear the Overlord stopped it", typed)
	}
	if _, err := store.Queue(Action{ID: "stop-again", Kind: "run_stop", RunID: r.ID, Generation: r.Identity}); err == nil {
		t.Error("a second Stop was queued for a command that already ended")
	}
}

// An administrator's item has no terminal on its card until Windows started
// it elevated, after the Overlord confirmed Windows' own prompt: its card
// waits for that, then shows the elevated terminal as any other.
func TestAnAdministratorsRunShowsItsTerminalOnceWindowsStartsIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store, Instance: "instance", done: make(chan struct{}), Options: Options{Runs: &fakeRunLauncher{started: liveStart(t)}}}
	server := runBoard(t, s)
	r := commandRun(t, store, strings.Repeat("c", 64), "grant-acl-1", "powershell", "icacls C:\\data /grant svc:F\n", true)
	pressRun(t, s, r, "press-acl")
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	waiting := store.Snapshot().Runs[0]

	// Act: Windows starts the elevated host, which serves the terminal.
	hostTerminal(t, runDir(h.State, r), runTerminal)
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	shown := store.Snapshot().Runs[0]

	// Assert
	if waiting.State != "running" || waiting.Terminal {
		t.Fatalf("before Windows started it the item = %s with terminal %v, want it running with no terminal yet", waiting.State, waiting.Terminal)
	}
	if !shown.Terminal {
		t.Fatal("the item shows no terminal once Windows started it")
	}
	view := openNativeView(t, server, "run="+r.ID+"&token=instance")
	view.waitFor(t, "program ready")
}

// An administrator's item asks Windows to start the terminal's host elevated
// and out of sight, so the consent prompt is Windows' own and nothing else
// opens a window: the host runs the runner in the item's folder and records
// itself in the item's directory, and the helper waits for it.
func TestAnElevatedRunStartsItsHostHiddenThroughWindows(t *testing.T) {
	// Arrange
	args := []string{"host", "--state", `C:\Users\Over Lord\AppData\Local\CodeGoblins\state\runs\ab12`, "--id", runTerminal, "--dir", `C:\work dir`, "--", `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "-File", `C:\it's here\runner.ps1`, `a "quoted" arg`}

	// Act
	script := string(elevateScript(`C:\Program Files\cfo.exe`, args, `C:\runs\ab12\declined.txt`))
	line := elevatedCommandLine(args)

	// Assert
	decomposed, err := windows.DecomposeCommandLine("cfo.exe " + line)
	if err != nil || strings.Join(decomposed[1:], "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("the elevated command line %q reads back as %q (%v), want exactly %q", line, decomposed, err, args)
	}
	for _, want := range []string{"Start-Process -FilePath 'C:\\Program Files\\cfo.exe'", "-ArgumentList '" + strings.ReplaceAll(line, "'", "''") + "'", "-Verb RunAs", "-WindowStyle Hidden", "-PassThru", "declined.txt", "$run.WaitForExit()"} {
		if !strings.Contains(script, want) {
			t.Errorf("the elevation script lacks %q:\n%s", want, script)
		}
	}
}

// A run's terminal is shown only while its command runs, and a credential
// request's terminal, where values are typed, only to the board's own page on
// this PC.
func TestARunTerminalViewOpensOnlyWhileItsCommandRuns(t *testing.T) {
	store, h := testStore(t)
	s := &Service{Store: store, Instance: "instance", done: make(chan struct{}), Options: Options{Runs: &fakeRunLauncher{started: liveStart(t)}}}
	server := runBoard(t, s)
	ready := commandRun(t, store, strings.Repeat("c", 64), "ready-item-1", "powershell", "Write-Output ready\n", false)
	credential := commandRun(t, store, strings.Repeat("d", 64), "credential-0123456789", "powershell", "Write-Output values\n", false)
	store.mu.Lock()
	store.db.Runs[1].CredentialRequest = "request-1"
	store.mu.Unlock()
	pressRun(t, s, credential, "press-credential")
	hostTerminal(t, runDir(h.State, credential), runTerminal)
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	for name, view := range map[string]struct {
		query  string
		header string
		reason string
	}{
		"a command not running":  {query: "run=" + ready.ID + "&token=instance", reason: "This command is not running."},
		"an unknown command":     {query: "run=missing-item-1&token=instance", reason: "This command is not running."},
		"values through a proxy": {query: "run=" + credential.ID + "&token=instance", header: "Tailscale-User-Login", reason: "Values are typed only on the board's own page on this PC."},
	} {
		t.Run(name, func(t *testing.T) {
			conn, _, err := dialNativeWith(server, view.query, view.header)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			_, _, err = conn.Read(context.Background())
			var closed websocket.CloseError
			if !errors.As(err, &closed) || closed.Code != websocket.StatusPolicyViolation || closed.Reason != view.reason {
				t.Errorf("the view ended with %v, want a close saying %q", err, view.reason)
			}
		})
	}
	view := openNativeView(t, server, "run="+credential.ID+"&token=instance")
	view.waitFor(t, "program ready")
}

// dialNativeWith opens a view from the board's own origin with header set,
// as a proxy in front of the board sets it.
func dialNativeWith(server *httptest.Server, query, header string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	headers := http.Header{"Origin": {server.URL}}
	if header != "" {
		headers.Set(header, "overlord@example.com")
	}
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/terminal/native?"+query, &websocket.DialOptions{HTTPHeader: headers})
}
