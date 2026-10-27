package harness

import (
	"strings"
	"testing"
)

// Claude's trust dialog focuses "No, exit" first. Only the focus on "Yes, I
// trust this folder" is its answer, and the dialog is recognized however its
// sentence wraps. Claude Code marks the focus with ❯ in a terminal it knows
// draws Unicode and with > in any other, as in a native terminal, whose
// environment names no such terminal: live on 2026-10-01, Claude Code 2.1.287
// in a native goblin's terminal drew "> No, exit".
func TestClaudesTrustDialogIsAnsweredOnlyOnYes(t *testing.T) {
	screens, _ := NativeScreens(Claude)
	for name, test := range map[string]struct {
		screen []string
		answer bool
	}{
		"focus on No, exit":       {[]string{" Quick safety check: Is this a project you created or one you", "trust? (Like your own code, a", " ❯ No, exit", "   Yes, I trust this folder"}, false},
		"focus on Yes":            {[]string{" Quick safety check: Is this a project you created or one you trust?", "   No, exit", " ❯ Yes, I trust this folder"}, true},
		"ASCII focus on No, exit": {[]string{" Do you trust the files in this folder?", " Claude Code'll be able to read, edit, and execute files here.", " Security guide", " > No, exit", "   Yes, I trust this folder", " Enter to confirm · Esc to cancel"}, false},
		"ASCII focus on Yes":      {[]string{" Do you trust the files in this folder?", " Security guide", "   No, exit", " > Yes, I trust this folder", " Enter to confirm · Esc to cancel"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			dialog, found := screens.Dialog(test.screen)
			if !found {
				t.Fatalf("Dialog(%q) found none, want the trust dialog", test.screen)
			}
			focused, ok := dialog.Focused(test.screen)
			if !ok {
				t.Fatalf("Focused(%q) cannot read the focus, want the focused option", test.screen)
			}
			if answer := dialog.Chosen(focused); answer != test.answer {
				t.Errorf("focused %q (%v): answer = %v, want %v", focused, ok, answer, test.answer)
			}
		})
	}
}

// Codex's update prompt is answered with Skip, never Update now or Skip until
// next version, and its hook review prompt only with Continue without
// trusting, never Review hooks or Trust all, its summary row saying how many
// hooks were left untrusted.
func TestCodexsUpdatePromptIsAnsweredOnlyWithSkip(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	update := []string{"  ✨ Update available! 0.154.0 -> 0.157.0", "", "  1. Update now (runs `npm install -g @openai/codex`)", "  2. Skip", "  3. Skip until next version"}
	for focus, want := range map[int]bool{2: false, 3: true, 4: false} {
		screen := append([]string(nil), update...)
		screen[focus] = "› " + screen[focus][2:]
		dialog, found := screens.Dialog(screen)
		focused, ok := dialog.Focused(screen)
		if !found || !ok || dialog.Chosen(focused) != want {
			t.Errorf("focus on %q: dialog %q, chosen %v; want %v", focused, dialog.Name, dialog.Chosen(focused), want)
		}
	}
	hooks := []string{"  Hooks need review", "  2 hooks are new or changed.", "› 1. Review hooks", "  2. Trust all and continue", "  3. Continue without trusting (hooks won't run)", "", "Press enter to confirm or esc to go back"}
	dialog, found := screens.Dialog(hooks)
	for option, want := range map[string]bool{"1. Review hooks": false, "2. Trust all and continue": false, "3. Continue without trusting (hooks won't run)": true} {
		if !found || dialog.Chosen(option) != want {
			t.Errorf("hook review prompt (found %v) chooses %q: %v, want %v", found, option, dialog.Chosen(option), want)
		}
	}
	if dialog.Summary == nil || dialog.Summary.FindString(strings.Join(hooks, "\n")) != "2 hooks are new or changed" {
		t.Errorf("hook review summary = %v, want it to find \"2 hooks are new or changed\"", dialog.Summary)
	}
}

// Codex 0.160 asks whether to trust a folder it was never told to trust with
// a new prompt, captured live on 2026-10-07 when a spawn into a new project
// gave up on it after two minutes. It is answered as the older directory
// trust prompt is, with Trust and continue, never Quit, however its sentence
// wraps; Escape would quit Codex.
func TestCodexsFolderTrustPromptIsAnsweredWithTrustAndContinue(t *testing.T) {
	// Arrange
	screens, _ := NativeScreens(Codex)
	prompt := []string{
		"  Note: You’re in a subdirectory of a Git project. Trusting will apply to the repository root:",
		`  c:\users\o\appdata\local\temp\esc-proof\projects\esc-proof`,
		"",
		"  Trust this folder? Codex can read, edit, and run files here, subject to your permission settings. Folder settings",
		"  can run code automatically, even without a model request. Continue only if you trust these files. Your trust",
		"  decision will be saved.",
		"",
		"› 1. Trust and continue",
		"  2. Quit",
		"",
		"  enter continue · esc quit",
	}
	wrapped := []string{"  Trust this", "  folder? Codex can read, edit, and run files here.", "  1. Trust and continue", "› 2. Quit", "  enter continue · esc quit"}
	for name, test := range map[string]struct {
		screen []string
		chosen bool
	}{
		"focus on Trust and continue": {prompt, true},
		"wrapped, focus on Quit":      {wrapped, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			dialog, found := screens.Dialog(test.screen)
			focused, ok := dialog.Focused(test.screen)

			// Assert
			if !found || !ok {
				t.Fatalf("Dialog = %q (found %v), focus %q (read %v); want the folder trust prompt with its focus read", dialog.Name, found, focused, ok)
			}
			if dialog.Chosen(focused) != test.chosen {
				t.Errorf("focus on %q: chosen %v, want %v", focused, dialog.Chosen(focused), test.chosen)
			}
			if screens.IsReady(test.screen) {
				t.Error("the prompt reads as Codex's composer waiting for input")
			}
		})
	}
}

func TestCodexsOptionalDaybreakOfferBlocksTheComposer(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	for name, test := range map[string]struct {
		rows      []string
		isOffered bool
	}{
		"offer":                       {[]string{"Set up security for Daybreak mode", "Set up Advanced Account Security with a hardware security key.", "› 1. Set up security", "Press a number to choose · esc to dismiss · type to continue"}, true},
		"wrapped offer":               {[]string{"Set up security for", "Daybreak mode", "› 1. Set up security", "Press a number to choose · esc to", "dismiss · type to continue"}, true},
		"earlier title in transcript": {[]string{"Set up security for Daybreak mode"}, false},
		"title without the optional offer footer": {[]string{"Set up security for Daybreak mode", "esc to dismiss"}, false},
		"another security screen":                 {[]string{"Set up Advanced Account Security", "esc to dismiss"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			rows := append(test.rows, "› Ask Codex to do anything", "100% context left")
			dialog, isFound := screens.Dialog(rows)
			if isFound != test.isOffered {
				t.Errorf("dialog found %v, want %v", isFound, test.isOffered)
			}
			if dialog.Chosen("1. Set up security") {
				t.Error("security enrollment is never an automatic answer")
			}
			if isReady := screens.IsReady(rows); isReady == test.isOffered {
				t.Errorf("ready %v while offered %v", isReady, test.isOffered)
			}
			if isEmpty := screens.ComposerEmpty(rows); isEmpty == test.isOffered {
				t.Errorf("empty composer %v while offered %v", isEmpty, test.isOffered)
			}
		})
	}
}

