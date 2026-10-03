package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// asOverlordsBoard makes every request for the switch read as one from a
// board of the Overlord's own: the desktop window he started from the Start
// menu, whose WebView2 holds the connection.
func asOverlordsBoard(s *Service) {
	s.peerOf = func(netip.AddrPort, netip.AddrPort) (int, error) { return 7001, nil }
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		started := time.Now().Add(-time.Hour)
		return []proc.Entry{
			{PID: pid, ParentPID: 6200, ExeBase: "msedgewebview2.exe", Start: started},
			{PID: 6200, ParentPID: 4242, ExeBase: "msedgewebview2.exe", Start: started.Add(-time.Second)},
			{PID: 4242, ParentPID: 900, ExeBase: "goblins-window.exe", Start: started.Add(-time.Minute)},
			{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)},
		}, []string{"USERNAME=overlord"}, nil
	}
}

// boardService is a supervisor a board can ask for the switch.
func boardService(store *Store) *Service {
	return &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, work: make(chan struct{}, 1)}
}

// askTheBoard sends the board's page a request as its own page on this PC
// would, and returns the status and the body.
func askTheBoard(t *testing.T, s *Service, method, path, body string, change func(*http.Request)) (int, string) {
	t.Helper()
	request := httptest.NewRequest(method, "http://"+credentialBoardHost+path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:50000"
	if method != "GET" {
		request.Header.Set("Origin", "http://"+credentialBoardHost)
		request.Header.Set("X-CFO-Token", s.Instance)
		request.Header.Set("Content-Type", "application/json")
	}
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	NewHTTP(s, credentialBoardHost, nil).ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

// The board's switch is the Overlord's as the command is. The supervisor
// finds the program that holds the connection and reads it and its parents:
// a browser or the desktop window he started from the desktop is his, and
// anything that marks an agent's refuses it, as does a board on another
// machine, one behind a proxy and a program it cannot find or read.
func TestOnlyABoardOfTheOverlordsOwnSwitchesAFKMode(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, _, _, _ := primaryFixture(t, store)
	cfo := proc.Entry{PID: primary.Process.PID, ExeBase: "supervisor.test.exe", Start: primary.Process.Start}
	started := primary.Process.Start.Add(time.Millisecond)
	desktop := proc.Entry{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)}
	browser := func(exe string, above ...proc.Entry) []proc.Entry {
		return append([]proc.Entry{
			{PID: 7001, ParentPID: 6200, ExeBase: exe, Start: started.Add(2 * time.Millisecond)},
			{PID: 6200, ParentPID: 4242, ExeBase: exe, Start: started.Add(time.Millisecond)},
		}, above...)
	}
	window := proc.Entry{PID: 4242, ParentPID: 900, ExeBase: "goblins-window.exe", Start: started}
	for _, c := range []struct {
		name     string
		ancestry []proc.Entry
		env      []string
		unread   error
		unfound  error
		// elsewhere makes the request one that is not the board's own page on
		// this PC, which is refused before any program is looked up.
		elsewhere func(*http.Request)
		from      string
		refusal   string
	}{
		{name: "the desktop window he started", ancestry: browser("msedgewebview2.exe", window, desktop), env: []string{"USERNAME=overlord"}, from: "his own board (goblins-window.exe pid 4242)"},
		{name: "a browser he started from the desktop", ancestry: browser("msedge.exe", desktop), env: []string{"USERNAME=overlord"}, from: "his own board (msedge.exe pid 6200)"},
		{name: "a browser he started from his own terminal", ancestry: browser("chrome.exe", proc.Entry{PID: 4242, ParentPID: 900, ExeBase: "powershell.exe", Start: started}, proc.Entry{PID: 900, ExeBase: "WindowsTerminal.exe", Start: started.Add(-time.Hour)}), from: "his own board (powershell.exe pid 4242)"},
		{name: "a browser a test runner started", ancestry: browser("chrome.exe", proc.Entry{PID: 4242, ParentPID: 900, ExeBase: "node.exe", Start: started}, desktop), refusal: "the program that shows this board runs under an agent harness (node.exe pid 4242)"},
		{name: "a browser Claude Code started", ancestry: browser("msedge.exe", proc.Entry{PID: 4242, ParentPID: 900, ExeBase: "claude.exe", Start: started}, desktop), refusal: "under an agent harness (claude.exe pid 4242)"},
		{name: "a browser opened from a goblin's terminal", ancestry: browser("msedge.exe", desktop), env: []string{"CFO_ROLE=goblin"}, refusal: "the program that shows this board runs in a goblin's terminal"},
		{name: "a browser opened from a terminal of the fleet", ancestry: browser("msedge.exe", desktop), env: []string{"CFO_HOST_ID=cfo"}, refusal: "in native terminal cfo"},
		{name: "a browser a gate agent started", ancestry: browser("msedge.exe", desktop), env: []string{"NO_MISTAKES_GATE=1"}, refusal: "as a gate agent"},
		{name: "a browser the registered CFO started", ancestry: browser("msedge.exe", cfo), refusal: "under the registered CFO"},
		{name: "a browser whose harness is gone but left its mark", ancestry: browser("chrome.exe"), env: []string{"CLAUDECODE=1"}, refusal: "an agent harness (its environment carries CLAUDECODE)"},
		// A browser whose opener exited has parents that stop short of the
		// desktop. It may be his own, so the refusal names the way out.
		{name: "a browser whose opener has exited", ancestry: browser("msedge.exe"), env: []string{"USERNAME=overlord"}, refusal: "could not follow its parents to the desktop, as it cannot those of a browser whose opener has since exited"},
		{name: "a program that cannot be read", unread: errors.New("access is denied"), refusal: "could not read the process that asked for it"},
		{name: "a connection Windows does not list", unfound: errors.New("no such connection"), refusal: "could not tell which program shows this board"},
		{name: "a board on another machine", ancestry: browser("msedge.exe", desktop), elsewhere: func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" }, refusal: "not the board's own page on the PC the fleet runs on"},
		{name: "a board behind a proxy", ancestry: browser("msedge.exe", desktop), elsewhere: func(r *http.Request) { r.Header.Set("X-Forwarded-For", "100.64.0.7") }, refusal: "or reached it through a proxy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			lookedUp := false
			s := &Service{Store: store,
				peerOf: func(peer, board netip.AddrPort) (int, error) {
					lookedUp = true
					if peer.String() != "127.0.0.1:50000" || board.String() != credentialBoardHost {
						t.Errorf("asked for the program at %s of a connection to %s, want the request's own end and the board's", peer, board)
					}
					return 7001, c.unfound
				},
				inspectCaller: func(int) ([]proc.Entry, []string, error) { return c.ancestry, c.env, c.unread }}
			request := httptest.NewRequest("POST", "http://"+credentialBoardHost+"/api/afk", nil)
			request.RemoteAddr = "127.0.0.1:50000"
			if c.elsewhere != nil {
				c.elsewhere(request)
			}

			// Act
			from, err := s.overlordsBoard(request, credentialBoardHost, time.Now())

			// Assert
			if c.refusal == "" {
				if err != nil || from != c.from {
					t.Fatalf("overlordsBoard = %q, %v, want %q accepted", from, err, c.from)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.refusal) || !strings.Contains(err.Error(), "he turns it on or off from a terminal or a board of his own, and the registered CFO only at his ask, with his words") {
				t.Fatalf("overlordsBoard = %q, %v, want it refused as %q and saying whose switch it is", from, err, c.refusal)
			}
			if c.elsewhere != nil && lookedUp {
				t.Error("a request that is not the board's own page had its program looked up, want it refused before that")
			}
		})
	}
}

// Windows names the process that owns each end of a connection on this
// machine, and the supervisor's proof of whose board asks stands on reading the
// right end. So the two ends here belong to two processes: a stand-in of this
// test's own making connects to a listener the test holds. The far end is the
// stand-in's, and never this process, which holds the board's end; a lookup
// that read the wrong end would take every board for the supervisor itself.
func TestWindowsNamesTheProcessAtTheOtherEndOfAConnectionToTheBoard(t *testing.T) {
	// Arrange
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	standIn := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-Command", "$held = New-Object Net.Sockets.TcpClient('127.0.0.1', "+port+"); Start-Sleep -Seconds 120")
	standIn.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := standIn.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = standIn.Process.Kill()
		_ = standIn.Wait()
	})
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		t.Fatalf("the stand-in never connected: %v", err)
	}
	defer server.Close()
	peer := netip.MustParseAddrPort(server.RemoteAddr().String())
	board := netip.MustParseAddrPort(server.LocalAddr().String())

	// Act
	pid, err := tcpPeerProcess(peer, board)
	_, unlisted := tcpPeerProcess(netip.AddrPortFrom(peer.Addr(), 1), board)

	// Assert
	if err != nil || pid != standIn.Process.Pid {
		t.Errorf("tcpPeerProcess(%s, %s) = %d, %v, want the stand-in %d that connected, and never this process %d, which holds the board's end", peer, board, pid, err, standIn.Process.Pid, os.Getpid())
	}
	if unlisted == nil {
		t.Error("a port nothing connects from named a process, want an error")
	}
}

