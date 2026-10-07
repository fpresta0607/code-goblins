package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// firstRunMachine is a machine as the first-run page sees it: a projects
// folder holding the checkouts alpha and beta beside a plain folder and a
// file, Claude Code and Codex on PATH, each saying it is signed in, with
// their sign-ins saved in the home, and a Code Goblins home that remembers no
// agent yet. It records every projects root set, every agent asked whether
// it is signed in, every agent remembered and the agent of every CFO started.
type firstRunMachine struct {
	run      *FirstRun
	root     string
	home     string
	cfoHome  string
	saved    string
	recorded []string
	started  []string
	// signIns is what each agent's status command says, and asked is every
	// agent asked, which Setup asks at once.
	signIns map[string]SignInState
	askedMu sync.Mutex
	asked   []string
	// runs is whether a CFO runs, reopened counts the CFOs brought back,
	// and reopenErr is what bringing one back ends with; restarted counts
	// the restarts of a running one, which end with restartErr or else on
	// its conversation, unless restartEnds says its harness could not
	// resume it.
	runs        bool
	reopened    int
	reopenErr   error
	restarted   int
	restartErr  error
	restartEnds bool
}

func newFirstRunMachine(t *testing.T) *firstRunMachine {
	t.Helper()
	m := &firstRunMachine{root: t.TempDir(), home: t.TempDir(), signIns: map[string]SignInState{"claude": SignedIn, "codex": SignedIn}}
	for _, dir := range []string{"alpha/.git", "beta/.git", "notes"} {
		if err := os.MkdirAll(filepath.Join(m.root, filepath.FromSlash(dir)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(m.root, "readme.txt"), []byte("not a project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{".claude/.credentials.json", ".codex/auth.json"} {
		path := filepath.Join(m.home, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.cfoHome = filepath.Join(t.TempDir(), "CodeGoblins")
	m.run = &FirstRun{
		Home:       m.home,
		CFOHome:    m.cfoHome,
		SavedAgent: func() string { return m.saved },
		SaveAgent:  func(agent string) error { m.saved = agent; return nil },
		LookPath: func(name string) (string, error) {
			switch name {
			case "claude":
				return `C:\Tools\claude.exe`, nil
			case "codex":
				return `C:\Tools\codex.cmd`, nil
			}
			return "", exec.ErrNotFound
		},
		ProjectsRoot:    func() (string, error) { return "", nil },
		SetProjectsRoot: func(dir string) error { m.recorded = append(m.recorded, dir); return nil },
		CFORuns:         func() bool { return m.runs },
		StartCFO:        func(agent string) error { m.started = append(m.started, agent); return nil },
		ReopenCFO:       func() error { m.reopened++; return m.reopenErr },
		RestartCFO: func() (CFOConversation, bool, error) {
			m.restarted++
			if m.restartErr != nil {
				return CFOConversation{}, false, m.restartErr
			}
			return CFOConversation{Harness: "claude", Session: "a1b2c3d4-session"}, !m.restartEnds, nil
		},
		SignIn: func(_ context.Context, agent string) SignInState {
			m.askedMu.Lock()
			defer m.askedMu.Unlock()
			m.asked = append(m.asked, agent)
			if state, ok := m.signIns[agent]; ok {
				return state
			}
			return SignInUnknown
		},
	}
	return m
}

// The page shows every harness a CFO can run in, by the table of what is
// proved: Claude Code marked as the recommended one, each with the few words
// on what a CFO in it gets, and none refused that this machine has. Only one
// that is not installed says why Start cannot pick it.
func TestFirstRunShowsTheFoldersProjectsAndEachAgent(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)

	// Act
	setup := m.run.Setup(t.Context(), m.root)

	// Assert
	if setup.ProjectsRoot != m.root || strings.Join(setup.Checkouts, ",") != "alpha,beta" || setup.Problem != "" || setup.CFORuns {
		t.Fatalf("setup = %+v, want the folder with alpha and beta", setup)
	}
	want := []SetupAgent{
		{ID: "claude", Name: "Claude Code", Recommended: true, Note: "the best experience", Installed: true, SignIn: SignedIn},
		{ID: "codex", Name: "Codex", Note: "woken by a typed line; no digest or guards", Installed: true, SignIn: SignedIn},
		{ID: "pi", Name: "pi", Note: "woken by a typed line; no digest, guards or resume", SignIn: SignInUnknown, Reason: "Install pi to start the CFO"},
	}
	if len(setup.Agents) != len(want) {
		t.Fatalf("agents = %+v, want %+v", setup.Agents, want)
	}
	for i := range want {
		if setup.Agents[i] != want[i] {
			t.Errorf("agent %d = %+v, want %+v", i, setup.Agents[i], want[i])
		}
	}
}

// The page says what each agent's own status command says, in the
// environment the CFO's terminal starts with, never whether a sign-in file
// sits in the home this supervisor sees. Found 2026-10-06 in a scratch
// profile: the page said Not signed in while the CFO's terminal started
// Claude Code signed in with the PC user's own account.
func TestFirstRunSaysWhatEachAgentsOwnStatusCommandSays(t *testing.T) {
	for _, c := range []struct {
		name    string
		saved   bool
		says    SignInState
		wantSay SignInState
	}{
		{"signed in with no sign-in saved in the home the supervisor sees", false, SignedIn, SignedIn},
		{"signed out though a sign-in file is in that home", true, SignedOut, SignedOut},
		{"a status command that did not say", true, SignInUnknown, SignInUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			if !c.saved {
				if err := os.RemoveAll(filepath.Join(m.home, ".claude")); err != nil {
					t.Fatal(err)
				}
			}
			m.signIns["claude"] = c.says

			// Act
			claude := m.run.Setup(t.Context(), m.root).Agents[0]

			// Assert
			if claude.ID != "claude" || claude.SignIn != c.wantSay {
				t.Fatalf("Claude Code = %+v, want the sign-in %q", claude, c.wantSay)
			}
		})
	}
}

// Only an installed agent is asked, and Start asks none: it needs only why
// an agent cannot start, so a status command never slows it.
func TestFirstRunAsksOnlyInstalledAgentsAndStartAsksNone(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)

	// Act
	m.run.Setup(t.Context(), m.root)

	// Assert
	slices.Sort(m.asked)
	if !slices.Equal(m.asked, []string{"claude", "codex"}) {
		t.Fatalf("Setup asked %q, want Claude Code and Codex, the installed agents", m.asked)
	}

	// Arrange
	m.asked = nil

	// Act
	if err := m.run.Start("", "claude"); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(m.asked) != 0 {
		t.Fatalf("Start asked %q whether they are signed in, want none asked", m.asked)
	}
}

// The page reads what the terminal quick start already knows, so neither
// asks it again: the home the CFO starts in, and the agent goblins remembered
// for it, which is none until one is chosen.
func TestFirstRunShowsTheHomeAndTheAgentTheQuickStartRemembered(t *testing.T) {
	for _, saved := range []string{"", "claude", "codex"} {
		t.Run("remembered "+saved, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			m.saved = saved

			// Act
			setup := m.run.Setup(t.Context(), "")

			// Assert
			if setup.Home != m.cfoHome || setup.Agent != saved {
				t.Fatalf("setup names the home %q and the agent %q, want %q and %q", setup.Home, setup.Agent, m.cfoHome, saved)
			}
		})
	}
}

func TestFirstRunOpensOnTheRecordedProjectsFolder(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)
	m.run.ProjectsRoot = func() (string, error) { return m.root, nil }

	// Act
	setup := m.run.Setup(t.Context(), "")

	// Assert
	if setup.ProjectsRoot != m.root || len(setup.Checkouts) != 2 {
		t.Fatalf("setup = %+v, want the recorded folder and its projects", setup)
	}
}

