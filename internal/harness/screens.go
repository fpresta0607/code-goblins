package harness

import (
	"regexp"
	"strings"
)

// Screens is what a spawn into a native terminal recognizes on a harness's
// own screen, as its console holds it: the startup dialogs it may answer, the
// composer that shows the harness waiting for input, and a turn in progress.
// The texts are each harness's own, read from ConPTY captures on Windows unless
// a comment says otherwise. A screen that matches none of them is never typed
// into.
type Screens struct {
	Dialogs []Dialog
	// Ready matches a row of the composer waiting for input.
	Ready *regexp.Regexp
	// Working matches a row while a turn runs.
	Working *regexp.Regexp
	// Pasted is how the composer shows typed text it collapsed as a paste.
	Pasted []string
}

// Dialog is one startup prompt. A spawn answers it only while one of Markers
// shows, by moving the focus, the option whose row starts with Focus, down to
// the option that starts with Accept, then confirming that option with Enter.
// A dialog without Accept is never answered: it stops the spawn.
type Dialog struct {
	Name    string
	Markers []string
	Focus   string
	Accept  string
}

// NativeScreens returns what kind shows on its own screen, and false for a
// harness a native terminal cannot start yet.
func NativeScreens(kind Kind) (Screens, bool) {
	switch kind {
	case Claude:
		return Screens{
			Dialogs: []Dialog{{
				Name:    "the workspace trust dialog",
				Markers: []string{"Is this a project you created or one you trust?", "Do you trust the files in this folder?"},
				// It focuses "No, exit" first, so a bare Enter quits Claude.
				Focus:  "❯",
				Accept: "Yes, I trust this folder",
			}},
			// A goblin runs with permission checks bypassed, which the
			// composer's footer says while it waits.
			Ready: regexp.MustCompile(`bypass permissions on`),
			// A spinner glyph, then a verb that ends in an ellipsis, as in
			// "✽ Reticulating…".
			Working: regexp.MustCompile(`^[·✢✳✶✻✽] \S.*…`),
			Pasted:  []string{"[Pasted text #"},
		}, true
	case Codex:
		return Screens{
			Dialogs: []Dialog{
				{Name: "the update prompt", Markers: []string{"Update available!"}, Focus: "›", Accept: "2. Skip"},
				{Name: "the directory trust prompt", Markers: []string{"Do you trust the contents of this directory?"}, Focus: "›", Accept: "1. Yes, continue"},
				// Trusting hooks is the Overlord's decision, never a spawn's.
				{Name: "the hook review prompt", Markers: []string{"Hooks need review"}, Focus: "›"},
			},
			// Codex's composer, working and paste texts are its known ones,
			// not yet seen in a capture on this machine (CFO decision 2353):
			// the first live native codex spawn checks them.
			Ready:   regexp.MustCompile(`context left`),
			Working: regexp.MustCompile(`esc to interrupt|\bWorking\b`),
			Pasted:  []string{"[Pasted Content"},
		}, true
	case Pi:
		return Screens{
			// Its focused option has not been seen, so it is never answered.
			Dialogs: []Dialog{{Name: "the project trust prompt", Markers: []string{"Trust project folder?"}}},
			// The context meter that starts the footer's last row, as in
			// "0.0%/1.0M (auto)".
			Ready: regexp.MustCompile(`^\d+(\.\d+)?%/`),
			// A braille spinner in the rule above the editor, as in
			// "── ⠸ Working ──".
			Working: regexp.MustCompile(`[\x{2800}-\x{28FF}]\s+Working`),
		}, true
	}
	return Screens{}, false
}

// Dialog returns the dialog screen shows, if any.
func (s Screens) Dialog(screen []string) (Dialog, bool) {
	for _, dialog := range s.Dialogs {
		if dialog.Shows(screen) {
			return dialog, true
		}
	}
	return Dialog{}, false
}

// Shows reports whether screen shows the dialog.
func (d Dialog) Shows(screen []string) bool {
	compact := compactScreen(screen)
	for _, marker := range d.Markers {
		if strings.Contains(compact, compactScreen([]string{marker})) {
			return true
		}
	}
	return false
}

// IsReady reports whether screen shows the composer waiting for input and no
// turn in progress.
func (s Screens) IsReady(screen []string) bool {
	return anyRow(screen, s.Ready) && !s.IsWorking(screen)
}

// IsWorking reports whether screen shows a turn in progress.
func (s Screens) IsWorking(screen []string) bool {
	return anyRow(screen, s.Working)
}

// Shows reports whether screen shows text typed into the composer: its end,
// where the composer's cursor keeps it in view, or the placeholder of a paste.
func (s Screens) Shows(screen []string, typed string) bool {
	compact := compactScreen(screen)
	end := []rune(compactScreen([]string{typed}))
	if len(end) > 40 {
		end = end[len(end)-40:]
	}
	if len(end) > 0 && strings.Contains(compact, string(end)) {
		return true
	}
	for _, placeholder := range s.Pasted {
		if strings.Contains(compact, compactScreen([]string{placeholder})) {
			return true
		}
	}
	return false
}

// Chosen reports whether focused, the focused option's text, is the option
// to confirm.
func (d Dialog) Chosen(focused string) bool {
	return d.Accept != "" && strings.HasPrefix(focused, d.Accept)
}

// Focused returns the text of the focused option: the one row that starts
// with the dialog's focus glyph, after it. More or fewer than one such row
// means the focus cannot be read.
func (d Dialog) Focused(screen []string) (string, bool) {
	var focused []string
	for _, row := range screen {
		if option, found := strings.CutPrefix(strings.TrimSpace(row), d.Focus); found {
			focused = append(focused, strings.TrimSpace(option))
		}
	}
	if d.Focus == "" || len(focused) != 1 {
		return "", false
	}
	return focused[0], true
}

// compactScreen joins screen without any whitespace, since a harness wraps a
// long sentence across rows at the terminal's width.
func compactScreen(screen []string) string {
	return strings.Join(strings.Fields(strings.Join(screen, " ")), "")
}

func anyRow(screen []string, pattern *regexp.Regexp) bool {
	for _, row := range screen {
		if pattern.MatchString(row) {
			return true
		}
	}
	return false
}
