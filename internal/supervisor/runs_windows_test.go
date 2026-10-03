package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// fakeRunLauncher records each launch in place of opening a window.
type fakeRunLauncher struct {
	mu       sync.Mutex
	launches []RunLaunch
	started  RunStarted
	err      error
}

func (f *fakeRunLauncher) Launch(_ context.Context, l RunLaunch) (RunStarted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches = append(f.launches, l)
	return f.started, f.err
}

func (f *fakeRunLauncher) all() []RunLaunch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.launches)
}

// liveStart names this test process, so a run under it reads as still open.
func liveStart(t *testing.T) RunStarted {
	t.Helper()
	entries, err := proc.Ancestry(os.Getpid(), 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("this process's start time: %v %v", entries, err)
	}
	return RunStarted{PID: os.Getpid(), Start: entries[0].Start}
}

// readyRun records a run item the way cfo run-request does, its script on disk.
func readyRun(t *testing.T, store *Store, identity, id, shell string, admin bool, created time.Time) Run {
	t.Helper()
	r := Run{ID: id, Identity: identity, By: "cfo", Title: "Run " + id, Shell: shell, Admin: admin, Command: "Write-Output ready\n", Cwd: store.Home.Root, CreatedAt: created}
	name, script := runScript(r)
	dir := runDir(store.Home.State, r)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), script, 0600); err != nil {
		t.Fatal(err)
	}
	r.ScriptSum = runDigest(script)
	if err := store.acceptRun(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func pressRun(t *testing.T, s *Service, r Run, action string) {
	t.Helper()
	if _, err := s.Store.Queue(Action{ID: action, Kind: "run", RunID: r.ID, Generation: r.Identity}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
}

func commandFile(t *testing.T, command string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "command.txt")
	if err := os.WriteFile(path, []byte(command), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Only the registered primary CFO creates a run item; any other process is
// refused before anything is written.
func TestRunRequestRefusesAProcessThatIsNotTheCFO(t *testing.T) {
	store, h := testStore(t)
	runPipe(t, &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State, Terminals: terminal.HerdrSessions(&herdr.Client{Commands: &cfoRunner{t: t, pid: os.Getpid()}})}}})
	file := commandFile(t, "Write-Output hello\n")
	err := PublishRun(h, RunRequest{ID: "install-tool", Title: "Install the tool", Shell: "powershell", CommandFile: file})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("a run request from a process that is not the CFO = %v, want it refused", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(h.State, "runs")); len(entries) != 0 || len(store.Snapshot().Runs) != 0 {
		t.Fatalf("a refused request left %d script folders and runs %+v, want none", len(entries), store.Snapshot().Runs)
	}
}

// The command file is read once and stored byte for byte as the script Run
// executes, so no quoting can change it and a later edit of the file changes
// nothing; the item runs in the CFO home unless it names a folder.
func TestRunRequestStoresTheCommandAsTheScriptItRuns(t *testing.T) {
	command := "Write-Output \"it's $env:USERNAME\" `\n'single' \"double\" $(Get-Date) caf\u00e9\r\nexit 3\n"
	for _, test := range []struct {
		shell, script string
		bom           bool
	}{
		{shell: "powershell", script: "command.ps1", bom: true},
		{shell: "pwsh", script: "command.ps1", bom: true},
		{shell: "bash", script: "command.sh"},
	} {
		t.Run(test.shell, func(t *testing.T) {
			store, h := testStore(t)
			_, identity, _, connection := primaryFixture(t, store)
			runPipe(t, &Service{Store: store, Options: Options{CFO: connection}})
			file := commandFile(t, command)
			req := RunRequest{ID: "fix-path-" + test.shell, Title: "Put Go on PATH", Shell: test.shell, CommandFile: file}
			if err := PublishRun(h, req); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("Remove-Item -Recurse C:\\\n"), 0600); err != nil {
				t.Fatal(err)
			}
			runs := store.Snapshot().Runs
			if len(runs) != 1 {
				t.Fatalf("runs = %+v, want the one published", runs)
			}
			r := runs[0]
			if r.Command != command || r.Cwd != h.Root || r.State != "ready" || r.Identity != identity || r.Admin || !r.ExpiresAt.Equal(r.CreatedAt.Add(24*time.Hour)) {
				t.Fatalf("run = %+v, want the exact command, ready in the CFO home for 24 hours", r)
			}
			want := []byte(command)
			if test.bom {
				want = append([]byte("\xef\xbb\xbf"), want...)
			}
			data, err := os.ReadFile(filepath.Join(runDir(h.State, r), test.script))
			if err != nil || !bytes.Equal(data, want) || runDigest(data) != r.ScriptSum {
				t.Fatalf("script = %q %v, want exactly %q with its digest recorded", data, err, want)
			}
			if err := PublishRun(h, req); err == nil || !strings.Contains(err.Error(), "already used") {
				t.Fatalf("republishing the ID with other text = %v, want it refused", err)
			}
		})
	}
}

