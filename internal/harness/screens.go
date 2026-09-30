package harness

import (
	"regexp"
	"strings"
	"unicode/utf8"
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
	// PasteTakesEnter says the harness reads fast typing as a paste and can
	// take the Enter that ends it as part of the paste, leaving the text in
	// its composer: Codex 0.154.0 did, live, on 2026-09-28.
	PasteTakesEnter bool
	// Undrawn says the harness can hold typed text without drawing it until
	// its next redraw, which a resize brings: an idle Codex 0.154 did, live,
	// on 2026-09-29.
	Undrawn bool
	// Empty matches the composer row that shows only while the composer
	// holds nothing, as Codex's placeholder does. RuledComposer says the
	// composer is instead the rows between the screen's last two full-width
	// rules, as pi 0.85 draws its editor, and holds nothing while they are
	// blank.
	Empty         *regexp.Regexp
	RuledComposer bool
}

// Dialog is one startup prompt. A spawn answers it only while one of Markers
// shows, by moving the focus, the option whose row starts with Focus, down to
// the option that starts with Accept, then confirming that option with Enter.
// A dialog without Accept is never answered: it stops the spawn. Summary marks
// a dialog whose answer leaves untrusted what it lists: the row it matches says
// how much, and the spawn reports it.
type Dialog struct {
	Name    string
	Markers []string
	Focus   string
	Accept  string
	Summary *regexp.Regexp
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
			// "✽ Reticulating…". Claude Code draws the spinner from one of
			// three glyph lists; one has "*" where the others have "✳".
			Working: regexp.MustCompile(`^[·✢✳*✶✻✽] \S.*…`),
			Pasted:  []string{"[Pasted text #"},
		}, true
	case Codex:
		return Screens{
			Dialogs: []Dialog{
				{Name: "the update prompt", Markers: []string{"Update available!"}, Focus: "›", Accept: "2. Skip"},
				{Name: "the directory trust prompt", Markers: []string{"Do you trust the contents of this directory?"}, Focus: "›", Accept: "1. Yes, continue"},
				// Trusting hooks is the Overlord's decision, never a spawn's: a
				// goblin continues without trusting them, so they do not run,
				// and the spawn reports them.
				{Name: "the hook review prompt", Markers: []string{"Hooks need review"}, Focus: "›", Accept: "3. Continue without trusting", Summary: regexp.MustCompile(`\d+ hooks? (is|are) new or changed`)},
			},
			// Captured live on Codex 0.154: the empty composer shows its
			// placeholder, at start and after a turn (the binary also holds
			// "Ask a follow-up question"), above a footer naming the
			// model and folder ("gpt-6-astra low · ~\..."); a footer that
			// counts the context left shows while text waits behind a turn. A
			// turn in progress shows only in the status row, as in "• Working
			// (5s • esc to interrupt)", whose glyph alternates with ◦: a reply
			// may say "Working" anywhere else.
			Ready:           regexp.MustCompile(`^› (Ask Codex to do anything|Ask a follow-up question)|context left`),
			Working:         regexp.MustCompile(`esc to interrupt|^\s*• Working \(`),
			Pasted:          []string{"[Pasted Content"},
			PasteTakesEnter: true,
			Undrawn:         true,
			Empty:           regexp.MustCompile(`^› (Ask Codex to do anything|Ask a follow-up question)\s*$`),
		}, true
	case Pi:
		return Screens{
			// Captured on pi 0.85.1 started without --approve: "→ Trust" is
			// focused first, above "Trust parent folder", "Trust (this session
			// only)", "Do not trust" and "Do not trust (this session only)".
			// Trusting for this session only matches --approve and saves
			// nothing to pi's trust store; a pi that does not offer it still
			// stops the spawn with the prompt named.
			Dialogs: []Dialog{{Name: "the project trust prompt", Markers: []string{"Trust project folder?"}, Focus: "→", Accept: "Trust (this session only)"}},
			// The context meter in the footer's last row, as in "0.0%/1.0M
			// (auto)", which the session's token counts and cost lead once a
			// turn has run: "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)".
			Ready: regexp.MustCompile(`(^|\s)\d+(\.\d+)?%/\d`),
			// A braille spinner in the rule above the editor, as in
			// "── ⠸ Working ──".
			Working: regexp.MustCompile(`[\x{2800}-\x{28FF}]\s+Working`),
			// The editor draws a full-width rule above and below its rows
			// and nothing at their sides (pi-tui's Editor.render).
			RuledComposer: true,
		}, true
	}
	return Screens{}, false
}

// NativeDefault reports whether a spawn that names no backend starts kind in a
// native terminal, which it does only once kind's native launch has been
// proven live: Claude Code; pi, proven on pi 0.85.1 (started with --approve,
// so it never asks to trust the folder); and codex, proven on Codex 0.154.
// Kimi, which has no native screens, starts in Herdr.
func NativeDefault(kind Kind) bool {
	return kind == Claude || kind == Pi || kind == Codex
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

// ComposerEmpty reports whether screen shows the harness's composer holding
// nothing, so text typed now cannot land on top of text somebody left there
// unsent. It is false whenever that cannot be read, including for a harness
// whose empty composer it does not know.
func (s Screens) ComposerEmpty(screen []string) bool {
	if s.Empty != nil {
		return anyRow(screen, s.Empty)
	}
	if !s.RuledComposer {
		return false
	}
	var rules []int
	for i, row := range screen {
		if isRule(row) {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 || rules[len(rules)-1]-rules[len(rules)-2] < 2 {
		return false
	}
	for _, row := range screen[rules[len(rules)-2]+1 : rules[len(rules)-1]] {
		if strings.TrimSpace(row) != "" {
			return false
		}
	}
	return true
}

// isRule reports whether row is a full-width rule: box-drawing dashes and
// nothing else.
func isRule(row string) bool {
	row = strings.TrimSpace(row)
	return utf8.RuneCountInString(row) >= 8 && strings.Trim(row, "─") == ""
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

// runningWork matches a row that shows a harness running a turn, a tool, or
// a background job it will report back from, whichever harness drew it: the
// interrupt hint Claude Code and Codex show while a turn runs, Codex's status
// row, pi's rule, Claude Code's spinner and the "Running…" under a tool in
// progress, and the count of background shells in Claude Code's footer. The
// line that ends a Claude Code turn says "1 shell still running" and stays on
// screen after the shell ends, so only the footer's count is read.
var runningWork = []*regexp.Regexp{
	regexp.MustCompile(`esc to interrupt`),
	regexp.MustCompile(`^[•◦]\s+Working \(`),
	regexp.MustCompile(`Running…`),
	regexp.MustCompile(`[\x{2800}-\x{28FF}]\s+Working`),
	regexp.MustCompile(`^[·✢✳*✶✻✽] \S.*…`),
	regexp.MustCompile(`(^|·)\s*\d+ (shells?|background tasks?)\s*(·|$)`),
}

// RunningWork returns the first row of screen that shows a tool, a turn or a
// background job running, and whether there is one.
func RunningWork(screen []string) (string, bool) {
	for _, row := range screen {
		row = strings.TrimSpace(row)
		for _, pattern := range runningWork {
			if pattern.MatchString(row) {
				return row, true
			}
		}
	}
	return "", false
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
