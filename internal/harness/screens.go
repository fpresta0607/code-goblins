package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
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
	// Offers are the optional offers the harness draws inline above its
	// composer rather than in its place, as Codex draws its Daybreak security
	// offer. Codex hands every key to a composer that holds a draft, taking
	// Escape and a digit as the offer's own only over an empty one and only
	// while no turn runs (codex-rs tui/src/bottom_pane/actionable_banner.rs,
	// handle_inline_banner_key), and the console keeps the rows of an offer an
	// earlier redraw dismissed. So an offer is never a dialog: nothing waits
	// on it or reports it, and a delivery dismisses one Offer reads as live
	// before it types (#1432, #1556).
	Offers []Dialog
	// Ready matches a row of the composer waiting for input.
	Ready *regexp.Regexp
	// Working matches a row while a turn runs.
	Working *regexp.Regexp
	// Starting matches the row a harness shows when it turned a submitted
	// prompt away because its startup has not finished, and put the prompt
	// back in its composer.
	Starting *regexp.Regexp
	// Pasted is how the composer shows typed text it collapsed as a paste.
	Pasted []string
	// PasteTakesEnter says the harness reads fast typing as a paste and can
	// take the Enter that ends it as part of the paste, leaving the text in
	// its composer: Codex 0.154.0 did, live, on 2026-09-28.
	PasteTakesEnter bool
	// Resubmits is how many more times Enter is pressed while typed text
	// still waits in a ruled composer over the empty row an Enter taken as a
	// new line added, and no turn has started: one for Claude Code, which
	// left a resumed goblin's instruction in its composer over an empty row
	// on a loaded machine, live, on 2026-10-20. Text that still shows over no
	// new row is also what a harness that takes its Enter late shows, and
	// pressed again it would get a second Enter after the instruction
	// (#1232). Codex has none: End ends its paste, so the Enter after it
	// submits (#1171).
	Resubmits int
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
	// Footer bounds an unruled composer below its last prompt row.
	Footer *regexp.Regexp
	// Choice matches the focused row of a list of numbered choices the
	// harness draws in its composer's place while it waits on an answer only
	// its terminal takes, as Claude Code draws a permission prompt, and
	// Choices any other row of that list.
	Choice, Choices *regexp.Regexp
	// Modes matches the composer footer's row that names the session's
	// permission mode, its first group the mode, for a harness that cycles
	// modes with Shift+Tab.
	Modes *regexp.Regexp
}

