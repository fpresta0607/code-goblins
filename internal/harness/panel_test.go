package harness

import (
	"reflect"
	"strings"
	"testing"
)

// What a harness draws in its input line's place reads as a panel the board
// can offer as a list: the rows that ask or show, each choice with the one in
// focus and its number, and the keys its footer names. A choice's wrapped
// rows and a detail under it in brackets join it, and the conversation above
// the panel is left out. The permission prompt is Claude Code 2.1.302's,
// recorded in a native terminal on 2026-10-17, the hook review Codex 0.160's
// and the trust prompt pi 0.85.1's; the picker and the panel are made up in
// the shape of Claude Code's model picker and permissions panel.
func TestAnOpenScreenReadsAsItsRowsChoicesAndKeys(t *testing.T) {
	rule, dashes := strings.Repeat("─", 56), strings.Repeat("╌", 56)
	for name, test := range map[string]struct {
		screen []string
		want   Panel
	}{
		"Claude Code's permission prompt": {
			[]string{"❯ Create a file named nine.txt containing the word nine.", "", "● Write(nine.txt)", "", rule, " Create file", " nine.txt", dashes, "  1 nine", dashes,
				" Do you want to create nine.txt?", " ❯ 1. Yes", "   2. Yes, and switch to accept edits (auto-approve file", "      edits and common file commands) for this session", "      (shift+tab)", "   3. No", ""},
			Panel{Rows: []string{"Create file", "nine.txt", dashes, " 1 nine", dashes, "Do you want to create nine.txt?"}, Choices: []Choice{
				{Text: "Yes", Number: 1, Focused: true},
				{Text: "Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)", Number: 2},
				{Text: "No", Number: 3},
			}},
		},
		"Claude Code's model picker": {
			[]string{"● Done. The branch is pushed.", "", " Select model", " Switch between Claude models.", "", " ❯ 1. Default (recommended)", "   2. Sonnet", "   3. Haiku", "", " Enter to confirm · Esc to exit"},
			Panel{Rows: []string{"Select model", "Switch between Claude models."}, Choices: []Choice{{Text: "Default (recommended)", Number: 1, Focused: true}, {Text: "Sonnet", Number: 2}, {Text: "Haiku", Number: 3}}, Hints: []string{"Enter to confirm · Esc to exit"}},
		},
		"Claude Code's permissions panel": {
			[]string{"● Done. The branch is pushed.", "", " Permissions:  Allow   Ask   Deny   Workspace", "", "  ❯ Add a new rule…", "    Bash(go test:*)", "    Read(./**)", "", " Press ↑↓ to navigate · Enter to select · Esc to cancel"},
			Panel{Rows: []string{"Permissions:  Allow   Ask   Deny   Workspace"}, Choices: []Choice{{Text: "Add a new rule…", Focused: true}, {Text: "Bash(go test:*)"}, {Text: "Read(./**)"}}, Hints: []string{"Press ↑↓ to navigate · Enter to select · Esc to cancel"}},
		},
		"Codex's hook review": {
			[]string{"• Ran go test ./...", "", "  Hooks need review", "  2 hooks are new or changed.", "› 1. Review hooks", "  2. Trust all and continue", "  3. Continue without trusting (hooks won't run)", "", "Press enter to confirm or esc to go back"},
			Panel{Rows: []string{"Hooks need review", "2 hooks are new or changed."}, Choices: []Choice{{Text: "Review hooks", Number: 1, Focused: true}, {Text: "Trust all and continue", Number: 2}, {Text: "Continue without trusting (hooks won't run)", Number: 3}}, Hints: []string{"Press enter to confirm or esc to go back"}},
		},
		"pi's trust prompt": {
			[]string{" Trust project folder?", " C:\\dev\\app", "", " This allows pi to load .pi settings.", "", " → Trust", " Trust parent folder", " (C:\\dev)", " Trust (this session only)", " Do not trust", " Do not trust (this session only)", "", " ↑↓ navigate enter select escape/ctrl+c cancel"},
			Panel{Rows: []string{"Trust project folder?", "C:\\dev\\app", "", "This allows pi to load .pi settings."}, Choices: []Choice{{Text: "Trust", Focused: true}, {Text: "Trust parent folder (C:\\dev)"}, {Text: "Trust (this session only)"}, {Text: "Do not trust"}, {Text: "Do not trust (this session only)"}}, Hints: []string{"↑↓ navigate enter select escape/ctrl+c cancel"}},
		},
		"a panel with nothing to choose": {
			[]string{"● Done.", "", " Total cost: $0.12", " Total duration: 3m 4s", "", " Esc to close"},
			Panel{Rows: []string{"Total cost: $0.12", "Total duration: 3m 4s"}, Hints: []string{"Esc to close"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			got := ReadPanel(test.screen)

			// Assert
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ReadPanel read\n%#v\nwant\n%#v", got, test.want)
			}
		})
	}
}

// A panel taller than the screen's last rows a panel can take keeps its
// choices and its newest rows, and an empty screen reads as nothing.
func TestAPanelReadsOnlyTheScreensLastRows(t *testing.T) {
	// Arrange
	screen := []string{" Choose one"}
	for range panelRowLimit {
		screen = append(screen, " a line of a long preview")
	}
	screen = append(screen, " ❯ 1. Yes", "   2. No")

	// Act
	got, empty := ReadPanel(screen), ReadPanel(nil)

	// Assert
	if len(got.Rows)+len(got.Choices) > panelRowLimit || len(got.Choices) != 2 || got.Rows[0] != "a line of a long preview" {
		t.Errorf("a long panel read %d rows and %d choices, first row %q", len(got.Rows), len(got.Choices), got.Rows[0])
	}
	if !reflect.DeepEqual(empty, Panel{}) {
		t.Errorf("an empty screen read %#v", empty)
	}
}

// The permission mode a composer's footer names reads as the mode, from
// Claude Code's footer rows only, never from a reply above that quotes one,
// and a harness without modes or a screen with no footer reads as none. The
// footer rows are Claude Code 2.1.302's.
func TestAComposerFooterNamesItsPermissionMode(t *testing.T) {
	rule := strings.Repeat("─", 56)
	for name, test := range map[string]struct {
		kind   Kind
		screen []string
		want   string
	}{
		"auto mode":                   {Claude, []string{"● Done.", rule, "❯ ", rule, "  ⏵⏵ auto mode on (shift+tab to cycle)"}, "auto mode"},
		"accept edits with an update": {Claude, []string{"● Done.", rule, "❯ ", rule, "  ⏵⏵ accept edits on (shift+tab to cycle)", "  ✓ Update installed · Restart to update"}, "accept edits"},
		"a reply that quotes a mode":  {Claude, []string{"● It runs with plan mode on.", "", "", "", " Select model", " ❯ 1. Default", "   2. Sonnet", " Enter to confirm · Esc to exit"}, ""},
		"Codex":                       {Codex, []string{"› Ask Codex to do anything", "  100% context left"}, ""},
		"nothing":                     {Claude, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			screens, _ := NativeScreens(test.kind)
			if got := screens.Mode(test.screen); got != test.want {
				t.Errorf("Mode(%q) = %q, want %q", test.screen, got, test.want)
			}
		})
	}
}
