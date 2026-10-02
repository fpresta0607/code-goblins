package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// asOverlordsTerminal makes every pipe client read as a terminal of the
// Overlord's own: his shell, with no agent above it and nothing of the fleet
// in its environment. A test names it because its own process runs under go
// test, and often under a goblin's harness, which the supervisor refuses.
func asOverlordsTerminal(s *Service) {
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		started := time.Now().Add(-time.Hour)
		return []proc.Entry{
			{PID: pid, ParentPID: 4242, ExeBase: "cfo.exe", Start: started},
			{PID: 4242, ParentPID: 900, ExeBase: "powershell.exe", Start: started.Add(-time.Minute)},
			{PID: 900, ExeBase: "WindowsTerminal.exe", Start: started.Add(-time.Hour)},
		}, []string{"USERNAME=overlord"}, nil
	}
}

// asGoblinsTerminal makes every pipe client read as a command a goblin runs.
func asGoblinsTerminal(s *Service) {
	asOverlordsTerminal(s)
	clean := s.inspectCaller
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		ancestry, env, err := clean(pid)
		return ancestry, append(env, "CFO_ROLE=goblin"), err
	}
}

func askToAnnounce(t *testing.T, s *Service, body string) []string {
	t.Helper()
	request := httptest.NewRequest("POST", "http://board.local/api/announce", strings.NewReader(body))
	request.Header.Set("Origin", "http://board.local")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CFO-Token", s.Instance)
	response := httptest.NewRecorder()
	NewHTTP(s, "board.local", nil).ServeHTTP(response, request)
	var result struct {
		Claimed []string `json:"claimed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); response.Code != 200 || err != nil {
		t.Fatalf("POST /api/announce = %d %s (%v)", response.Code, response.Body.String(), err)
	}
	return result.Claimed
}

func afkEntries(t *testing.T, stateDir string) []afk.Entry {
	t.Helper()
	entries, unreadable, err := afk.Entries(stateDir, "")
	if err != nil || unreadable != 0 {
		t.Fatalf("the AFK log: %d unreadable lines, %v", unreadable, err)
	}
	return entries
}

// AFK mode is the Overlord's switch. The supervisor reads the process at the
// other end of its pipe, and anything that marks it as an agent's refuses the
// switch: a goblin's or the CFO's terminal, a gate agent, an agent harness
// above it. A process it cannot read is refused too, never taken for his.
func TestOnlyATerminalOfTheOverlordsOwnSwitchesAFKMode(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, _, _, _ := primaryFixture(t, store)
	cfo := proc.Entry{PID: primary.Process.PID, ExeBase: "supervisor.test.exe", Start: primary.Process.Start}
	shell := func(above ...proc.Entry) []proc.Entry {
		return append([]proc.Entry{
			{PID: 7001, ParentPID: 4242, ExeBase: "cfo.exe", Start: cfo.Start.Add(4 * time.Millisecond)},
			{PID: 4242, ParentPID: 900, ExeBase: "powershell.exe", Start: cfo.Start.Add(2 * time.Millisecond)},
		}, above...)
	}
	above := func(exe string) proc.Entry {
		return proc.Entry{PID: 900, ExeBase: exe, Start: cfo.Start.Add(-time.Hour)}
	}
	late := shell(above("WindowsTerminal.exe"))
	late[0].Start = time.Now().Add(time.Hour)
	for _, c := range []struct {
		name     string
		ancestry []proc.Entry
		env      []string
		unread   error
		from     string
		refusal  string
	}{
		{name: "his own shell", ancestry: shell(above("WindowsTerminal.exe")), env: []string{"USERNAME=overlord"}, from: "his own terminal (powershell.exe pid 4242)"},
		{name: "a goblin's terminal", ancestry: shell(above("WindowsTerminal.exe")), env: []string{"CFO_ROLE=goblin"}, refusal: "a goblin's terminal"},
		{name: "a gate agent", ancestry: shell(above("WindowsTerminal.exe")), env: []string{"NO_MISTAKES_GATE=1"}, refusal: "a gate agent"},
		{name: "the registered CFO", ancestry: shell(cfo), refusal: "under the registered CFO"},
		{name: "a native terminal of the fleet", ancestry: shell(above("WindowsTerminal.exe")), env: []string{"cfo_host_id=cfo"}, refusal: "native terminal cfo"},
		{name: "a Herdr pane", ancestry: shell(above("WindowsTerminal.exe")), env: []string{"HERDR_PANE_ID=w1:p2"}, refusal: "a Herdr pane"},
		{name: "under Claude Code", ancestry: shell(above("claude.exe")), refusal: "an agent harness (claude.exe pid 900)"},
		{name: "under a harness that runs on node", ancestry: shell(above("node.exe")), refusal: "an agent harness (node.exe pid 900)"},
		{name: "under Codex", ancestry: shell(above("Codex.exe")), refusal: "an agent harness (Codex.exe pid 900)"},
		// Git Bash's timeout replaces its own process, so a command an agent
		// runs under it has a parent that already exited and no harness left
		// among its ancestors; what its harness put in its environment remains.
		{name: "under Claude Code with its parents cut off", ancestry: shell()[:1], env: []string{"CLAUDECODE=1"}, refusal: "an agent harness (its environment carries CLAUDECODE)"},
		{name: "an agent's environment with its parents cut off", ancestry: shell()[:1], env: []string{"AI_AGENT=claude-code"}, refusal: "an agent harness (its environment carries AI_AGENT)"},
		// Git Bash's env cuts the parents the same way and can remove those
		// variables too, so nothing is left that marks the agent. Parents that
		// stop short of the desktop are parents the supervisor could not read.
		{name: "its parents cut off and nothing of a harness in its environment", ancestry: []proc.Entry{
			{PID: 7001, ParentPID: 4242, ExeBase: "cfo.exe", Start: cfo.Start.Add(4 * time.Millisecond)},
			{PID: 4242, ParentPID: 900, ExeBase: "env.exe", Start: cfo.Start.Add(2 * time.Millisecond)},
		}, env: []string{"USERNAME=overlord"}, refusal: "could not follow its parents to the desktop"},
		{name: "shells above it whose parents are cut off", ancestry: shell(above("bash.exe")), env: []string{"USERNAME=overlord"}, refusal: "could not follow its parents to the desktop"},
		{name: "his own shell opened from the desktop", ancestry: shell(above("Explorer.EXE")), env: []string{"USERNAME=overlord"}, from: "his own terminal (powershell.exe pid 4242)"},
		// A terminal run as administrator is one the supervisor cannot read, and
		// it is the Overlord himself who meets this refusal, so it names the way out.
		{name: "a process that cannot be read", unread: errors.New("access is denied"), refusal: "could not read the process that asked for it, as it cannot one run as administrator"},
		{name: "a process that is gone", refusal: "could not read the process"},
		{name: "a process that started after the request", ancestry: late, refusal: "could not read the process"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &Service{Store: store, inspectCaller: func(int) ([]proc.Entry, []string, error) { return c.ancestry, c.env, c.unread }}

			// Act
			from, err := s.overlordsTerminal(7001, time.Now())

			// Assert
			if c.refusal == "" {
				if err != nil || from != c.from {
					t.Fatalf("overlordsTerminal = %q, %v, want %q accepted", from, err, c.from)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.refusal) || !strings.Contains(err.Error(), "only he turns it on or off") {
				t.Fatalf("overlordsTerminal = %q, %v, want it refused as %q and saying whose switch it is", from, err, c.refusal)
			}
		})
	}
}

// His switch over the pipe turns AFK mode on with who, when and the allowance
// read then, and the CFO is told through its wake queue.
func TestTheOverlordsSwitchTurnsAFKModeOnAndTheCFOIsToldInItsQueue(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store, Options: Options{Allowance: func(context.Context) ([]afk.Allowance, string) {
		return []afk.Allowance{{Provider: "claude", Window: "week", PercentUsed: 40}}, ""
	}}}
	asOverlordsTerminal(s)
	runPipe(t, s)

	// Act
	err := SwitchAFK(h, true)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	switched, err := afk.Read(h.State)
	if err != nil || !switched.On || switched.From != "his own terminal (powershell.exe pid 4242)" || len(switched.Allowance) != 1 || time.Since(switched.Since) > time.Minute {
		t.Errorf("the switch = %+v, %v, want on just now from his own terminal with the allowance read", switched, err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 1 || pending[0].Kind != "review" || pending[0].Key != "afk" || !strings.Contains(pending[0].Detail, "turned AFK mode on") {
		t.Errorf("the CFO's queue = %+v, %v, want one notice that the Overlord turned AFK mode on", pending, err)
	}
	if entries := afkEntries(t, h.State); len(entries) != 1 || entries[0].Kind != afk.KindOn {
		t.Errorf("the AFK log = %+v, want the line that turned it on", entries)
	}
}

// A goblin's attempt and the CFO's are refused by the process each runs in,
// read from the real process at the other end of the pipe, and change nothing.
func TestAGoblinsOrTheCFOsAttemptToTurnAFKModeOnIsRefusedAndChangesNothing(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(t *testing.T, store *Store)
		refusal string
	}{
		{name: "a goblin", refusal: "a goblin's terminal", arrange: func(t *testing.T, _ *Store) {
			t.Setenv("CFO_ROLE", "goblin")
		}},
		{name: "the registered CFO", refusal: "under the registered CFO", arrange: func(t *testing.T, store *Store) {
			// This test process is the registered CFO, and nothing else marks it.
			primaryFixture(t, store)
			for _, name := range []string{"CFO_ROLE", "NO_MISTAKES_GATE"} {
				t.Setenv(name, "")
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			c.arrange(t, store)
			runPipe(t, &Service{Store: store})

			// Act
			err := SwitchAFK(h, true)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("SwitchAFK = %v, want it refused as %q", err, c.refusal)
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
		})
	}
}

// Off is his switch as much as on: an agent that turned it off would bring
// the prompts back while he is away and end the stretch its report covers.
func TestOnlyTheOverlordTurnsAFKModeOff(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	asOverlordsTerminal(s)
	runPipe(t, s)
	if err := SwitchAFK(h, true); err != nil {
		t.Fatal(err)
	}
	asGoblinsTerminal(s)

	// Act
	err := SwitchAFK(h, false)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "a goblin's terminal") {
		t.Fatalf("a goblin turning AFK mode off = %v, want it refused", err)
	}
	if switched, err := afk.Read(h.State); err != nil || !switched.On {
		t.Errorf("the switch = %+v, %v, want it still on", switched, err)
	}
}

// The CFO logs what it decided under the authority over the pipe, where the
// supervisor proves the sender is the registered CFO. Nobody else's line is
// written, and nothing is logged as decided while AFK mode is off.
func TestOnlyTheRegisteredCFOsDecisionIsLoggedAndOnlyWhileAFKModeIsOn(t *testing.T) {
	deploy := afk.Entry{Kind: afk.KindDeploy, What: "acme production", Link: "https://acme.example/health", Evidence: "/health reads 200 with commit abc1234"}

	t.Run("the registered CFO while it is on", func(t *testing.T) {
		// Arrange
		store, h := testStore(t)
		_, _, _, cfo := primaryFixture(t, store)
		servePipe(t, store, cfo)
		if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
			t.Fatal(err)
		}

		// Act
		err := LogAFKDecision(h, deploy)

		// Assert
		entries := afkEntries(t, h.State)
		if err != nil || len(entries) != 2 || entries[1].Kind != afk.KindDeploy || entries[1].What != deploy.What || entries[1].Evidence != deploy.Evidence || entries[1].Link != deploy.Link {
			t.Fatalf("LogAFKDecision = %v, log %+v, want the deploy logged after the switch", err, entries)
		}
	})

	t.Run("the registered CFO while it is off", func(t *testing.T) {
		// Arrange
		store, h := testStore(t)
		_, _, _, cfo := primaryFixture(t, store)
		servePipe(t, store, cfo)

		// Act
		err := LogAFKDecision(h, deploy)

		// Assert
		if err == nil || !strings.Contains(err.Error(), "AFK mode is not on") {
			t.Fatalf("LogAFKDecision while off = %v, want it refused", err)
		}
		if entries := afkEntries(t, h.State); len(entries) != 0 {
			t.Errorf("the AFK log = %+v, want nothing", entries)
		}
	})

	t.Run("a process that is not the registered CFO", func(t *testing.T) {
		// Arrange
		store, h := testStore(t)
		_, _, _, cfo := primaryFixture(t, store)
		servePipe(t, store, cfo)
		if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
			t.Fatal(err)
		}
		// The registration names a process this test does not run under.
		registerElsewhere(t, h.State)

		// Act
		err := LogAFKDecision(h, deploy)

		// Assert
		if err == nil || !strings.Contains(err.Error(), "does not run under the registered CFO") {
			t.Fatalf("LogAFKDecision from outside the CFO = %v, want it refused", err)
		}
		if entries := afkEntries(t, h.State); len(entries) != 1 {
			t.Errorf("the AFK log = %+v, want only the switch", entries)
		}
	})
}

// registerElsewhere rewrites primary.json to name a live process this test
// does not run under, one of its own making that it ends.
func registerElsewhere(t *testing.T, stateDir string) {
	t.Helper()
	standIn := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-Command", "Start-Sleep -Seconds 120")
	standIn.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := standIn.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = standIn.Process.Kill()
		_ = standIn.Wait()
	})
	elsewhere := standIn.Process.Pid
	entries, err := proc.Ancestry(elsewhere, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the stand-in process's start time: %v %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var primary primaryRegistration
	if err := json.Unmarshal(data, &primary); err != nil {
		t.Fatal(err)
	}
	primary.Process.PID, primary.Process.OwnerPID, primary.Process.Start = elsewhere, elsewhere, entries[0].Start
	if data, err = json.Marshal(primary); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// While AFK mode is on the board is handed nothing to announce, so it shows
// no alert, sends no Windows notification and never opens the Command Center
// by itself. What it asked about then is not announced once he is back
// either, and what arrives after he is back alerts as usual.
func TestWhileAFKModeIsOnTheBoardIsHandedNothingToAnnounce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store, Instance: "test-instance"}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	away := askToAnnounce(t, s, `{"keys":["alert:question:q1","open:question:q1"],"news":["alert:task:a:g1:done:pr7"]}`)
	if _, err := afk.TurnOff(h.State, "the board", time.Now()); err != nil {
		t.Fatal(err)
	}
	back := askToAnnounce(t, s, `{"keys":["alert:question:q1","open:question:q1","alert:question:q2"],"news":["alert:task:a:g1:done:pr7"]}`)

	// Assert
	if len(away) != 0 {
		t.Errorf("claimed while AFK mode was on = %q, want nothing", away)
	}
	if !slices.Equal(back, []string{"alert:question:q2"}) {
		t.Errorf("claimed once he was back = %q, want only the item that arrived since", back)
	}
}

// A switch that cannot be read is not taken for on: the board announces as
// usual, so nothing that needs him is hidden by a guess, and the recovery
// cycle says the switch is unreadable.
func TestAnUnreadableSwitchHidesNothingAndIsReported(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, work: make(chan struct{}, 1)}
	if err := os.WriteFile(filepath.Join(h.State, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	claimed := askToAnnounce(t, s, `{"keys":["alert:question:q1"]}`)
	s.cycle(context.Background(), true)

	// Assert
	if !slices.Equal(claimed, []string{"alert:question:q1"}) {
		t.Errorf("claimed = %q, want the item announced as usual", claimed)
	}
	s.mu.Lock()
	reported := s.lastError
	s.mu.Unlock()
	if !strings.Contains(reported, "AFK mode's switch") {
		t.Errorf("the board's error = %q, want the unreadable switch named", reported)
	}
}

// His off always works. A switch that cannot be read is put back to off from
// a terminal of his own, the CFO is told there is no report of that stretch,
// and nothing else changes it: not an agent's off, and not his own on, which
// would guess at what the switch held.
func TestOnlyTheOverlordsOffResetsASwitchThatCannotBeRead(t *testing.T) {
	unreadable := func(t *testing.T, stateDir string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(stateDir, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("his off resets it", func(t *testing.T) {
		// Arrange
		store, h := testStore(t)
		s := &Service{Store: store}
		asOverlordsTerminal(s)
		runPipe(t, s)
		unreadable(t, h.State)

		// Act
		err := SwitchAFK(h, false)

		// Assert
		if err != nil {
			t.Fatal(err)
		}
		if switched, err := afk.Read(h.State); err != nil || switched.On {
			t.Errorf("the switch = %+v, %v, want it readable again and off", switched, err)
		}
		entries := afkEntries(t, h.State)
		if len(entries) != 1 || entries[0].Kind != afk.KindOff || entries[0].What != "his own terminal (powershell.exe pid 4242)" || !strings.Contains(entries[0].Evidence, "could not be read") {
			t.Errorf("the AFK log = %+v, want the one line of the reset, from his terminal, saying why", entries)
		}
		pending, err := wake.Pending(h.State)
		if err != nil || len(pending) != 1 || pending[0].Key != "afk" || !strings.Contains(pending[0].Detail, "could not be read") || !strings.Contains(pending[0].Detail, "no report") {
			t.Errorf("the CFO's queue = %+v, %v, want it told the switch was reset to off and that there is no report", pending, err)
		}
		if _, found, err := afk.ReadReport(h.State); err != nil || found {
			t.Errorf("ReadReport = %v, %v, want no report of a stretch nobody could read", found, err)
		}
	})

	for name, c := range map[string]struct {
		on      bool
		caller  func(*Service)
		refusal string
	}{
		"a goblin's off is refused": {false, asGoblinsTerminal, "a goblin's terminal"},
		"his on is refused":         {true, asOverlordsTerminal, "only the Overlord resets it, with cfo afk off"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			s := &Service{Store: store}
			c.caller(s)
			runPipe(t, s)
			unreadable(t, h.State)

			// Act
			err := SwitchAFK(h, c.on)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("SwitchAFK = %v, want it refused as %q", err, c.refusal)
			}
			if _, err := afk.Read(h.State); err == nil {
				t.Error("the switch reads again after a refused request, want it left as it was")
			}
			if entries := afkEntries(t, h.State); len(entries) != 0 {
				t.Errorf("the AFK log = %+v, want nothing", entries)
			}
		})
	}
}

func waitingItems(t *testing.T, store *Store) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.acceptQuestion(Question{ID: "drop-legacy-invoices", Identity: strings.Repeat("c", 64), Text: "Migration 0042 drops legacy_invoices. Apply it?", Options: []string{"Apply it", "Keep it held"}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.acceptReview(Review{ID: "waiting-task-1-7", Identity: strings.Repeat("d", 64), Task: "task-1", Title: "Waiting on you: sign in to Vercel", State: "open", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	readyRun(t, store, strings.Repeat("c", 64), "delete-merged-branches", "powershell", false, now)
}

// An item that waits on the Overlord while AFK mode is on is recorded as held
// for him, once, with what it asks and whose it is, and the board is handed
// nothing to announce for it.
func TestAnItemThatWaitsOnTheOverlordWhileAFKModeIsOnIsHeldOnceAndNotAnnounced(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, work: make(chan struct{}, 1)}
	waitingItems(t, store)
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	s.cycle(context.Background(), false)
	s.cycle(context.Background(), false)
	claimed := askToAnnounce(t, s, `{"keys":["alert:question:drop-legacy-invoices","open:question:drop-legacy-invoices","alert:review:waiting-task-1-7","alert:run:delete-merged-branches"]}`)

	// Assert
	if len(claimed) != 0 {
		t.Errorf("claimed while AFK mode is on = %q, want nothing: each item is held for him instead", claimed)
	}
	var held []afk.Entry
	for _, entry := range afkEntries(t, h.State) {
		if entry.Kind == afk.KindHeld {
			held = append(held, entry)
		}
	}
	slices.SortFunc(held, func(a, b afk.Entry) int { return strings.Compare(a.Item, b.Item) })
	if len(held) != 3 {
		t.Fatalf("held = %+v, want the question, the wait and the run item, each once", held)
	}
	if held[0].Item != "question:drop-legacy-invoices" || held[0].What != "Migration 0042 drops legacy_invoices. Apply it?" || held[0].Task != "" {
		t.Errorf("held question = %+v, want the CFO's question with its text", held[0])
	}
	if held[1].Item != "review:waiting-task-1-7" || held[1].What != "Waiting on you: sign in to Vercel" || held[1].Task != "task-1" {
		t.Errorf("held wait = %+v, want the goblin's wait with its reason", held[1])
	}
	if held[2].Item != "run:delete-merged-branches" || held[2].What != "Run delete-merged-branches" {
		t.Errorf("held run item = %+v, want the command left for him with its title", held[2])
	}
}

// What no longer waits on him is not held: an answered question, a closed
// item, a command that already ran.
func TestWhatNoLongerWaitsOnTheOverlordIsNotHeld(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	waitingItems(t, store)
	if err := store.withdrawReview("waiting-task-1-7", "task-1 reported again"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.db.Questions[0].Status, store.db.Questions[0].AnsweredBy = "succeeded", "overlord"
	store.db.Runs[0].State = "succeeded"
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	err = s.holdForOverlord(time.Now())

	// Assert
	if entries := afkEntries(t, h.State); err != nil || len(entries) != 1 {
		t.Fatalf("holdForOverlord = %v, log %+v, want only the switch", err, entries)
	}
}

// A supervisor that restarts mid-stretch holds nothing twice, and a new
// stretch holds again what still waits.
func TestAHeldItemIsHeldOnceForEachStretchAcrossARestart(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	waitingItems(t, store)
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := (&Service{Store: store}).holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	restarted := &Service{Store: store}
	if err := restarted.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	sameStretch := len(afkEntries(t, h.State))
	if _, err := afk.TurnOff(h.State, "the board", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.holdForOverlord(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	// Assert
	if sameStretch != 4 {
		t.Errorf("the log after a restart held %d lines, want 4: the switch and each item once", sameStretch)
	}
	if entries := afkEntries(t, h.State); len(entries) != 9 {
		t.Errorf("the log after a second stretch = %d lines, want 9: the first stretch, its end, the second switch and each item again", len(entries))
	}
}

// The CFO's items follow the home's CFO when it is closed and opened again,
// and keep the IDs they were held by: what was held for the Overlord in a
// stretch is not held a second time once it has followed.
func TestAHeldItemThatFollowsAReopenedCFOIsNotHeldAgain(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	waitingItems(t, store)
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	heldBefore := len(afkEntries(t, h.State))

	// Act
	reopened := strings.Repeat("e", 64)
	if err := store.followCFO(reopened); err != nil {
		t.Fatal(err)
	}
	err := s.holdForOverlord(time.Now())

	// Assert
	d := store.Snapshot()
	if d.Questions[0].Identity != reopened || d.Runs[0].Identity != reopened {
		t.Fatalf("the CFO's question and run item did not follow the reopened CFO (%s, %s), so this proves nothing", d.Questions[0].Identity, d.Runs[0].Identity)
	}
	if entries := afkEntries(t, h.State); err != nil || heldBefore != 4 || len(entries) != heldBefore {
		t.Errorf("holdForOverlord = %v with %d lines in the log after the items followed and %d before, want 4 both times: the switch and each item once", err, len(entries), heldBefore)
	}
}

// Turning AFK mode off produces the report of the stretch: what the CFO
// decided with its evidence, what each goblin finished, what is held for him
// and what became of it, and what was spent. The CFO is told to write it into
// its terminal.
func TestTurningAFKModeOffReportsTheStretch(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	readings := [][]afk.Allowance{
		{{Provider: "claude", Window: "week", PercentUsed: 40}},
		{{Provider: "claude", Window: "week", PercentUsed: 47}},
	}
	s := &Service{Store: store, Options: Options{Allowance: func(context.Context) ([]afk.Allowance, string) {
		reading := readings[0]
		readings = readings[1:]
		return reading, ""
	}}}
	asOverlordsTerminal(s)
	runPipe(t, s)
	if err := SwitchAFK(h, true); err != nil {
		t.Fatal(err)
	}
	pr := "https://github.com/acme/api/pull/12"
	for _, entry := range []afk.Entry{
		{Kind: afk.KindMerge, What: pr, Evidence: "gate run 41 passed and its test output was read"},
		{Kind: afk.KindMerge, What: pr, Evidence: "gh pr merge exited 0", Outcome: afk.OutcomeMerged},
	} {
		if _, err := afk.Log(h.State, entry, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.AppendStatus(h.State, "task-1", "done: PR "+pr); err != nil {
		t.Fatal(err)
	}
	waitingItems(t, store)
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "task-1", "working: the invoice export"); err != nil {
		t.Fatal(err)
	}
	if err := store.withdrawReview("waiting-task-1-7", "task-1 reported again: working: the invoice export"); err != nil {
		t.Fatal(err)
	}

	// Act
	err := SwitchAFK(h, false)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	report, found, err := afk.ReadReport(h.State)
	if err != nil || !found {
		t.Fatalf("ReadReport = %v, %v, want the report of the stretch", found, err)
	}
	if len(report.Decisions) != 1 || report.Decisions[0].What != pr || report.Decisions[0].Outcome != afk.OutcomeMerged || report.Decisions[0].Evidence != "gate run 41 passed and its test output was read" {
		t.Errorf("decisions = %+v, want the merge word with its evidence and outcome", report.Decisions)
	}
	if len(report.Finished) != 1 || report.Finished[0].Task != "task-1" || report.Finished[0].PR != pr {
		t.Errorf("finished = %+v, want task-1's pull request", report.Finished)
	}
	slices.SortFunc(report.Held, func(a, b afk.Held) int { return strings.Compare(a.Item, b.Item) })
	if len(report.Held) != 3 {
		t.Fatalf("held = %+v, want the question, the wait and the run item", report.Held)
	}
	if question := report.Held[0]; question.Item != "question:drop-legacy-invoices" || !question.Waiting || question.Now != "still waiting on you" {
		t.Errorf("held question = %+v, want it still waiting on him", question)
	}
	if wait := report.Held[1]; wait.Item != "review:waiting-task-1-7" || wait.Waiting || !strings.Contains(wait.Now, "withdrawn: task-1 reported again") || wait.Meanwhile != "working: the invoice export" {
		t.Errorf("held wait = %+v, want it withdrawn, with what its goblin did meanwhile", wait)
	}
	if len(report.Before) != 1 || report.Before[0].PercentUsed != 40 || len(report.After) != 1 || report.After[0].PercentUsed != 47 {
		t.Errorf("allowance = %+v then %+v, want the reading when it turned on and when it turned off", report.Before, report.After)
	}
	if switched, err := afk.Read(h.State); err != nil || switched.On || report.Session != switched.Session {
		t.Errorf("the switch = %+v, %v, want off, and the report of that stretch", switched, err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 2 || pending[1].Key != "afk" || !strings.Contains(pending[1].Detail, "turned AFK mode off") || !strings.Contains(pending[1].Detail, "cfo afk report") {
		t.Errorf("the CFO's queue = %+v, %v, want it told AFK mode is off and to write the report", pending, err)
	}
}

// A question the CFO answered while he was away is a decision in the log, not
// something held for him.
func TestAQuestionTheCFOAnsweredIsNotReportedAsHeld(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	asOverlordsTerminal(s)
	runPipe(t, s)
	if err := SwitchAFK(h, true); err != nil {
		t.Fatal(err)
	}
	waitingItems(t, store)
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	answered := time.Now().UTC()
	store.mu.Lock()
	store.db.Questions[0].Status, store.db.Questions[0].AnsweredBy, store.db.Questions[0].AnsweredAt = "succeeded", "cfo", &answered
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := afk.Log(h.State, afk.Entry{Kind: afk.KindAnswer, What: "drop-legacy-invoices", Evidence: "asked: Apply it? answered: Keep it held"}, answered); err != nil {
		t.Fatal(err)
	}

	// Act
	err = SwitchAFK(h, false)

	// Assert
	report, _, readErr := afk.ReadReport(h.State)
	if err != nil || readErr != nil {
		t.Fatal(err, readErr)
	}
	if slices.ContainsFunc(report.Held, func(held afk.Held) bool { return held.Item == "question:drop-legacy-invoices" }) || len(report.Held) != 2 {
		t.Errorf("held = %+v, want the wait and the run item, and not the question the CFO answered", report.Held)
	}
}

// The board closes a goblin's question as the CFO's once the CFO acks its
// notify, whether or not it answered. Only a logged answer is a decision, so a
// held question that closed that way with nothing logged stays in the report
// as held, saying so.
func TestAHeldQuestionClosedAsTheCFOsWithNoLoggedDecisionStaysInTheReport(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, record, _, cfo := goblinFixture(t, store)
	s := &Service{Store: store}
	asOverlordsTerminal(s)
	runPipe(t, s)
	if err := SwitchAFK(h, true); err != nil {
		t.Fatal(err)
	}
	asked := surfaced(t, store, meta, record, cfo)
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(h.State, record.Seq); err != nil {
		t.Fatal(err)
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}
	if closed := store.Snapshot().Questions[0]; closed.Status != "succeeded" || closed.AnsweredBy != "cfo" {
		t.Fatalf("the question = %+v, want it closed as answered by the CFO", closed)
	}

	// Act
	err := SwitchAFK(h, false)

	// Assert
	report, _, readErr := afk.ReadReport(h.State)
	if err != nil || readErr != nil {
		t.Fatal(err, readErr)
	}
	if len(report.Decisions) != 0 {
		t.Errorf("decisions = %+v, want none: the CFO logged nothing", report.Decisions)
	}
	if len(report.Held) != 1 {
		t.Fatalf("held = %+v, want the goblin's question", report.Held)
	}
	if held := report.Held[0]; held.Item != "question:"+asked.ID || held.Task != meta.ID || held.Waiting || !strings.Contains(held.Now, "closed as answered by the CFO") || !strings.Contains(held.Now, "no decision was logged for it") {
		t.Errorf("held question = %+v, want it not waiting, closed as answered by the CFO with no decision logged", held)
	}
}

// quota-axi can take as long as the pipe gives a request. The supervisor's
// cycle never waits behind that reading, and a request that changes nothing
// reads nothing.
func TestTheCycleDoesNotWaitForTheAllowanceReadingOfASwitch(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	var readings atomic.Int32
	reading, release := make(chan struct{}, 1), make(chan struct{})
	var released sync.Once
	free := func() { released.Do(func() { close(release) }) }
	defer free()
	s := &Service{Store: store, Options: Options{Allowance: func(context.Context) ([]afk.Allowance, string) {
		if readings.Add(1) == 1 {
			reading <- struct{}{}
		}
		<-release
		return []afk.Allowance{{Provider: "claude", Window: "week", PercentUsed: 40}}, ""
	}}}
	asOverlordsTerminal(s)
	runPipe(t, s)
	switched := make(chan error, 1)
	go func() { switched <- SwitchAFK(h, true) }()
	select {
	case <-reading:
	case err := <-switched:
		t.Fatalf("SwitchAFK = %v before the allowance was read", err)
	}

	// Act
	held := make(chan error, 1)
	go func() { held <- s.holdForOverlord(time.Now()) }()

	// Assert
	select {
	case err := <-held:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("holdForOverlord waited for the allowance reading of a switch")
	}
	free()
	if err := <-switched; err != nil {
		t.Fatal(err)
	}
	if err := SwitchAFK(h, true); err != nil {
		t.Fatal(err)
	}
	if state, err := afk.Read(h.State); err != nil || !state.On || len(state.Allowance) != 1 {
		t.Errorf("the switch = %+v, %v, want on with the allowance read", state, err)
	}
	if got := readings.Load(); got != 1 {
		t.Errorf("the allowance was read %d times, want once: on while on changes nothing", got)
	}
}
