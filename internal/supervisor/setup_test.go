package supervisor

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// firstRunMachine is a machine as the first-run page sees it: a projects
// folder holding the checkouts alpha and beta beside a plain folder and a
// file, a home where Claude Code and Codex are signed in, and Claude Code and
// Codex on PATH. It records every projects root set and every CFO started.
type firstRunMachine struct {
	run      *FirstRun
	root     string
	recorded []string
	started  []string
}

func newFirstRunMachine(t *testing.T) *firstRunMachine {
	t.Helper()
	m := &firstRunMachine{root: t.TempDir()}
	for _, dir := range []string{"alpha/.git", "beta/.git", "notes"} {
		if err := os.MkdirAll(filepath.Join(m.root, filepath.FromSlash(dir)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(m.root, "readme.txt"), []byte("not a project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, file := range []string{".claude/.credentials.json", ".codex/auth.json"} {
		path := filepath.Join(home, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.run = &FirstRun{
		Home: home,
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
		CFORuns:         func() bool { return false },
		StartCFO:        func(project string) error { m.started = append(m.started, project); return nil },
	}
	return m
}

func TestFirstRunShowsTheFoldersProjectsAndEachAgent(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)
	wait := "Goblins can wake only a Claude Code CFO today"

	// Act
	setup := m.run.Setup(m.root)

	// Assert
	if setup.ProjectsRoot != m.root || strings.Join(setup.Checkouts, ",") != "alpha,beta" || setup.Problem != "" || setup.CFORuns {
		t.Fatalf("setup = %+v, want the folder with alpha and beta", setup)
	}
	want := []SetupAgent{
		{ID: "claude", Name: "Claude Code", Installed: true, SignedIn: true},
		{ID: "codex", Name: "Codex", Installed: true, SignedIn: true, Reason: wait},
		{ID: "pi", Name: "Pi", Reason: wait},
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

func TestFirstRunOpensOnTheRecordedProjectsFolder(t *testing.T) {
	// Arrange
	m := newFirstRunMachine(t)
	m.run.ProjectsRoot = func() (string, error) { return m.root, nil }

	// Act
	setup := m.run.Setup("")

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
			setup := m.run.Setup(c.root)

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
			claude := m.run.Setup(m.root).Agents[0]

			// Assert
			if claude.ID != "claude" || claude.Reason != c.reason {
				t.Fatalf("Claude Code = %+v, want the reason %q", claude, c.reason)
			}
		})
	}
}

func TestFirstRunStartsTheCFOInThePickedProject(t *testing.T) {
	for _, c := range []struct {
		name        string
		recorded    func(root string) string
		wantRecords int
	}{
		{"a new projects folder is recorded", func(string) string { return "" }, 1},
		{"the recorded folder is kept", func(root string) string { return root }, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			m.run.ProjectsRoot = func() (string, error) { return c.recorded(m.root), nil }

			// Act
			err := m.run.Start(m.root, "beta", "claude")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(m.recorded) != c.wantRecords || c.wantRecords == 1 && m.recorded[0] != m.root {
				t.Fatalf("projects roots recorded = %q, want %d of %s", m.recorded, c.wantRecords, m.root)
			}
			if len(m.started) != 1 || m.started[0] != filepath.Join(m.root, "beta") {
				t.Fatalf("CFOs started = %q, want one in beta", m.started)
			}
		})
	}
}

func TestFirstRunRefusesAStartItCannotMake(t *testing.T) {
	for _, c := range []struct {
		name, project, agent, refusal string
		change                        func(m *firstRunMachine) string
	}{
		{"a CFO already runs", "beta", "claude", "The CFO already runs", func(m *firstRunMachine) string {
			m.run.CFORuns = func() bool { return true }
			return m.root
		}},
		{"an agent goblins cannot wake", "beta", "codex", "Goblins can wake only a Claude Code CFO today", func(m *firstRunMachine) string { return m.root }},
		{"an unknown agent", "beta", "gemini", "Pick Claude Code", func(m *firstRunMachine) string { return m.root }},
		{"a plain folder", "notes", "claude", "Pick one of the projects in this folder", func(m *firstRunMachine) string { return m.root }},
		{"a path out of the folder", `..\beta`, "claude", "Pick one of the projects in this folder", func(m *firstRunMachine) string { return m.root }},
		{"a relative folder", "beta", "claude", "Enter the full path of a folder", func(*firstRunMachine) string { return "projects" }},
		{"no folder, with one recorded", "beta", "claude", "Enter the full path of a folder", func(m *firstRunMachine) string {
			m.run.ProjectsRoot = func() (string, error) { return m.root, nil }
			return ""
		}},
		{"a folder that cannot be recorded", "beta", "claude", "The projects folder could not be recorded", func(m *firstRunMachine) string {
			m.run.SetProjectsRoot = func(string) error { return errors.New("registry refused") }
			return m.root
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := newFirstRunMachine(t)
			root := c.change(m)

			// Act
			err := m.run.Start(root, c.project, c.agent)

			// Assert
			if !errors.As(err, new(StartRefusal)) || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("err = %v, want a refusal saying %q", err, c.refusal)
			}
			if len(m.started) != 0 || len(m.recorded) != 0 {
				t.Fatalf("CFOs started = %q, projects roots recorded = %q, want none", m.started, m.recorded)
			}
		})
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

	start := func(project string) string {
		data, err := json.Marshal(map[string]string{"root": m.root, "project": project, "agent": "claude"})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// Act
	pageCode, page := request("GET", "/", "")
	setupCode, setup := request("GET", "/api/setup?root="+url.QueryEscape(m.root), "")
	refusedCode, refused := request("POST", "/api/setup/start", start("notes"))
	changes, unsubscribe := s.subscribe()
	defer unsubscribe()
	startCode, _ := request("POST", "/api/setup/start", start("alpha"))

	// Assert
	if pageCode != http.StatusOK || !strings.Contains(page, "board") {
		t.Fatalf("GET / = %d %q, want the board's page", pageCode, page)
	}
	if setupCode != http.StatusOK || !strings.Contains(setup, `"checkouts":["alpha","beta"]`) {
		t.Fatalf("GET /api/setup = %d %s", setupCode, setup)
	}
	if refusedCode != http.StatusConflict || !strings.Contains(refused, "Pick one of the projects in this folder") {
		t.Fatalf("a start in a plain folder = %d %s, want 409 with the reason", refusedCode, refused)
	}
	if startCode != http.StatusOK || len(m.started) != 1 || m.started[0] != filepath.Join(m.root, "alpha") {
		t.Fatalf("a start in alpha = %d, started %q", startCode, m.started)
	}
	select {
	case <-changes:
	default:
		t.Fatal("a start sent the board no fresh snapshot, so it shows no CFO until the next ping")
	}
}

func TestABoardWithoutFirstRunSaysItCannotStartACFO(t *testing.T) {
	store, _ := testStore(t)
	s := &Service{Store: store, Instance: "instance-1"}
	handler := NewHTTP(s, "board.local", nil)
	req := httptest.NewRequest("POST", "http://board.local/api/setup/start", strings.NewReader(`{"root":"C:\\dev","project":"alpha","agent":"claude"}`))
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
