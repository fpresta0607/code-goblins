package harness

import "strings"

// besideTurnCommands holds, for each harness, the built-in slash commands its
// own terminal runs at once while a turn runs, by name and alias. A command
// missing here is one the harness queues for the turn's end, refuses while a
// turn runs or runs only on some condition, so whoever types for it holds it
// for the turn's end (#2193, #2332). A session's own commands and skills are
// prompts for a turn in every harness, and are never here.
var besideTurnCommands = map[Kind]map[string]bool{
	Claude: names(claudeBesideTurn),
	Codex:  names(codexBesideTurn),
	Pi:     names(piBesideTurn),
}

// claudeBesideTurn are the commands Claude Code 2.1.322's own command table
// marks immediate, which it runs while a turn runs rather than queueing them,
// as read from that release on 2026-11-11, and claudeBesideTurnGivenWords
// those it marks so whenever words follow them. Both leave out what that
// table runs at once only in its fullscreen interface, which no read of a
// screen tells from its other one: /diff, /help and /theme, and /add-dir,
// /advisor and /config with no words after them. They leave out /background
// and /sandbox too, each immediate on a condition of its own.
const (
	claudeBesideTurn           = "artifacts autocompact brief btw bug share color daemon effort exit quit fast feedback focus goal hooks import loops mcp model permissions allowed-tools plugin plugins marketplace remote-control rc rename name skill-doctor skills status stop tasks bashes usage cost stats use-case version wellbeing breaks break-reminder downtime workflows"
	claudeBesideTurnGivenWords = "add-dir advisor config settings"
)

// codexBesideTurn are the commands whose available_during_task is true in
// codex-rs/tui/src/slash_command.rs at Codex 0.160.0. Codex answers every
// other command typed into a turn with a line saying it is disabled while a
// task is in progress, and drops it.
const codexBesideTurn = "diff resume model permissions copy raw rename mention skills hooks status daemon warnings pwd usage debug-config ps stop app goal voice mcp apps plugins title statusline approve feedback ide quit exit side btw agents subagents"

// piBesideTurn are the built-in commands pi 1.4.0's editor handles before it
// asks whether a turn is streaming (setupEditorSubmitHandler in its
// interactive mode), so each runs at once. It leaves out /reload, which
// answers that it waits for the response to finish.
const piBesideTurn = "settings scoped-models model thinking export import share bug copy name session changelog hotkeys fork clone tree trust login logout new compact debug resume quit"

var claudeGivenWords = names(claudeBesideTurnGivenWords)

func names(list string) map[string]bool {
	set := map[string]bool{}
	for _, name := range strings.Fields(list) {
		set[name] = true
	}
	return set
}

// RunsBesideTurn reports whether harness kind runs the slash command text at
// once while a turn runs, as its own terminal does with it typed there. A bang
// command, a session's own command and words are not such a command.
func RunsBesideTurn(kind Kind, text string) bool {
	command, isSlash := strings.CutPrefix(strings.TrimSpace(text), "/")
	if !isSlash {
		return false
	}
	name, words, _ := strings.Cut(command, " ")
	if kind == Claude && strings.TrimSpace(words) != "" && claudeGivenWords[name] {
		return true
	}
	return besideTurnCommands[kind][name]
}
