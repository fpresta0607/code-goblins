package afk

import (
	"strings"
	"time"
)

// stamp is how the notice and the report write a time.
const stamp = "2006-01-02 15:04 UTC"

func at(when time.Time) string { return when.UTC().Format(stamp) }

// Notice is what the session digest and every wake tell the CFO while AFK
// mode is on: who turned it on, when and from where, what the CFO decides
// itself under it, and what stays the Overlord's alone while he is away. The
// terms are his directive's own. It is empty while AFK mode is off.
func Notice(state State) []string {
	if !state.On {
		return nil
	}
	turned := "the Supreme Overlord turned it on " + at(state.Since) + " from " + state.From + ", and he is away until he turns it off."
	if state.Asked != "" {
		turned = "it was turned on " + at(state.Since) + " " + SwitchedBy(state.From, state.Asked) + ", and the Supreme Overlord is away until it is turned off."
	}
	return []string{
		"AFK MODE IS ON: " + turned,
		"It is complete autopilot: no Command Center prompt opens for him while it is on, and nothing is asked of him or held for him.",
		"Under it you decide these yourself, without waiting, and each is logged with its evidence:",
		"- The merge word for a goblin pull request that is gated or locally verified with the output read, green in CI on current main (its head holds main's tip, which is what makes its merge ref's first parent origin/main) and mergeable, recovery, security, money-path and production-deploy pull requests included. cfo pr merge <url> --verified \"<what verified it>\" checks it, logs it and merges it.",
		"- Each deploy, named and verified read-only. Log it: cfo afk log --kind deploy --what \"<the deploy>\" --evidence \"<what you read>\" [--link <url>].",
		"- A merged migration that adds or changes, applied to production and dev and read back from the applied list. Log it with --kind migration.",
		"- Installing a merged build through <candidate> update once the merge queue settles. Log it with --kind install.",
		"- A goblin's question that is yours to answer. cfo answer logs it.",
		"- Every other decision with choices, one you would have asked him. Answer it yourself with your recommendation, act on your answer, and log it with cfo afk log --kind left --what \"<the question>\" --evidence \"<what your answer stands on>\" --diagnosis \"<what is wrong, found to its cause>\" --tried \"<what you already tried>\" --option \"<choice>\" --option \"<choice>\" --answer \"<your choice>\": once he is back it is settled in his History with your answer, never asked of him, and a change he makes there reaches you once, as a change, so you undo or redo what yours started.",
		"While he is away only what a tool refuses or needs his own sign-in stays his: merging a teammate's pull request or deleting a branch through a merge, which cfo pr merge refuses, updating Code Goblins, any spend his account refuses, his own sign-ins and identity checks, and anything else a tool refuses. Everything else is yours to decide, a migration or command that drops or deletes data included, which you run only after a backup you can restore.",
		"Nothing is asked of him, while he is away or when he is back: never publish anything as a cfo question or a cfo run-request, since cfo question refuses while it is on, and nothing you log becomes a Command Center question while he is away. A step you cannot take, such as his own sign-in or a step a tool refuses, becomes one card in his Command Center once he is back: give it a backlog row in data/backlog.md, log it with cfo afk log --kind his --what \"<the step he must take, in plain words>\" --evidence \"<why you cannot take it, and its backlog row>\" [--link <url>] [--run <command.ps1|command.sh>], and work around it. Once you see to it yourself, settle it with cfo afk settle --at <its at> --how \"<what became of it>\". Tell a goblin blocked only on it to move to its next piece of work.",
	}
}

// Banner is the one line a wake with no room for the terms carries while AFK
// mode is on, and empty while it is off.
func Banner(state State) string {
	if !state.On {
		return ""
	}
	return "AFK mode is on since " + at(state.Since) + ": the Overlord is away, you decide under his authority and log each decision; cfo drain prints its terms and what stays his alone."
}

// NoticeFor is the notice for the home whose state directory is stateDir, as
// the digest and cfo drain print it: nothing while AFK mode is off, and one
// line saying so when its switch cannot be read, since leaving it out would
// read as off.
func NoticeFor(stateDir string) []string {
	state, err := Read(stateDir)
	if err != nil {
		return []string{unreadable(err)}
	}
	return Notice(state)
}

// BannerFor is the banner for that home, the same way, on one line.
func BannerFor(stateDir string) string {
	state, err := Read(stateDir)
	if err != nil {
		return unreadable(err)
	}
	return Banner(state)
}

// unreadable says the switch cannot be read, on one line.
func unreadable(err error) string {
	return "AFK MODE: UNREADABLE (" + strings.Join(strings.Fields(err.Error()), " ") + "): whether the Overlord is away is unknown, so decide nothing under its authority until it reads again."
}
