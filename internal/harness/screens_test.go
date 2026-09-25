package harness

import "testing"

// Claude's trust dialog focuses "No, exit" first. Only the focus on "Yes, I
// trust this folder" is its answer, and the dialog is recognized however its
// sentence wraps.
func TestClaudesTrustDialogIsAnsweredOnlyOnYes(t *testing.T) {
	screens, _ := NativeScreens(Claude)
	for name, test := range map[string]struct {
		screen []string
		answer bool
	}{
		"focus on No, exit": {[]string{" Quick safety check: Is this a project you created or one you", "trust? (Like your own code, a", " ❯ No, exit", "   Yes, I trust this folder"}, false},
		"focus on Yes":      {[]string{" Quick safety check: Is this a project you created or one you trust?", "   No, exit", " ❯ Yes, I trust this folder"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			dialog, found := screens.Dialog(test.screen)
			if !found {
				t.Fatalf("Dialog(%q) found none, want the trust dialog", test.screen)
			}
			focused, ok := dialog.Focused(test.screen)
			if answer := ok && dialog.Chosen(focused); answer != test.answer {
				t.Errorf("focused %q (%v): answer = %v, want %v", focused, ok, answer, test.answer)
			}
		})
	}
}

// Codex's update prompt is answered with Skip, never Update now or Skip until
// next version, and its hook review prompt is never answered.
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
	hooks := []string{"  Hooks need review", "› 1. Review hooks", "  2. Trust all and continue", "  3. Continue without trusting (hooks won't run)"}
	dialog, found := screens.Dialog(hooks)
	for _, option := range []string{"1. Review hooks", "2. Trust all and continue", "3. Continue without trusting (hooks won't run)"} {
		if !found || dialog.Chosen(option) {
			t.Errorf("hook review prompt (found %v) chooses %q, want it never answered", found, option)
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