func TestRunRequestExecutesACommandFileWithOrWithoutAUtf8BOM(t *testing.T) {
	for _, prefix := range []string{"", "\xef\xbb\xbf"} {
		t.Run(fmt.Sprintf("prefix_%x", prefix), func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			_, _, _, connection := primaryFixture(t, store)
			runPipe(t, &Service{Store: store, Options: Options{CFO: connection}})
			file := commandFile(t, prefix+"Write-Output 'scratch-run-result-7421'\r\n")

			// Act
			if err := PublishRun(h, RunRequest{ID: "utf8-command-proof", Title: "Check the command file", Shell: "powershell", CommandFile: file}); err != nil {
				t.Fatal(err)
			}
			run := store.Snapshot().Runs[0]
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			program := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(runDir(h.State, run), "command.ps1"))
			program.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
			output, err := program.CombinedOutput()

			// Assert
			if err != nil || strings.TrimSpace(string(output)) != "scratch-run-result-7421" {
				t.Fatalf("published command execution = %q, %v; want the whole command to run", output, err)
			}
		})
	}
}

// Run goes only through the board's action checks: without the per-session
// token, or from another origin, nothing is queued and nothing runs.
func TestRunActionIsRefusedWithoutTheTokenOrFromAnotherOrigin(t *testing.T) {
	_, h := testStore(t)
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s, err := Start(context.Background(), h, Options{Runs: launcher})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := readyRun(t, s.Store, strings.Repeat("c", 64), "cleanup-temp", "powershell", false, time.Now().UTC())
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	post := func(origin, token string) int {
		t.Helper()
		body := fmt.Sprintf(`{"id":"press-cleanup","kind":"run","run_id":%q,"generation":%q}`, r.ID, r.Identity)
		req, _ := http.NewRequest("POST", server.URL+"/api/actions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set("X-CFO-Token", token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	for _, c := range []struct{ origin, token string }{{server.URL, ""}, {server.URL, "not-the-token"}, {"https://evil.invalid", s.Instance}, {"", s.Instance}} {
		if code := post(c.origin, c.token); code != 403 {
			t.Fatalf("Run from origin %q with token %q = %d, want 403", c.origin, c.token, code)
		}
	}
	runAction := func(a Action) bool { return a.Kind == "run" }
	if got := s.Store.Snapshot(); got.Runs[0].State != "ready" || slices.ContainsFunc(got.Actions, runAction) || len(launcher.all()) != 0 {
		t.Fatalf("after refused Runs the item is %s with actions %+v and %d launches, want it untouched", got.Runs[0].State, got.Actions, len(launcher.all()))
	}
	if code := post(server.URL, s.Instance); code != 202 {
		t.Fatalf("Run from the board with its token = %d, want 202", code)
	}
	if got := s.Store.Snapshot().Runs[0]; got.State != "running" {
		t.Fatalf("an admitted Run left the item %s, want it running", got.State)
	}
}

// A run item runs once, in exactly the shell it names: the first Run claims
// it, a second is refused with nothing launched again, and an admin item goes
// to the launcher as one that must elevate through Windows UAC.
func TestRunItemRunsOnceInExactlyItsShell(t *testing.T) {
	for _, admin := range []bool{false, true} {
		t.Run(fmt.Sprintf("admin %v", admin), func(t *testing.T) {
			store, h := testStore(t)
			launcher := &fakeRunLauncher{started: liveStart(t)}
			s := &Service{Store: store, Options: Options{Runs: launcher}}
			r := readyRun(t, store, strings.Repeat("c", 64), "install-node", "pwsh", admin, time.Now().UTC())
			if _, err := store.Queue(Action{ID: "press-1", Kind: "run", RunID: r.ID, Generation: r.Identity}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Queue(Action{ID: "press-2", Kind: "run", RunID: r.ID, Generation: r.Identity}); err == nil || !strings.Contains(err.Error(), "already ran") {
				t.Fatalf("a second Run = %v, want it refused", err)
			}
			if err := store.ProcessOne(context.Background(), s.execute); err != nil {
				t.Fatal(err)
			}
			dir := runDir(h.State, r)
			want := RunLaunch{Shell: "pwsh", Admin: admin, Script: filepath.Join(dir, "command.ps1"), Dir: dir, Cwd: h.Root}
			if launches := launcher.all(); len(launches) != 1 || launches[0] != want {
				t.Fatalf("launches = %+v, want exactly %+v", launches, want)
			}
			if got := store.Snapshot().Runs[0]; got.State != "running" || got.RanAt == nil || got.PID != os.Getpid() {
				t.Fatalf("run = %+v, want it running under the launched process", got)
			}
		})
	}
}

// An item nobody ran within 24 hours expires, and Run on it is refused.
func TestExpiredRunItemIsRefused(t *testing.T) {
	store, _ := testStore(t)
	r := readyRun(t, store, strings.Repeat("c", 64), "stale-item", "bash", false, time.Now().UTC().Add(-25*time.Hour))
	if _, err := store.Queue(Action{ID: "press-late", Kind: "run", RunID: r.ID, Generation: r.Identity}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Run after 24 hours = %v, want it refused as expired", err)
	}
	if err := store.expireRuns(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Runs[0]; got.State != "expired" || got.Reason == "" {
		t.Fatalf("run = %+v, want it expired with a reason", got)
	}
	if _, err := store.Queue(Action{ID: "press-later", Kind: "run", RunID: r.ID, Generation: r.Identity}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Run on an expired item = %v, want it refused", err)
	}
}

// The registered CFO withdraws a run item nobody ran, with its reason: the
// item leaves the Command Center, state/runs.audit records the withdrawal
// like a run, and Run on it is refused with nothing launched. On 2026-10-01
// the CFO could only disable install-main-66714dea by renaming the binary it
// named, because cfo review --clear does not reach a run item.
func TestTheCFOWithdrawsARunItemNobodyRan(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, _, _, connection := primaryFixture(t, store)
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s := &Service{Store: store, Options: Options{CFO: connection, Runs: launcher}}
	runPipe(t, s)
	if err := PublishRun(h, RunRequest{ID: "install-main-66714dea", Title: "Install main", Shell: "powershell", CommandFile: commandFile(t, "Write-Output install\n")}); err != nil {
		t.Fatal(err)
	}

	// Act
	err := WithdrawRun(h, "install-main-66714dea", "the candidate binary is gone")

	// Assert
	if err != nil {
		t.Fatalf("WithdrawRun = %v, want the item withdrawn", err)
	}
	r := store.Snapshot().Runs[0]
	if r.State != "withdrawn" || r.Reason != "the candidate binary is gone" || r.FinishedAt == nil {
		t.Fatalf("run = %+v, want it withdrawn with the CFO's reason", r)
	}
	audit, err := os.ReadFile(filepath.Join(h.State, "runs.audit"))
	if fields := strings.Fields(string(audit)); err != nil || len(fields) != 9 || fields[1] != r.ID || fields[2] != r.ScriptSum || fields[3] != "withdrawn" || strings.Join(fields[4:], " ") != "the candidate binary is gone" {
		t.Fatalf("audit = %q %v, want one line with the item, its script digest, withdrawn and the reason", audit, err)
	}
	if _, err := store.Queue(Action{ID: "press-withdrawn", Kind: "run", RunID: r.ID, Generation: r.Identity}); err == nil || !strings.Contains(err.Error(), "withdrew") {
		t.Fatalf("Run on a withdrawn item = %v, want it refused", err)
	}
	if launches := launcher.all(); len(launches) != 0 {
		t.Fatalf("launches = %+v, want nothing launched", launches)
	}
}

// Only the registered CFO withdraws a run item, only one of its own that
// nobody ran yet, and only with a reason; a refused withdrawal changes nothing.
func TestARunWithdrawalIsRefusedWhenItCannotHold(t *testing.T) {
	for _, c := range []struct {
		name              string
		isRegistered      bool
		id, reason        string
		age               time.Duration
		connectionTask    string
		credentialRequest string
		ran               bool
		refusal           string
	}{
		{name: "a process that is not the CFO", id: "install-tool", reason: "not needed", refusal: "not registered"},
		{name: "an item that is not on the board", isRegistered: true, id: "no-such-item", reason: "not needed", refusal: "no run item with that ID"},
		{name: "an item that already ran", isRegistered: true, id: "install-tool", reason: "not needed", ran: true, refusal: "already running"},
		{name: "an item past its lifetime", isRegistered: true, id: "install-tool", reason: "not needed", age: 25 * time.Hour, refusal: "expired"},
		{name: "a connection repair the Overlord asked for", isRegistered: true, id: "install-tool", reason: "not needed", connectionTask: "task-1", refusal: "connection repair the Overlord asked for on task-1"},
		{name: "the terminal the Overlord opened for a credential request", isRegistered: true, id: "install-tool", reason: "not needed", credentialRequest: "cred-0123456789abcdef", refusal: "terminal the Overlord opened for a credential request"},
		{name: "no reason", isRegistered: true, id: "install-tool", reason: " ", refusal: "reason"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			_, identity, _, connection := primaryFixture(t, store)
			s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
			var r Run
			if c.connectionTask == "" && c.credentialRequest == "" {
				r = readyRun(t, store, identity, "install-tool", "powershell", false, time.Now().UTC().Add(-c.age))
			} else {
				r = Run{ID: "install-tool", Identity: identity, Title: "Run install-tool", Shell: "powershell", Command: "Write-Output ready\n", Cwd: store.Home.Root, CreatedAt: time.Now().UTC(), ConnectionTask: c.connectionTask, CredentialRequest: c.credentialRequest}
				if c.connectionTask != "" {
					r.ConnectionGeneration = "1"
				}
				name, script := runScript(r)
				dir := runDir(store.Home.State, r)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), script, 0600); err != nil {
					t.Fatal(err)
				}
				r.ScriptSum = runDigest(script)
				if err := store.acceptRun(r); err != nil {
					t.Fatal(err)
				}
			}
			if c.ran {
				pressRun(t, s, r, "press-install")
			}
			if !c.isRegistered {
				if err := os.Remove(filepath.Join(h.State, "primary.json")); err != nil {
					t.Fatal(err)
				}
			}
			runPipe(t, s)
			before := store.Snapshot().Runs[0]

			// Act
			err := WithdrawRun(h, c.id, c.reason)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("WithdrawRun = %v, want it refused (%q)", err, c.refusal)
			}
			if after := store.Snapshot().Runs[0]; after.State != before.State || after.Reason != before.Reason {
				t.Fatalf("run = %+v after a refused withdrawal, want it unchanged from %+v", after, before)
			}
		})
	}
}

