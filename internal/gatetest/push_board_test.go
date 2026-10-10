package gatetest

import (
	"slices"
	"strings"
	"testing"
)

// board is a checkout whose board is installed, with three sources, a
// fixture page and the browser specs named.
func board(specs map[string]string) map[string]string {
	files := map[string]string{
		"frontend/node_modules/.keep":           "",
		"frontend/src/RunCard.tsx":              "",
		"frontend/src/DiskMeter.tsx":            "",
		"frontend/src/feedback.ts":              "",
		"frontend/src/feedback.test.ts":         "",
		"frontend/tests/fixtures/tip-sides.tsx": "",
		"frontend/tests/panel-states.ts":        "",
	}
	for name, content := range specs {
		files["frontend/tests/"+name] = content
	}
	return files
}

// A change to the board picks the board's checks: its type check, its lint,
// its unit tests, and the browser specs the change touched or names. A spec
// is touched when it changed, when a fixture page it opens changed, or when
// a helper it imports changed, and it is named when it shares a word of its
// name with a changed source: run-terminal with RunCard. The other specs are
// CI's, and the pick says how many.
func TestPushPicksTheBoardsChecksAndTheSpecsThatCoverTheChange(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, board(map[string]string{
		"run-terminal.spec.ts": "",
		"run-finishes.spec.ts": "",
		"goblin-run.spec.ts":   "",
		"tips.spec.ts":         `await page.goto("/tests/fixtures/tip-sides.html");`,
		"panel-row.spec.ts":    `import { states } from "./panel-states";`,
		"afk.spec.ts":          "",
		"disk-meter.spec.ts":   "",
		"quick-tour.spec.ts":   "",
	}), fleetPackages,
		"frontend/src/RunCard.tsx", "frontend/src/feedback.test.ts", "frontend/tests/afk.spec.ts", "frontend/tests/fixtures/tip-sides.tsx", "frontend/tests/panel-states.ts", "frontend/tests/gone.spec.ts")

	// Act
	pick := push(found, nil)

	// Assert
	var boards []Step
	for _, step := range pick.Steps {
		if step.Dir == "frontend" {
			boards = append(boards, step)
		}
	}
	wantCommands := [][]string{
		{"npm", "run", "typecheck"},
		{"npm", "run", "lint"},
		{"npm", "test"},
		{"npx", "playwright", "test", "--reporter=line", "tests/afk.spec.ts", "tests/tips.spec.ts", "tests/panel-row.spec.ts", "tests/goblin-run.spec.ts", "tests/run-finishes.spec.ts", "tests/run-terminal.spec.ts"},
	}
	if len(boards) != len(wantCommands) {
		t.Fatalf("push picks %d checks of the board, want %d:\n%s", len(boards), len(wantCommands), strings.Join(whats(pick), "\n"))
	}
	for index, want := range wantCommands {
		if !slices.Equal(boards[index].Command, want) {
			t.Errorf("check %d of the board runs %q, want %q", index+1, boards[index].Command, want)
		}
	}
	wantWhats := []string{
		"the board's type check (a file under frontend changed)",
		"the board's lint (a file under frontend changed)",
		"the board's unit tests (a file under frontend changed)",
		"6 browser specs (the change touched or names them)",
	}
	for index, want := range wantWhats {
		if got := boards[index].String(); got != want {
			t.Errorf("check %d of the board is %q, want %q", index+1, got, want)
		}
	}
	wantDetail := []string{
		"tests/afk.spec.ts (changed)",
		"tests/tips.spec.ts (opens the changed fixture tip-sides)",
		"tests/panel-row.spec.ts (imports the changed tests/panel-states.ts)",
		"tests/goblin-run.spec.ts (named like the changed src/RunCard.tsx)",
		"tests/run-finishes.spec.ts (named like the changed src/RunCard.tsx)",
		"tests/run-terminal.spec.ts (named like the changed src/RunCard.tsx)",
	}
	if got := boards[3].Detail; !slices.Equal(got, wantDetail) {
		t.Errorf("the specs are picked as\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantDetail, "\n"))
	}
	for _, wantLeft := range []string{
		"the 2 other browser specs (the change touched no file of theirs and names none of them)",
		"the board's build, the test that cfo embeds it and the licence check (CI's frontend job runs them)",
	} {
		if !slices.Contains(lefts(pick), wantLeft) {
			t.Errorf("push leaves to CI\n%s\nwant among it %s", strings.Join(lefts(pick), "\n"), wantLeft)
		}
	}
}