// Dialog is one startup prompt. A spawn answers it only while one of Markers
// shows, by moving the focus, the option whose row starts with one of Focus,
// down to the option that starts with Accept, then confirming that option with
// Enter.
// EscapeHint instead names the footer of a dialog or an offer dismissed with
// Escape, never accepted. A dialog without either is never answered: it stops
// the spawn. Summary marks
// a dialog whose answer leaves untrusted what it lists: the row it matches says
// how much, and the spawn reports it.
type Dialog struct {
	Name       string
	Markers    []string
	Focus      []string
	Accept     string
	EscapeHint string
	Summary    *regexp.Regexp
	// OverlayOption distinguishes an offer over a composer from quoted text.
	OverlayOption *regexp.Regexp
	// Menu bounds focused choices to their confirmation footer at screen end.
	Menu *regexp.Regexp
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
				// Claude Code marks the focus with ❯ only in a terminal its
				// environment names as one that draws Unicode, and with >
				// elsewhere, as in a native terminal.
				Focus:  []string{"❯", ">"},
				Accept: "Yes, I trust this folder",
			}},
			// The composer's footer names the session's permission mode
			// while it waits, as in "⏵⏵ auto mode on (shift+tab to
			// cycle)" or "⏸ manual mode on": a goblin runs with permission
			// checks bypassed, and a CFO in whichever mode its session
			// chose. The modes are Claude Code 2.1.302's.
			Ready: claudeModes,
			Modes: claudeModes,
			// A spinner glyph, then a verb that ends in an ellipsis, as in
			// "✽ Reticulating…". Claude Code draws the spinner from one of
			// three glyph lists; one has "*" where the others have "✳".
			Working:   regexp.MustCompile(`^[·✢✳*✶✻✽] \S.*…`),
			Pasted:    []string{"[Pasted text #"},
			Resubmits: 1,
			// Claude Code 2.1.288 encloses its bare prompt between rules.
			Empty:         regexp.MustCompile(`^[>❯][ \x{00a0}]*$`),
			RuledComposer: true,
			// Claude Code 2.1.302 asks permission for a call under its
			// diff, as in "Do you want to create nine.txt?", over
			// "❯ 1. Yes", "  2. Yes, and switch to accept edits ..." and
			// "  3. No", with no composer or footer under them; its focus
			// mark is > where its terminal draws no Unicode.
			Choice:  regexp.MustCompile(`^\s*[❯>] \d+\. \S`),
			Choices: regexp.MustCompile(`^\s{2,}\d+\. \S`),
		}, true
	case Codex:
		hookMenu := `(?m)^[ \t]*(?:› )?1\. Review hooks[ \t]*\n[ \t]*(?:› )?2\. Trust all and continue[ \t]*\n[ \t]*(?:› )?3\. Continue without trusting(?: \(hooks won't run\))?[ \t]*\n[ \t\n]*`
		return Screens{
			Offers: []Dialog{{Name: "the optional Daybreak security setup offer", Markers: []string{"Set up security for Daybreak mode"}, EscapeHint: "Press a number to choose · esc to dismiss · type to continue", OverlayOption: regexp.MustCompile(`^\s*› 1\. Set up security\s*$`)}},
			Dialogs: []Dialog{
				{Name: "the update prompt", Markers: []string{"Update available!"}, Focus: []string{"›"}, Accept: "2. Skip"},
				{Name: "the directory trust prompt", Markers: []string{"Do you trust the contents of this directory?"}, Focus: []string{"›"}, Accept: "1. Yes, continue", Menu: regexp.MustCompile(`(?m)^[ \t]*(?:› )?1\. Yes, continue[ \t]*\n[ \t]*(?:› )?2\. No, quit[ \t]*\n[ \t\n]*Press enter to continue[ \t\n]*\z`)},
				// Codex 0.160 asks this in its place, live on 2026-10-07 in a
				// project it was never told to trust; Escape there quits Codex.
				{Name: "the folder trust prompt", Markers: []string{"Trust this folder?"}, Focus: []string{"›"}, Accept: "1. Trust and continue", Menu: regexp.MustCompile(`(?m)^[ \t]*(?:› )?1\. Trust and continue[ \t]*\n[ \t]*(?:› )?2\. Quit[ \t]*\n[ \t\n]*enter continue · esc quit[ \t\n]*\z`)},
				// Trusting hooks is the Overlord's decision, never a spawn's: a
				// goblin continues without trusting them, so they do not run,
				// and the spawn reports them.
				{Name: "the hook review prompt", Markers: []string{"Hooks need review"}, Focus: []string{"›"}, EscapeHint: "enter confirm / esc skip", Summary: regexp.MustCompile(`\d+ hooks? (is|are) new or changed`), Menu: regexp.MustCompile(hookMenu + `enter confirm / esc skip[ \t\n]*\z`)},
				{Name: "the hook review prompt", Markers: []string{"Hooks need review"}, Focus: []string{"›"}, Accept: "3. Continue without trusting", Summary: regexp.MustCompile(`\d+ hooks? (is|are) new or changed`), Menu: regexp.MustCompile(hookMenu + `(?:Press enter to confirm or esc to go back|enter confirm · esc skip)[ \t\n]*\z`)},
			},
			// Captured live on Codex 0.154: the empty composer shows its
			// placeholder, at start and after a turn (the binary also holds
			// "Ask a follow-up question"), above a footer naming the
			// model and folder ("gpt-6-astra low · ~\..."); a footer that
			// counts the context left shows while text waits in a turn. A
			// turn in progress shows only in the status row, as in "• Working
			// (5s • esc to interrupt)", whose glyph alternates with ◦ and may
			// be blank, and whose heading may name the step instead of
			// Working. A reply, the composer or a shell may quote that text, so
			// only a row of that shape counts: no prompt or quote before the
			// elapsed time and the interrupt hint.
			Ready:           regexp.MustCompile(`^› (Ask Codex to do anything|Ask a follow-up question)|context left`),
			Working:         regexp.MustCompile(`^\s*([•◦]\s+)?[^\s"'›>][^"'›>]*\([^()]*esc to interrupt\)|^\s*[•◦] Working \(`),
			Pasted:          []string{"[Pasted Content"},
			PasteTakesEnter: true,
			Undrawn:         true,
			Empty:           regexp.MustCompile(`^› (Ask Codex to do anything|Ask a follow-up question)\s*$`),
			Footer:          regexp.MustCompile(`^\s*(\? for shortcuts\b|\d+% context left\b|\S+ \S+ · \S|(?i:gpt-\S+) (minimal|low|medium|high|xhigh)\s*$)`),
		}, true
	case Pi:
		return Screens{
			// Captured on pi 0.85.1 started without --approve: "→ Trust" is
			// focused first, above "Trust parent folder", "Trust (this session
			// only)", "Do not trust" and "Do not trust (this session only)".
			// Trusting for this session only matches --approve and saves
			// nothing to pi's trust store; a pi that does not offer it still
			// stops the spawn with the prompt named.
			Dialogs: []Dialog{{Name: "the project trust prompt", Markers: []string{"Trust project folder?"}, Focus: []string{"→"}, Accept: "Trust (this session only)"}},
			// The context meter in the footer's last row, as in "0.0%/1.0M
			// (auto)", which the session's token counts and cost lead once a
			// turn has run: "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)".
			// After a compaction it shows "?/272k (auto)" until the next turn.
			Ready: regexp.MustCompile(`(^|\s)(\d+(\.\d+)?%|\?)/\d`),
			// A braille spinner in the rule above the editor, as in
			// "── ⠸ Working ──".
			Working: regexp.MustCompile(`[\x{2800}-\x{28FF}]\s+Working`),
			// pi 0.85 draws its editor and footer, and takes typed text,
			// before its startup completes, which on a machine without fd and
			// rg includes downloading them (InteractiveMode.init); until then
			// it answers Enter with this status (handleStartupSubmit).
			Starting: regexp.MustCompile(`Startup is still in progress`),
			// pi's editor collapses a burst of more than ten lines or a
			// thousand characters into one marker in its composer, as
			// "[paste #1 +12 lines]" or "[paste #1 1200 chars]", and holds the
			// text itself. A steer or a wake line long enough to collapse
			// therefore shows, in the composer, no text the fleet could
			// otherwise find there.
			Pasted: []string{"[paste #"},
			// The editor draws a full-width rule above and below its rows
			// and nothing at their sides (pi-tui's Editor.render).
			RuledComposer: true,
		}, true
	}
	return Screens{}, false
}

