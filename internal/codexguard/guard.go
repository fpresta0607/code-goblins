// Package codexguard keeps a goblin's terminal from starting Codex while a
// Codex session would bill paid credits. Once Codex's included allowance is
// used up, every session it starts runs on credits, and the allowance floor
// holds only the goblins that run on Codex: on 2026-10-16 and 17 goblins on
// Claude Code started eight Codex sessions inside their own proofs and rigs,
// one pair of them billing 380.5 credits.
//
// Each goblin's terminal finds its own guard folder first on PATH. The folder
// is empty while Codex runs on its allowance, so codex is the one the user
// installed. While the allowance is used up the supervisor arms it with a
// codex of its own that only refuses: codex.cmd, which cmd, PowerShell and
// every program that searches PATHEXT find, and a shell script named codex,
// which Git Bash finds. Each runs cfo codex-guard, which says why in one line,
// wakes the CFO once, and starts nothing. The guard holds against a goblin
// that starts codex by name, as a rig, a probe or a test does; one that names
// the real program by its path, or runs with a PATH of its own, passes it.
package codexguard

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// Variable names the guard folders a terminal's PATH begins with, its own
// first, so a terminal started from it, such as a scratch home's goblin a rig
// spawns, keeps them all.
const Variable = "CFO_CODEX_GUARD"

// folder is the state folder that holds every task's guard folder.
const folder = "codex-guard"

// commandShown is how much of a refused command a wake names.
const commandShown = 300

// Dir is task id's guard folder in the home whose state folder is stateDir.
func Dir(stateDir, id string) string { return filepath.Join(stateDir, folder, id) }

// Chain is the guard folders a terminal's PATH begins with: own, the
// terminal's own, then inherited, the ones the environment that started it
// carries, each once, as Windows compares paths. Either may be empty, and a
// folder is named whether or not it is there: a disarmed guard is none.
func Chain(own, inherited string) string {
	var chain []string
	for _, dir := range append([]string{own}, filepath.SplitList(inherited)...) {
		if dir != "" && !slices.ContainsFunc(chain, func(kept string) bool { return strings.EqualFold(filepath.Clean(kept), filepath.Clean(dir)) }) {
			chain = append(chain, dir)
		}
	}
	return strings.Join(chain, string(os.PathListSeparator))
}

// Arm puts the refusing codex in task id's guard folder: it runs program, the
// home's cfo.exe, and says Codex's allowance is used up until until. A folder
// already armed so is left as it is.
func Arm(stateDir, program, id string, until time.Time) error {
	if err := state.ValidTaskID(id); err != nil {
		return err
	}
	dir := Dir(stateDir, id)
	guard := []string{"codex-guard", "--state", stateDir, "--task", id, "--until", until.UTC().Format(time.RFC3339)}
	cmdLine := `@"` + cmdEscaped(program) + `"`
	shLine := "exec " + shQuoted(filepath.ToSlash(program))
	for _, arg := range guard {
		cmdLine += ` "` + cmdEscaped(arg) + `"`
		shLine += " " + shQuoted(arg)
	}
	stubs := map[string]string{
		"codex.cmd": cmdLine + " -- %*\r\n",
		"codex":     "#!/bin/sh\n" + shLine + ` -- "$@"` + "\n",
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("codexguard: arm %s: %w", id, err)
	}
	for _, name := range slices.Sorted(maps.Keys(stubs)) {
		path := filepath.Join(dir, name)
		if current, err := fsx.ReadFile(path); err == nil && string(current) == stubs[name] {
			continue
		}
		if err := fsx.AtomicWriteFile(path, []byte(stubs[name])); err != nil {
			return fmt.Errorf("codexguard: arm %s: %w", id, err)
		}
	}
	return nil
}

// Disarm empties task id's guard folder, so its codex is the user's own again.
func Disarm(stateDir, id string) error {
	if err := state.ValidTaskID(id); err != nil {
		return err
	}
	return fsx.RemoveAll(Dir(stateDir, id))
}

// Prune removes the guard folder of every task live does not name.
func Prune(stateDir string, live []string) error {
	entries, err := os.ReadDir(filepath.Join(stateDir, folder))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("codexguard: read the guard folders: %w", err)
	}
	var problems error
	for _, entry := range entries {
		if !slices.ContainsFunc(live, func(id string) bool { return strings.EqualFold(id, entry.Name()) }) {
			problems = errors.Join(problems, fsx.RemoveAll(filepath.Join(stateDir, folder, entry.Name())))
		}
	}
	return problems
}

// Refuse is what task id's armed codex does when it is started with args at
// now: it returns the one line that says why codex started nothing and, the
// first time the task meets the guard before until, wakes the CFO with the
// goblin and the command. A guard the supervisor has not yet disarmed after
// until says Codex's allowance renewed and wakes nobody.
func Refuse(stateDir, id string, until time.Time, args []string, now time.Time) (string, error) {
	if err := state.ValidTaskID(id); err != nil {
		return "", err
	}
	renews := until.UTC().Format(time.RFC3339)
	if !now.Before(until) {
		return fmt.Sprintf("codex: refused, because the codex guard of task %s is still armed, though Codex's included allowance renewed at %s; the supervisor disarms it at its next fleet reading, within a minute, so run it again then", id, renews), nil
	}
	line := fmt.Sprintf("codex: refused, because Codex's included allowance is used up until %s, so a Codex session started now bills paid credits, which task %s is not authorized to spend; nothing was started and the CFO has been told", renews, id)
	command := shownCommand(args)
	detail := fmt.Sprintf("codex_refused: goblin %s started `%s` while Codex's included allowance is used up until %s, so the session would have billed paid credits; its codex guard refused it and started nothing. `cfo auth grant %s --codex-credits` authorizes the task to spend them", id, command, renews, id)
	_, isNew, err := wake.AppendFirst(stateDir, "codex-refused/"+id+"/"+renews, "allowance", id, detail)
	if err == nil && isNew {
		_, err = wake.PublishEpisode(stateDir)
	}
	if err != nil {
		return line, fmt.Errorf("codexguard: wake the CFO: %w", err)
	}
	return line, nil
}

// shownCommand is the refused command as a wake names it: codex and its
// arguments, each with a space quoted, cut to commandShown characters.
func shownCommand(args []string) string {
	words := []string{"codex"}
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\"") {
			arg = strconv.Quote(arg)
		}
		words = append(words, arg)
	}
	command := strings.Join(words, " ")
	if runes := []rune(command); len(runes) > commandShown {
		command = string(runes[:commandShown]) + "..."
	}
	return command
}

// cmdEscaped is s as cmd reads it back between double quotes: a percent sign
// doubled, which cmd would otherwise expand.
func cmdEscaped(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// shQuoted is s as one word of a shell script.
func shQuoted(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