// The board's checks come after the changed packages and before the
// packages that only import what changed: what the goblin wrote is checked
// before the time limit can cut the pick short.
func TestPushChecksTheBoardBeforeThePackagesThatOnlyImportTheChange(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, board(nil), fleetPackages, "internal/auth/need.go", "frontend/src/RunCard.tsx")

	// Act
	pick := push(found, nil)

	// Assert
	got := whats(pick)
	changed, lint, importer := slices.Index(got, "tests of internal/auth (changed)"), slices.Index(got, "the board's lint (a file under frontend changed)"), slices.Index(got, "tests of internal/state (imports internal/auth)")
	if changed < 0 || lint < changed || importer < lint {
		t.Errorf("push picks\n%s\nwant the changed package, then the board's checks, then the importer", strings.Join(got, "\n"))
	}
}

// A change that touches nothing under frontend picks none of the board's
// checks and leaves nothing of the board to CI.
func TestPushLeavesTheBoardAloneWhenNothingUnderItChanged(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, board(map[string]string{"afk.spec.ts": ""}), fleetPackages, "internal/auth/need.go")

	// Act
	pick := push(found, nil)

	// Assert
	said := strings.Join(append(whats(pick), lefts(pick)...), "\n")
	if !slices.Contains(whats(pick), "tests of internal/auth (changed)") || strings.Contains(said, "board") || strings.Contains(said, "browser") {
		t.Errorf("push says of a change outside the board:\n%s\nwant the changed package and no word of the board", said)
	}
}

// A checkout whose board is not installed cannot run npm's checks: where
// npm finds no program it can exit 0 having run nothing. The pick runs none
// of them and says what to do, so the gap is read and not passed over.
func TestPushLeavesTheBoardToCIWhereItIsNotInstalled(t *testing.T) {
	// Arrange
	files := board(map[string]string{"run-terminal.spec.ts": ""})
	delete(files, "frontend/node_modules/.keep")
	found := checkout(t, fleet, files, fleetPackages, "frontend/src/RunCard.tsx")

	// Act
	pick := push(found, nil)

	// Assert
	if slices.ContainsFunc(pick.Steps, func(step Step) bool { return step.Dir == "frontend" }) {
		t.Errorf("push picks\n%s\nwant no check of a board that is not installed", strings.Join(whats(pick), "\n"))
	}
	if wantLeft := "the board's type check, lint, unit tests and browser specs (frontend/node_modules is missing here, so run npm ci in frontend and this again to check the board before you push)"; !slices.Contains(lefts(pick), wantLeft) {
		t.Errorf("push leaves to CI\n%s\nwant among it %s", strings.Join(lefts(pick), "\n"), wantLeft)
	}
}

// A file's words are the words of its name without what kind of file it is.
func TestWordsAreTheWordsOfAFileName(t *testing.T) {
	for name, want := range map[string][]string{
		"RunCard.tsx":               {"run", "card"},
		"run-terminal.spec.ts":      {"run", "terminal"},
		"terminalView.ts":           {"terminal", "view"},
		"afk.css":                   {"afk"},
		"baby-goblin-panel.spec.ts": {"baby", "goblin", "panel"},
		"useDictation.ts":           {"use", "dictation"},
	} {
		if got := words(name); !slices.Equal(got, want) {
			t.Errorf("words(%q) = %q, want %q", name, got, want)
		}
	}
}