// The whole chain over a real connection: the supervisor finds this test's
// own process at the other end and reads it, and a process marked as a
// goblin's is refused. Nothing here stands in for Windows.
func TestARequestFromAGoblinsOwnProcessIsRefusedOverARealConnection(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: NewHTTP(s, listener.Addr().String(), nil)}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	t.Setenv("CFO_ROLE", "goblin")
	request, err := http.NewRequest("POST", "http://"+listener.Addr().String()+"/api/afk", strings.NewReader(`{"on":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://"+listener.Addr().String())
	request.Header.Set("X-CFO-Token", s.Instance)
	request.Header.Set("Content-Type", "application/json")

	// Act
	response, err := http.DefaultClient.Do(request)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var refused struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&refused); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || !strings.Contains(refused.Error, "the program that shows this board runs in a goblin's terminal") {
		t.Fatalf("POST /api/afk from a goblin's process = %d %q, want it refused as a goblin's", response.StatusCode, refused.Error)
	}
	if switched, err := afk.Read(h.State); err != nil || switched.On {
		t.Errorf("the switch = %+v, %v, want it still off", switched, err)
	}
}

// His switch on the board turns AFK mode on with who and when, the CFO is told
// through its wake queue, and the snapshot every board gets says it is on. Off
// keeps the report, which the board's page then reads.
func TestTheBoardsSwitchTurnsAFKModeOnAndOffAndTheBoardIsShownIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	s.Options.Allowance = func(context.Context) ([]afk.Allowance, string) {
		return []afk.Allowance{{Provider: "claude", Window: "week", PercentUsed: 40}}, ""
	}
	asOverlordsBoard(s)
	before, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	unfound, none := askTheBoard(t, s, "GET", "/api/afk/report", "", nil)

	// Act
	onCode, onBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)
	on, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	againCode, _ := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)
	pr := "https://github.com/acme/api/pull/12"
	if _, err := afk.Log(h.State, afk.Entry{Kind: afk.KindMerge, What: pr, Link: pr, Evidence: "gate run 41 passed", Outcome: afk.OutcomeMerged}, time.Now()); err != nil {
		t.Fatal(err)
	}
	offCode, offBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil)
	off, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	offAgainCode, offAgainBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil)
	reportCode, reportBody := askTheBoard(t, s, "GET", "/api/afk/report", "", nil)

	// Assert
	if before.AFK.State != "off" || before.AFK.Held == nil || before.AFK.Report != "" {
		t.Errorf("the snapshot before = %+v, want off with nothing held and no report", before.AFK)
	}
	if unfound != 200 || strings.TrimSpace(none) != `{"found":false}` {
		t.Errorf("the report before any stretch = %d %s, want that there is none", unfound, none)
	}
	if onCode != 200 || strings.TrimSpace(onBody) != `{"state":"on"}` || againCode != 200 {
		t.Fatalf("POST on = %d %s, then %d again, want it turned on and a second on to change nothing", onCode, onBody, againCode)
	}
	if on.AFK.State != "on" || on.AFK.From != "his own board (goblins-window.exe pid 4242)" || on.AFK.Since == nil || time.Since(*on.AFK.Since) > time.Minute {
		t.Errorf("the snapshot while on = %+v, want on just now from his own board", on.AFK)
	}
	if offCode != 200 || strings.TrimSpace(offBody) != `{"state":"off"}` || offAgainCode != 200 || strings.TrimSpace(offAgainBody) != `{"state":"off"}` {
		t.Errorf("POST off = %d %s, then %d %s again, want it turned off and a second off to ask nothing new", offCode, offBody, offAgainCode, offAgainBody)
	}
	switched, err := afk.Read(h.State)
	if err != nil || switched.On || switched.EndedFrom != "his own board (goblins-window.exe pid 4242)" || len(switched.Allowance) != 1 {
		t.Errorf("the switch = %+v, %v, want off from his own board with the allowance read when it turned on", switched, err)
	}
	if off.AFK.State != "off" || off.AFK.Report != switched.Session || off.AFK.Ended == nil || !off.AFK.Ended.Equal(switched.Ended) {
		t.Errorf("the snapshot after = %+v, want off with the report of stretch %s named", off.AFK, switched.Session)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 2 || !strings.Contains(pending[0].Detail, "turned AFK mode on from his own board") || !strings.Contains(pending[1].Detail, "turned AFK mode off from his own board") {
		t.Errorf("the CFO's queue = %+v, %v, want it told of the on and the off, each from his board", pending, err)
	}
	var page struct {
		Found    bool   `json:"found"`
		Session  string `json:"session"`
		Lasted   string `json:"lasted"`
		From     string `json:"from"`
		Sections []struct {
			Title   string      `json:"title"`
			Entries []afk.Entry `json:"entries"`
		} `json:"sections"`
		Finished []afk.Finish `json:"finished"`
		Held     []afk.Held   `json:"held"`
		Spent    []string     `json:"spent"`
		Notes    []string     `json:"notes"`
	}
	if err := json.Unmarshal([]byte(reportBody), &page); reportCode != 200 || err != nil {
		t.Fatalf("GET /api/afk/report = %d %s (%v)", reportCode, reportBody, err)
	}
	if !page.Found || page.Session != switched.Session || page.Lasted != "under a minute" || page.From != "his own board (goblins-window.exe pid 4242)" {
		t.Errorf("the report page = %+v, want the stretch that just ended", page)
	}
	if len(page.Sections) == 0 || page.Sections[0].Title != "Merged" || len(page.Sections[0].Entries) != 1 || page.Sections[0].Entries[0].What != pr || page.Sections[0].Entries[0].Evidence != "gate run 41 passed" {
		t.Errorf("the report's sections = %+v, want the merge under Merged with its evidence", page.Sections)
	}
	// The page reads every list, so one with nothing in it is still a list.
	if page.Finished == nil || page.Held == nil || page.Notes == nil {
		t.Errorf("the report page leaves out a list with nothing in it, want each one there and empty: %s", reportBody)
	}
	if len(page.Spent) != 1 || !strings.Contains(page.Spent[0], "claude week: 40% used when it turned on") {
		t.Errorf("spent = %q, want the reading when it turned on beside the one when it turned off", page.Spent)
	}
}

// A request that is not proven his changes nothing: the switch stays where it
// was, nothing is logged and the CFO is told nothing.
func TestABoardThatIsNotHisIsRefusedAndChangesNothing(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	asOverlordsBoard(s)
	his := s.inspectCaller
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		ancestry, env, err := his(pid)
		ancestry[2].ExeBase = "node.exe"
		return ancestry, env, err
	}

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)

	// Assert
	if code != http.StatusForbidden || !strings.Contains(body, "under an agent harness (node.exe pid 4242)") {
		t.Fatalf("POST /api/afk from a browser an agent started = %d %s, want it refused as an agent's", code, body)
	}
	if switched, err := afk.Read(h.State); err != nil || switched.On {
		t.Errorf("the switch = %+v, %v, want it still off", switched, err)
	}
	if entries := afkEntries(t, h.State); len(entries) != 0 {
		t.Errorf("the AFK log = %+v, want nothing", entries)
	}
	if pending, _ := wake.Pending(h.State); len(pending) != 0 {
		t.Errorf("the CFO's queue = %+v, want nothing", pending)
	}
}

// A body the switch cannot read is refused before anything is proven or
// changed, whoever sent it.
func TestTheBoardsSwitchRefusesABodyItCannotRead(t *testing.T) {
	store, h := testStore(t)
	s := boardService(store)
	asOverlordsBoard(s)
	for name, body := range map[string]string{"no word of on or off": `{}`, "a word that is not a yes or no": `{"on":"yes"}`, "a field it does not take": `{"on":true,"from":"his own terminal"}`, "two requests": `{"on":true}{"on":false}`, "no JSON": `on`} {
		t.Run(name, func(t *testing.T) {
			// Act
			code, answer := askTheBoard(t, s, "POST", "/api/afk", body, nil)

			// Assert
			if code != http.StatusBadRequest {
				t.Errorf("POST /api/afk %s = %d %s, want a bad request", body, code, answer)
			}
			if switched, err := afk.Read(h.State); err != nil || switched.On {
				t.Errorf("the switch = %+v, %v, want it still off", switched, err)
			}
		})
	}
}

// His off on the board resets a switch that cannot be read, as his off in a
// terminal does, and his on is refused with the way back, since it would
// guess at what the switch held. The snapshot says the switch cannot be read
// and never takes it for on.
func TestTheBoardsOffResetsASwitchThatCannotBeReadAndItsOnDoesNot(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	asOverlordsBoard(s)
	if err := os.WriteFile(filepath.Join(h.State, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	onCode, onBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)
	_, stillUnreadable := afk.Read(h.State)
	offCode, offBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil)

	// Assert
	if unreadable.AFK.State != "unreadable" || !strings.Contains(unreadable.AFK.Problem, "cannot be read") || len(unreadable.AFK.Held) != 0 {
		t.Errorf("the snapshot = %+v, want the switch shown as one that cannot be read", unreadable.AFK)
	}
	if onCode != http.StatusConflict || !strings.Contains(onBody, "cannot be read") || stillUnreadable == nil {
		t.Errorf("POST on = %d %s with the switch then reading as %v, want it refused and the switch left as it was", onCode, onBody, stillUnreadable)
	}
	if offCode != 200 || strings.TrimSpace(offBody) != `{"state":"off"}` {
		t.Fatalf("POST off = %d %s, want the switch reset to off", offCode, offBody)
	}
	if switched, err := afk.Read(h.State); err != nil || switched.On {
		t.Errorf("the switch = %+v, %v, want it readable again and off", switched, err)
	}
	if entries := afkEntries(t, h.State); len(entries) != 1 || entries[0].Kind != afk.KindOff || entries[0].What != "his own board (goblins-window.exe pid 4242)" {
		t.Errorf("the AFK log = %+v, want the one line of the reset, from his board", entries)
	}
}

// While AFK mode is on the snapshot says how many decisions the CFO logged
// and lists what is held for the Overlord, each with what became of it and
// what its goblin did meanwhile. What the CFO answered is counted as decided
// and is not listed as held.
func TestTheSnapshotShowsWhatWasDecidedAndWhatIsHeldWhileAFKModeIsOn(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	if _, _, err := afk.TurnOn(h.State, "his own board (goblins-window.exe pid 4242)", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	waitingItems(t, store)
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []afk.Entry{
		{Kind: afk.KindAnswer, What: "drop-legacy-invoices", Evidence: "asked: Apply it? answered: Keep it held"},
		{Kind: afk.KindDeploy, What: "acme production", Evidence: "/health reads 200"},
	} {
		if err := s.logAFKDecision(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.AppendStatus(h.State, "task-1", "working: the invoice export"); err != nil {
		t.Fatal(err)
	}
	if err := store.withdrawReview("waiting-task-1-7", "task-1 reported again"); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := s.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	view := snapshot.AFK
	if view.State != "on" || view.Decided != 2 || view.From != "his own board (goblins-window.exe pid 4242)" || view.Report != "" {
		t.Errorf("the snapshot = %+v, want on from his board with two decisions", view)
	}
	held := map[string]afk.Held{}
	for _, one := range view.Held {
		held[one.Item] = one
	}
	if len(view.Held) != 2 || len(held) != 2 {
		t.Fatalf("held = %+v, want the goblin's wait and the command left for him, and not the question the CFO answered", view.Held)
	}
	if command := held["run:delete-merged-branches"]; !command.Waiting || command.Now != "still waiting for you to run it" || command.What != "Run delete-merged-branches" || command.Task != "" {
		t.Errorf("the held command = %+v, want the CFO's own, still waiting on him", command)
	}
	if wait := held["review:waiting-task-1-7"]; wait.Waiting || !strings.Contains(wait.Now, "withdrawn: task-1 reported again") || wait.Meanwhile != "working: the invoice export" || wait.Task != "task-1" {
		t.Errorf("the held wait = %+v, want it withdrawn, with what its goblin did meanwhile", wait)
	}
	data, err := json.Marshal(snapshot)
	if err != nil || !strings.Contains(string(data), `"afk":{"state":"on"`) {
		t.Errorf("the snapshot's JSON = %v, want the afk field the board reads", err)
	}
}

// A command the board made itself, for a credential card's terminal or a
// connection's repair, is ready only for the moment after the Overlord's own
// click. It never waited on him, so it is not held.
func TestACommandTheBoardMadeItselfIsNotHeld(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	now := time.Now().UTC()
	readyRun(t, store, strings.Repeat("c", 64), "left-for-him", "powershell", false, now)
	readyRun(t, store, strings.Repeat("c", 64), "credential-terminal", "powershell", false, now)
	readyRun(t, store, strings.Repeat("c", 64), "connection-repair", "powershell", false, now)
	store.mu.Lock()
	for i := range store.db.Runs {
		switch store.db.Runs[i].ID {
		case "credential-terminal":
			store.db.Runs[i].CredentialRequest = "request-1"
		case "connection-repair":
			store.db.Runs[i].ConnectionTask = "task-1"
		}
	}
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, now); err != nil {
		t.Fatal(err)
	}

	// Act
	err = s.holdForOverlord(now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var held []string
	for _, entry := range afkEntries(t, h.State) {
		if entry.Kind == afk.KindHeld {
			held = append(held, entry.Item)
		}
	}
	if len(held) != 1 || held[0] != "run:left-for-him" {
		t.Errorf("held = %q, want only the command the CFO left for him", held)
	}
}

// The report read after AFK mode turned off shows each item it held as it
// stands now, on the board's page and to cfo afk report alike, while the
// report kept when the stretch ended still says what became of each by then.
func TestTheReportReadLaterShowsEachHeldItemAsItStandsNow(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	asOverlordsBoard(s)
	waitingItems(t, store)
	if code, body := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil); code != 200 {
		t.Fatalf("POST on = %d %s", code, body)
	}
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, body := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil); code != 200 {
		t.Fatalf("POST off = %d %s", code, body)
	}
	held := func() map[string]afk.Held {
		t.Helper()
		code, body := askTheBoard(t, s, "GET", "/api/afk/report", "", nil)
		var page struct {
			Held []afk.Held `json:"held"`
		}
		if err := json.Unmarshal([]byte(body), &page); code != 200 || err != nil {
			t.Fatalf("GET /api/afk/report = %d %s (%v)", code, body, err)
		}
		report, found, err := ReadAFKReport(h)
		if err != nil || !found {
			t.Fatalf("ReadAFKReport = %v, %v, want the report", found, err)
		}
		byItem := map[string]afk.Held{}
		for i, one := range page.Held {
			if report.Held[i] != one {
				t.Errorf("cfo afk report reads %+v where the board's page reads %+v", report.Held[i], one)
			}
			byItem[one.Item] = one
		}
		return byItem
	}
	before := held()

	// Act: he answers the question and the wait is withdrawn after the off.
	answered := time.Now().UTC()
	store.mu.Lock()
	store.db.Questions[0].Status, store.db.Questions[0].AnsweredBy, store.db.Questions[0].Answer, store.db.Questions[0].AnsweredAt = "succeeded", "overlord", "Keep it held", &answered
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.withdrawReview("waiting-task-1-7", "task-1 reported again: working"); err != nil {
		t.Fatal(err)
	}
	after := held()

	// Assert
	for _, item := range []string{"question:drop-legacy-invoices", "review:waiting-task-1-7", "run:delete-merged-branches"} {
		if one := before[item]; !one.Waiting || !strings.HasPrefix(one.Now, "still waiting") {
			t.Errorf("before he acted, %s = %+v, want it still waiting on him", item, one)
		}
	}
	if question := after["question:drop-legacy-invoices"]; question.Waiting || question.Now != "you answered it: Keep it held" {
		t.Errorf("the question after his answer = %+v, want that he answered it", question)
	}
	if wait := after["review:waiting-task-1-7"]; wait.Waiting || wait.Now != "withdrawn: task-1 reported again: working" {
		t.Errorf("the wait after it was withdrawn = %+v, want it withdrawn", wait)
	}
	if run := after["run:delete-merged-branches"]; !run.Waiting || run.Now != "still waiting for you to run it" {
		t.Errorf("the run nobody touched = %+v, want it still waiting", run)
	}
	kept, _, err := afk.ReadReport(h.State)
	if err != nil || len(kept.Held) != 3 || slices.ContainsFunc(kept.Held, func(one afk.Held) bool { return !one.Waiting }) {
		t.Errorf("the report kept = %+v, %v, want each item as it stood when AFK mode turned off", kept.Held, err)
	}
}

// What became of a held item that cannot be read is an error, never the
// report's older word for it.
func TestTheReportWhoseHeldItemsCannotBeReadIsRefused(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	waitingItems(t, store)
	if err := afk.SaveReport(h.State, afk.Report{Session: "afk-1", Held: []afk.Held{{Item: "question:drop-legacy-invoices", What: "Apply it?", Waiting: true, Now: "still waiting on you"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, ".supervisor.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	_, found, err := ReadAFKReport(h)

	// Assert
	if err == nil || found || !strings.Contains(err.Error(), "what became of each item held for him cannot be read") {
		t.Errorf("ReadAFKReport = %v, %v, want it refused for want of the items' records", found, err)
	}
}

func TestTheBoardReadsAnAFKSwitchAgainOnlyWhenItsFileChanges(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	now := time.Now().UTC()
	steps := []struct {
		name   string
		change func() error
		state  string
		reads  int64
	}{
		{"missing", func() error { return nil }, "off", 0},
		{"on", func() error { _, _, err := afk.TurnOn(h.State, "his board", nil, now); return err }, "on", 0},
		{"off", func() error { _, err := afk.TurnOff(h.State, "his board", now.Add(time.Minute)); return err }, "off", 0},
		{"on again", func() error {
			_, _, err := afk.TurnOn(h.State, "his board", nil, now.Add(2*time.Minute))
			return err
		}, "on", 0},
		{"removed", func() error { return os.Remove(filepath.Join(h.State, "afk.json")) }, "off", 0},
		{"unreadable", func() error { return os.WriteFile(filepath.Join(h.State, "afk.json"), []byte("{"), 0o600) }, "unreadable", 1},
		{"reset", func() error { return afk.Reset(h.State, "his board", errors.New("unreadable"), now.Add(2*time.Minute)) }, "off", 0},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if err := step.change(); err != nil {
				t.Fatal(err)
			}
			view, err := s.afkView(store.Snapshot())
			if err != nil || view.State != step.state {
				t.Fatalf("changed switch reads %+v, %v, want %s", view, err, step.state)
			}
			opened := fsx.Opens()

			// Act
			again, err := s.afkView(store.Snapshot())

			// Assert
			if err != nil || again.State != step.state {
				t.Errorf("unchanged switch reads %+v, %v, want %s", again, err, step.state)
			}
			if count := fsx.Opens() - opened; count != step.reads {
				t.Errorf("unchanged %s switch opened %d files, want %d", step.name, count, step.reads)
			}
		})
	}
}
