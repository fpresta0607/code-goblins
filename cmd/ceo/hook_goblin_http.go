package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblinHookHandler does, in the board's own process, the work of a Claude
// Code goblin's hooks that run at every tool call, which its Claude Code
// posts to the board (install.GoblinHooks): the seal before a command, and
// the native hook with the key alarm after every tool call. Each was a ceo
// process of its own, and the goblins' were most of the processes the fleet
// started (#1610).
//
// A hook carries the goblin's variables in its headers (install.HookHeader),
// and only one its terminal's proof proves is the goblin's: the terminal it
// names is its task's, and its proof is one the terminal was given. Any
// other is refused, and the board refuses one from off this PC before it
// comes here (supervisor.HTTP.GoblinHooks), so no web page, other user or
// other goblin can speak for a goblin. A seal refused or not answered
// refuses the command (install.Hook.ShouldBlockOnFailure).
type goblinHookHandler struct {
	home home.Home
}

func (g goblinHookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "a goblin hook posts its event", http.StatusMethodNotAllowed)
		return
	}
	getenv := func(variable string) string { return r.Header.Get(install.HookHeader(variable)) }
	task, terminal, proof := getenv("CEO_TASK_ID"), getenv(host.IDVariable), getenv(host.ProofVariable)
	if getenv(harness.RoleVariable) != harness.RoleGoblin || state.ValidTaskID(task) != nil || terminal != task || proof == "" {
		http.Error(w, "a goblin hook names its goblin's task, terminal and proof", http.StatusForbidden)
		return
	}
	proofs, err := host.Proofs(g.home.State, terminal)
	if err != nil || !slices.Contains(proofs, host.ProofSum(proof)) {
		http.Error(w, "the hook's proof is not its goblin's terminal's", http.StatusForbidden)
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, nativehook.MaxInputBytes))
	if maxBytes := (*http.MaxBytesError)(nil); errors.As(err, &maxBytes) {
		http.Error(w, "the hook's event exceeds 64 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, "the hook's event could not be read", http.StatusBadRequest)
		return
	}
	var stdout, stderr bytes.Buffer
	exit := 0
	switch r.URL.Path {
	case install.GoblinSealPath:
		// Claude Code names no folder it started in to an HTTP hook, and the
		// goblin's own cwd is wherever it went, so the seal reads the
		// worktree the goblin's task record names, the folder its Claude
		// Code started in.
		meta, err := state.ReadTaskMeta(g.home.State, task)
		if err != nil {
			http.Error(w, "the goblin's task record could not be read, so its command waits", http.StatusServiceUnavailable)
			return
		}
		if event, ok := claudehook.ReadPayload(bytes.NewReader(payload)); ok {
			if refusal := goblinToolRefusal(g.home, meta, meta.Worktree, event); refusal != "" {
				exit = claudehook.DenyPreToolOnStdout(&stdout, refusal)
			}
		}
	case install.GoblinToolPath:
		exit = runHookParts(&stdout, &stderr,
			hookPart{name: "the native hook", limit: nativeHookLimit, run: func(stdout, stderr io.Writer) int {
				return nativeHookEvent("claude", g.home.Root, g.home.State, getenv, bytes.NewReader(payload), stdout, stderr)
			}},
			hookPart{name: "the key alarm", limit: keyAlarmLimit, run: func(_, stderr io.Writer) int {
				return goblinKeyAlarm("claude", g.home.State, getenv, bytes.NewReader(payload), stderr)
			}},
		)
	default:
		http.NotFound(w, r)
		return
	}
	if exit != 0 {
		http.Error(w, stderr.String(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(stdout.Bytes())
}
