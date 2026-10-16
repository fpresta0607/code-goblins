package spawn

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblinSettingsFile is the Claude Code settings file, in the home's state
// folder, that every Claude Code goblin starts with.
const goblinSettingsFile = "goblin-claude-settings.json"

// goblinSettings writes the settings file a goblin of this home on kind
// starts with, which holds the goblin hooks, and returns its path: written
// anew at each launch, so it is this build's set. Only Claude Code takes
// one, and for any other harness it writes nothing and returns "".
func (s *Service) goblinSettings(kind harness.Kind) (string, error) {
	if kind != harness.Claude {
		return "", nil
	}
	settings, err := install.GoblinSettings(s.HomeRoot, s.StateDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.StateDir, goblinSettingsFile)
	if err := fsx.AtomicWriteFile(path, settings); err != nil {
		return "", fmt.Errorf("write the goblin hooks to %s: %w", path, err)
	}
	return path, nil
}

// codexHooks are the arguments that give a goblin of this home on kind its
// native session hooks: Codex's -c overrides, and none for any other harness,
// whose hooks its settings file or its extension carries.
func (s *Service) codexHooks(kind harness.Kind) ([]string, error) {
	if kind != harness.Codex {
		return nil, nil
	}
	return nativehook.CodexArguments(filepath.Join(s.HomeRoot, home.BinDir, "cfo.exe"), s.HomeRoot, s.StateDir)
}

// BrowserSessionVariable names the session chrome-devtools-axi keeps its
// bridge and its browser under. A terminal with none shares the tool's one
// unnamed session with every other program of the user, and on 2026-10-09 a
// bridge of that session held 2.5 GB that no owner could be proved for.
const BrowserSessionVariable = "CHROME_DEVTOOLS_AXI_SESSION"

// callerSessionVariables name the session a process runs in, in the order
// they are read: the explicit CFO_SESSION_ID and CFO_SESSION_HARNESS pair,
// then the variable each harness sets for the commands its agent runs, since
// a CFO in Claude Code, Codex or pi names only its own.
var callerSessionVariables = []struct{ session, harness string }{
	{"CFO_SESSION_ID", ""},
	{"CODEX_THREAD_ID", "codex"},
	{"CLAUDE_CODE_SESSION_ID", "claude"},
	{"PI_SESSION_ID", "pi"},
}

// CallerSession is the session this process runs in, as its environment
// names it, or empty strings when it names none a board can hold. A harness
// variable can be inherited, as a pi CFO started inside a Codex session
// carries that session's CODEX_THREAD_ID, so it is the caller only by
// default.
func CallerSession() (session, harness string) {
	for _, variable := range callerSessionVariables {
		session, harness = os.Getenv(variable.session), variable.harness
		if harness == "" {
			harness = os.Getenv("CFO_SESSION_HARNESS")
		}
		if session != "" {
			break
		}
	}
	return boardSession(session, harness)
}

// boardSession is session and harness when a board can hold them as a
// session's parent, and empty strings otherwise: a session of another
// harness, an overlong id or one with a line break is none.
func boardSession(session, harness string) (string, string) {
	if session == "" || len(session) > 256 || strings.ContainsAny(session, "\x00\r\n") || harness != "codex" && harness != "claude" && harness != "pi" {
		return "", ""
	}
	return session, harness
}

// IsCallerSessionVariable says name is one CallerSession reads, which a
// process that runs in no session, such as cfo serve, forgets from whoever
// started it.
func IsCallerSessionVariable(name string) bool {
	return strings.EqualFold(name, "CFO_SESSION_HARNESS") || slices.ContainsFunc(callerSessionVariables, func(variable struct{ session, harness string }) bool {
		return strings.EqualFold(name, variable.session)
	})
}

// nativeEnvironment stamps explicit task identity, and the goblin's parent
// as parentSession names it. The task's browser session is its own, named by
// its id, which is always a name the tool takes: at most 64 letters, digits,
// dots, dashes and underscores.
func (s Service) nativeEnvironment(env map[string]string, meta state.TaskMeta) {
	env["CFO_TASK_ID"], env["CFO_SPAWN_GEN"] = meta.ID, meta.SpawnGen
	env[BrowserSessionVariable] = meta.ID
	env["CFO_PARENT_SESSION_ID"], env["CFO_PARENT_HARNESS"] = s.parentSession(meta)
	env["CFO_ROOT_SESSION_ID"] = os.Getenv("CFO_ROOT_SESSION_ID")
}

// parentSession is the session a goblin records as its parent: the session
// its start explicitly names, else the registered CFO's, else the session
// its start runs in. Every goblin is dispatched by the CFO, so the CFO's
// registration is surer than a harness variable its start may have
// inherited, and it is the only CFO a start the supervisor runs by itself,
// such as a resume after a memory pause, knows. A helper's parent is its
// goblin, which its task record names, so a helper never takes the CFO.
func (s Service) parentSession(meta state.TaskMeta) (string, string) {
	if os.Getenv("CFO_SESSION_ID") == "" && meta.Parent == "" && s.CFOSession != nil {
		if session, harness := boardSession(s.CFOSession()); session != "" {
			return session, harness
		}
	}
	return CallerSession()
}
