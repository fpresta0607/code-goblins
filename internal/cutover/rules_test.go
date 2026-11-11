package cutover

import (
	"strings"
	"testing"
)

func TestTheRulesRenameTheCommandAndKeepEveryOtherWord(t *testing.T) {
	r := newRules([]string{"gate", "spawn", "help"}, []string{newName + "-afk"})
	upper := strings.ToUpper(oldName)
	for _, test := range []struct {
		name, text, want string
	}{
		{"a call of a command", "run " + oldName + " gate test", "run " + newCommand + " gate test"},
		{"a call at a line's start", oldName + "  spawn cg-x", newCommand + "  spawn cg-x"},
		{"a word that is no command", "the " + oldName + " decides", "the " + oldName + " decides"},
		{"the program", `C:\home\bin\` + oldName + `.exe watch`, `C:\home\bin\` + newCommand + `.exe watch`},
		{"the hook program", oldName + "-hook.exe", newCommand + "-hook.exe"},
		{"a setting", "$env:" + upper + "_HOME and %" + upper + "_PROJECTS_ROOT%", "$env:" + strings.ToUpper(newCommand) + "_HOME and %" + strings.ToUpper(newCommand) + "_PROJECTS_ROOT%"},
		{"the command in backticks", "`" + oldName + "`", "`" + newCommand + "`"},
		{"a skill", "read " + oldName + "-afk first", "read " + newName + "-afk first"},
		{"a skill this build does not ship", "read " + oldName + "-mine", "read " + oldName + "-mine"},
		{"a task's id", "task cg-" + oldName + "-wakes and " + oldName + "-native-board", "task cg-" + oldName + "-wakes and " + oldName + "-native-board"},
		{"a state file", `state\` + oldName + `-engine.json.`, `state\` + newName + `-engine.json.`},
		{"a handoff note", oldName + "-handoff-2026-11-05.md", newName + "-handoff-2026-11-05.md"},
		{"the hooks' marker", "# " + upper + " native hooks v1", "# " + strings.ToUpper(newName) + " native hooks v1"},
		{"the last update's copy", `bin\previous-` + oldName + `.exe`, `bin\previous-` + newCommand + `.exe`},
		{"identifiers side by side", "primary-" + oldName + " previous-" + oldName, "primary-" + newName + " previous-" + newCommand},
		{"a branch that begins with an identifier", "feat/board-" + oldName + "-entry and board-" + oldName + "-queue", "feat/board-" + oldName + "-entry and board-" + oldName + "-queue"},
		{"the role in prose", "The " + upper + " decides", "The " + upper + " decides"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := r.text(test.text, nil)

			// Assert
			if got != test.want {
				t.Fatalf("rewrote %q as %q, want %q", test.text, got, test.want)
			}
		})
	}
}

func TestTheRulesKeepTheOverlordsQuotedWords(t *testing.T) {
	// Arrange
	r := newRules([]string{"gate"}, nil)
	text := "Run " + oldName + " gate test.\nHe wrote: \"" + oldName + " gate test hangs\"\n> " + oldName + " gate test is his\nHe wrote “" + oldName + " gate test”"

	// Act
	got := r.text(text, quotedWords)

	// Assert
	want := "Run " + newCommand + " gate test.\nHe wrote: \"" + oldName + " gate test hangs\"\n> " + oldName + " gate test is his\nHe wrote “" + oldName + " gate test”"
	if got != want {
		t.Fatalf("rewrote\n%q as\n%q, want\n%q", text, got, want)
	}
}

func TestTheJSONRulesRenameIdentifiersAndKeepProse(t *testing.T) {
	// Arrange
	r := newRules([]string{"gate"}, nil)
	text := `{"host":"` + oldName + `","started_by_` + oldName + `":true,"` + oldName + `_identity":"x","labels":["` + oldName + `: running"],"fast":[["` + oldName + `","gate"]],"problem":"The ` + strings.ToUpper(oldName) + ` started","task":"cg-` + oldName + `-x"}`

	// Act
	got := r.json(text)

	// Assert
	want := `{"host":"` + newName + `","started_by_` + newName + `":true,"` + newName + `_identity":"x","labels":["` + newName + `: running"],"fast":[["` + newCommand + `","gate"]],"problem":"The ` + strings.ToUpper(oldName) + ` started","task":"cg-` + oldName + `-x"}`
	if got != want {
		t.Fatalf("rewrote\n%s as\n%s, want\n%s", text, got, want)
	}
}

func TestTheNewSpellingKeepsEachLettersCase(t *testing.T) {
	for _, test := range []struct{ old, want string }{
		{oldName, newName},
		{strings.ToUpper(oldName), strings.ToUpper(newName)},
		{"C" + oldName[1:], "C" + newName[1:]},
		{"x-" + oldName + "-y", "x-" + newName + "-y"},
	} {
		if got := newSpelling(test.old); got != test.want || oldSpelling(got) != test.old {
			t.Errorf("newSpelling(%q) = %q, and back %q; want %q", test.old, got, oldSpelling(got), test.want)
		}
	}
}