// Pi's project trust prompt, captured on pi 0.85.1 started without --approve,
// focuses "Trust" first and is answered only with "Trust (this session
// only)", which saves nothing to pi's trust store.
func TestPisTrustPromptIsAnsweredOnlyForThisSession(t *testing.T) {
	screens, _ := NativeScreens(Pi)
	screen := []string{" Trust project folder?", " C:\\dev\\app", "", " This allows pi to load .pi settings and resources, install missing project packages, and execute project extensions.", "", " → Trust", " Trust parent folder", " (C:\\dev)", " Trust (this session only)", " Do not trust", " Do not trust (this session only)", "", " ↑↓ navigate enter select escape/ctrl+c cancel"}

	dialog, found := screens.Dialog(screen)
	focused, ok := dialog.Focused(screen)

	if !found || !ok || focused != "Trust" {
		t.Fatalf("Dialog found %v, focused %q (%v); want the trust prompt focused on \"Trust\"", found, focused, ok)
	}
	for option, want := range map[string]bool{"Trust": false, "Trust parent folder": false, "Trust (this session only)": true, "Do not trust": false, "Do not trust (this session only)": false} {
		if got := dialog.Chosen(option); got != want {
			t.Errorf("Chosen(%q) = %v, want %v", option, got, want)
		}
	}
}

// Each harness's composer reads as ready only while no turn runs.
func TestAComposerIsReadyOnlyWhileNoTurnRuns(t *testing.T) {
	for kind, test := range map[Kind]struct {
		ready, working []string
	}{
		Claude: {
			ready:   []string{"❯ Try \"fix typecheck errors\"", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
			working: []string{"✽ Reticulating…", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		},
		Codex: {
			ready:   []string{"• Working tree is clean and all tests pass.", "› Ask Codex to do anything", "  100% context left"},
			working: []string{"• Working (12s • esc to", "› Ask Codex to do anything", "  100% context left"},
		},
		Pi: {
			ready:   []string{"────", "0.0%/1.0M (auto)                    (openrouter) z-ai/glm-5.3-flash • high"},
			working: []string{"── ⠸ Working ──", "0.0%/1.0M (auto)                    (openrouter) z-ai/glm-5.3-flash • high"},
		},
	} {
		screens, _ := NativeScreens(kind)
		if !screens.IsReady(test.ready) || screens.IsWorking(test.ready) {
			t.Errorf("%s: %q reads as ready %v, working %v; want ready", kind, test.ready, screens.IsReady(test.ready), screens.IsWorking(test.ready))
		}
		if screens.IsReady(test.working) || !screens.IsWorking(test.working) {
			t.Errorf("%s: %q reads as ready %v, working %v; want working", kind, test.working, screens.IsReady(test.working), screens.IsWorking(test.working))
		}
	}
}

// A harness that waits on an answer at a screen of its own, which only its
// terminal takes, asks: a dialog it is known to draw, or Claude Code's list
// of numbered choices with one focused while no composer, footer or turn
// shows. The permission prompt is Claude Code 2.1.302's, recorded in a native
// terminal on 2026-10-17. A numbered prompt or reply above a waiting
// composer, a dialog's words a diff quotes above it, and a turn under a list
// never ask, and a mode a reply names above a list never stops it asking. The
// quoting diff and the reply are made up.
func TestAHarnessAsksAtItsOwnScreenOnlyWhileItsChoicesWaitWithNoComposer(t *testing.T) {
	permission := []string{"❯ Create a file named nine.txt containing the word nine.", "", "● Write(nine.txt)", "", strings.Repeat("─", 56), " Create file", " nine.txt", strings.Repeat("╌", 56), "  1 nine", strings.Repeat("╌", 56),
		" Do you want to create nine.txt?", " ❯ 1. Yes", "   2. Yes, and switch to accept edits (auto-approve file", "      edits and common file commands) for this session", "      (shift+tab)", "   3. No", ""}
	composer := []string{strings.Repeat("─", 56), "❯ ", strings.Repeat("─", 56), "  ⏵⏵ auto mode on (shift+tab to cycle)"}
	quoted := []string{"● Update(internal/harness/screens.go)", "  ⎿  Updated internal/harness/screens.go with 1 addition", `      84    Markers: []string{"Is this a project you created or one you trust?", "Do you trust the files in this folder?"},`, "",
		strings.Repeat("─", 56), "❯ ", strings.Repeat("─", 56), "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	named := []string{"● This session runs with manual mode on, so each write asks first.", "", "● Write(nine.txt)", " Do you want to create nine.txt?", " ❯ 1. Yes", "   2. No"}
	for name, test := range map[string]struct {
		kind   Kind
		screen []string
		asks   bool
	}{
		"Claude Code's permission prompt":              {Claude, permission, true},
		"its prompt with the focus on No":              {Claude, []string{" Do you want to create nine.txt?", "   1. Yes", "   2. Yes, and switch to accept edits", " ❯ 3. No"}, true},
		"its prompt where no Unicode is drawn":         {Claude, []string{" Do you want to proceed?", " > 1. Yes", "   2. No"}, true},
		"its trust dialog":                             {Claude, []string{" Do you trust the files in this folder?", " > No, exit", "   Yes, I trust this folder"}, true},
		"a numbered prompt above the waiting composer": {Claude, append([]string{"❯ 1. Fix the bug", "  2. Then test it", ""}, composer...), false},
		"a reply's numbered list above the composer":   {Claude, append([]string{"● Next:", "  1. Read the file", "  2. Fix it"}, composer...), false},
		"a turn under the list":                        {Claude, append(append([]string{}, permission...), "✽ Pondering… (esc to interrupt)"), false},
		"a quoted trust dialog above the composer":     {Claude, quoted, false},
		"a mode a reply names above its prompt":        {Claude, named, true},
		"a lone focused line":                          {Claude, []string{" ❯ 1. Yes"}, false},
		"Codex's update prompt":                        {Codex, []string{"Update available!", "› 1. Update now", "  2. Skip"}, true},
		"a list Codex is not known to ask on":          {Codex, []string{"› 1. Yes", "  2. No"}, false},
		"nothing":                                      {Claude, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			screens, _ := NativeScreens(test.kind)
			if asks := screens.IsAsking(test.screen); asks != test.asks {
				t.Errorf("%q asks %v; want %v", test.screen, asks, test.asks)
			}
		})
	}
}

// A harness has a screen of its own open in its input line's place while no
// row of its composer's footer shows and no turn runs, as Claude Code draws a
// picker, a panel or a question, and nothing open once its input line is
// back, a turn running over it or not. A footer the conversation above a
// panel quotes leaves the panel open. The permission prompt and the footer
// are Claude Code 2.1.302's recorded rows, and the picker and the panel are
// made up in the shape of its model picker and its permissions panel.
func TestAHarnessHasItsOwnScreenOpenOnlyWhileItsInputLineIsGone(t *testing.T) {
	footer := "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	permission := []string{"● Write(nine.txt)", " Do you want to create nine.txt?", " ❯ 1. Yes", "   2. No"}
	picker := []string{"● Done. The branch is pushed.", "", " Select model", " Switch between Claude models.", "", " ❯ 1. Default (recommended)", "   2. Sonnet", "   3. Haiku", "", " Enter to confirm · Esc to exit"}
	panel := []string{"● Done. The branch is pushed.", "", " Permissions:  Allow   Ask   Deny   Workspace", "", "  ❯ Add a new rule…", "    Bash(go test:*)", "    Read(./**)", "", " Press ↑↓ to navigate · Enter to select · Esc to cancel"}
	quoted := append([]string{"● Its footer reads ⏵⏵ bypass permissions on (shift+tab to cycle) while it waits."}, panel[1:]...)
	for name, test := range map[string]struct {
		kind   Kind
		screen []string
		open   bool
	}{
		"its permission prompt":                   {Claude, permission, true},
		"its model picker":                        {Claude, picker, true},
		"its permissions panel":                   {Claude, panel, true},
		"a footer a reply quotes above its panel": {Claude, quoted, true},
		"its composer with an update line":        {Claude, claudeScreen([]string{"● Done. The branch is pushed."}, footer, "  ✓ Update installed · Restart to update"), false},
		"its composer":                            {Claude, claudeScreen([]string{"● Done. The branch is pushed."}, footer), false},
		"a turn over its composer":                {Claude, claudeScreen([]string{"✽ Reticulating… (3s · esc to interrupt)"}, footer), false},
		"a turn with no composer":                 {Claude, []string{"✽ Reticulating… (3s · esc to interrupt)"}, false},
		"a blank screen":                          {Claude, []string{"", "   ", ""}, false},
		"Codex at its composer":                   {Codex, []string{"• ok", "› Ask Codex to do anything", "  gpt-6-astra low · ~"}, false},
		"a harness it cannot read":                {Kind("other"), picker, false},
	} {
		t.Run(name, func(t *testing.T) {
			screens, _ := NativeScreens(test.kind)
			if open := screens.IsOpen(test.screen); open != test.open {
				t.Errorf("%q open %v; want %v", test.screen, open, test.open)
			}
		})
	}
}

// Claude Code's footer names the session's permission mode, which a CFO the
// Overlord opens chooses for itself, so its composer reads as ready, and its
// turn as working, in every mode. The rows are Claude Code 2.1.302's, read
// from its build; the manual one is a live capture's, where it holds no
// cycle hint.
func TestClaudeCodesComposerIsReadInEveryPermissionMode(t *testing.T) {
	screens, _ := NativeScreens(Claude)
	for mode, footer := range map[string]string{
		"manual":             "⏸ manual mode on · ? for shortcuts · ← 2 agents",
		"plan":               "⏸ plan mode on (shift+tab to cycle)",
		"accept edits":       "⏵⏵ accept edits on (shift+tab to cycle)",
		"bypass permissions": "⏵⏵ bypass permissions on (shift+tab to cycle)",
		"don't ask":          "⏵⏵ don't ask on (shift+tab to cycle)",
		"auto":               "⏵⏵ auto mode on (shift+tab to cycle)",
	} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			idle := claudeScreen([]string{"● Done. The branch is pushed."}, footer)
			working := claudeScreen([]string{"✽ Reticulating… (3s · esc to interrupt)"}, footer)

			// Act
			idleReady, workingReady := screens.IsReady(idle), screens.IsReady(working)

			// Assert
			if !idleReady || !screens.ComposerEmpty(idle) {
				t.Errorf("idle under %q: ready %v, empty %v; want an empty composer ready for input", footer, idleReady, screens.ComposerEmpty(idle))
			}
			if workingReady || !screens.IsWorking(working) {
				t.Errorf("in a turn under %q: ready %v, working %v; want working", footer, workingReady, screens.IsWorking(working))
			}
		})
	}
}

