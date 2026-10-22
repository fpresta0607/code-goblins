package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

// serve is the Tailscale serve command the Tailscale row names on a PC whose
// tailnet has not approved HTTPS, and scrawlOff the one the Scrawl row names
// for a route of the owner's own straight to Scrawl on 4388.
const (
	serve     = "tailscale serve --bg --https=4311 http://127.0.0.1:4310"
	scrawlOff = "tailscale serve --https=4388 off"
)

// fixBoard is a board whose remote access is off on a PC whose Tailscale has
// not approved HTTPS, so its Tailscale row names the first serve to run.
func fixBoard(t *testing.T, options Options) (*Service, *HTTP, *remoteCommands) {
	t.Helper()
	store, h := testStore(t)
	commands := &remoteCommands{}
	options.RemoteAccess = &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: commands}
	s := &Service{Store: store, Instance: "instance", Options: options, subscribers: map[chan struct{}]struct{}{}}
	return s, NewHTTP(s, "127.0.0.1:4310", nil), commands
}

// bypassScrawl turns remote access on and adds a route of the owner's own
// straight to Scrawl, so the Scrawl row names the command that removes it.
func bypassScrawl(t *testing.T, s *Service, commands *remoteCommands) {
	t.Helper()
	t.Setenv("LAVISH_AXI_PORT", "4387")
	if _, err := s.Options.RemoteAccess.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands.config = `{"TCP":{"4311":{"HTTPS":true},"4388":{"HTTPS":true}},"Web":{"pc.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}},"pc.example.ts.net:4388":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4387"}}}}}`
}

// pressFix posts body to the setting the way the board's own page on this PC
// does.
func pressFix(board *HTTP, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/remote", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", "instance")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

// fixBody is the press of a row's play button under the command it shows.
func fixBody(row, command string) string {
	body, _ := json.Marshal(map[string]string{"fix": row, "command": command})
	return string(body)
}

// The Overlord, 2026-10-18: a command that mends a row of Remote access has
// "a play button that runs it directly in a PowerShell window, the same way a
// Command Center run card runs a command. It stays in the section". It runs
// the command the row names now, never anything the browser sends, in a
// terminal of its own whose PATH is read as a new sign-in would, so a
// Tailscale installed since the supervisor started is found, and the answer
// names its run.
func TestAFixRunsTheCommandItsRowNamesInATerminalOfItsOwn(t *testing.T) {
	for _, c := range []struct {
		row, command string
		isBypassed   bool
	}{
		{row: "tailscale", command: serve},
		{row: "scrawl", command: scrawlOff, isBypassed: true},
	} {
		t.Run(c.row, func(t *testing.T) {
			// Arrange
			launcher := &fakeRunLauncher{started: liveStart(t)}
			s, board, commands := fixBoard(t, Options{Runs: launcher})
			if c.isBypassed {
				bypassScrawl(t, s, commands)
			}

			// Act
			response := pressFix(board, fixBody(c.row, c.command))
			err := s.Store.ProcessOne(context.Background(), s.execute)

			// Assert
			var answer struct {
				RunID string `json:"run_id"`
			}
			if response.Code != http.StatusOK || err != nil || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
				t.Fatalf("the press = %d %s, then %v; want its command started", response.Code, response.Body, err)
			}
			runs := s.Store.Snapshot().Runs
			if len(runs) != 1 || runs[0].ID != answer.RunID || runs[0].RemoteFix != c.row || runs[0].Shell != "powershell" || runs[0].State != "running" {
				t.Fatalf("runs = %+v, answer named %q; want the one it names running in PowerShell for the %s row", runs, answer.RunID, c.row)
			}
			for _, line := range []string{"$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')", c.command, "exit $LASTEXITCODE"} {
				if !strings.Contains(runs[0].Command, "\n"+line+"\n") {
					t.Errorf("the script\n%s\nlacks the line %q", runs[0].Command, line)
				}
			}
			if launches := launcher.all(); len(launches) != 1 || launches[0].Script != filepath.Join(runDir(s.Store.Home.State, runs[0]), "command.ps1") || launches[0].Hidden || launches[0].Admin {
				t.Errorf("launched %+v, want its own script in a terminal he sees", launches)
			}
		})
	}
}

