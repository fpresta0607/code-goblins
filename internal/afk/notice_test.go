package afk

import (
	"strings"
	"testing"
)

func TestTheCFOIsToldNothingWhileAFKModeIsOff(t *testing.T) {
	for name, state := range map[string]State{
		"never on": {},
		"ended":    {Session: "afk-1", Since: night, Ended: night.Add(1), From: "the board"},
	} {
		t.Run(name, func(t *testing.T) {
			if lines := Notice(state); len(lines) != 0 {
				t.Errorf("Notice = %q, want nothing", lines)
			}
			if banner := Banner(state); banner != "" {
				t.Errorf("Banner = %q, want nothing", banner)
			}
		})
	}
}

// The notice is the authority the CFO acts on while the Overlord is away, so
// every term of his directive is pinned: what the CFO decides, the checks a
// merge word needs, and each thing that stays his alone.
func TestTheNoticeSaysWhoTurnedItOnWhenAndTheAuthoritysTerms(t *testing.T) {
	notice := strings.Join(Notice(State{On: true, Session: "afk-1", Since: night, From: "his own terminal (powershell.exe pid 4242)"}), "\n")

	for name, phrase := range map[string]string{
		"that it is on":                  "AFK MODE IS ON",
		"who turned it on":               "the Supreme Overlord turned it on",
		"when":                           "2026-10-02 02:10 UTC",
		"where":                          "his own terminal (powershell.exe pid 4242)",
		"no prompts":                     "No Command Center prompt opens for him",
		"held for him":                   "held for him",
		"logged with evidence":           "logged with its evidence",
		"merge: gated or verified":       "gated or locally verified with the output read",
		"merge: green on current main":   "green in CI on current main",
		"merge: the merge ref check":     "merge ref's first parent equals origin/main",
		"merge: mergeable":               "mergeable",
		"merge: the classes it covers":   "recovery, security, money-path and production-deploy pull requests included",
		"merge: the command":             "cfo pr merge <url> --verified",
		"deploy":                         "named and verified read-only",
		"migration":                      "A merged migration that adds or changes, applied to production and dev and read back",
		"install":                        "update once the merge queue settles",
		"answers":                        "A goblin's question that is yours to answer",
		"his alone: never decided":       "never decided for him",
		"his alone: destructive":         "a migration or command that drops or deletes data",
		"his alone: branch deletion":     "deleting a branch",
		"his alone: a teammate's work":   "pushing to a teammate's branch or merging a teammate's pull request",
		"his alone: spend":               "any spend beyond his account's limits",
		"his alone: identity":            "his own sign-ins and identity checks",
		"his alone: a tool's refusal":    "anything a tool refuses",
		"how to leave it for him":        "cfo run-request with the exact command",
		"the goblin moves on":            "move to its next piece of work",
		"the command that logs the rest": "cfo afk log --kind",
	} {
		if !strings.Contains(notice, phrase) {
			t.Errorf("the notice does not say %s (%q):\n%s", name, phrase, notice)
		}
	}
	if strings.ContainsRune(notice, 0x2014) {
		t.Error("the notice uses an em dash")
	}
}

func TestTheBannerIsOneLineThatSaysItIsOnAndWhereTheTermsAre(t *testing.T) {
	banner := Banner(State{On: true, Session: "afk-1", Since: night, From: "the board"})

	if strings.Contains(banner, "\n") || !strings.Contains(banner, "AFK mode is on") || !strings.Contains(banner, "2026-10-02 02:10 UTC") || !strings.Contains(banner, "cfo drain") {
		t.Errorf("Banner = %q, want one line saying it is on, since when, and that cfo drain prints the terms", banner)
	}
}