// Codex 0.154's composer, captured live on a native terminal: its footer names
// the model and folder, not the context left, so the empty composer's
// placeholder is what shows it ready, at start and after a turn; its working
// row's glyph alternates between • and ◦.
func TestCodexsLiveComposerIsReadyAndItsTurnIsWorking(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	footer := "  gpt-6-astra low · ~\\AppData\\Local\\Temp\\cfo-codex-proof\\projects\\proof\\.worktrees\\cxprobe"
	for name, test := range map[string]struct {
		screen         []string
		ready, working bool
	}{
		"at start":       {[]string{"  Tip: Use /mcp to list configured MCP tools.", "› Ask Codex to do anything", footer}, true, false},
		"after a turn":   {[]string{"› Reply with the single word ok and nothing else.", "• ok", "› Ask Codex to do anything", footer + " · Reply with ok"}, true, false},
		"working":        {[]string{"› Reply with the single word ok and nothing else.", "• Working (0s • esc to interrupt)", "› Ask Codex to do anything", footer}, false, true},
		"working, later": {[]string{"› Reply with the single word ok and nothing else.", "◦ Working (4s • esc to interrupt)", "› Ask Codex to do anything", footer + " · renaming... ⠏"}, false, true},
		"follow-up":      {[]string{"› Reply with the single word ok and nothing else.", "• ok", "› Ask a follow-up question", footer}, true, false},
		"text typed":     {[]string{"› Reply with the single word ok and nothing else.", footer}, false, false},
	} {
		if got := screens.IsReady(test.screen); got != test.ready {
			t.Errorf("%s: ready = %v, want %v", name, got, test.ready)
		}
		if got := screens.IsWorking(test.screen); got != test.working {
			t.Errorf("%s: working = %v, want %v", name, got, test.working)
		}
	}
}

// Codex's status row is its only sign of a turn in progress, and a resumed
// conversation found in one is taken as started, so text that quotes the row
// in a shell, a reply or the composer must not read as one.
func TestCodexWorkingIsReadOnlyFromItsStatusRow(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	for _, test := range []struct {
		row     string
		working bool
	}{
		{"Working (1m 51s • esc to interrupt) · 1 background terminal running · /ps to view · /stop to close", true},
		{"• Planning the fix (12s • esc to interrupt)", true},
		{`PS C:\proof> Write-Output "Working (1s • esc to interrupt)"`, false},
		{"The previous screen said esc to interrupt.", false},
		{"› Explain Working (1s • esc to interrupt)", false},
	} {
		t.Run(test.row, func(t *testing.T) {
			// Act
			working := screens.IsWorking([]string{test.row})

			// Assert
			if working != test.working {
				t.Fatalf("working = %v, want %v for %q", working, test.working, test.row)
			}
		})
	}
}

