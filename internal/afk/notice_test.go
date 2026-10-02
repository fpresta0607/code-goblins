package afk

import (
	"os"
	"path/filepath"
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

// The digest, cfo drain and the wake banners all read the switch through
// these two, so what they print cannot drift: the notice or the banner while
// it is on, nothing while it is off, and a switch that cannot be read said as
// such rather than left out, which would read as off.
func TestTheNoticeAndBannerForAHomeFollowItsSwitch(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		dir := t.TempDir()

		if lines, banner := NoticeFor(dir), BannerFor(dir); len(lines) != 0 || banner != "" {
			t.Errorf("NoticeFor = %q, BannerFor = %q, want nothing", lines, banner)
		}
	})
	t.Run("on", func(t *testing.T) {
		dir, _ := turnedOn(t)

		lines, banner := NoticeFor(dir), BannerFor(dir)

		if len(lines) == 0 || !strings.HasPrefix(lines[0], "AFK MODE IS ON: the Supreme Overlord turned it on 2026-10-02 02:10 UTC") {
			t.Errorf("NoticeFor = %q, want the notice", lines)
		}
		if !strings.Contains(banner, "AFK mode is on since 2026-10-02 02:10 UTC") {
			t.Errorf("BannerFor = %q, want the banner", banner)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
			t.Fatal(err)
		}

		lines, banner := NoticeFor(dir), BannerFor(dir)

		if len(lines) != 1 || !strings.HasPrefix(lines[0], "AFK MODE: UNREADABLE (") {
			t.Errorf("NoticeFor = %q, want the unreadable switch said", lines)
		}
		if !strings.HasPrefix(banner, "AFK MODE: UNREADABLE (") || strings.Contains(banner, "\n") {
			t.Errorf("BannerFor = %q, want the unreadable switch said on one line", banner)
		}
	})
}
