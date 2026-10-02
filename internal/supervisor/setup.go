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
	// StartCFO starts agent as the CFO in native terminal cfo, in the home;
	// ReopenCFO brings the home's closed CFO back as goblins does.
	CFORuns   func() bool
	StartCFO  func(agent string) error
	ReopenCFO func() error
	mu        sync.Mutex
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

// SetupAgent is one agent the first-run page shows: its name, whether it is
// the recommended one and the few words on what a CFO in it gets, all from
// the table of what is proved; whether this machine has it on PATH and a
// sign-in saved for it; and why Start cannot pick it when it cannot.
type SetupAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Recommended bool   `json:"recommended"`
	Note        string `json:"note,omitempty"`
	Installed   bool   `json:"installed"`
	SignedIn    bool   `json:"signed_in"`
	Reason      string `json:"reason,omitempty"`
}

// firstRunSignIn is the file under the home folder each agent's sign-in is
// saved in.
var firstRunSignIn = map[string]string{
	"claude": ".claude/.credentials.json",
	"codex":  ".codex/auth.json",
	"pi":     ".pi/agent/auth.json",
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
	// Every harness the table of what is proved names can be the CFO, and
	// none is refused that the fleet can wake.
	for _, agent := range CFOCapabilities() {
		path, err := f.LookPath(agent.Agent)
		info, statErr := os.Stat(filepath.Join(f.Home, filepath.FromSlash(firstRunSignIn[agent.Agent])))
		shown := SetupAgent{ID: agent.Agent, Name: agent.Name, Recommended: agent.Recommended, Note: agent.Note, Installed: err == nil, SignedIn: statErr == nil && info.Mode().IsRegular() && info.Size() > 0}
		switch {
		case agent.Wake == CFOWakeNone:
			shown.Reason = "Goblins cannot wake a " + agent.Name + " CFO"
		case !shown.Installed:
			shown.Reason = "Install " + agent.Name + " to start the CFO"
		case agent.Agent == "claude" && !strings.EqualFold(filepath.Ext(path), ".exe"):
			// A native terminal starts Claude Code itself, with no shell to
			// run a script shim; Codex and pi start through theirs.
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

// Start starts the CFO with agent, any harness the fleet can wake, in the
// Code Goblins home, as goblins does, and remembers the agent as goblins
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
		return StartRefusal{Reason: "Pick one of the agents this page offers to start the CFO"}
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

// Reopen brings the home's closed CFO back, as running goblins does: as the
// agent the home remembers, on the conversation it last registered with where
// its harness resumes one. It starts none beside a CFO that runs or is
// starting.
func (f *FirstRun) Reopen() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CFORuns() {
		return StartRefusal{Reason: "The CFO already runs; open its terminal from the board"}
	}
	if err := f.ReopenCFO(); err != nil {
		return fmt.Errorf("the CFO could not be reopened: %w", err)
	}
	return nil
}

// reopenCFO serves POST /api/cfo/reopen, the board's Reopen for a closed CFO.
func (h *HTTP) reopenCFO(w http.ResponseWriter, r *http.Request) {
	// Reopen takes nothing: a body that names a field is refused, as every
	// endpoint refuses a field it does not take.
	var input struct{}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.Service.Options.FirstRun == nil {
		apiError(w, http.StatusConflict, "This board cannot start a CFO")
		return
	}
	if err := h.Service.Options.FirstRun.Reopen(); err != nil {
		status := http.StatusInternalServerError
		if errors.As(err, new(StartRefusal)) {
			status = http.StatusConflict
		}
		apiError(w, status, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, struct {
		Reopened bool `json:"reopened"`
	}{true})
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