// Codex 0.160 on its alternate screen and pi 0.85.1 with --tui-mode
// fullscreen, as goblins and the CFO start them since 2026-10-17, read in a
// native terminal on that day: the rows a turn shows and the rows after it,
// with the scratch folder's path shortened. They read as their inline screens
// did, ready only while no turn runs.
func TestFullScreenCodexAndPiReadAsReadyOnlyWhileNoTurnRuns(t *testing.T) {
	rule := strings.Repeat("─", 40)
	codexFooter := "  ? for shortcuts                                    ⚠ 5 warnings · f2 to view"
	piFooter := "↑8.4k ↓357 R15k CH91.8% $0.001 0.8%/1.0M (auto)             (openrouter) deepseek/deepseek-v4.1-flash • high"
	for name, test := range map[string]struct {
		kind                  Kind
		screen                []string
		ready, working, empty bool
	}{
		"codex in a turn": {Codex, []string{"› Count from 1 to 300, one number per line, nothing else.", "", "", "Working (2s • esc to interrupt)", "", "", "› Ask Codex to do anything", "", `  GPT-6-Astra low · ~\work\task · ⠋`, codexFooter}, false, true, true},
		"codex after it":  {Codex, []string{"• done", "", "  Worked for 10s • 10:28 PM", "", "", "› Ask Codex to do anything", "", `  GPT-6-Astra low · ~\work\task · Count to 300`, codexFooter}, true, false, true},
		"pi in a turn":    {Pi, []string{" Reply from 127.0.0.1: bytes=32 time<1ms TTL=128", " Elapsed 2.0s", "", "", "── ⠸ Working " + rule, "", rule, `~\work\task`, piFooter}, false, true, false},
		"pi after it":     {Pi, []string{" Took 5.7s", "", " done", "", rule, "", rule, `~\work\task`, piFooter}, true, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			screens, _ := NativeScreens(test.kind)

			// Act
			ready, working, empty := screens.IsReady(test.screen), screens.IsWorking(test.screen), screens.ComposerEmpty(test.screen)

			// Assert
			if ready != test.ready || working != test.working || empty != test.empty {
				t.Fatalf("ready %v, working %v, empty %v; want %v, %v, %v", ready, working, empty, test.ready, test.working, test.empty)
			}
		})
	}
}

// After its first turn pi's footer leads with the session's token counts and
// cost, so its context meter is found anywhere in the row, as seen live on pi
// 0.85.1.
func TestPisComposerIsReadyAfterItsFirstTurn(t *testing.T) {
	screens, _ := NativeScreens(Pi)
	ready := []string{"────", "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)                                   (openrouter) z-ai/glm-5.3-flash • high"}

	if !screens.IsReady(ready) {
		t.Errorf("%q reads as not ready; want ready", ready)
	}
}

// After a compaction pi does not know its context use until the next turn and
// shows the meter as "?", as seen live on pi 0.85.1 on 2026-10-08: a pi CFO
// that read as busy there was never typed the wake its compaction raised.
func TestPisComposerIsReadyAfterACompaction(t *testing.T) {
	screens, _ := NativeScreens(Pi)
	rule := strings.Repeat("─", 40)
	ready := []string{" [compaction]", " Compacted from 5,245 tokens (ctrl+o to expand)", rule, "", rule, "↑7.0k ↓543 R9.2k CH88.2% $0.056 (sub) ?/272k (auto)                    gpt-5.5 • low"}

	if !screens.IsReady(ready) || !screens.ComposerEmpty(ready) {
		t.Errorf("%q reads as ready %v, empty %v; want both", ready, screens.IsReady(ready), screens.ComposerEmpty(ready))
	}
}

