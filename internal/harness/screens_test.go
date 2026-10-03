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
	hooks := []string{"  Hooks need review", "  2 hooks are new or changed.", "› 1. Review hooks", "  2. Trust all and continue", "  3. Continue without trusting (hooks won't run)"}
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
		"claude tool running":       {[]string{"  Bash(npm test)", "  ⎿  Running… (22s · timeout 10m)", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, "⎿  Running… (22s · timeout 10m)"},
		"claude spinner":            {[]string{"✽ Skedaddling… (42m 36s · ↓ 9.8k tokens)", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, "✽ Skedaddling… (42m 36s · ↓ 9.8k tokens)"},
		"claude interrupt hint":     {[]string{"❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"}, "⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"},
		"claude background shell":   {[]string{"✻ Cooked for 23s · done 12:01 PM · 1 shell still running", "❯", "  ⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage"}, "⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage"},
		"claude two shells at end":  {[]string{"❯", "  ⏵⏵ bypass permissions on · 2 shells"}, "⏵⏵ bypass permissions on · 2 shells"},
		"codex status row":          {[]string{"• Working (5s • esc to interrupt)", "› Ask Codex to do anything"}, "• Working (5s • esc to interrupt)"},
		"codex status row, wrapped": {[]string{"◦ Working (12s • esc to", "› Ask Codex to do anything"}, "◦ Working (12s • esc to"},
		"pi rule":                   {[]string{"── ⠸ Working ──", "0.0%/1.0M (auto)"}, "── ⠸ Working ──"},
	} {
		running, ok := RunningWork(test.screen)
		if !ok || running != test.running {
			t.Errorf("%s: RunningWork = %q, %v; want %q", name, running, ok, test.running)
		}
	}
	for name, screen := range map[string][]string{
		"claude idle, shell gone": {"✻ Cooked for 23s · done 12:01 PM · 1 shell still running", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"claude idle":             {"● Done. The branch is pushed.", "❯ Try \"fix typecheck errors\"", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"codex idle":              {"• Working tree is clean and all tests pass.", "› Ask Codex to do anything", "  100% context left"},
		"pi idle":                 {"────", "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)"},
		"a reply naming a shell":  {"● Run it in 1 shell and report back.", "❯", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
	} {
		if running, ok := RunningWork(screen); ok {
			t.Errorf("%s: RunningWork = %q, want nothing running", name, running)
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
		"codex placeholder":        {Codex, []string{"• ok", "› Ask Codex to do anything", "  gpt-6-astra low · work"}, true},
		"codex follow-up":          {Codex, []string{"• ok", "› Ask a follow-up question", "  gpt-6-astra low · work"}, true},
		"codex text typed":         {Codex, []string{"• ok", "› Reply with ok", "  gpt-6-astra low · work"}, false},
		"pi empty editor":          {Pi, []string{"Done.", rule, "  ", rule, footer}, true},
		"pi text typed":            {Pi, []string{"Done.", rule, " run the tests ", rule, footer}, false},
		"pi two lines typed":       {Pi, []string{rule, "first", "second", rule, footer}, false},
		"pi editor scrolled":       {Pi, []string{"─── ↑ 3 more ───────────────", "last line", rule, footer}, false},
		"pi rule missing":          {Pi, []string{"Done.", footer}, false},
		"claude is never typed in": {Claude, []string{"❯ ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, false},
		"unknown composer":         {Kind("unknown"), []string{"❯ "}, false},
	} {
		screens, _ := NativeScreens(test.kind)
		if got := screens.ComposerEmpty(test.screen); got != test.empty {
			t.Errorf("%s: ComposerEmpty = %v, want %v", name, got, test.empty)
		}
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