// When the command finishes, its output and exit code are on the item, an
// audit line records the digest of exactly the script that ran, and the CFO
// gets the result as its answer, once.
func TestRunResultReachesTheCFOAndTheAudit(t *testing.T) {
	for _, test := range []struct {
		code  int
		state string
	}{{0, "succeeded"}, {3, "failed"}} {
		t.Run(test.state, func(t *testing.T) {
			store, h := testStore(t)
			_, identity, runner, connection := primaryFixture(t, store)
			s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
			r := readyRun(t, store, identity, "migrate-db", "powershell", false, time.Now().UTC())
			pressRun(t, s, r, "press-migrate")
			dir := runDir(h.State, r)
			if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte("\xef\xbb\xbfapplying migration 42\nmigration 42 applied"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "exit.txt"), []byte(strconv.Itoa(test.code)), 0600); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := s.finishRuns(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			got := store.Snapshot().Runs[0]
			if got.State != test.state || got.ExitCode == nil || *got.ExitCode != test.code || got.Output != "applying migration 42\nmigration 42 applied" || got.FinishedAt == nil {
				t.Fatalf("run = %+v, want %s with exit code %d and its output", got, test.state, test.code)
			}
			if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], fmt.Sprintf("finished with exit code %d", test.code)) || !strings.Contains(runner.prompts[0], "migration 42 applied") {
				t.Fatalf("the CFO got %q, want the exit code and the output's end once", runner.prompts)
			}
			audit, err := os.ReadFile(filepath.Join(h.State, "runs.audit"))
			fields := strings.Fields(string(audit))
			if err != nil || len(fields) != 4 || fields[1] != r.ID || fields[2] != r.ScriptSum || fields[3] != strconv.Itoa(test.code) {
				t.Fatalf("audit = %q %v, want one line with the item, its script digest and exit code", audit, err)
			}
		})
	}
}

