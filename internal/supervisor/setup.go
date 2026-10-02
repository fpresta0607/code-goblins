package supervisor

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// FirstRun is what the board's first-run page reads and changes on this
// machine: the Code Goblins home the CFO starts in, the agent the quick start
// remembered, the agents this machine has, the folder that holds the
// Overlord's checkouts, and the CFO it starts. Without it the board can start
// no CFO.
type FirstRun struct {
	// Home is the user's home folder, where each agent keeps its sign-in.
	Home     string
	LookPath func(file string) (string, error)
	// CFOHome is the Code Goblins home: the one folder a CFO starts in,
	// from which it works across every project.
	CFOHome string
	// SavedAgent is the agent goblins remembered for this home, or empty
	// when none was chosen yet, and SaveAgent remembers the one the page
	// starts, so the terminal and the board hold one answer. SaveAgent("")
	// forgets the agent, and a home that remembered none is no error.
	SavedAgent func() string
	SaveAgent  func(agent string) error
	// ProjectsRoot and SetProjectsRoot read and record the projects folder,
	// as cfo install --projects-root does.
	ProjectsRoot    func() (string, error)
	SetProjectsRoot func(root string) error
	// CFORuns says a CFO is registered or its native terminal is up;
	// StartCFO starts agent as the CFO in native terminal cfo, in the home.
	CFORuns  func() bool
	StartCFO func(agent string) error
	mu       sync.Mutex
}

// StartRefusal is a start the board cannot make, of the CFO from the
// first-run page or of a queued task; its message is the reason, in the
// board's words. Passing says a queued task's refusal has a cause that passes
// by itself and the board sees pass: memory under the floor, or another Start
// running.
type StartRefusal struct {
	Reason  string
	Passing bool
}

func (r StartRefusal) Error() string { return r.Reason }

// Setup is the first-run page: the home the CFO starts in, the agent the
// quick start remembered, the agents this machine has, whether a CFO already
// runs, and the projects folder with the git checkouts in it, where goblins
// find a project by its name, or why the folder offers none. No project is a
// place to start the CFO: it starts in the home.
type Setup struct {
	Home         string       `json:"home"`
	Agent        string       `json:"agent"`
	ProjectsRoot string       `json:"projects_root"`
	Checkouts    []string     `json:"checkouts"`
	Problem      string       `json:"problem,omitempty"`
	Agents       []SetupAgent `json:"agents"`
	CFORuns      bool         `json:"cfo_runs"`
}

// SetupAgent is one agent the first-run page shows: whether this machine
// has it on PATH and a sign-in saved for it, and why Start cannot pick it
// when it cannot.
type SetupAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	SignedIn  bool   `json:"signed_in"`
	Reason    string `json:"reason,omitempty"`
}

// firstRunAgents are the agents a CFO could run on, with the program each
// starts as and the file under the home folder its sign-in is saved in.
var firstRunAgents = []struct{ id, name, program, signIn string }{
	{"claude", "Claude Code", "claude", ".claude/.credentials.json"},
	{"codex", "Codex", "codex", ".codex/auth.json"},
	{"pi", "Pi", "pi", ".pi/agent/auth.json"},
}

// ProjectCheckouts is every git checkout directly under root, the projects
// goblins work in. It stats each entry rather than reading its type, so a
// junction to a checkout kept on another drive counts as the folder it is.
func ProjectCheckouts(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var checkouts []string
	for _, entry := range entries {
		checkout := filepath.Join(root, entry.Name())
		if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(checkout, ".git")); err == nil {
			checkouts = append(checkouts, checkout)
		}
	}
	return checkouts, nil
}

// Setup reads the first-run page for root, or for the recorded projects
// folder when root is empty.
func (f *FirstRun) Setup(root string) Setup {
	setup := Setup{Home: f.CFOHome, Agent: f.SavedAgent(), Checkouts: []string{}, CFORuns: f.CFORuns()}
	if root == "" {
		recorded, err := f.ProjectsRoot()
		if err != nil {
			setup.Problem = "The recorded projects folder cannot be read: " + err.Error()
		}
		root = recorded
	}
	setup.ProjectsRoot = root
	if root != "" && setup.Problem == "" {
		setup.Checkouts, setup.Problem = projectNames(root)
	}
	for _, agent := range firstRunAgents {
		path, err := f.LookPath(agent.program)
		info, statErr := os.Stat(filepath.Join(f.Home, filepath.FromSlash(agent.signIn)))
		shown := SetupAgent{ID: agent.id, Name: agent.name, Installed: err == nil, SignedIn: statErr == nil && info.Mode().IsRegular() && info.Size() > 0}
		switch {
		case agent.id != "claude":
			shown.Reason = "Goblins can wake only a Claude Code CFO today"
		case !shown.Installed:
			shown.Reason = "Install Claude Code to start the CFO"
		case !strings.EqualFold(filepath.Ext(path), ".exe"):
			// A native terminal starts a program itself, with no shell to
			// run a script shim.
			shown.Reason = "The CFO starts from the native build of Claude Code, claude.exe"
		}
		setup.Agents = append(setup.Agents, shown)
	}
	return setup
}