func TestFirstRunSaysWhyAFolderOffersNoProject(t *testing.T) {
	m := newFirstRunMachine(t)
	for _, c := range []struct{ name, root, problem string }{
		{"a relative path", "projects", "Enter the full path of a folder"},
		{"a missing folder", filepath.Join(m.root, "missing"), "This folder cannot be read"},
		{"a folder without a checkout", filepath.Join(m.root, "notes"), "No git checkout is in this folder"},
	} {
		t.Run(c.name, func(t *testing.T) {
			setup := m.run.Setup(t.Context(), c.root)

			if !strings.HasPrefix(setup.Problem, c.problem) || len(setup.Checkouts) != 0 {
				t.Fatalf("setup = %+v, want the problem %q", setup, c.problem)
			}
		})
	}
}

func TestFirstRunSaysWhyClaudeCodeCannotStart(t *testing.T) {
	for _, c := range []struct{ name, path, reason string }{
		{"not installed", "", "Install Claude Code to start the CFO"},
		{"a script shim", `C:\Tools\claude.cmd`, "The CFO starts from the native build of Claude Code, claude.exe"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			m.run.LookPath = func(string) (string, error) {
				if c.path == "" {
					return "", exec.ErrNotFound
				}
				return c.path, nil
			}

			// Act
			claude := m.run.Setup(t.Context(), m.root).Agents[0]

			// Assert
			if claude.ID != "claude" || claude.Reason != c.reason {
				t.Fatalf("Claude Code = %+v, want the reason %q", claude, c.reason)
			}
		})
	}
}

