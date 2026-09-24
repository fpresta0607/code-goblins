package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/proc"
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
	r := Run{ID: id, Identity: identity, Title: "Run " + id, Shell: shell, Admin: admin, Command: "Write-Output ready\n", Cwd: store.Home.Root, CreatedAt: created}
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
	_, h := testStore(t)
	file := commandFile(t, "Write-Output hello\n")
	err := PublishRun(context.Background(), h, &herdr.Client{Commands: &cfoRunner{t: t, pid: os.Getpid()}}, RunRequest{ID: "install-tool", Title: "Install the tool", Shell: "powershell", CommandFile: file})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("a run request from a process that is not the CFO = %v, want it refused", err)
	}
	for _, dir := range []string{"runs-inbox", "runs"} {
		if entries, _ := os.ReadDir(filepath.Join(h.State, dir)); len(entries) != 0 {
			t.Fatalf("%s holds %d entries after a refused request, want none", dir, len(entries))
		}
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
			_, identity, runner, _ := primaryFixture(t, store)
			client := &herdr.Client{Commands: runner}
			file := commandFile(t, command)
			req := RunRequest{ID: "fix-path-" + test.shell, Title: "Put Go on PATH", Shell: test.shell, CommandFile: file}
			if err := PublishRun(context.Background(), h, client, req); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("Remove-Item -Recurse C:\\\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestRuns(); err != nil {
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
			if err := PublishRun(context.Background(), h, client, req); err == nil || !strings.Contains(err.Error(), "already used") {
				t.Fatalf("republishing the ID with other text = %v, want it refused", err)
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