// A run's card shows the command's output while it runs: the board reads what
// it printed so far, and once it ends, the output the item kept.
func TestRunOutputShowsWhatARunningCommandPrinted(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, identity, _, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	r := readyRun(t, store, identity, "migrate-db", "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "press-migrate")
	dir := runDir(h.State, r)
	if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte("\xef\xbb\xbfapplying migration 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	read := func(path string) (int, string, string) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var answer struct {
			Output string `json:"output"`
			State  string `json:"state"`
		}
		_ = json.NewDecoder(response.Body).Decode(&answer)
		return response.StatusCode, answer.Output, answer.State
	}

	// Act and Assert
	if code, output, state := read("/api/runs/" + r.ID + "/output"); code != 200 || output != "applying migration 42\n" || state != "running" {
		t.Fatalf("a running command = %d %q %s, want 200, what it printed so far and running", code, output, state)
	}
	if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte("applying migration 42\nmigration 42 applied"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit.txt"), []byte("0"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, output, state := read("/api/runs/" + r.ID + "/output"); code != 200 || output != "applying migration 42\nmigration 42 applied" || state != "succeeded" {
		t.Fatalf("a finished run = %d %q %s, want 200, its kept output and succeeded", code, output, state)
	}
	for _, path := range []string{"/api/runs/unknown/output", "/api/runs/" + r.ID + "/script", "/api/runs/" + r.ID + "/../output"} {
		if code, _, _ := read(path); code != 404 {
			t.Fatalf("%s = %d, want 404", path, code)
		}
	}
}

