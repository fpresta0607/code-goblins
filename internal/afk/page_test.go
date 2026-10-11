package afk

import (
	"strings"
	"testing"
	"time"
)

// pageAnswer is the CFO's answer to a goblin's page while AFK mode is on.
func pageAnswer() Entry {
	return Entry{
		Kind: KindAnswer, What: "notify-cg-dot-41", Task: "cg-dot", Evidence: "asked: Approve the mockups? answered: Build it as shown",
		Link: "http://127.0.0.1:4387/session/f26e", Question: "Approve the mockups? (page http://127.0.0.1:4387/session/f26e)",
		Options: []string{"Build it as shown", "Change it"}, Recommendation: "Build it as shown", Answer: "Build it as shown",
	}
}

// The CFO's answer to a goblin's page is asked of the Overlord once AFK mode
// turns off, with the CFO's choice checked, so the log takes it only when that
// question can be asked: its choice is one of its choices, and so is the
// choice the goblin recommended.
func TestAnAnswerToAPageIsLoggedOnlyWhenItCanBeAskedOfHim(t *testing.T) {
	for name, test := range map[string]struct {
		change func(*Entry)
		says   string
	}{
		"as it is":                     {func(*Entry) {}, ""},
		"no question":                  {func(entry *Entry) { entry.Question = " " }, "the question"},
		"an answer it does not offer":  {func(entry *Entry) { entry.Answer = "Ship it" }, "one of its choices"},
		"a recommendation not offered": {func(entry *Entry) { entry.Recommendation = "Ship it" }, "one of its choices"},
		"a choice twice":               {func(entry *Entry) { entry.Options = []string{"Build it as shown", "Build it as shown"} }, "distinct"},
		"no choices":                   {func(entry *Entry) { entry.Options = nil }, "distinct"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir, on := turnedOn(t)
			entry := pageAnswer()
			test.change(&entry)

			// Act
			logged, err := Log(dir, entry, night.Add(time.Hour))

			// Assert
			if test.says == "" {
				if err != nil || logged.Link != entry.Link || logged.Answer != entry.Answer || !OnPage(logged) {
					t.Fatalf("Log = %+v, %v, want the answer to the page logged", logged, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.says) {
				t.Errorf("Log = %v, want it refused naming %q", err, test.says)
			}
			if entries, _, _ := Entries(dir, on.Session); len(entries) != 1 {
				t.Errorf("log = %+v, want only the switch", entries)
			}
		})
	}
}

// The question an answer to a page is asked as names it by when it was
// logged, apart from the questions of lines left for him.
func TestAnAnswerToAPageIsAskedUnderItsOwnID(t *testing.T) {
	entry := pageAnswer()
	entry.At = night

	if got := PageQuestionID(entry); got != "afk-page-20261002T021000.000000000Z" || got == LeftQuestionID(entry) {
		t.Errorf("PageQuestionID = %q, want afk-page- and when it was logged", got)
	}
	plain := entry
	plain.Link = ""
	if OnPage(plain) {
		t.Error("an answer with no page reads as an answer to a page")
	}
}