// A Codex CFO starts from the page as a Claude Code one does, now that the
// fleet wakes it: the page refuses no harness that works.
func TestFirstRunStartsAHarnessTheFleetCanWake(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)

	// Act
	err := m.run.Start("", "codex")

	// Assert
	if err != nil || len(m.started) != 1 || m.started[0] != "codex" || m.saved != "codex" {
		t.Fatalf("Start as Codex = %v, started %q, remembered %q; want Codex started once and remembered", err, m.started, m.saved)
	}
}

// The table of what is proved is the one place that says what a CFO in each
// harness gets: Claude Code alone is recommended and goes without nothing,
// every harness in it can be woken, and a Codex or pi CFO says plainly what
// it goes without.
func TestTheCFOCapabilityTableSaysOnlyWhatIsProved(t *testing.T) {
	table := CFOCapabilities()
	if len(table) != 3 || table[0].Agent != "claude" || table[1].Agent != "codex" || table[2].Agent != "pi" {
		t.Fatalf("the table names %+v, want Claude Code, Codex and pi in that order", table)
	}
	for _, row := range table {
		if row.Wake != CFOWakeFor(row.Agent) || row.Wake == CFOWakeNone {
			t.Errorf("%s: wake %q, want what CFOWakeFor says, and a wake", row.Agent, row.Wake)
		}
		if row.Recommended != (row.Agent == "claude") {
			t.Errorf("%s: recommended %v, want Claude Code alone recommended", row.Agent, row.Recommended)
		}
		if (len(row.Lacks) == 0) != (row.Agent == "claude") {
			t.Errorf("%s: lacks %q, want Claude Code alone to go without nothing", row.Agent, row.Lacks)
		}
		if row.Name == "" || row.Note == "" || row.Registers == "" {
			t.Errorf("%s: a row of the table is missing its name, note or how it registers: %+v", row.Agent, row)
		}
	}
	if !table[0].Resumes || !table[1].Resumes || table[2].Resumes {
		t.Errorf("resumes: %v %v %v, want Claude Code and Codex to come back on their conversation and pi not", table[0].Resumes, table[1].Resumes, table[2].Resumes)
	}
	if row, ok := CFOCapabilityFor("codex"); !ok || row.Agent != "codex" {
		t.Errorf("CFOCapabilityFor(codex) = %+v, %v", row, ok)
	}
	if _, ok := CFOCapabilityFor("kimi"); ok {
		t.Error("CFOCapabilityFor(kimi) found a row for a harness no CFO runs in")
	}
}

// Start asks for no project: it starts the agent as the CFO, which the
// machine starts in the home, and remembers the agent as goblins would. A
// projects folder is recorded when one is entered and is new, and the CFO
// starts with none entered, whatever is recorded.
func TestFirstRunStartsTheCFOWithNoProjectAndRemembersItsAgent(t *testing.T) {
	for _, c := range []struct {
		name        string
		entered     func(root string) string
		recorded    func(root string) string
		wantRecords int
	}{
		{"a new projects folder is recorded", func(root string) string { return root }, func(string) string { return "" }, 1},
		{"the recorded folder is kept", func(root string) string { return root }, func(root string) string { return root }, 0},
		{"no folder entered, none recorded", func(string) string { return "" }, func(string) string { return "" }, 0},
		{"no folder entered, one recorded", func(string) string { return "" }, func(root string) string { return root }, 0},
		{"no folder entered, the recorded one unreadable", func(string) string { return "" }, func(root string) string { return filepath.Join(root, "missing") }, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			m.saved = "codex"
			m.run.ProjectsRoot = func() (string, error) { return c.recorded(m.root), nil }

			// Act
			err := m.run.Start(c.entered(m.root), "claude")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(m.recorded) != c.wantRecords || c.wantRecords == 1 && m.recorded[0] != m.root {
				t.Fatalf("projects roots recorded = %q, want %d of %s", m.recorded, c.wantRecords, m.root)
			}
			if len(m.started) != 1 || m.started[0] != "claude" || m.saved != "claude" {
				t.Fatalf("CFOs started = %q, agent remembered = %q; want Claude Code started once and remembered", m.started, m.saved)
			}
		})
	}
}