// Dialog returns the dialog screen shows, if any.
func (s Screens) Dialog(screen []string) (Dialog, bool) {
	for _, dialog := range s.Dialogs {
		if !dialog.Shows(screen) || s.IsWorking(screen) {
			continue
		}
		// A menu replaces the composer. Its text above a live composer is
		// conversation history.
		if len(dialog.Focus) > 0 && s.showsComposerOrTurn(screen) {
			continue
		}
		return dialog, true
	}
	return Dialog{}, false
}

// Offer returns the offer screen shows while it may still take a key, which
// a delivery dismisses with one Escape before it types: no turn runs, no
// dialog's words show, and the composer neither holds text nor has anything
// open in its place, since Escape is then a dialog's, a picker's or a draft's
// own key, and Codex's folder trust prompt quits on it. The rows an earlier
// redraw left read the same as a live offer, so what Offer reports is pressed
// once and never waited on.
func (s Screens) Offer(screen []string) (Dialog, bool) {
	if s.IsWorking(screen) || s.ComposerHoldsText(screen) || s.IsOpen(screen) {
		return Dialog{}, false
	}
	for _, dialog := range s.Dialogs {
		if dialog.Shows(screen) {
			return Dialog{}, false
		}
	}
	for _, offer := range s.Offers {
		if offer.Shows(screen) {
			return offer, true
		}
	}
	return Dialog{}, false
}

// Shows reports whether screen shows the dialog.
func (d Dialog) Shows(screen []string) bool {
	if d.Menu != nil && !d.Menu.MatchString(strings.Join(screen, "\n")) {
		return false
	}
	if d.OverlayOption != nil && !anyRow(screen, d.OverlayOption) {
		return false
	}
	compact := compactScreen(screen)
	if d.EscapeHint != "" && !strings.Contains(compact, compactScreen([]string{d.EscapeHint})) {
		return false
	}
	for _, marker := range d.Markers {
		if strings.Contains(compact, compactScreen([]string{marker})) {
			return true
		}
	}
	return false
}

// IsReady reports whether screen shows the composer waiting for input and no
// turn or recognized dialog in progress.
func (s Screens) IsReady(screen []string) bool {
	_, hasDialog := s.Dialog(screen)
	return !hasDialog && anyRow(screen, s.Ready) && !s.IsWorking(screen)
}

