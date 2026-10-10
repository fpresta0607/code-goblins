package supervisor

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// ErrNotOwnSession says a process is not in the CFO's own session. Each
// error that wraps it says why.
var ErrNotOwnSession = errors.New("this is not the CFO's own session, which is the agent native terminal cfo runs")

// OwnSession proves this process runs in the CFO's own session and returns
// that session's harness, the process the home's session lock and
// registration name.
//
// The CFO's own session is the agent that native terminal cfo of this home
// runs: goblins, the board's first-run page, Restart and the comeback after a
// restart each start the CFO there, and nothing starts it anywhere else. A
// process is in it when its environment names that terminal, it is neither a
// goblin's nor a gate agent's, and the terminal's host record names a program
// that is one of its own ancestors, by pid and creation time, or, where its
// chain of parents stops short, it carries the terminal's proof value.
//
// `cfo install` puts the CFO's hooks in the user's settings, so they run in
// every Claude Code session on the machine, and on 2026-10-09 one the
// Overlord's desktop app reopened after a restart took the session lock
// before the CFO came back, was rewoken with the fleet's wakes and
// acknowledged them. Every session but this one is therefore left alone: it
// takes no lock, registers nothing, is never rewoken, and may not act as the
// CFO.
//
// The terminal's host is not asked. A host that does not answer cannot be
// typed into, which a registration cares about and Register checks, and its
// program still holds the home.
func OwnSession(stateDir string) (proc.Entry, error) {
	ancestry, at, err := ownProgram(stateDir)
	if err != nil {
		return proc.Entry{}, err
	}
	return ancestry[at], nil
}

// ownProgram is OwnSession, returning this process's ancestry with the
// harness's place in it.
func ownProgram(stateDir string) ([]proc.Entry, int, error) {
	notOwn := func(why string) ([]proc.Entry, int, error) {
		return nil, 0, fmt.Errorf("%w: %s", ErrNotOwnSession, why)
	}
	env := os.Environ()
	switch id := environmentValue(env, host.IDVariable); {
	case environmentValue(env, harness.RoleVariable) == harness.RoleGoblin:
		return notOwn("this one is a goblin's")
	case environmentValue(env, gateAgentVariable) != "":
		return notOwn("this one is a gate agent's")
	case id == "":
		return notOwn("this one runs in no native terminal")
	case id != NativeCFOTerminal:
		return notOwn("this one runs in native terminal " + id)
	}
	_, ancestry, at, err := terminalAncestry(stateDir, NativeCFOTerminal)
	if err != nil {
		return notOwn(err.Error())
	}
	return ancestry, at, nil
}

// terminalAncestry proves this process runs under the program of native
// terminal id and returns the terminal's record and this process's ancestry
// with the program's place in it: the record names one of this process's own
// ancestors, by pid and creation time, or, where the chain of parents stops
// short of it, this process carries the terminal's proof value. An inherited
// variable alone proves nothing.
func terminalAncestry(stateDir, id string) (host.Record, []proc.Entry, int, error) {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		return host.Record{}, nil, 0, fmt.Errorf("native terminal %s has no host record: %w", id, err)
	}
	ancestry, err := proc.Ancestry(os.Getpid(), 32)
	if err != nil {
		return host.Record{}, nil, 0, err
	}
	at := slices.IndexFunc(ancestry, func(entry proc.Entry) bool {
		// A record from before creation times names its program by pid alone.
		return entry.PID == record.ChildPID && (record.ChildStart.IsZero() || entry.Start.Equal(record.ChildStart))
	})
	if at < 0 {
		program, err := terminalProgram(record, os.Environ())
		if err != nil || len(ancestry) == 0 {
			return host.Record{}, nil, 0, fmt.Errorf("native terminal %s runs pid %d, and this command does not run under it", id, record.ChildPID)
		}
		ancestry, at = []proc.Entry{ancestry[0], program}, 1
	}
	return record, ancestry, at, nil
}

// NotTheCFO is why this process may not act as the CFO, or nil when it may:
// it runs in an agent's session that is not the CFO's own. The CFO's own
// session may, and so may a goblin's and a gate agent's, which keep the
// commands they have always had, and a terminal no agent harness runs, such
// as the Overlord's own.
func NotTheCFO(stateDir string) error {
	env := os.Environ()
	if environmentValue(env, harness.RoleVariable) == harness.RoleGoblin || environmentValue(env, gateAgentVariable) != "" {
		return nil
	}
	if _, _, err := ownProgram(stateDir); err == nil {
		return nil
	}
	// A process whose parents cannot be read is judged by its environment.
	ancestry, _ := proc.Ancestry(os.Getpid(), 32)
	if where := harnessMark(ancestry, env); where != "" {
		return fmt.Errorf("%w: this command runs %s", ErrNotOwnSession, where)
	}
	return nil
}