func TestFirstRunRefusesAStartItCannotMake(t *testing.T) {
	for _, c := range []struct {
		name, agent, refusal string
		change               func(m *firstRunMachine) string
	}{
		{"a CFO already runs", "claude", "The CFO already runs", func(m *firstRunMachine) string {
			m.run.CFORuns = func() bool { return true }
			return m.root
		}},
		{"an agent this machine does not have", "pi", "Install pi to start the CFO", func(m *firstRunMachine) string { return m.root }},
		{"an unknown agent", "gemini", "Pick one of the agents this page offers", func(m *firstRunMachine) string { return m.root }},
		{"a relative folder", "claude", "Enter the full path of a folder", func(*firstRunMachine) string { return "projects" }},
		{"an entered folder without a checkout", "claude", "No git checkout is in this folder", func(m *firstRunMachine) string { return filepath.Join(m.root, "notes") }},
		{"an entered folder that cannot be read", "claude", "This folder cannot be read", func(m *firstRunMachine) string { return filepath.Join(m.root, "missing") }},
		{"an agent that cannot be remembered", "claude", "The agent could not be remembered", func(m *firstRunMachine) string {
			m.run.SaveAgent = func(string) error { return errors.New("the state folder is read-only") }
			return ""
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			root := c.change(m)

			// Act
			err := m.run.Start(root, c.agent)

			// Assert
			if !errors.As(err, new(StartRefusal)) || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("err = %v, want a refusal saying %q", err, c.refusal)
			}
			if len(m.started) != 0 || len(m.recorded) != 0 || m.saved != "" {
				t.Fatalf("CFOs started = %q, projects roots recorded = %q, agent remembered = %q; want none", m.started, m.recorded, m.saved)
			}
		})
	}
}

// Remembering is part of the start: the CFO starts in a home that already
// remembers its agent, and a start that fails puts back what the home
// remembered before, which may be nothing, so the next goblins in the
// terminal starts the agent he chose there.
func TestFirstRunPutsBackTheRememberedAgentWhenTheCFOCannotStart(t *testing.T) {
	for _, before := range []string{"codex", ""} {
		t.Run("remembered "+before, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			m.saved = before
			var atStart string
			m.run.StartCFO = func(string) error {
				atStart = m.saved
				return errors.New("the terminal host is down")
			}

			// Act
			err := m.run.Start("", "claude")

			// Assert
			if err == nil || errors.As(err, new(StartRefusal)) || !strings.Contains(err.Error(), "the terminal host is down") {
				t.Fatalf("err = %v, want the failed start with its cause, not a refusal", err)
			}
			if atStart != "claude" || m.saved != before {
				t.Fatalf("agent remembered as the CFO started = %q, and after it failed = %q; want claude, then %q as it was", atStart, m.saved, before)
			}
		})
	}
}

// A failed start whose remembered agent cannot be put back says both.
func TestFirstRunSaysWhenAFailedStartCannotPutBackTheRememberedAgent(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)
	m.saved = "codex"
	m.run.StartCFO = func(string) error {
		m.run.SaveAgent = func(string) error { return errors.New("the state folder is read-only") }
		return errors.New("the terminal host is down")
	}

	// Act
	err := m.run.Start("", "claude")

	// Assert
	if err == nil || errors.As(err, new(StartRefusal)) {
		t.Fatalf("err = %v, want an error that is not a refusal", err)
	}
	for _, said := range []string{"the terminal host is down", "the state folder is read-only"} {
		if !strings.Contains(err.Error(), said) {
			t.Errorf("err = %v, want it to say %q", err, said)
		}
	}
}

// A projects folder that cannot be recorded refuses the start before the
// agent is remembered or anything is started.
func TestFirstRunRefusesAProjectsFolderItCannotRecord(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)
	m.run.SetProjectsRoot = func(string) error { return errors.New("registry refused") }

	// Act
	err := m.run.Start(m.root, "claude")

	// Assert
	if !errors.As(err, new(StartRefusal)) || !strings.Contains(err.Error(), "The projects folder could not be recorded") || len(m.started) != 0 || m.saved != "" {
		t.Fatalf("err = %v, started %q, remembered %q; want the refusal and nothing started or remembered", err, m.started, m.saved)
	}
}