// A run whose window closes before its command finishes, or whose elevation
// Windows does not start, ends failed with that reason and no exit code, and
// the CFO is told it did not finish.
func TestRunThatDoesNotFinishEndsWithItsReason(t *testing.T) {
	for _, test := range []struct {
		name     string
		started  func(*testing.T) RunStarted
		declined string
		reason   string
	}{
		{
			name: "window closed",
			// This PID with another start time is a process that is gone.
			started: func(*testing.T) RunStarted { return RunStarted{PID: os.Getpid(), Start: time.Unix(1, 0)} },
			reason:  "its window closed before the command finished",
		},
		{
			name:     "elevation declined",
			started:  liveStart,
			declined: "The operation was canceled by the user.",
			reason:   "Windows did not start it elevated: The operation was canceled by the user.",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			_, identity, runner, connection := primaryFixture(t, store)
			s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: test.started(t)}}}
			r := readyRun(t, store, identity, "install-driver", "powershell", true, time.Now().UTC())
			pressRun(t, s, r, "press-driver")
			if test.declined != "" {
				if err := os.WriteFile(filepath.Join(runDir(h.State, r), "declined.txt"), []byte(test.declined), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.finishRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := store.Snapshot().Runs[0]
			if got.State != "failed" || got.ExitCode != nil || got.Reason != test.reason {
				t.Fatalf("run = %+v, want failed with %q and no exit code", got, test.reason)
			}
			if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], "did not finish") {
				t.Fatalf("the CFO got %q, want to hear it did not finish", runner.prompts)
			}
			if audit, err := os.ReadFile(filepath.Join(h.State, "runs.audit")); err != nil || !strings.HasSuffix(strings.TrimSpace(string(audit)), r.ScriptSum+" none") {
				t.Fatalf("audit = %q %v, want the run recorded with no exit code", audit, err)
			}
		})
	}
}

// A Run whose start was interrupted before its launch was recorded, as when
// the supervisor stops mid-start, ends the item instead of leaving it running
// forever; a Run still being started is left alone.
func TestRunWhoseStartWasInterruptedEnds(t *testing.T) {
	store, _ := testStore(t)
	s := &Service{Store: store, Options: Options{Runs: &fakeRunLauncher{started: liveStart(t)}}}
	r := readyRun(t, store, strings.Repeat("c", 64), "install-sdk", "powershell", false, time.Now().UTC())
	if _, err := store.Queue(Action{ID: "press-sdk", Kind: "run", RunID: r.ID, Generation: r.Identity}); err != nil {
		t.Fatal(err)
	}
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Runs[0]; got.State != "running" {
		t.Fatalf("an item whose Run is still queued = %+v, want it running", got)
	}
	interrupted := func(context.Context, Action) (Evaluation, error) {
		return Evaluation{}, errors.New("supervisor stopping")
	}
	if err := store.ProcessOne(context.Background(), interrupted); err != nil {
		t.Fatal(err)
	}
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Runs[0]; got.State != "failed" || !strings.HasPrefix(got.Reason, "its start was interrupted, so whether its window opened is unknown") {
		t.Fatalf("an item whose start was interrupted = %+v, want it failed with the reason", got)
	}
}