// notAbsolute is why a folder that is not a full path offers no project.
const notAbsolute = `Enter the full path of a folder, such as C:\dev.`

// projectNames names the git checkouts directly under root, or says why root
// offers none.
func projectNames(root string) ([]string, string) {
	if !filepath.IsAbs(root) {
		return []string{}, notAbsolute
	}
	checkouts, err := ProjectCheckouts(root)
	if err != nil {
		return []string{}, "This folder cannot be read: " + err.Error()
	}
	if len(checkouts) == 0 {
		return []string{}, "No git checkout is in this folder; pick the folder that holds your projects."
	}
	names := make([]string, len(checkouts))
	for i, checkout := range checkouts {
		names[i] = filepath.Base(checkout)
	}
	return names, ""
}

// Start starts the CFO with agent, which only Claude Code can be today, in
// the Code Goblins home, as goblins does, and remembers the agent as goblins
// would. Remembering is part of the start: an agent that cannot be remembered
// refuses it with nothing started, and a start that fails puts back what the
// home remembered before, so every error means no CFO runs. It asks for no
// project: the CFO works across every project from its home. root, when one
// is entered, is recorded as the projects folder when it is not already; with
// none the CFO starts all the same. A start it cannot make is a StartRefusal;
// one runs at a time, so a second press finds the first CFO running.
func (f *FirstRun) Start(root, agent string) error {
	if root != "" && !filepath.IsAbs(root) {
		return StartRefusal{Reason: notAbsolute}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CFORuns() {
		return StartRefusal{Reason: "The CFO already runs; open its terminal from the board"}
	}
	setup := f.Setup(root)
	chosen := slices.IndexFunc(setup.Agents, func(shown SetupAgent) bool { return shown.ID == agent })
	switch {
	case chosen < 0:
		return StartRefusal{Reason: "Pick Claude Code to start the CFO"}
	case setup.Agents[chosen].Reason != "":
		return StartRefusal{Reason: setup.Agents[chosen].Reason}
	case root != "" && setup.Problem != "":
		return StartRefusal{Reason: setup.Problem}
	}
	if root != "" {
		if recorded, err := f.ProjectsRoot(); err != nil || !fsx.SamePath(recorded, root) {
			if err := f.SetProjectsRoot(root); err != nil {
				return StartRefusal{Reason: "The projects folder could not be recorded: " + err.Error()}
			}
		}
	}
	before := f.SavedAgent()
	if err := f.SaveAgent(agent); err != nil {
		return StartRefusal{Reason: "The agent could not be remembered: " + err.Error()}
	}
	if err := f.StartCFO(agent); err != nil {
		if restoreErr := f.SaveAgent(before); restoreErr != nil {
			return fmt.Errorf("the CFO could not be started: %w; and the agent remembered before could not be put back: %v", err, restoreErr)
		}
		return fmt.Errorf("the CFO could not be started: %w", err)
	}
	return nil
}

// setup serves GET /api/setup, the first-run page for the folder in ?root.
func (h *HTTP) setup(w http.ResponseWriter, r *http.Request) {
	if h.Service.Options.FirstRun == nil {
		apiError(w, http.StatusConflict, "This board cannot start a CFO")
		return
	}
	respond(w, http.StatusOK, h.Service.Options.FirstRun.Setup(r.URL.Query().Get("root")))
}

// startCFO serves POST /api/setup/start, the first-run page's Start.
func (h *HTTP) startCFO(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Root  string `json:"root"`
		Agent string `json:"agent"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.Service.Options.FirstRun == nil {
		apiError(w, http.StatusConflict, "This board cannot start a CFO")
		return
	}
	if err := h.Service.Options.FirstRun.Start(input.Root, input.Agent); err != nil {
		status := http.StatusInternalServerError
		if errors.As(err, new(StartRefusal)) {
			status = http.StatusConflict
		}
		apiError(w, status, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, struct {
		Started bool `json:"started"`
	}{true})
}