func TestTheBoardServesTheFirstRunPageAndStartsTheCFO(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)
	store, _ := testStore(t)
	s := &Service{Store: store, Instance: "instance-1", Options: Options{FirstRun: m.run}, subscribers: map[chan struct{}]struct{}{}}
	handler := NewHTTP(s, "board.local", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body>board</body></html>")}})
	request := func(method, path, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, "http://board.local"+path, strings.NewReader(body))
		if method == "POST" {
			req.Header.Set("Origin", "http://board.local")
			req.Header.Set("X-CFO-Token", "instance-1")
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		data, _ := io.ReadAll(response.Result().Body)
		return response.Code, string(data)
	}

	start := func(agent string) string {
		data, err := json.Marshal(map[string]string{"root": m.root, "agent": agent})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// Act
	pageCode, page := request("GET", "/", "")
	setupCode, setup := request("GET", "/api/setup?root="+url.QueryEscape(m.root), "")
	refusedCode, refused := request("POST", "/api/setup/start", start("pi"))
	changes, unsubscribe := s.subscribe()
	defer unsubscribe()
	startCode, _ := request("POST", "/api/setup/start", start("claude"))

	// Assert
	if pageCode != http.StatusOK || !strings.Contains(page, "board") {
		t.Fatalf("GET / = %d %q, want the board's page", pageCode, page)
	}
	if setupCode != http.StatusOK || !strings.Contains(setup, `"checkouts":["alpha","beta"]`) || !strings.Contains(setup, `"agent":""`) || !strings.Contains(setup, `"home":`) {
		t.Fatalf("GET /api/setup = %d %s", setupCode, setup)
	}
	// The page's parser takes a missing boolean for a broken answer and
	// draws nothing, so an agent that is not the recommended one says so.
	if strings.Count(setup, `"recommended":true`) != 1 || strings.Count(setup, `"recommended":false`) != 2 {
		t.Fatalf("GET /api/setup = %s, want each of the three agents to say whether it is the recommended one", setup)
	}
	if refusedCode != http.StatusConflict || !strings.Contains(refused, "Install pi to start the CFO") {
		t.Fatalf("a start as pi, which this machine lacks = %d %s, want 409 with the reason", refusedCode, refused)
	}
	if startCode != http.StatusOK || len(m.started) != 1 || m.started[0] != "claude" || m.saved != "claude" {
		t.Fatalf("a start as Claude Code = %d, started %q, remembered %q", startCode, m.started, m.saved)
	}
	select {
	case <-changes:
	default:
		t.Fatal("a start sent the board no fresh snapshot, so it shows no CFO until the next ping")
	}
}

// The board's Reopen brings a closed CFO back, as goblins does, and tells
// the board at once. It brings none back beside a CFO that runs, says why a
// CFO could not come back, and a board that starts no CFO says so.
func TestTheBoardReopensAClosedCFO(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arrange   func(m *firstRunMachine, s *Service)
		code      int
		reason    string
		reopened  int
		announced bool
	}{
		{"a closed CFO", func(*firstRunMachine, *Service) {}, http.StatusOK, `"reopened":true`, 1, true},
		{"a CFO that runs", func(m *firstRunMachine, _ *Service) { m.runs = true }, http.StatusConflict, "The CFO already runs", 0, false},
		{"a CFO that cannot come back", func(m *firstRunMachine, _ *Service) { m.reopenErr = errors.New("claude is not on PATH") }, http.StatusInternalServerError, "the CFO could not be reopened: claude is not on PATH", 1, false},
		{"a board that starts no CFO", func(_ *firstRunMachine, s *Service) { s.Options.FirstRun = nil }, http.StatusConflict, "This board cannot start a CFO", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			store, _ := testStore(t)
			s := &Service{Store: store, Instance: "instance-1", Options: Options{FirstRun: m.run}, subscribers: map[chan struct{}]struct{}{}}
			tc.arrange(m, s)
			handler := NewHTTP(s, "board.local", fstest.MapFS{})
			changes, unsubscribe := s.subscribe()
			defer unsubscribe()
			req := httptest.NewRequest("POST", "http://board.local/api/cfo/reopen", strings.NewReader("{}"))
			req.Header.Set("Origin", "http://board.local")
			req.Header.Set("X-CFO-Token", "instance-1")
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			// Act
			handler.ServeHTTP(response, req)

			// Assert
			if response.Code != tc.code || !strings.Contains(response.Body.String(), tc.reason) || m.reopened != tc.reopened {
				t.Fatalf("POST /api/cfo/reopen = %d %s, reopened %d; want %d with %q and %d reopened", response.Code, response.Body.String(), m.reopened, tc.code, tc.reason, tc.reopened)
			}
			announced := false
			select {
			case <-changes:
				announced = true
			default:
			}
			if announced != tc.announced {
				t.Errorf("the board was sent a fresh snapshot: %v, want %v", announced, tc.announced)
			}
		})
	}
}

