package connections

import (
	"context"
	"os/exec"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Environment is where the checks look: the CEO home, the user's home, the
// folders the fleet keeps its manifests and records in, and the seams a test
// replaces so no check starts a real process or reads a real account.
type Environment struct {
	// HomeRoot is the CEO home, where the crawler connector is kept.
	HomeRoot string
	// UserHome is the user's own home, which holds the harnesses' settings and
	// session records.
	UserHome string
	DataDir  string
	StateDir string
	// ProjectsRoot is the folder that holds the checkouts, "" when unset.
	ProjectsRoot func() string
	Runner       execx.Runner
	OpenStore    func() (auth.Store, error)
	// LookPath finds a program on PATH.
	LookPath func(string) (string, error)
	// UserEnvNames lists the names of the variables of this PC's user
	// environment, never a value.
	UserEnvNames func() ([]string, error)
}

// NewEnvironment is the environment of a real machine.
func NewEnvironment(homeRoot, userHome, dataDir, stateDir string, projectsRoot func() string) Environment {
	return Environment{
		HomeRoot: homeRoot, UserHome: userHome, DataDir: dataDir, StateDir: stateDir, ProjectsRoot: projectsRoot,
		Runner: execx.OSRunner{}, OpenStore: auth.OpenStore, LookPath: exec.LookPath, UserEnvNames: userEnvironmentNames,
	}
}

// workspaceRunner runs a project's commands in its checkout, each as a tree so
// a command the time limit ends takes what it started with it.
type workspaceRunner struct {
	runner execx.Runner
	dir    string
}

func (r workspaceRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	request.Dir, request.KillTree = r.dir, true
	return r.runner.Run(ctx, request)
}