// A launch that fails, such as a shell that is not installed, and a script
// changed after the CFO published it, both end the item with the reason and
// run nothing.
func TestRunThatCannotStartEndsTheItem(t *testing.T) {
	for _, test := range []struct {
		name    string
		launch  error
		tamper  bool
		reason  string
		launchN int
	}{
		{name: "shell missing", launch: errors.New("PowerShell 7 (pwsh) is not on PATH"), reason: "it could not start: PowerShell 7 (pwsh) is not on PATH", launchN: 1},
		{name: "script changed", tamper: true, reason: "it could not start: its script file is missing or changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			launcher := &fakeRunLauncher{err: test.launch}
			s := &Service{Store: store, Options: Options{Runs: launcher}}
			r := readyRun(t, store, strings.Repeat("c", 64), "install-pwsh", "pwsh", false, time.Now().UTC())
			if test.tamper {
				if err := os.WriteFile(filepath.Join(runDir(h.State, r), "command.ps1"), []byte("Remove-Item -Recurse C:\\"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			pressRun(t, s, r, "press-pwsh")
			if got := store.Snapshot().Runs[0]; got.State != "failed" || !strings.HasPrefix(got.Reason, test.reason) {
				t.Fatalf("run = %+v, want failed with %q", got, test.reason)
			}
			if action := store.Snapshot().Actions[0]; action.Status != "failed" || !strings.Contains(action.Message, "nothing ran") {
				t.Fatalf("action = %+v, want it failed saying nothing ran", action)
			}
			if n := len(launcher.all()); n != test.launchN {
				t.Fatalf("%d launches, want %d", n, test.launchN)
			}
		})
	}
}

// Republishing an item's ID is an idempotent retry only while the item still
// waits; once it ran or expired, the request is refused, since a re-run needs
// a new ID.
func TestRunRequestRefusesTheIDOfAnItemThatEnded(t *testing.T) {
	for _, test := range []struct {
		state string
		end   func(*testing.T, *Service, Run)
	}{
		{state: "expired", end: func(t *testing.T, s *Service, _ Run) {
			if err := s.Store.expireRuns(time.Now().Add(25 * time.Hour)); err != nil {
				t.Fatal(err)
			}
		}},
		{state: "succeeded", end: func(t *testing.T, s *Service, r Run) {
			pressRun(t, s, r, "press-tool")
			if err := os.WriteFile(filepath.Join(runDir(s.Store.Home.State, r), "exit.txt"), []byte("0"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.finishRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.state, func(t *testing.T) {
			store, h := testStore(t)
			_, _, _, connection := primaryFixture(t, store)
			s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
			runPipe(t, s)
			req := RunRequest{ID: "install-tool", Title: "Install the tool", Shell: "powershell", CommandFile: commandFile(t, "Write-Output hello\n")}
			if err := PublishRun(h, req); err != nil {
				t.Fatal(err)
			}
			if err := PublishRun(h, req); err != nil {
				t.Fatalf("republishing a ready item = %v, want an idempotent success", err)
			}
			test.end(t, s, store.Snapshot().Runs[0])
			if got := store.Snapshot().Runs[0]; got.State != test.state {
				t.Fatalf("run = %+v, want it %s", got, test.state)
			}
			if err := PublishRun(h, req); err == nil || !strings.Contains(err.Error(), "a re-run needs a new ID") {
				t.Fatalf("republishing the ID of the %s item = %v, want it refused", test.state, err)
			}
		})
	}
}

// A Run action ID reused for another item is refused, not answered with the
// earlier Run, so the other item stays ready.
func TestRunActionIDReusedForAnotherItemIsRefused(t *testing.T) {
	store, _ := testStore(t)
	identity := strings.Repeat("c", 64)
	first := readyRun(t, store, identity, "install-node", "powershell", false, time.Now().UTC())
	second := readyRun(t, store, identity, "install-go", "powershell", false, time.Now().UTC())
	if _, err := store.Queue(Action{ID: "press-1", Kind: "run", RunID: first.ID, Generation: identity}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "press-1", Kind: "run", RunID: second.ID, Generation: identity}); err == nil || !strings.Contains(err.Error(), "already used for another action") {
		t.Fatalf("reusing a Run's action ID for another item = %v, want it refused", err)
	}
	got := store.Snapshot()
	if i := slices.IndexFunc(got.Runs, func(r Run) bool { return r.ID == second.ID }); got.Runs[i].State != "ready" || len(got.Actions) != 1 {
		t.Fatalf("runs %+v with actions %+v, want the other item ready and one action", got.Runs, got.Actions)
	}
}

// The PowerShell runner shows a prompt with no line ending as soon as it is
// written, takes the answer typed in the window, and keeps the output, stderr
// included, and the exit code. It runs the generated runner for real, in a
// console with no window, its input and output connected to this test.
func TestPowerShellRunnerShowsAPromptBeforeItIsAnswered(t *testing.T) {
	exists := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	shell, err := runShellPath("powershell", exec.LookPath, exists, os.Getenv("SystemRoot"))
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "command.ps1")
	command := "\xef\xbb\xbf[Console]::Out.Write('Your name: ')\r\n$name = [Console]::In.ReadLine()\r\nWrite-Output \"hello $name\"\r\n[Console]::Error.WriteLine('careful')\r\nexit 7\r\n"
	if err := os.WriteFile(script, []byte(command), 0600); err != nil {
		t.Fatal(err)
	}
	runner, args := runnerScript(RunLaunch{Shell: "powershell", Script: script, Dir: dir, Cwd: dir}, shell)
	if err := os.WriteFile(args[len(args)-1], runner, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	chunks := make(chan string, 256)
	go func() {
		defer close(chunks)
		buffer := make([]byte, 4096)
		for {
			n, err := stdout.Read(buffer)
			if n > 0 {
				chunks <- string(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	var shown strings.Builder
	waitFor := func(text string) {
		t.Helper()
		deadline := time.After(30 * time.Second)
		for !strings.Contains(shown.String(), text) {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					t.Fatalf("the runner ended showing %q, want %q", shown.String(), text)
				}
				shown.WriteString(chunk)
			case <-deadline:
				t.Fatalf("the window shows %q, want %q before anything is typed", shown.String(), text)
			}
		}
	}
	waitFor("Your name: ")
	if _, err := io.WriteString(stdin, "Overlord\r\n"); err != nil {
		t.Fatal(err)
	}
	waitFor("Press Enter")
	if _, err := io.WriteString(stdin, "\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("runner = %v, want it to end once Enter is pressed", err)
	}
	output := readRunOutput(dir)
	if !strings.Contains(output, "hello Overlord") || !strings.Contains(output, "careful") {
		t.Fatalf("output.log = %q, want the answered prompt's output and stderr", output)
	}
	if code, ok := readRunExit(dir); !ok || code != 7 {
		t.Fatalf("exit.txt = %d %v, want 7", code, ok)
	}
}

// A run window takes what the Overlord types in it: the item's script reads
// its window's console, not an empty input, so a prompt such as cfo auth
// store's hidden one waits for an answer. It opens a real window.
func TestRunWindowGivesTheItemItsConsoleAsInput(t *testing.T) {
	// Arrange
	exists := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	if _, err := runShellPath("powershell", exec.LookPath, exists, os.Getenv("SystemRoot")); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "command.ps1")
	if err := os.WriteFile(script, []byte("\xef\xbb\xbfWrite-Output \"input redirected: $([Console]::IsInputRedirected)\"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Act
	started, err := OSRunLauncher{}.Launch(context.Background(), RunLaunch{Shell: "powershell", Script: script, Dir: dir, Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if window, err := os.FindProcess(started.PID); err == nil {
			_ = window.Kill()
			_, _ = window.Wait()
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for _, ok := readRunExit(dir); !ok; _, ok = readRunExit(dir) {
		if time.Now().After(deadline) {
			t.Fatal("the run window never finished its script")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Assert
	if output := readRunOutput(dir); !strings.Contains(output, "input redirected: False") {
		t.Fatalf("output.log = %q, want the script to read its window's console", output)
	}
}