// The board's Restart does what goblins resume does for a CFO that runs, as
// for one whose screen froze: it restarts it on its conversation and says
// whether it came back on it. It restarts nothing while no CFO runs, and a
// CFO it could not restart says why.
func TestTheBoardRestartsARunningCFO(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arrange   func(m *firstRunMachine, s *Service)
		body      string
		code      int
		reply     string
		restarted int
		announced bool
	}{
		{"a running CFO", func(m *firstRunMachine, _ *Service) { m.runs = true }, "{}", http.StatusOK, `"restarted":true,"resumed":true,"session":"a1b2c3d4-session"`, 1, true},
		{"a CFO whose harness could not resume its conversation", func(m *firstRunMachine, _ *Service) { m.runs, m.restartEnds = true, true }, "{}", http.StatusOK, `"resumed":false`, 1, true},
		{"no CFO running", func(*firstRunMachine, *Service) {}, "{}", http.StatusConflict, "No CFO runs to restart", 0, false},
		{"a CFO left running", func(m *firstRunMachine, _ *Service) {
			m.runs, m.restartErr = true, errors.New("the CFO in native terminal cfo registered no conversation it can come back on, so it is left running")
		}, "{}", http.StatusInternalServerError, "the CFO could not be restarted: the CFO in native terminal cfo registered no conversation", 1, false},
		{"a body field the route does not take", func(m *firstRunMachine, _ *Service) { m.runs = true }, `{"now":true}`, http.StatusBadRequest, "Invalid request JSON", 0, false},
		{"a board that starts no CFO", func(_ *firstRunMachine, s *Service) { s.Options.FirstRun = nil }, "{}", http.StatusConflict, "This board cannot start a CFO", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			store, _ := testStore(t)
			s := &Service{Store: store, Instance: "instance-1", Options: Options{FirstRun: m.run}, subscribers: map[chan struct{}]struct{}{}}
			tc.arrange(m, s)
			handler := NewHTTP(s, "board.local", fstest.MapFS{})
			changes, unsubscribe := s.subscribe()
			defer unsubscribe()
			req := httptest.NewRequest("POST", "http://board.local/api/cfo/restart", strings.NewReader(tc.body))
			req.Header.Set("Origin", "http://board.local")
			req.Header.Set("X-CFO-Token", "instance-1")
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			// Act
			handler.ServeHTTP(response, req)

			// Assert
			if response.Code != tc.code || !strings.Contains(response.Body.String(), tc.reply) || m.restarted != tc.restarted {
				t.Fatalf("POST /api/cfo/restart = %d %s, restarted %d; want %d with %q and %d restarted", response.Code, response.Body.String(), m.restarted, tc.code, tc.reply, tc.restarted)
			}
			announced := false
			select {
			case <-changes:
				announced = true
			default:
			}
			if announced != tc.announced {
				t.Errorf("the board was sent a fresh snapshot: %v, want %v", announced, tc.announced)
			}
		})
	}
}

func TestABoardWithoutFirstRunSaysItCannotStartACFO(t *testing.T) {
	store, _ := testStore(t)
	s := &Service{Store: store, Instance: "instance-1"}
	handler := NewHTTP(s, "board.local", nil)
	req := httptest.NewRequest("POST", "http://board.local/api/setup/start", strings.NewReader(`{"root":"C:\\dev","agent":"claude"}`))
	req.Header.Set("Origin", "http://board.local")
	req.Header.Set("X-CFO-Token", "instance-1")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "This board cannot start a CFO") {
		t.Fatalf("POST /api/setup/start = %d %s", response.Code, response.Body.String())
	}
}

// With no CFO registered and no native terminal cfo up, the snapshot says no
// CFO runs, so the board shows its first-run page.
func TestASnapshotSaysWhenNoCFORuns(t *testing.T) {
	// Arrange
	store, _ := testStore(t)

	// Act
	snapshot, err := (&Service{Store: store}).Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CFORuns {
		t.Fatal("the snapshot says a CFO runs with none registered or starting")
	}
}