// IsIdleAtEmptyComposer reports whether screen shows the harness waiting at
// its composer with nothing in it: no turn, no dialog, no work running on it.
func (s Screens) IsIdleAtEmptyComposer(screen []string) bool {
	_, dialog := s.Dialog(screen)
	_, running := RunningWork(screen)
	return s.IsReady(screen) && !dialog && !running && s.ComposerEmpty(screen)
}

// IsWorking reports whether screen shows a turn in progress.
func (s Screens) IsWorking(screen []string) bool {
	return anyRow(screen, s.Working)
}

// workingWordsMax caps the words WorkingWords returns, so a heading Codex
// names its step by stays one short phrase beside the board's dither.
const workingWordsMax = 48

// WorkingWords is what the last row of a turn in progress says the harness
// is doing, with its spinner, its timer and its interrupt hint cut away:
// Claude Code's verb, as "Cogitating" from "✻ Cogitating… (12s · esc to
// interrupt)", the heading Codex names its step by, as "Working" or
// "Exploring the tests", and pi's "Working". It is "" while no turn shows.
func (s Screens) WorkingWords(screen []string) string {
	for i := len(screen) - 1; i >= 0 && s.Working != nil; i-- {
		if !s.Working.MatchString(screen[i]) {
			continue
		}
		words := strings.TrimLeftFunc(screen[i], func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if cut := strings.IndexAny(words, "…(─"); cut >= 0 {
			words = words[:cut]
		}
		words = strings.TrimSpace(words)
		if runes := []rune(words); len(runes) > workingWordsMax {
			words = strings.TrimSpace(string(runes[:workingWordsMax])) + "…"
		}
		return words
	}
	return ""
}

// IsAsking reports whether screen shows the harness waiting on an answer at
// a screen of its own, which only its terminal takes: a dialog it is known to
// draw while no row shows a turn, the composer's footer or a ruled composer,
// or a list of numbered choices with one focused and another on screen while
// no row under the last focused one shows any of them. What the conversation
// shows decides nothing: a dialog's words a diff quotes, or a numbered prompt,
// has the composer waiting under it, and a mode a reply names lies above a
// list that asks.
func (s Screens) IsAsking(screen []string) bool {
	if _, dialog := s.Dialog(screen); dialog && !s.showsComposerOrTurn(screen) {
		return true
	}
	if s.Choice == nil {
		return false
	}
	for i := len(screen) - 1; i >= 0; i-- {
		if s.Choice.MatchString(screen[i]) {
			return !s.showsComposerOrTurn(screen[i+1:]) && anyRow(screen, s.Choices)
		}
	}
	return false
}

// showsComposerOrTurn reports whether rows show a turn, the composer's footer
// or a ruled composer.
func (s Screens) showsComposerOrTurn(rows []string) bool {
	_, composer := ruledComposer(rows)
	return composer || s.IsWorking(rows) || anyRow(rows, s.Ready)
}

// footerRows is how many of a screen's last rows that are not blank hold
// the composer's footer: its mode row, and a line Claude Code adds under it,
// as an update waiting for a restart.
const footerRows = 3

// IsOpen reports whether screen shows something of the harness's own drawn
// in its input line's place, a picker, a panel, a dialog or a question,
// while no turn runs: what asks, or a screen whose last rows show no row of
// the composer's footer, as Claude Code draws its pickers and panels. The
// footer is read only there, under the composer, since the conversation
// above a picker can quote it. A screen back at its input line, a turn
// running over it or not, has nothing open, and so has a blank one.
func (s Screens) IsOpen(screen []string) bool {
	if s.IsAsking(screen) {
		return true
	}
	if s.Ready == nil || s.IsWorking(screen) {
		return false
	}
	var last []string
	for i := len(screen) - 1; i >= 0 && len(last) < footerRows; i-- {
		if strings.TrimSpace(screen[i]) != "" {
			last = append(last, screen[i])
		}
	}
	return len(last) > 0 && !anyRow(last, s.Ready)
}

// claudeModes is the footer row Claude Code 2.1.302 names its permission mode
// on while its composer waits, as in "⏵⏵ auto mode on (shift+tab to cycle)".
var claudeModes = regexp.MustCompile(`(manual mode|plan mode|accept edits|bypass permissions|don't ask|auto mode) on\b`)

// Mode returns the permission mode screen's composer footer names, read only
// in the footer's last rows as IsOpen reads them, and "" while no footer shows
// one, as while a picker is open, or for a harness without modes.
func (s Screens) Mode(screen []string) string {
	if s.Modes == nil {
		return ""
	}
	seen := 0
	for i := len(screen) - 1; i >= 0 && seen < footerRows; i-- {
		if strings.TrimSpace(screen[i]) == "" {
			continue
		}
		seen++
		if match := s.Modes.FindStringSubmatch(screen[i]); match != nil {
			return match[1]
		}
	}
	return ""
}

// IsStarting reports whether screen shows the harness turning a submitted
// prompt away because its startup has not finished.
func (s Screens) IsStarting(screen []string) bool {
	return s.Starting != nil && anyRow(screen, s.Starting)
}

// ComposerEmpty reports whether screen shows the harness's composer holding
// nothing, so text typed now cannot land on top of text somebody left there
// unsent. It is false whenever that cannot be read, including for a harness
// whose empty composer it does not know.
func (s Screens) ComposerEmpty(screen []string) bool {
	if _, hasDialog := s.Dialog(screen); hasDialog {
		return false
	}
	composer, isRead := s.composer(screen)
	if !isRead {
		return false
	}
	if s.Empty != nil {
		return len(composer) == 1 && s.Empty.MatchString(strings.TrimSpace(composer[0]))
	}
	for _, row := range composer {
		if strings.TrimSpace(row) != "" {
			return false
		}
	}
	return true
}

// ComposerHoldsText reports whether screen proves the harness's composer
// holds text somebody left there unsent, so text typed now would land on top
// of it. A screen showing a dialog, or one whose composer cannot be read,
// proves nothing either way: it holds no text.
func (s Screens) ComposerHoldsText(screen []string) bool {
	if _, hasDialog := s.Dialog(screen); hasDialog {
		return false
	}
	if _, isRead := s.composer(screen); !isRead {
		return false
	}
	return !s.ComposerEmpty(screen)
}

// ComposerMatches proves the complete draft survived a startup dialog.
// Unlike Shows, a suffix, an older prompt or a collapsed paste is not enough.
func (s Screens) ComposerMatches(screen []string, typed string) bool {
	if _, hasDialog := s.Dialog(screen); hasDialog {
		return false
	}
	composer, isRead := s.composer(screen)
	if !isRead {
		return false
	}
	first := strings.TrimSpace(composer[0])
	if s.Empty != nil {
		for _, prompt := range []string{"› ", "❯ ", "> "} {
			if content, found := strings.CutPrefix(first, prompt); found {
				first = content
				break
			}
		}
	}
	want := compactScreen([]string{typed})
	return want != "" && compactScreen(append([]string{first}, composer[1:]...)) == want
}

// composer reads the current draft, including rows after a blank line. A
// placeholder in history cannot prove the current composer empty, and a
// footer-shaped draft row cannot replace the trailing UI footer block.
func (s Screens) composer(screen []string) ([]string, bool) {
	if s.RuledComposer {
		return ruledComposer(screen)
	}
	if s.Footer == nil {
		return nil, false
	}
	end := len(screen) - emptyRowsAtEnd(screen)
	footer := end
	for footer > 0 && s.Footer.MatchString(screen[footer-1]) {
		footer--
	}
	if footer == end {
		return nil, false
	}
	for start := footer - 1; start >= 0; start-- {
		row := strings.TrimSpace(screen[start])
		if row == "›" || strings.HasPrefix(row, "› ") {
			composer := screen[start:footer]
			return composer[:len(composer)-emptyRowsAtEnd(composer)], true
		}
	}
	return nil, false
}

// TookEnterAsNewLine reports whether screen shows the harness's ruled
// composer holding text over more empty rows at its end than atPress, the
// screen as Enter was pressed, did: the harness took that Enter as a new
// line, not a submit, as Claude Code did on a loaded machine, live, on
// 2026-10-20, leaving a resumed goblin's instruction over an empty row. A
// composer that shows no row the Enter added has not taken it yet, or took it
// some way this cannot read. It is false whenever either composer cannot be
// read.
func (s Screens) TookEnterAsNewLine(atPress, screen []string) bool {
	if !s.RuledComposer {
		return false
	}
	before, isRead := ruledComposer(atPress)
	if !isRead {
		return false
	}
	after, isRead := ruledComposer(screen)
	if !isRead {
		return false
	}
	empty := emptyRowsAtEnd(after)
	return empty < len(after) && empty > emptyRowsAtEnd(before)
}

// ruledComposer returns the rows between screen's last two full-width rules,
// and false when there are no such rows.
func ruledComposer(screen []string) ([]string, bool) {
	var rules []int
	for i, row := range screen {
		if isRule(row) {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 || rules[len(rules)-1]-rules[len(rules)-2] < 2 {
		return nil, false
	}
	return screen[rules[len(rules)-2]+1 : rules[len(rules)-1]], true
}

// emptyRowsAtEnd counts the empty rows rows ends with.
func emptyRowsAtEnd(rows []string) int {
	count := 0
	for count < len(rows) && strings.TrimSpace(rows[len(rows)-1-count]) == "" {
		count++
	}
	return count
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
// with one of the dialog's focus glyphs, after it. More or fewer than one such
// row means the focus cannot be read.
func (d Dialog) Focused(screen []string) (string, bool) {
	if d.Menu != nil {
		screen = strings.Split(d.Menu.FindString(strings.Join(screen, "\n")), "\n")
	}
	var focused []string
	for _, row := range screen {
		for _, glyph := range d.Focus {
			if option, found := strings.CutPrefix(strings.TrimSpace(row), glyph); found {
				focused = append(focused, strings.TrimSpace(option))
				break
			}
		}
	}
	if len(focused) != 1 {
		return "", false
	}
	return focused[0], true
}

// runningWork matches a row that shows a harness running a turn, a tool, or
// a background job it will report back from, whichever harness drew it: the
// interrupt hint Claude Code and Codex show while a turn runs, Codex's status
// row, pi's rule, Claude Code's spinner and the "Running…" under a tool in
// progress, the count of background shells in Claude Code's footer, and its
// spinner row while a turn waits on background agents or tasks it started.
// The footer counts background shells and monitors, alone or together, as
// "4 shells, 1 monitor". The line that ends a Claude Code turn says "1 shell
// still running" and stays on screen after the shell ends, so only the
// footer's count is read, and never on that line wrapped before "still".
var runningWork = []*regexp.Regexp{
	regexp.MustCompile(`esc to interrupt`),
	regexp.MustCompile(`^[•◦]\s+Working \(`),
	regexp.MustCompile(`Running…`),
	regexp.MustCompile(`[\x{2800}-\x{28FF}]\s+Working`),
	regexp.MustCompile(`^[·✢✳*✶✻✽] \S.*…`),
	backgroundCount,
	backgroundAgents,
}

// backgroundCount is Claude Code's footer count of the background shells and
// monitors a turn left running, backgroundAgents its spinner row while a turn
// waits on the background agents it started, and turnEnded the line that ends
// a turn, which counts them too.
var (
	backgroundCount  = regexp.MustCompile(`(^|·)\s*\d+ (shells?|monitors?)(,\s*\d+ (shells?|monitors?))*\s*(·|$)`)
	backgroundAgents = regexp.MustCompile(`^[·✢✳*✶✻✽] Waiting for \d+ background `)
	turnEnded        = regexp.MustCompile(`· done \d{1,2}:\d{2}`)
)

// RunningWork returns the first row of screen that shows a tool, a turn or a
// background job running, and whether there is one.
func RunningWork(screen []string) (string, bool) {
	for _, row := range screen {
		row = strings.TrimSpace(row)
		for _, pattern := range runningWork {
			if pattern == backgroundCount && turnEnded.MatchString(row) {
				continue
			}
			if pattern.MatchString(row) {
				return row, true
			}
		}
	}
	return "", false
}

func HasBackgroundWork(screen []string) bool {
	for _, row := range screen {
		row = strings.TrimSpace(row)
		if turnEnded.MatchString(row) {
			continue
		}
		if backgroundCount.MatchString(row) || backgroundAgents.MatchString(row) {
			return true
		}
	}
	return false
}

// OutputDigest is a digest of what has been written to screen: every row but
// those RunningWork reads, which a harness redraws by itself while work runs,
// with its spinner, its clock and its interrupt hint. Two screens that differ
// only in those rows have the same digest, so a turn whose clock ticks on with
// nothing written reads as still.
func OutputDigest(screen []string) string {
	hash := sha256.New()
	for _, row := range screen {
		if _, isRunning := RunningWork([]string{row}); !isRunning {
			hash.Write([]byte(row + "\n"))
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
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
