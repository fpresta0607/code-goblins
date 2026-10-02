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
// itself under it, and what stays the Overlord's alone. The terms are his
// directive's own. It is empty while AFK mode is off.
func Notice(state State) []string {
	if !state.On {
		return nil
	}
	return []string{
		"AFK MODE IS ON: the Supreme Overlord turned it on " + at(state.Since) + " from " + state.From + ", and he is away until he turns it off.",
		"No Command Center prompt opens for him while it is on: what waits on him is held for him, and cfo afk status lists it.",
		"Under it you decide these yourself, without waiting, and each is logged with its evidence:",
		"- The merge word for a goblin pull request that is gated or locally verified with the output read, green in CI on current main (its head holds main's tip, which is what makes its merge ref's first parent origin/main) and mergeable, recovery, security, money-path and production-deploy pull requests included. cfo pr merge <url> --verified \"<what verified it>\" checks it, logs it and merges it.",
		"- Each deploy, named and verified read-only. Log it: cfo afk log --kind deploy --what \"<the deploy>\" --evidence \"<what you read>\" [--link <url>].",
		"- A merged migration that adds or changes, applied to production and dev and read back from the applied list. Log it with --kind migration.",
		"- Installing a merged build through <candidate> update once the merge queue settles. Log it with --kind install.",
		"- A goblin's question that is yours to answer. cfo answer logs it.",
		"These stay his alone and are never decided for him, in AFK mode or out of it: a migration or command that drops or deletes data, deleting a branch, pushing to a teammate's branch or merging a teammate's pull request, any spend beyond his account's limits, his own sign-ins and identity checks, and anything a tool refuses.",
		"Leave each of those for him as a cfo question, or as a cfo run-request with the exact command, and tell a goblin blocked only on it to move to its next piece of work.",
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