// On 2026-09-28 the monitor woke the CFO with "agent turn ended; waiting on
// input" for a native Claude Code goblin 29 minutes into one turn. Claude
// Code 2.1.283 draws its spinner from one of three glyph lists, one of them
// "·✢*✶✻✽", and a turn sampled on its "*" frame read as over, since the
// composer's footer shows throughout a turn. Every frame of every list reads
// as a turn in progress.
func TestAClaudeTurnReadsAsWorkingOnEverySpinnerFrame(t *testing.T) {
	screens, _ := NativeScreens(Claude)
	for _, glyph := range []string{"·", "✢", "✳", "*", "✶", "✻", "✽"} {
		screen := []string{glyph + " Churning… (29m 30s · ↓ 12.4k tokens · esc to interrupt)", "", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
		if !screens.IsWorking(screen) || screens.IsReady(screen) {
			t.Errorf("a turn on the %q frame reads as working %v, ready %v; want working", glyph, screens.IsWorking(screen), screens.IsReady(screen))
		}
	}
}

// Typed text shows in a composer by its end, however the composer wraps it,
// or as a paste's placeholder.
func TestTypedTextShowsByItsEndOrAsAPaste(t *testing.T) {
	screens, _ := NativeScreens(Claude)
	typed := "Read the brief at C:\\briefs\\t1.md and follow it exactly. Report outcomes to the CFO."
	for name, test := range map[string]struct {
		screen []string
		shows  bool
	}{
		"wrapped":     {[]string{"❯ Read the brief at C:\\briefs\\t1.md and follow it", "  exactly. Report outcomes to the CFO."}, true},
		"placeholder": {[]string{"❯ [Pasted text #1 +0 lines]"}, true},
		"not typed":   {[]string{"❯ Try \"fix typecheck errors\""}, false},
		"cut short":   {[]string{"❯ Read the brief at C:\\briefs\\t1.md and follow it exactly."}, false},
	} {
		if shows := screens.Shows(test.screen, typed); shows != test.shows {
			t.Errorf("%s: Shows = %v, want %v", name, shows, test.shows)
		}
	}
}

func TestOnlyACompleteRetainedComposerCanBeSubmittedAfterADialog(t *testing.T) {
	rule := strings.Repeat("─", 80)
	instruction := "Read the saved instruction and continue the unfinished work."
	for name, test := range map[string]struct {
		kind    Kind
		screen  []string
		matches bool
	}{
		"codex complete":                 {Codex, []string{"› " + instruction, "", "100% context left"}, true},
		"codex busy complete":            {Codex, []string{"\u2022 Working (5s \u2022 esc to interrupt)", "", "\u203a " + instruction, "", "100% context left"}, true},
		"codex busy model footer":        {Codex, []string{"\u2022 Working (5s \u2022 esc to interrupt)", "", "\u203a " + instruction, "", "  GPT-6-Astra low \u00b7 work"}, true},
		"codex blank before extra draft": {Codex, []string{"\u203a " + instruction, "", "  another instruction", "", "100% context left"}, false},
		"codex wrapped":                  {Codex, []string{"› Read the saved instruction and continue", "  the unfinished work.", "", "100% context left"}, true},
		"codex suffix only":              {Codex, []string{"› " + instruction[len(instruction)-40:], "", "100% context left"}, false},
		"codex old prompt":               {Codex, []string{"› " + instruction, "", "• Done", "› Ask Codex to do anything", "", "100% context left"}, false},
		"codex paste":                    {Codex, []string{"› [Pasted Content 58 chars]", "", "100% context left"}, false},
		"codex missing footer":           {Codex, []string{"› " + instruction, ""}, false},
		"codex Daybreak overlay":         {Codex, []string{"Set up security for Daybreak mode", "\u203a 1. Set up security", "Press a number to choose · esc to dismiss · type to continue", "› " + instruction, "", "100% context left"}, false},
		"codex extra draft":              {Codex, []string{"› " + instruction + " do something else", "", "100% context left"}, false},
		"claude complete":                {Claude, []string{rule, "❯ " + instruction, rule, "auto mode on"}, true},
		"claude old draft":               {Claude, []string{rule, "❯ " + instruction, rule, "a reply", rule, "❯ another draft", rule, "auto mode on"}, false},
		"pi complete":                    {Pi, []string{rule, instruction, rule, "0.0%/1.0M (auto)"}, true},
		"pi partial":                     {Pi, []string{rule, "continue the unfinished work.", rule, "0.0%/1.0M (auto)"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			screens, _ := NativeScreens(test.kind)
			if got := screens.ComposerMatches(test.screen, instruction); got != test.matches {
				t.Errorf("ComposerMatches = %v, want %v", got, test.matches)
			}
		})
	}
}

func TestConversationTextIsNotAStartupDialog(t *testing.T) {
	rule := strings.Repeat("─", 80)
	for name, test := range map[string]struct {
		kind   Kind
		screen []string
	}{
		"hook review in a turn":                {Codex, []string{"Hooks need review", "› 1. Review hooks", "enter confirm / esc skip", "• Working (0s • esc to interrupt)"}},
		"hook review above composer":           {Codex, []string{"Hooks need review", "› 1. Review hooks", "enter confirm / esc skip", "› Ask Codex to do anything", "100% context left"}},
		"old hook review above composer":       {Codex, []string{"Hooks need review", "› 1. Review hooks", "Press enter to confirm or esc to go back", "› Ask Codex to do anything", "100% context left"}},
		"Daybreak quoted in a turn":            {Codex, []string{"Set up security for Daybreak mode", "Press a number to choose · esc to dismiss · type to continue", "• Working (0s • esc to interrupt)"}},
		"Daybreak title and footer above idle": {Codex, []string{"Set up security for Daybreak mode", "Press a number to choose \u00b7 esc to dismiss \u00b7 type to continue", "", "\u203a Ask Codex to do anything", "100% context left"}},
		"Claude trust above composer":          {Claude, []string{"Do you trust the files in this folder?", "> No, exit", rule, ">", rule, "auto mode on"}},
		"Pi trust above composer":              {Pi, []string{"Trust project folder?", "→ Trust", rule, "", rule, "0.0%/1.0M (auto)"}},
	} {
		t.Run(name, func(t *testing.T) {
			screens, _ := NativeScreens(test.kind)
			if dialog, isShown := screens.Dialog(test.screen); isShown {
				t.Errorf("conversation text reads as %s", dialog.Name)
			}
		})
	}
}

func TestOnlyTheTrailingFooterBoundsTheCurrentComposer(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	instruction := "Read the saved instruction and continue the unfinished work."
	for _, footer := range []string{"100% context left", "GPT-6-Astra xhigh \u00b7 D:/x"} {
		t.Run(footer, func(t *testing.T) {
			for _, draft := range []string{instruction, "Ask Codex to do anything"} {
				rows := []string{"\u203a " + draft, "", footer, "extra retained instruction", "", "100% context left"}
				if screens.ComposerEmpty(rows) || screens.ComposerMatches(rows, instruction) {
					t.Errorf("draft %q with quoted footer %q reads as empty or exact", draft, footer)
				}
			}
		})
	}
	for _, draft := range []string{instruction, "Ask Codex to do anything"} {
		rows := []string{"\u2022 Working (5s \u2022 esc to interrupt)", "", "\u203a " + draft, "", "GPT-6-Astra xhigh \u00b7 D:/x", "? for shortcuts", ""}
		if screens.ComposerMatches(rows, instruction) != (draft == instruction) || screens.ComposerEmpty(rows) != (draft != instruction) {
			t.Errorf("genuine model and shortcuts footer obscured draft %q", draft)
		}
	}
}

// A stale wake needs evidence that the goblin is not working, and a pane that
// shows a tool or a turn running is evidence that it is, whichever harness
// drew it. The rows below are live captures: Claude Code 2.1 mid-tool and with
// a background shell its turn left running (2026-09-30), Codex 0.154's status
// row and pi 0.85.1's rule. A background shell counts only while the footer
// still counts it: the line that ended the turn keeps saying "1 shell still
// running" after the shell is gone.
func TestRunningWorkIsReadFromAnyHarnessPane(t *testing.T) {
	for name, test := range map[string]struct {
		screen  []string
		running string
	}{
		"claude tool running":        {[]string{"  Bash(npm test)", "  ⎿  Running… (22s · timeout 10m)", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, "⎿  Running… (22s · timeout 10m)"},
		"claude spinner":             {[]string{"✽ Skedaddling… (42m 36s · ↓ 9.8k tokens)", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, "✽ Skedaddling… (42m 36s · ↓ 9.8k tokens)"},
		"claude interrupt hint":      {[]string{"❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, "⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"},
		"claude background shell":    {[]string{"✻ Cooked for 23s · done 12:01 PM · 1 shell still running", "❯", "  ⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage"}, "⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage"},
		"claude two shells at end":   {[]string{"❯", "  ⏵⏵ bypass permissions on · 2 shells"}, "⏵⏵ bypass permissions on · 2 shells"},
		"claude monitor":             {[]string{"✻ Worked for 5m 15s · done 9:08 AM · 1 monitor still running", ">", "⏵⏵ bypass permissions on · PR #149 · 1 monitor · ← 2 agents · ↓ to manage"}, "⏵⏵ bypass permissions on · PR #149 · 1 monitor · ← 2 agents · ↓ to manage"},
		"claude shells and monitor":  {[]string{"✻ Cooked for 45s · done 10:04 AM · 4 shells, 1 monitor", "still running", ">", "⏵⏵ bypass permissions on · 4 shells, 1 monitor · …"}, "⏵⏵ bypass permissions on · 4 shells, 1 monitor · …"},
		"claude two monitors at end": {[]string{">", "⏵⏵ bypass permissions on · 2 monitors"}, "⏵⏵ bypass permissions on · 2 monitors"},
		"codex status row":           {[]string{"• Working (5s • esc to interrupt)", "› Ask Codex to do anything"}, "• Working (5s • esc to interrupt)"},
		"codex status row, wrapped":  {[]string{"◦ Working (12s • esc to", "› Ask Codex to do anything"}, "◦ Working (12s • esc to"},
		"pi rule":                    {[]string{"── ⠸ Working ──", "0.0%/1.0M (auto)"}, "── ⠸ Working ──"},
		"claude waits on an agent":   {[]string{"✻ Waiting for 1 background agent to finish", ">", "⏵⏵ bypass permissions on (shift+tab to cycle) · ← 2 agents · ↓ to manage"}, "✻ Waiting for 1 background agent to finish"},
	} {
		running, ok := RunningWork(test.screen)
		if !ok || running != test.running {
			t.Errorf("%s: RunningWork = %q, %v; want %q", name, running, ok, test.running)
		}
	}
	for name, screen := range map[string][]string{
		"claude idle, shell gone":  {"✻ Cooked for 23s · done 12:01 PM · 1 shell still running", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"claude idle":              {"● Done. The branch is pushed.", "❯ Try \"fix typecheck errors\"", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"codex idle":               {"• Working tree is clean and all tests pass.", "› Ask Codex to do anything", "  100% context left"},
		"pi idle":                  {"────", "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)"},
		"a reply naming a shell":   {"● Run it in 1 shell and report back.", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"claude idle, no monitor":  {"✻ Brewed for 2s · done 12:56 AM · 1 monitor still running", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"claude idle, end wrapped": {"✻ Cooked for 45s · done 10:04 AM · 4 shells, 1 monitor", "still running", ">", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"a reply about waiting":    {"● Waiting for 1 background agent is the plan.", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
	} {
		if running, ok := RunningWork(screen); ok {
			t.Errorf("%s: RunningWork = %q, want nothing running", name, running)
		}
	}
}

// HasBackgroundWork answers off the same rows RunningWork reads as background
// work, indented as a harness draws them, and never off the line that ended
// the turn or a reply that only says so.
func TestHasBackgroundWorkReadsEveryRowRunningWorkDoes(t *testing.T) {
	for name, screen := range map[string][]string{
		"agents awaited":      {"  ✻ Waiting for 1 background agent to finish", ">", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"agents and a shell":  {"· Waiting for 2 background tasks to finish", ">", "  ⏵⏵ bypass permissions on · 1 shell"},
		"a footer shell":      {">", "  ⏵⏵ bypass permissions on · 1 shell"},
		"shells and monitors": {">", "  ⏵⏵ bypass permissions on · 4 shells, 1 monitor"},
	} {
		if !HasBackgroundWork(screen) {
			t.Errorf("%s: HasBackgroundWork(%q) = false, want true", name, screen)
		}
	}
	for name, screen := range map[string][]string{
		"a turn ended, its shell gone": {"✻ Cooked for 23s · done 12:01 PM · 1 shell still running", ">", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"a reply about waiting":        {"● Waiting for 1 background agent is the plan.", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"claude idle":                  {"● Done. The branch is pushed.", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
	} {
		if HasBackgroundWork(screen) {
			t.Errorf("%s: HasBackgroundWork(%q) = true, want false", name, screen)
		}
	}
}

// A screen whose only movement is the harness's own clock, spinner or hint
// shows no new output, and a row written to it does: on 2026-10-08 the CFO
// was woken about 30 times for goblins whose screens were filling with test
// output, and a wedged goblin's spinner ticks on all the same.
func TestOutputDigestMovesOnlyWithOutput(t *testing.T) {
	for name, test := range map[string]struct {
		before, after []string
		isMoved       bool
	}{
		"claude tool clock":   {[]string{"  Bash(npm test)", "  ⎿  Running… (22s · timeout 10m)", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, []string{"  Bash(npm test)", "  ⎿  Running… (23s · timeout 10m)", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, false},
		"claude spinner":      {[]string{"● Bash(go test ./...)", "✽ Skedaddling… (42m 36s · ↓ 9.8k tokens)", "❯"}, []string{"● Bash(go test ./...)", "✶ Skedaddling… (42m 37s · ↓ 9.8k tokens)", "❯"}, false},
		"codex status row":    {[]string{"• Ran go test ./...", "• Working (5s • esc to interrupt)", "› Ask Codex to do anything"}, []string{"• Ran go test ./...", "◦ Working (6s • esc to interrupt)", "› Ask Codex to do anything"}, false},
		"pi rule":             {[]string{"read file.go", "── ⠸ Working ──", "0.0%/1.0M (auto)"}, []string{"read file.go", "── ⠼ Working ──", "0.0%/1.0M (auto)"}, false},
		"claude tool output":  {[]string{"  Bash(npm test)", "  ⎿  ok 431 - board renders", "  ⎿  Running… (22s · timeout 10m)", "❯"}, []string{"  Bash(npm test)", "  ⎿  ok 432 - board renders the train", "  ⎿  Running… (23s · timeout 10m)", "❯"}, true},
		"codex command lines": {[]string{"• Running go test ./...", "  └ ok  internal/monitor 3.1s", "• Working (5s • esc to interrupt)"}, []string{"• Running go test ./...", "  └ ok  internal/supervisor 9.4s", "• Working (6s • esc to interrupt)"}, true},
		"a reply at the end":  {[]string{"● Tests pass.", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, []string{"● Tests pass. Opening the pull request.", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, true},
	} {
		if isMoved := OutputDigest(test.before) != OutputDigest(test.after); isMoved != test.isMoved {
			t.Errorf("%s: output moved = %v, want %v", name, isMoved, test.isMoved)
		}
	}
}

// Text is typed into a CFO's composer only while it holds nothing, so a line
// somebody left unsent is never typed over: Codex shows its placeholder only
// while its composer is empty, and pi's editor is the rows between its last
// two rules. A harness whose empty composer is not known never reads empty.
func TestAComposerReadsEmptyOnlyWhileItHoldsNothing(t *testing.T) {
	rule := strings.Repeat("─", 40)
	footer := "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)"
	for name, test := range map[string]struct {
		kind   Kind
		screen []string
		empty  bool
	}{
		"codex placeholder":                         {Codex, []string{"• ok", "› Ask Codex to do anything", "  gpt-6-astra low · work"}, true},
		"codex follow-up":                           {Codex, []string{"• ok", "› Ask a follow-up question", "  gpt-6-astra low · work"}, true},
		"codex old placeholder above partial draft": {Codex, []string{"\u203a Ask Codex to do anything", "\u2022 Earlier quoted composer", "", "\u203a partial draft", "", "100% context left"}, false},
		"codex old placeholder above bare prompt":   {Codex, []string{"\u203a Ask Codex to do anything", "\u2022 Earlier quoted composer", "", "\u203a", "", "100% context left"}, false},
		"codex busy empty":                          {Codex, []string{"\u2022 Working (5s \u2022 esc to interrupt)", "", "\u203a Ask Codex to do anything", "", "100% context left"}, true},
		"codex placeholder with continued draft":    {Codex, []string{"\u203a Ask Codex to do anything", "  continued draft", "", "100% context left"}, false},
		"codex placeholder without footer":          {Codex, []string{"\u203a Ask Codex to do anything"}, false},
		"codex text typed":                          {Codex, []string{"• ok", "› Reply with ok", "  gpt-6-astra low · work"}, false},
		"pi empty editor":                           {Pi, []string{"Done.", rule, "  ", rule, footer}, true},
		"pi text typed":                             {Pi, []string{"Done.", rule, " run the tests ", rule, footer}, false},
		"pi two lines typed":                        {Pi, []string{rule, "first", "second", rule, footer}, false},
		"pi editor scrolled":                        {Pi, []string{"─── ↑ 3 more ───────────────", "last line", rule, footer}, false},
		"pi rule missing":                           {Pi, []string{"Done.", footer}, false},
		"claude is never typed in":                  {Claude, []string{"❯ ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, false},
		"unknown composer":                          {Kind("unknown"), []string{"❯ "}, false},
	} {
		screens, _ := NativeScreens(test.kind)
		if got := screens.ComposerEmpty(test.screen); got != test.empty {
			t.Errorf("%s: ComposerEmpty = %v, want %v", name, got, test.empty)
		}
	}
}

// A composer that cannot be read, or one a startup dialog covers, proves no
// draft: only a readable composer showing text is refused, so a recoverable
// dialog or an unreadable footer is left to the delivery that answers it.
func TestAComposerHoldsTextOnlyWhenItProvesADraft(t *testing.T) {
	rule := strings.Repeat("─", 120)
	footer := "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	for name, test := range map[string]struct {
		kind   Kind
		screen []string
		holds  bool
	}{
		"codex empty placeholder":             {Codex, []string{"• ok", "› Ask Codex to do anything", "  gpt-6-astra low · work"}, false},
		"codex text typed":                    {Codex, []string{"• ok", "› An unfinished user draft", "  gpt-6-astra low · work"}, true},
		"codex composer without its footer":   {Codex, []string{"› Ask Codex to do anything"}, false},
		"codex hook review over the composer": {Codex, []string{"Hooks need review", "2 hooks are new or changed", "› 1. Review hooks", "  2. Trust all and continue", "  3. Continue without trusting (hooks won't run)", "enter confirm / esc skip"}, false},
		"claude empty composer":               {Claude, []string{rule, "❯", rule, footer}, false},
		"claude draft":                        {Claude, []string{rule, "❯ scratch draft for composer proof", rule, footer}, true},
		"claude composer without its rules":   {Claude, []string{"❯ scratch draft", footer}, false},
		"pi empty editor":                     {Pi, []string{rule, "  ", rule, footer}, false},
		"pi text typed":                       {Pi, []string{rule, " run the tests ", rule, footer}, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			screens, _ := NativeScreens(test.kind)

			// Act
			holds := screens.ComposerHoldsText(test.screen)

			// Assert
			if holds != test.holds {
				t.Fatalf("ComposerHoldsText = %v, want %v", holds, test.holds)
			}
		})
	}
}

// Claude Code 2.1.288, captured in an isolated native terminal on 2026-10-03:
// the last two rules enclose the current composer, including collapsed paste.
func TestClaudesCapturedComposerReadsEmptyWithoutMistakingADraft(t *testing.T) {
	rule := strings.Repeat("─", 140)
	footer := "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	screens, _ := NativeScreens(Claude)
	for name, test := range map[string]struct {
		screen []string
		empty  bool
	}{
		"empty":            {[]string{rule, "❯", rule, footer}, true},
		"cleared":          {[]string{rule, "❯\u00a0", rule, footer}, true},
		"ascii prompt":     {[]string{rule, ">\u00a0", rule, footer}, true},
		"draft":            {[]string{rule, "❯\u00a0scratch draft for composer proof", rule, footer}, false},
		"collapsed paste":  {[]string{rule, "❯\u00a0[Pasted text #1]", rule, footer}, false},
		"wrapped draft":    {[]string{rule, "❯", "  continued draft", rule, footer}, false},
		"old empty prompt": {[]string{rule, "❯", rule, "old reply", rule, "❯ draft", rule, footer}, false},
		"missing rule":     {[]string{"❯", rule, footer}, false},
		"unknown hint":     {[]string{rule, "❯ Try fix typecheck errors", rule, footer}, false},
		"no screen":        {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := screens.ComposerEmpty(test.screen); got != test.empty {
				t.Fatalf("ComposerEmpty = %v, want %v", got, test.empty)
			}
		})
	}
}

// Claude Code that takes the Enter ending typed text as a new line leaves the
// text in its composer over the empty row that Enter added, as a resumed
// goblin's was on a loaded machine on 2026-10-20. Text that shows over no new
// row may still wait on an Enter not taken yet, so it never reads as one taken
// as a new line, and nor does an empty row the composer showed as the Enter
// was pressed (#1232).
func TestAnEnterReadsTakenAsANewLineOnlyByTheEmptyRowItAdded(t *testing.T) {
	rule := strings.Repeat("─", 120)
	footer := "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	typed := []string{rule, "> run the tests", rule, footer}
	for name, test := range map[string]struct {
		kind            Kind
		atPress, screen []string
		isNewLine       bool
	}{
		"a new empty row under the text":     {Claude, typed, []string{rule, "> run the tests", "  ", rule, footer}, true},
		"a second new empty row":             {Claude, []string{rule, "> run the tests", "  ", rule, footer}, []string{rule, "> run the tests", "  ", " ", rule, footer}, true},
		"the Enter not taken yet":            {Claude, typed, typed, false},
		"an empty row there at the press":    {Claude, []string{rule, "> run the tests", "  ", rule, footer}, []string{rule, "> run the tests", "  ", rule, footer}, false},
		"the composer cleared by the submit": {Claude, typed, []string{"> run the tests", "", rule, ">", rule, footer}, false},
		"only empty rows":                    {Pi, []string{rule, "run the tests", rule, footer}, []string{rule, "", "", rule, footer}, false},
		"no composer read at the press":      {Claude, nil, []string{rule, "> run the tests", "  ", rule, footer}, false},
		"no composer read now":               {Claude, typed, []string{"> run the tests", "  ", footer}, false},
		"a composer that is not ruled":       {Codex, []string{"› run the tests"}, []string{"› run the tests", ""}, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			screens, _ := NativeScreens(test.kind)

			// Act
			isNewLine := screens.TookEnterAsNewLine(test.atPress, test.screen)

			// Assert
			if isNewLine != test.isNewLine {
				t.Fatalf("TookEnterAsNewLine = %v, want %v", isNewLine, test.isNewLine)
			}
		})
	}
}

func TestCodexModelOnlyFooterBoundsTheCurrentComposer(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	instruction := "Continue the unfinished work."
	for _, draft := range []string{"Ask Codex to do anything", instruction, instruction + " extra retained text"} {
		rows := []string{"\u203a " + draft, "", "  gpt-6.1-sol high", ""}
		if screens.ComposerEmpty(rows) != (draft == "Ask Codex to do anything") || screens.ComposerMatches(rows, instruction) != (draft == instruction) {
			t.Errorf("model-only footer obscured the current draft %q", draft)
		}
	}
	for _, draft := range []string{"Ask Codex to do anything", instruction} {
		rows := []string{"\u203a " + draft, "", "  gpt-6.1-sol high", "extra retained text", "", "100% context left"}
		if screens.ComposerEmpty(rows) || screens.ComposerMatches(rows, instruction) {
			t.Errorf("model-only footer quoted in draft %q hides additional text", draft)
		}
	}
}

func TestCodexTrustMenuBlocksARetainedDraft(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	for _, prompt := range []struct {
		title, option, other, footer string
	}{
		{"Do you trust the contents of this directory?", "\u203a 1. Yes, continue", "  2. No, quit", "Press enter to continue"},
		{"Trust this folder? Codex can read, edit, and run files here.", "\u203a 1. Trust and continue", "  2. Quit", "enter continue · esc quit"},
	} {
		for _, draft := range []string{"Ask Codex to do anything", "Overlord: Reply received"} {
			rows := []string{prompt.title, prompt.option, prompt.other, "", prompt.footer}
			if _, isShown := screens.Dialog(rows); !isShown {
				t.Errorf("trust menu hiding %q is not recognized", draft)
			}
			if screens.IsReady(rows) || screens.ComposerEmpty(rows) || screens.ComposerMatches(rows, draft) {
				t.Errorf("trust menu hiding %q permits composer input", draft)
			}
		}
	}
}

func TestCodexTrustHistoryDoesNotBecomeTheCurrentDialog(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	for _, menu := range []struct {
		name, title, accept, other, footer string
	}{
		{"directory", "Do you trust the contents of this directory?", "1. Yes, continue", "2. No, quit", "Press enter to continue"},
		{"folder", "Trust this folder?", "1. Trust and continue", "2. Quit", "enter continue · esc quit"},
	} {
		for _, footer := range []string{"100% context left", "gpt-6.1-sol high"} {
			for _, draft := range []string{menu.accept, menu.accept + " with more retained text", "1. Inspect the failing test"} {
				t.Run(menu.name+"/"+footer+"/"+draft, func(t *testing.T) {
					rows := []string{menu.title, "", "› " + draft, footer}
					if dialog, isShown := screens.Dialog(rows); isShown {
						t.Errorf("current draft is misread as %s", dialog.Name)
					}
					if screens.ComposerEmpty(rows) || !screens.ComposerMatches(rows, draft) {
						t.Error("quoted title changed the complete retained draft")
					}
				})
			}
			for _, history := range [][]string{
				{menu.title, "› " + menu.accept},
				{menu.title, "› 1. Inspect the failing test"},
				{menu.title, "› " + menu.accept, "  " + menu.other, "", menu.footer},
			} {
				rows := append(append([]string{}, history...), "", "› Ask Codex to do anything", footer)
				if dialog, isShown := screens.Dialog(rows); isShown {
					t.Errorf("%s: history above an idle composer is misread as %s: %q", menu.name, dialog.Name, rows)
				}
				if !screens.IsReady(rows) || !screens.ComposerEmpty(rows) {
					t.Errorf("%s: quoted menu blocks the idle composer: %q", menu.name, rows)
				}
			}
		}
	}
}

func TestCodexHookHistoryDoesNotBecomeTheCurrentDialog(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	for _, footer := range []string{"", "Press enter to confirm or esc to go back", "enter confirm / esc skip", "enter confirm · esc skip"} {
		for _, draft := range []string{"3. Continue without trusting", "3. Continue without trusting with more retained text", "1. Inspect the failing test"} {
			t.Run(footer+"/"+draft, func(t *testing.T) {
				rows := []string{"Hooks need review"}
				if footer != "" {
					rows = append(rows, "1 hook is new or changed", "1. Review hooks", "2. Trust all and continue", "3. Continue without trusting (hooks won't run)", "", footer)
				}
				rows = append(rows, "", "› "+draft, "gpt-6.1-sol high")
				if dialog, isShown := screens.Dialog(rows); isShown {
					t.Errorf("current draft is misread as %s", dialog.Name)
				}
				if screens.ComposerEmpty(rows) || !screens.ComposerMatches(rows, draft) {
					t.Error("hook history changed the complete retained draft")
				}
				if footer != "" {
					rows[2] = "› 1. Review hooks"
					if dialog, isShown := screens.Dialog(rows); isShown {
						t.Errorf("quoted menu focus blocks the current draft as %s", dialog.Name)
					}
				}
			})
		}
	}
}

func TestCodexHookMenusKeepTheirSafeAnswerAndFocus(t *testing.T) {
	screens, _ := NativeScreens(Codex)
	for _, footer := range []string{"Press enter to confirm or esc to go back", "enter confirm / esc skip", "enter confirm · esc skip"} {
		for _, summary := range []string{"1 hook is new or changed", "2 hooks are new or changed"} {
			for focus := range 3 {
				t.Run(footer+"/"+summary+"/"+string(rune('1'+focus)), func(t *testing.T) {
					options := []string{"1. Review hooks", "2. Trust all and continue", "3. Continue without trusting (hooks won't run)"}
					rows := []string{"› Earlier quoted input", "enter confirm / esc skip", "", "Hooks need review", summary, ""}
					for index, option := range options {
						prefix := "  "
						if index == focus {
							prefix = "› "
						}
						rows = append(rows, prefix+option)
					}
					rows = append(rows, "", footer, "")
					dialog, isShown := screens.Dialog(rows)
					if !isShown {
						t.Fatal("genuine hook menu is not recognized")
					}
					if focused, ok := dialog.Focused(rows); !ok || focused != options[focus] {
						t.Errorf("focused = %q, %v; want the active menu option %q", focused, ok, options[focus])
					}
					if dialog.EscapeHint == "" {
						for index, option := range options {
							if dialog.Chosen(option) != (index == 2) {
								t.Errorf("chosen %q = %v; want only continue without trusting", option, dialog.Chosen(option))
							}
						}
					} else if dialog.EscapeHint != footer {
						t.Errorf("escape hint = %q, want %q", dialog.EscapeHint, footer)
					}
					if dialog.Summary == nil || dialog.Summary.FindString(strings.Join(rows, "\n")) != summary {
						t.Errorf("untrusted hook summary does not match %q", summary)
					}
					if screens.IsReady(rows) || screens.ComposerEmpty(rows) || screens.ComposerMatches(rows, options[focus]) {
						t.Error("genuine hook menu permits composer input")
					}
				})
			}
		}
	}
}

// pi collapses a long burst typed into its editor into a paste marker and
// holds the text itself, as Claude Code and Codex do, so a steer or a wake
// line long enough to collapse still reads as shown in the composer. The
// marker was read off pi's own editor: "[paste #1 +12 lines]" or
// "[paste #1 1200 chars]".
func TestAPiComposerShowsACollapsedPaste(t *testing.T) {
	rule := strings.Repeat("─", 80)
	footer := "↑8.4k ↓357 R15k CH91.8% $0.001 0.8%/1.0M (auto)"
	screens, _ := NativeScreens(Pi)
	typed := "CFO: fix the monitor's turn reading. " + strings.Repeat("and check the family tree too. ", 40)
	for name, test := range map[string]struct {
		screen []string
		shows  bool
	}{
		"lines pasted":  {[]string{rule, "[paste #1 +12 lines]", rule, footer}, true},
		"chars pasted":  {[]string{rule, "[paste #1 1200 chars]", rule, footer}, true},
		"nothing typed": {[]string{rule, "", rule, footer}, false},
		"another draft": {[]string{rule, "[paste #2 +3 lines]", rule, footer}, true},
	} {
		t.Run(name, func(t *testing.T) {
			if shows := screens.Shows(test.screen, typed); shows != test.shows {
				t.Fatalf("Shows = %v, want %v", shows, test.shows)
			}
		})
	}
}