// A fix runs only from the board on this PC, only for a row that names a
// command now and only while it is the command the board showed, and one at a
// time; anything else is refused with nothing run.
func TestAFixIsRefusedWhereItCannotRun(t *testing.T) {
	for _, c := range []struct {
		name     string
		body     string
		cannot   bool
		earlier  bool
		phone    bool
		accessOn bool
		want     int
		says     string
	}{
		{name: "from a phone", body: fixBody("tailscale", serve), phone: true, want: http.StatusForbidden, says: "Run it from the board on this PC"},
		{name: "a row the setting has not", body: fixBody("router", serve), want: http.StatusBadRequest, says: "Name the row to mend"},
		{name: "with no command shown", body: `{"fix":"tailscale"}`, want: http.StatusBadRequest, says: "which row's command to run"},
		{name: "beside a switch", body: `{"fix":"tailscale","command":"` + serve + `","on":true}`, want: http.StatusBadRequest, says: "which row's command to run"},
		{name: "a command that is not the row's now", body: fixBody("tailscale", "winget install --exact --id Tailscale.Tailscale"), want: http.StatusConflict, says: "The command changed"},
		{name: "a row with nothing to mend", body: fixBody("tailscale", serve), accessOn: true, want: http.StatusConflict, says: "Nothing there needs mending now"},
		{name: "a board that opens no terminal", body: fixBody("tailscale", serve), cannot: true, want: http.StatusServiceUnavailable, says: "cannot open a terminal"},
		{name: "while another runs", body: fixBody("tailscale", serve), earlier: true, want: http.StatusConflict, says: "still runs"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			options := Options{Runs: &fakeRunLauncher{started: liveStart(t)}}
			if c.cannot {
				options.Runs = nil
			}
			s, board, _ := fixBoard(t, options)
			if c.accessOn {
				if _, err := s.Options.RemoteAccess.Enable(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if c.earlier {
				if response := pressFix(board, fixBody("tailscale", serve)); response.Code != http.StatusOK {
					t.Fatalf("the first press = %d %s", response.Code, response.Body)
				}
			}
			before := len(s.Store.Snapshot().Runs)
			var handler http.Handler = board
			request := httptest.NewRequest("POST", "http://"+board.Host+"/api/remote", strings.NewReader(c.body))
			request.Header.Set("Origin", "http://"+board.Host)
			if c.phone {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if _, err := s.Options.RemoteAccess.Enable(context.Background()); err != nil {
					t.Fatal(err)
				}
				handler = remoteaccess.NewGate(ctx, s.Options.RemoteAccess, board)
				request = httptest.NewRequest("POST", "https://pc.example.ts.net:4311/api/remote", strings.NewReader(c.body))
				request.Header.Set("Origin", "https://pc.example.ts.net:4311")
				request.Header.Set("Tailscale-User-Login", "owner@example.com")
			}
			request.RemoteAddr = "127.0.0.1:50000"
			request.Header.Set("X-CFO-Token", "instance")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			// Act
			handler.ServeHTTP(response, request)

			// Assert
			if response.Code != c.want || !strings.Contains(response.Body.String(), c.says) {
				t.Fatalf("the press = %d %s, want %d saying %q", response.Code, response.Body, c.want, c.says)
			}
			if after := len(s.Store.Snapshot().Runs); after != before {
				t.Errorf("run items went from %d to %d, want nothing new", before, after)
			}
		})
	}
}

// A fix whose start could not be queued never ran and holds nothing up: the
// next press runs.
func TestAFixThatNeverStartedHoldsNothingUp(t *testing.T) {
	// Arrange
	s, board, _ := fixBoard(t, Options{Runs: &fakeRunLauncher{started: liveStart(t)}})
	s.Store.mu.Lock()
	for i := range maxActions {
		s.Store.db.Actions = append(s.Store.db.Actions, Action{ID: "waiting-" + strings.Repeat("a", i+1), Kind: "evaluate", Status: "queued", CreatedAt: time.Now().UTC()})
	}
	s.Store.mu.Unlock()
	if response := pressFix(board, fixBody("tailscale", serve)); response.Code != http.StatusConflict {
		t.Fatalf("the press with the queue full = %d %s, want it refused", response.Code, response.Body)
	}
	s.Store.mu.Lock()
	s.Store.db.Actions = nil
	s.Store.mu.Unlock()

	// Act
	response := pressFix(board, fixBody("tailscale", serve))

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("the next press = %d %s, want it run", response.Code, response.Body)
	}
	if runs := s.Store.Snapshot().Runs; len(runs) != 2 || runs[0].State != "ready" || runs[1].State != "running" {
		t.Errorf("runs = %+v, want the one never started still ready and the next running", runs)
	}
}

// A fix is the board's own, made at his click: no Skip or answer in words
// takes it, as none of his items that the board made does.
func TestAFixIsNotOneToSkip(t *testing.T) {
	// Arrange
	s, board, _ := fixBoard(t, Options{Runs: &fakeRunLauncher{started: liveStart(t)}})
	if response := pressFix(board, fixBody("tailscale", serve)); response.Code != http.StatusOK {
		t.Fatalf("the press = %d %s", response.Code, response.Body)
	}
	r := s.Store.Snapshot().Runs[0]

	// Act
	_, err := s.Store.Queue(Action{ID: "skip-fix", Kind: "run_skip", RunID: r.ID, Generation: r.Identity})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "the board's own") {
		t.Errorf("a skip of the fix = %v, want it refused as the board's own", err)
	}
}

// A fix ends on the setting that ran it, which reads its row again. Nobody
// asked for it, so the CFO is not told how it ended.
func TestAFixEndsWithoutTellingTheCFO(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, _, terminal, connection := primaryFixture(t, store)
	s := &Service{Store: store, Instance: "instance", subscribers: map[chan struct{}]struct{}{}, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)},
		RemoteAccess: &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: &remoteCommands{}}}}
	if response := pressFix(NewHTTP(s, "127.0.0.1:4310", nil), fixBody("tailscale", serve)); response.Code != http.StatusOK {
		t.Fatalf("the press = %d %s", response.Code, response.Body)
	}
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	r := store.Snapshot().Runs[0]
	if err := os.WriteFile(filepath.Join(runDir(h.State, r), "exit.txt"), []byte("0"), 0600); err != nil {
		t.Fatal(err)
	}

	// Act
	err := s.finishRuns(context.Background())

	// Assert
	if ended := store.Snapshot().Runs[0]; err != nil || ended.State != "succeeded" {
		t.Fatalf("the fix = %+v, %v; want it succeeded", ended, err)
	}
	if typed := terminal.lines(t); len(typed) != 0 {
		t.Errorf("the CFO was told %q, want nothing", typed)
	}
}
