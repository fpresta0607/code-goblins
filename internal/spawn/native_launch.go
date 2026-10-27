package spawn

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/codexguard"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A native terminal starts at the size goblins starts the CFO at.
const (
	nativeCols = 120
	nativeRows = 40
)

// nativePoll is how often a native spawn reads the terminal's screen.
const nativePoll = 250 * time.Millisecond

// nativeStartup bounds a harness's startup, dialogs included; nativeKeyEffect
// bounds how long one key takes to show on the screen; nativeAccepted bounds
// how long a submitted instruction takes to show the harness working; and
// nativeReadGrace bounds a run of failed screen reads, since a console can
// refuse an attach for a moment while it starts under load.
var (
	nativeStartup   = 120 * time.Second
	nativeKeyEffect = 60 * time.Second
	nativeAccepted  = 90 * time.Second
	nativeReadGrace = 10 * time.Second
	nativeCloseWait = 15 * time.Second
	// nativeDialogSettle is how long a startup dialog shows before its first
	// key: a harness draws a dialog before it reads keys.
	nativeDialogSettle = 2 * time.Second
	// nativeReadySettle is how long a harness's composer stays ready, with no
	// dialog drawn over it, before anything is typed into it.
	nativeReadySettle = 2 * time.Second
	// nativeRedrawNudge is how long typed text may stay undrawn before a
	// harness that holds it so is made to redraw.
	nativeRedrawNudge = time.Second
	// nativeTypedPace is the time allowed per typed character, on top of
	// nativeKeyEffect, for a harness that can take typed text in slowly.
	nativeTypedPace = 50 * time.Millisecond
	// nativeTypedCap bounds how long typed text may keep arriving in a
	// harness that takes it in slowly.
	nativeTypedCap = 10 * time.Minute
	// nativeQueuedProof bounds how long a delivery to a harness already in
	// a turn waits for the harness's record to show it taken, in case the
	// tool call running was ending.
	nativeQueuedProof = 5 * time.Second
	// nativeStartingPress is how often Enter is pressed again while a harness
	// turns its instruction away as still starting.
	nativeStartingPress = time.Second
)

// maxDialogMoves bounds the focus moves one dialog takes.
const maxDialogMoves = 8

var errNativeStartupDialog = errors.New("a startup dialog interrupted instruction delivery")

// launchNativeHost starts the harness in a native terminal of its own, the
// task's id, and returns its host's record, the zero record when it launched
// none. It is the last step of a start's turn: from here the host counts as a
// running terminal, which the next start's admission sees. A goblin's
// terminal keeps off the cores the fleet's work leaves to the Overlord's own
// apps.
func (s Service) launchNativeHost(id string, kind harness.Kind, launch harness.Launch, userEnv []string, credentials auth.Result) (host.Record, error) {
	if _, ok := harness.NativeScreens(kind); !ok {
		return host.Record{}, fmt.Errorf("spawn: %s cannot run in a native terminal yet", kind)
	}
	program, err := nativeProgram(kind, launch)
	if err != nil {
		return host.Record{}, err
	}
	if len(s.HostCommand) == 0 {
		return host.Record{}, errors.New("spawn: the command that runs a native terminal's host is required")
	}
	if !filepath.IsAbs(s.HomeRoot) {
		return host.Record{}, fmt.Errorf("spawn: the home's root %q is not an absolute path, so the goblin's cfo commands could not name the home that spawned it", s.HomeRoot)
	}
	record, err := host.Launch(s.StateDir, s.HostCommand, s.nativeHostEnvironment(userEnv, launch, credentials), host.Spec{ID: id, Args: program, Dir: launch.Dir, Cols: nativeCols, Rows: nativeRows, ShouldLeaveCoresForApps: true})
	if err != nil {
		return host.Record{}, fmt.Errorf("spawn: start native terminal %s: %w", id, err)
	}
	return record, nil
}

// briefNativeHarness waits for the harness in task id's native terminal,
// which record names, to start and delivers its instruction. It reads the
// terminal's screen throughout: it answers a startup dialog only once it
// recognizes it, types the instruction only at the harness's composer, and
// submits it only once the composer shows it. It runs after the start's turn,
// so a slow startup holds up no other start.
func (s Service) briefNativeHarness(ctx context.Context, id string, record host.Record, kind harness.Kind, launch harness.Launch) error {
	screens, ok := harness.NativeScreens(kind)
	if !ok {
		return fmt.Errorf("spawn: %s cannot run in a native terminal yet", kind)
	}
	untrusted, _, err := s.awaitNativeReady(ctx, record, screens)
	if err != nil {
		return err
	}
	if untrusted != "" {
		if err := s.reportUntrusted(id, kind, launch.Dir, untrusted); err != nil {
			return err
		}
	}
	screen, err := s.readNativeScreen(ctx, record)
	if err != nil {
		return err
	}
	// Text typed into a composer that holds something lands on top of it, and
	// the Enter that submits the instruction would submit that draft instead.
	// A startup dialog drawn over an empty composer, or a composer this cannot
	// read, proves no draft: the delivery answers the one and types into the
	// other as it always has.
	if withoutInlineOffer(screens).ComposerHoldsText(screen) {
		return fmt.Errorf("spawn: native terminal %s holds a draft, so nothing was typed into it; the terminal, its task record and its worktree are kept", record.ID)
	}
	instruction, err := s.typedInstruction(id, launch.PromptInstruction())
	if err != nil {
		return err
	}
	return s.deliverNativeInstruction(ctx, record, screens, state.TaskMeta{ID: id, Harness: string(kind), Worktree: launch.Dir, SpawnGen: launch.Env["CFO_SPAWN_GEN"]}, launch.ResumeSession, instruction, true)
}

// withoutInlineOffer returns screens that read the optional offer Codex draws
// inline above its composer as the harness's rendered history. The offer is
// drawn above the composer rather than in its place, and Codex hands every key
// to a composer that holds a draft, taking Escape only over an empty one and
// only while no turn runs (codex-rs tui/src/bottom_pane/actionable_banner.rs,
// handle_inline_banner_key). So it never holds a delivery back, and nothing
// ever chooses its option: rows left on a screen an earlier redraw drew make
// a ready composer look blocked, as they did for cg-command-center-notifications
// on 2026-10-27 (#1432), while a live one is dismissed with Escape. Every other
// dialog a harness draws still stops or answers a spawn.
func withoutInlineOffer(screens harness.Screens) harness.Screens {
	if !slices.ContainsFunc(screens.Dialogs, isInlineOffer) {
		return screens
	}
	history := screens
	history.Dialogs = slices.DeleteFunc(slices.Clone(screens.Dialogs), isInlineOffer)
	return history
}

// isInlineOffer reports whether dialog is a harness's optional offer drawn
// above the composer, which owns no key a composer holding a draft uses.
func isInlineOffer(dialog harness.Dialog) bool {
	return dialog.OverlayOption != nil
}

// inlineOffer reports the optional offer screen shows, if any. A screen that
// shows a dialog the spawn must answer is never read as an offer, even when
// its rows are stale history the offer no longer draws: Escape is that
// dialog's own key, where the folder trust prompt quits Codex, so nothing
// sends it there.
func inlineOffer(screens harness.Screens, screen []string) (harness.Dialog, bool) {
	if screens.IsWorking(screen) {
		return harness.Dialog{}, false
	}
	var offer harness.Dialog
	var found bool
	for _, dialog := range screens.Dialogs {
		if !dialog.Shows(screen) {
			continue
		}
		if !isInlineOffer(dialog) {
			return harness.Dialog{}, false
		}
		offer, found = dialog, true
	}
	return offer, found
}

// dismissOffer reads the terminal's screen and presses the one key a live
// inline offer takes over a composer that is not proved to hold text, Escape.
// Codex gives that key to the composer once it holds a draft, and takes it as
// its own over an empty one, while a leading digit typed at a live offer is
// taken as the offer's own choice, so every delivery dismisses one shown over
// such a composer before it types. It decides on the screen it read and again
// on a fresh read after the offer has had nativeDialogSettle to be drawn, as
// answerDialog does, since a harness draws an offer before it reads keys and
// an Escape sent into a screen that does not take it yet is lost. A dialog, a
// picker or a draft drawn since the delivery last read means no key is sent:
// Escape leaves a picker, quits Codex's folder trust prompt, and belongs to a
// composer that holds text. It returns the screen it read and whether it
// pressed the key.
func (s Service) dismissOffer(ctx context.Context, record host.Record, screens harness.Screens) ([]string, bool, error) {
	screen, err := s.readNativeScreen(ctx, record)
	if err != nil {
		return nil, false, err
	}
	offer, live := liveInlineOffer(screens, screen)
	if !live {
		return screen, false, nil
	}
	if err := s.sleep(ctx, nativeDialogSettle); err != nil {
		return nil, false, err
	}
	screen, err = s.readNativeScreen(ctx, record)
	if err != nil {
		return nil, false, err
	}
	offer, live = liveInlineOffer(screens, screen)
	if !live {
		return screen, false, nil
	}
	return screen, true, s.dismissInlineOffer(ctx, record, offer)
}

// liveInlineOffer reports the inline offer screen shows over a composer that
// is not proved to hold text, which a delivery dismisses before it types.
func liveInlineOffer(screens harness.Screens, screen []string) (harness.Dialog, bool) {
	history := withoutInlineOffer(screens)
	offer, shown := inlineOffer(screens, screen)
	if !shown || history.ComposerHoldsText(screen) || history.IsOpen(screen) {
		return harness.Dialog{}, false
	}
	return offer, true
}

// dismissInlineOffer presses the one key a live inline offer takes, Escape. An
// offer still showing afterwards was left by an earlier redraw and owns
// nothing, so it is read as history rather than waited out: waiting on it is
// what made a ready composer fail its startup (#1432).
func (s Service) dismissInlineOffer(ctx context.Context, record host.Record, dialog harness.Dialog) error {
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("spawn: type into native terminal %s: %w", record.ID, err)
	}
	defer client.Close()
	if err := client.Input([]byte("\x1b")); err != nil {
		return fmt.Errorf("spawn: dismiss %s in native terminal %s: %w", dialog.Name, record.ID, err)
	}
	return nil
}

// typedInstructionLimit is the longest instruction typed whole into a
// harness.
const typedInstructionLimit = 400

// typedInstruction is what is typed to deliver instruction to the harness in
// native terminal id: the instruction itself, or, for one longer than
// typedInstructionLimit or of more than one line, a line pointing at the
// task's instruction.md, where the whole instruction is written. A harness
// takes a long burst of typing as a paste. Live after the 2026-09-29 reboot an
// idle Codex 0.154 took a 2,940-character brief in at about 17 characters a
// second, and took an Enter pressed while the rest still arrived as part of
// the text. On 2026-10-09 Claude Code 2.1.29x collapsed what its goblins were
// resumed with as "[Pasted text #1]" and handed their models a pasted block
// with nothing written beside it, which a model does not take as the
// operator's own words: three goblins asked whether to proceed and sat until
// someone said yes. One short typed line is the operator's own message to
// every harness.
func (s Service) typedInstruction(id, instruction string) (string, error) {
	if utf8.RuneCountInString(instruction) <= typedInstructionLimit && !strings.ContainsAny(instruction, "\r\n") {
		return instruction, nil
	}
	dir := filepath.Join(s.StateDir, "tasktmp", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("spawn: write the instruction for native terminal %s: %w", id, err)
	}
	path := filepath.Join(dir, "instruction.md")
	if err := os.WriteFile(path, []byte(instruction+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("spawn: write the instruction for native terminal %s: %w", id, err)
	}
	return instructionPointer(path), nil
}

// instructionPointer is the line typed in place of an instruction written to
// path.
func instructionPointer(path string) string {
	return "Read " + path + " and follow it exactly: it is your instruction from the CFO."
}

// awaitNativeReady reads the terminal's screen until the harness's composer
// waits for input, answering each startup dialog it recognizes on the way.
// Only a screen read successfully counts, so seeing no dialog can only come
// from a screen that shows none. The composer counts as ready once it has
// read so throughout nativeReadySettle: Codex 0.154 drew its composer, then
// its hook review over it a second later, and a brief typed at the first sight
// of the composer went into the review. A harness can be in a turn when it is
// read, its status row showing it working: a resumed conversation can come
// back in one, as a Codex goblin with a background terminal running did live,
// and a new goblin is in one once a cfo send that reached it before its
// instruction starts it, as cg-cards-stay-open was on 2026-10-15 (#959). Once
// that has held as long as a composer must, it has started and working says
// so. It returns what a dialog answered without trust left untrusted, in the
// dialog's own words, or nothing.
func (s Service) awaitNativeReady(ctx context.Context, record host.Record, screens harness.Screens) (untrusted string, working bool, err error) {
	deadline := time.Now().Add(nativeStartup)
	history := withoutInlineOffer(screens)
	ready, busy := 0, 0
	for {
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return "", false, fmt.Errorf("spawn: %w", err)
		}
		if dialog, found := history.Dialog(screen); found {
			if dialog.Summary != nil {
				if untrusted = dialog.Summary.FindString(strings.Join(screen, "\n")); untrusted == "" {
					untrusted = dialog.Name
				}
			}
			if err := s.answerDialog(ctx, record, dialog, screen); err != nil {
				return "", false, err
			}
			ready, busy = 0, 0
			continue
		}
		if !history.IsReady(screen) {
			ready = 0
		} else if ready++; ready > readySettleReads() {
			return untrusted, false, nil
		}
		if !screens.IsWorking(screen) {
			busy = 0
		} else if busy++; busy > readySettleReads() {
			return untrusted, true, nil
		}
		if time.Now().After(deadline) {
			return "", false, fmt.Errorf("spawn: native terminal %s showed neither a dialog it knows nor its harness's composer within %s; its screen ends:\n%s", record.ID, nativeStartup, host.ScreenTail(screen, 8))
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return "", false, err
		}
	}
}

// readySettleReads is how many reads in a row, nativePoll apart, span
// nativeReadySettle.
func readySettleReads() int {
	return int(nativeReadySettle / nativePoll)
}

// reportUntrusted tells the CFO, as cfo notify does, that the harness in
// native terminal id started without trusting the hooks its startup asked to
// review, so they do not run, and names the hooks a Codex session in dir, the
// task's worktree, loads: trusting a hook is the Overlord's decision, never a
// spawn's. The harness already runs, so a hook that cannot be named is
// reported, never a reason to stop.
func (s Service) reportUntrusted(id string, kind harness.Kind, dir, summary string) error {
	detail := fmt.Sprintf("%s started without trusting its hooks, so they do not run (%s)", kind, summary)
	if kind == harness.Codex {
		var hooks []string
		meta, err := state.ReadTaskMeta(s.StateDir, id)
		if err == nil {
			hooks, err = harness.CodexHooks(meta.Project, dir)
		}
		if len(hooks) > 0 {
			detail += "; the hooks it loads: " + strings.Join(hooks, "; ")
		}
		if err != nil {
			detail += "; not every hook could be named: " + err.Error()
		}
		detail += "; config.toml [hooks] tables are not named"
	}
	line := "working: " + bounded(state.NormalizeStatusDetail(detail+". Only the Overlord trusts hooks."), 1000)
	if err := state.AppendStatus(s.StateDir, id, line); err != nil {
		return fmt.Errorf("spawn: report the hooks %s started without: %w", kind, err)
	}
	if _, err := wake.Append(s.StateDir, "notify", id, line); err != nil {
		return fmt.Errorf("spawn: report the hooks %s started without: %w", kind, err)
	}
	return nil
}

// AnswerDialog answers one recognized startup dialog in the native terminal
// record names, as a spawn answers it, for a harness spawn did not start,
// such as the CFO's.
func AnswerDialog(ctx context.Context, record host.Record, dialog harness.Dialog, screen []string) error {
	return Service{}.answerDialog(ctx, record, dialog, screen)
}

// answerDialog answers one recognized startup dialog. It moves the focus down
// until the option to choose has it, and confirms that option with Enter. Each
// key waits until its effect shows before the next is sent, so a harness slow
// to redraw is never sent a key twice, and a dialog still drawing is read
// again until its focus shows. An optional offer with a known Escape hint is
// dismissed instead. A dialog no spawn may answer stops the spawn.
func (s Service) answerDialog(ctx context.Context, record host.Record, dialog harness.Dialog, screen []string) error {
	if dialog.Accept == "" && dialog.EscapeHint == "" {
		return fmt.Errorf("spawn: native terminal %s shows %s, which a spawn never answers; its screen ends:\n%s", record.ID, dialog.Name, host.ScreenTail(screen, 8))
	}
	// Codex 0.154 drew its hook review before it read keys: a Down sent the
	// moment the review showed was lost, and the focus never moved. The first
	// key waits until the dialog has shown for a moment.
	if err := s.sleep(ctx, nativeDialogSettle); err != nil {
		return err
	}
	screen, err := s.readNativeScreen(ctx, record)
	if err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	if !dialog.Shows(screen) {
		return nil
	}
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("spawn: type into native terminal %s: %w", record.ID, err)
	}
	defer client.Close()
	if dialog.EscapeHint != "" {
		if err := client.Input([]byte("\x1b")); err != nil {
			return fmt.Errorf("spawn: dismiss %s in native terminal %s: %w", dialog.Name, record.ID, err)
		}
		if _, err := s.awaitScreen(ctx, record, nativeKeyEffect, func(screen []string) bool { return !dialog.Shows(screen) }); err != nil {
			return fmt.Errorf("spawn: native terminal %s still shows %s after Escape: %w", record.ID, dialog.Name, err)
		}
		return nil
	}
	if _, ok := dialog.Focused(screen); !ok {
		screen, err = s.awaitScreen(ctx, record, nativeKeyEffect, func(screen []string) bool {
			_, ok := dialog.Focused(screen)
			return ok || !dialog.Shows(screen)
		})
		if err != nil {
			return fmt.Errorf("spawn: native terminal %s shows %s, but not which option has the focus: %w", record.ID, dialog.Name, err)
		}
	}
	for moves := 0; dialog.Shows(screen); moves++ {
		focused, _ := dialog.Focused(screen)
		if dialog.Chosen(focused) {
			if err := client.Input([]byte("\r")); err != nil {
				return fmt.Errorf("spawn: answer %s in native terminal %s: %w", dialog.Name, record.ID, err)
			}
			if _, err := s.awaitScreen(ctx, record, nativeKeyEffect, func(screen []string) bool { return !dialog.Shows(screen) }); err != nil {
				return fmt.Errorf("spawn: native terminal %s still shows %s after %q was chosen: %w", record.ID, dialog.Name, dialog.Accept, err)
			}
			return nil
		}
		if moves == maxDialogMoves {
			return fmt.Errorf("spawn: the focus in native terminal %s never reached %q in %s", record.ID, dialog.Accept, dialog.Name)
		}
		if err := client.Input([]byte("\x1b[B")); err != nil {
			return fmt.Errorf("spawn: move the focus in native terminal %s: %w", record.ID, err)
		}
		screen, err = s.awaitScreen(ctx, record, nativeKeyEffect, func(screen []string) bool {
			next, ok := dialog.Focused(screen)
			return !dialog.Shows(screen) || (ok && next != focused)
		})
		if err != nil {
			return fmt.Errorf("spawn: the focus in native terminal %s did not move from %q: %w", record.ID, focused, err)
		}
	}
	return nil
}

// deliverNativeInstruction submits the instruction to the harness of task
// meta and returns once the harness is proven to have taken it. A harness
// idle at its composer takes it as the prompt of a new turn: its native hooks
// report a prompt taken since the submit, or its screen shows it working when
// it was not working before. A harness already in a turn queues it and hands
// it to its model at its next tool call, never interrupting the call that
// runs, and runs its prompt hook as it queues it, so only its own record of
// the conversation proves it taken; with none soon after the submit the
// error says it is queued for that tool call. Before submission, startup may
// recover a recognized dialog and reuse a complete draft or type into a
// proved-empty composer. A submitted instruction is never typed again. A
// Codex has its submit key pressed once, and a Claude Code once more only
// once its composer shows that key taken as a new line: either can take the
// key late, the text still showing in its composer, and a key pressed again
// then would reach it after the instruction, as an empty submission. A
// harness that turns it away as still starting, putting it back in its
// composer, has not taken it: while its screen says so, and did not already
// say so before the submit, Enter is pressed again until it does, for as long
// as a harness's startup is given, and its time to show itself working counts
// from the last press.
func (s Service) deliverNativeInstruction(ctx context.Context, record host.Record, screens harness.Screens, meta state.TaskMeta, session, instruction string, isStartup bool) error {
	recoveries := 0
	recoverStartup := func() error {
		if !isStartup || recoveries == 3 {
			return fmt.Errorf("spawn: native terminal %s: %w", record.ID, errNativeStartupDialog)
		}
		recoveries++
		ctx, cancel := context.WithTimeout(ctx, nativeStartup)
		defer cancel()
		untrusted, _, err := s.awaitNativeReady(ctx, record, screens)
		if err != nil {
			return err
		}
		if untrusted != "" {
			return s.reportUntrusted(meta.ID, harness.Kind(meta.Harness), meta.Worktree, untrusted)
		}
		return nil
	}
	var before, atPress []string
	var busy bool
	var submitted time.Time
	for {
		var err error
		before, err = s.readNativeScreen(ctx, record)
		if err != nil {
			return fmt.Errorf("%w: %w", fleet.ErrNotTyped, err)
		}
		busy = screens.IsWorking(before)
		submitted = time.Now()
		atPress, err = s.submitNative(ctx, record, screens, instruction, launchSettle, recoveries > 0)
		if err == nil {
			break
		}
		if !isStartup || !errors.Is(err, errNativeStartupDialog) {
			return err
		}
		if err := recoverStartup(); err != nil {
			return err
		}
	}
	within := nativeAccepted
	if busy {
		within = nativeQueuedProof
	}
	deadline := time.Now().Add(within)
	pressed, presses := time.Now(), 0
	var startingSince, startingPressed time.Time
	for {
		if busy && s.Took != nil && s.Took(ctx, meta, session, instruction, submitted) {
			return nil
		}
		if !busy && s.PromptSince != nil {
			if taken, err := s.PromptSince(record.ID, meta.SpawnGen, submitted); err == nil && taken {
				return nil
			}
		}
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return err
		}
		current := withoutInlineOffer(screens)
		if _, hasDialog := current.Dialog(screen); hasDialog {
			if err := recoverStartup(); err != nil {
				return err
			}
			// Enter already reached the harness. Dismiss the startup prompt,
			// then await that submission without typing or submitting it again.
			deadline = time.Now().Add(within)
			continue
		}
		if !busy && screens.IsWorking(screen) {
			return nil
		}
		if !busy && !screens.IsStarting(before) && screens.IsStarting(screen) && !current.ComposerEmpty(screen) {
			if startingSince.IsZero() {
				startingSince = time.Now()
			}
			if time.Since(startingSince) > nativeStartup {
				return fmt.Errorf("spawn: native terminal %s was still starting %s after its harness first turned the instruction away; its screen ends:\n%s", record.ID, nativeStartup, host.ScreenTail(screen, 8))
			}
			if time.Since(startingPressed) >= nativeStartingPress {
				if err := pressNativeEnter(record); err != nil {
					return err
				}
				startingPressed = time.Now()
				deadline = startingPressed.Add(within)
			}
		}
		// A harness that took the last Enter as a new line leaves the text in
		// its composer over the row that Enter added to the screen as it was
		// pressed: Enter is pressed again, as often as the harness's screens
		// allow and further apart each time, while the text shows over that
		// row and no turn has started. Text that shows over no new row may
		// wait on an Enter the harness has not taken yet, so it gets none
		// that would reach it after the instruction.
		if !busy && presses < screens.Resubmits && time.Since(pressed) >= time.Duration(presses+1)*time.Second && screens.Shows(screen, instruction) && screens.TookEnterAsNewLine(atPress, screen) {
			if err := pressNativeEnter(record); err != nil {
				return err
			}
			atPress, pressed, presses = screen, time.Now(), presses+1
		}
		if time.Now().After(deadline) {
			if busy {
				return fmt.Errorf("spawn: native terminal %s has the text submitted: %w", record.ID, fleet.ErrQueuedForToolCall)
			}
			return fmt.Errorf("spawn: native terminal %s never showed its harness working on the instruction: not within %s; its screen ends:\n%s", record.ID, within, host.ScreenTail(screen, 8))
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return err
		}
	}
}

// pressNativeEnter presses Enter in native terminal record.
func pressNativeEnter(record host.Record) error {
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("spawn: press Enter in native terminal %s: %w", record.ID, err)
	}
	defer client.Close()
	if err := client.Input([]byte("\r")); err != nil {
		return fmt.Errorf("spawn: press Enter in native terminal %s: %w", record.ID, err)
	}
	return nil
}

// submitNative types the instruction into the harness's composer and submits
// it settle after the composer shows it, since a dialog that opened meanwhile
// would take the Enter as its answer. An inline offer shown over a composer
// that is not proved to hold text is dismissed before anything is typed, and
// again before the Enter. It returns the screen as the Enter was pressed.
func (s Service) submitNative(ctx context.Context, record host.Record, screens harness.Screens, instruction string, settle time.Duration, isRecovering bool) ([]string, error) {
	offers := screens
	screens = withoutInlineOffer(screens)
	checked := func(screen []string) ([]string, error) {
		if _, hasDialog := screens.Dialog(screen); hasDialog {
			return nil, fmt.Errorf("%w: %w", fleet.ErrNotTyped, errNativeStartupDialog)
		}
		return screen, nil
	}
	read := func() ([]string, error) {
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", fleet.ErrNotTyped, err)
		}
		return checked(screen)
	}
	before, dismissed, err := s.dismissOffer(ctx, record, offers)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", fleet.ErrNotTyped, err)
	}
	if before, err = checked(before); err != nil {
		return nil, err
	}
	if dismissed {
		if err := s.sleep(ctx, nativePoll); err != nil {
			return nil, err
		}
		if before, err = read(); err != nil {
			return nil, err
		}
	}
	if screens.IsOpen(before) {
		return nil, fmt.Errorf("%w: native terminal %s is waiting on its own screen", fleet.ErrNotTyped, record.ID)
	}
	shouldType := true
	if isRecovering && !screens.ComposerEmpty(before) {
		if !screens.ComposerMatches(before, instruction) {
			return nil, fmt.Errorf("spawn: native terminal %s holds an incomplete or unreadable draft after its startup dialog, so nothing more was typed or submitted", record.ID)
		}
		shouldType = false
	}
	client, err := host.Dial(record)
	if err != nil {
		return nil, fmt.Errorf("spawn: type into native terminal %s: %w: %w", record.ID, fleet.ErrNotTyped, err)
	}
	defer client.Close()
	if shouldType {
		if err := client.Input([]byte(instruction)); err != nil {
			return nil, fmt.Errorf("spawn: type the instruction into native terminal %s: %w", record.ID, err)
		}
	}
	if err := s.awaitTyped(ctx, client, record, screens, instruction); err != nil {
		return nil, fmt.Errorf("spawn: the instruction typed into native terminal %s never showed in its composer, so it was not submitted: %w", record.ID, err)
	}
	if err := s.sleep(ctx, settle); err != nil {
		return nil, err
	}
	atPress, dismissed, err := s.dismissOffer(ctx, record, offers)
	if err != nil {
		return nil, err
	}
	if dismissed {
		if err := s.sleep(ctx, nativePoll); err != nil {
			return nil, err
		}
		if atPress, err = s.readNativeScreen(ctx, record); err != nil {
			return nil, err
		}
	}
	if _, hasDialog := screens.Dialog(atPress); hasDialog {
		return nil, errNativeStartupDialog
	}
	if !screens.Shows(atPress, instruction) {
		return nil, fmt.Errorf("spawn: native terminal %s no longer shows the instruction, so it was not submitted", record.ID)
	}
	submit := "\r"
	if screens.PasteTakesEnter && !fleet.IsCommand(instruction) {
		// End flushes Codex's paste burst so Enter submits instead of adding a newline.
		submit = "\x1b[F\r"
	}
	if err := client.Input([]byte(submit)); err != nil {
		return nil, fmt.Errorf("spawn: submit the instruction in native terminal %s: %w", record.ID, err)
	}
	return atPress, nil
}

// awaitTyped reads the terminal's screen until it shows typed, for at most
// typedWait. A harness that can take typed text in slowly is given
// nativeKeyEffect more each time its screen changes while the text is still
// arriving, up to nativeTypedCap: live after the 2026-09-29 reboot a resumed
// Codex took a pointer line in at about a character a second. Such a harness,
// which can also hold typed text undrawn until its next redraw, is made to
// redraw while the text does not show: every
// nativeRedrawNudge the terminal is widened by one column for a poll and set
// back, which a harness takes as a resize and never as input.
func (s Service) awaitTyped(ctx context.Context, client *host.Client, record host.Record, screens harness.Screens, typed string) error {
	within := typedWait(screens, typed)
	began := time.Now()
	deadline := began.Add(within)
	nudged := began
	var last []string
	for {
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return err
		}
		if _, hasDialog := screens.Dialog(screen); hasDialog {
			return errNativeStartupDialog
		}
		if screens.Shows(screen, typed) {
			return nil
		}
		if screens.Undrawn && last != nil && !slices.Equal(screen, last) {
			if extended := time.Now().Add(nativeKeyEffect); extended.After(deadline) {
				deadline = extended
			}
			if limit := began.Add(nativeTypedCap); deadline.After(limit) {
				deadline = limit
			}
		}
		last = screen
		if time.Now().After(deadline) {
			return fmt.Errorf("not within %s, nor %s after its screen last changed; its screen ends:\n%s", within, nativeKeyEffect, host.ScreenTail(screen, 8))
		}
		if screens.Undrawn && time.Since(nudged) >= nativeRedrawNudge {
			if err := s.nudgeRedraw(ctx, client, screen); err != nil {
				return fmt.Errorf("resize native terminal %s so it redraws: %w", record.ID, err)
			}
			nudged = time.Now()
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return err
		}
	}
}

// typedWait is how long typed takes to show in the harness's composer: a key's
// effect, and for a harness that can take typed text in slowly, as an idle
// Codex 0.154 took a 2,940-character brief at about 17 characters a second,
// nativeTypedPace for each character on top.
func typedWait(screens harness.Screens, typed string) time.Duration {
	if !screens.Undrawn {
		return nativeKeyEffect
	}
	return nativeKeyEffect + time.Duration(utf8.RuneCountInString(typed))*nativeTypedPace
}

// nudgeRedraw widens the terminal by one column for a poll, long enough for
// its program to redraw at that width, and sets it back to the size screen
// shows: as many rows as it has and as many columns as its widest row.
func (s Service) nudgeRedraw(ctx context.Context, client *host.Client, screen []string) error {
	cols := 0
	for _, row := range screen {
		cols = max(cols, utf8.RuneCountInString(row))
	}
	if cols < 2 || len(screen) < 2 {
		return nil
	}
	if err := client.Resize(cols+1, len(screen)); err != nil {
		return err
	}
	if err := s.sleep(ctx, nativePoll); err != nil {
		return err
	}
	return client.Resize(cols, len(screen))
}

// awaitScreen reads the terminal's screen until done holds, for at most
// within, and returns the screen it last read.
func (s Service) awaitScreen(ctx context.Context, record host.Record, within time.Duration, done func([]string) bool) ([]string, error) {
	deadline := time.Now().Add(within)
	for {
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return nil, err
		}
		if done(screen) {
			return screen, nil
		}
		if time.Now().After(deadline) {
			return screen, fmt.Errorf("not within %s; its screen ends:\n%s", within, host.ScreenTail(screen, 8))
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return screen, err
		}
	}
}

// readNativeScreen reads the terminal's screen, reading again while reads fail
// for at most nativeReadGrace in a row. A failed read never stands for a
// screen: nothing is typed until one succeeds.
func (s Service) readNativeScreen(ctx context.Context, record host.Record) ([]string, error) {
	read := s.ReadScreen
	if read == nil {
		read = host.ReadScreen
	}
	var failing time.Time
	for {
		screen, err := read(record)
		if err == nil {
			return screen, nil
		}
		if failing.IsZero() {
			failing = time.Now()
		} else if time.Since(failing) > nativeReadGrace {
			return nil, err
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return nil, err
		}
	}
}

// nativeProgram is the command line a native terminal starts the harness with:
// the program this process's PATH finds, where the spawn validated it, started
// by its path, so the terminal, whose PATH is the user's, never starts another
// (#1075). A harness installed as a script shim names it as its Executable and
// runs through cmd /c. Any other must be a program: one found only as a script
// shim is refused.
func nativeProgram(kind harness.Kind, launch harness.Launch) ([]string, error) {
	path, err := exec.LookPath(cmp.Or(launch.Executable, string(kind)))
	if err != nil {
		return nil, fmt.Errorf("spawn: find %s: %w", kind, err)
	}
	if launch.Executable != "" {
		return cmdProgram(path, launch.Args...)
	}
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return nil, fmt.Errorf("spawn: %s resolves to %s, which a native terminal cannot start without a shell", kind, path)
	}
	return append([]string{path}, launch.Args...), nil
}

// NativeProgram is the command line a native terminal starts name with,
// followed by args: the program this process's PATH finds, started by its
// path, so a terminal or command whose PATH is the user's, such as the CFO's,
// never starts another (#1155), and a name this PATH does not find is
// refused. CreateProcess runs only executables, so a .exe runs as itself, and
// anything else, such as the npm .cmd shims codex and pi install, runs
// through cmd /c.
func NativeProgram(name string, args ...string) ([]string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s is not on PATH: %w", name, err)
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return append([]string{path}, args...), nil
	}
	return cmdProgram(path, args...)
}

// cmdProgram runs the script at path with args through cmd /c, which exits
// with it. cmd reads its command line itself, so an argument it would
// interpret is refused. A path with a space in it is quoted on that line, and
// once another argument is quoted too, cmd drops the path's opening quote and
// runs the path's first word instead: such a path runs by its short name, and
// one on a volume that keeps no short names is refused.
func cmdProgram(path string, args ...string) ([]string, error) {
	if strings.ContainsAny(path, " \t") {
		long, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, fmt.Errorf("spawn: %s: %w", path, err)
		}
		buffer := make([]uint16, windows.MAX_LONG_PATH)
		length, err := windows.GetShortPathName(long, &buffer[0], uint32(len(buffer)))
		if err != nil {
			return nil, fmt.Errorf("spawn: read the short name of %s: %w", path, err)
		}
		short := windows.UTF16ToString(buffer[:length])
		if strings.ContainsAny(short, " \t") {
			return nil, fmt.Errorf("spawn: %s has a space in its path and no short name without one, so cmd /c cannot start it by its path", path)
		}
		path = short
	}
	for _, arg := range append([]string{path}, args...) {
		if arg == "" || strings.ContainsAny(arg, "\"&|<>^%!()\r\n") {
			return nil, fmt.Errorf("spawn: %s's argument %q is one cmd would read as more than text", path, arg)
		}
	}
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	return append([]string{shell, "/c", path}, args...), nil
}

// inheritedSessionVariables name what a session marks its own processes with
// and a goblin or the CFO must never start with: the harness that runs the
// CFO marks its session (Claude Code treats a process that carries its markers
// as a child session, which neither saves a transcript nor may start inside
// another), and a Herdr pane names itself to the hooks that report into it.
// The user's environment should hold none of them; they are dropped from it
// all the same.
var inheritedSessionVariables = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_EXECPATH", "CLAUDE_PID", "CODEX_THREAD_ID", "CODEX_SANDBOX", "CODEX_SANDBOX_", "HERDR_", "CFO_SESSION_ID", "CFO_SESSION_HARNESS", host.IDVariable, host.ProofVariable}

// nativeHostEnvironment is the whole environment a native task's host and
// harness run with: userEnv, the environment Windows gives a new process of
// this user, never the spawning process's own, so nothing of the session that
// ran cfo spawn reaches the goblin; without the harness billing keys or any
// session marker, and without any variable of a service the task does not
// carry, which the user's environment may set too; then the credentials of the
// services the task carries, then the launch's variables (CFO_ROLE=goblin and
// the task's identity among them) and the spawning home's CFO_HOME and
// CFO_STATE_OVERRIDE, and the projects root the spawner names, where it names
// one, which win: the user's environment names the machine's installed home
// and projects root, which for a second home on the machine are another's.
// This block is how its credentials reach the harness at start, and the only
// way they do. A task's PATH begins with its codex guard folder, then the
// guard folders the spawning process runs under, the one thing of its own
// environment a terminal starts with: a rig in a goblin's terminal that
// spawns a scratch home's goblin keeps the goblin's guard (codexguard).
// Names compare without case, as Windows compares them.
func (s Service) nativeHostEnvironment(userEnv []string, launch harness.Launch, credentials auth.Result) []string {
	names := map[string]string{}
	values := map[string]string{}
	set := func(name, value string) {
		names[strings.ToUpper(name)] = name
		values[strings.ToUpper(name)] = value
	}
	withheld := map[string]bool{}
	for _, name := range credentials.WithheldNames {
		withheld[strings.ToUpper(name)] = true
	}
	for _, entry := range userEnv {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || auth.IsHarnessBillingKey(name) || IsSessionMarker(name) || withheld[strings.ToUpper(name)] {
			continue
		}
		set(name, value)
	}
	for name, value := range credentials.Env {
		if reservedLaunchName(launch.Env, name) || auth.IsHarnessBillingKey(name) {
			continue
		}
		set(name, value)
	}
	for name, value := range launch.Env {
		set(name, value)
	}
	set("CFO_HOME", s.HomeRoot)
	set("CFO_STATE_OVERRIDE", s.StateDir)
	if s.ProjectsRoot != "" {
		set(install.ProjectsRootVariable, s.ProjectsRoot)
	}
	own := ""
	if id := launch.Env["CFO_TASK_ID"]; id != "" {
		own = codexguard.Dir(s.StateDir, id)
	}
	if guards := codexguard.Chain(own, os.Getenv(codexguard.Variable)); guards != "" {
		set(codexguard.Variable, guards)
		path := guards
		if userPath := values["PATH"]; userPath != "" {
			path += string(os.PathListSeparator) + userPath
		}
		set(cmp.Or(names["PATH"], "PATH"), path)
	}
	env := make([]string, 0, len(values))
	for upper, value := range values {
		env = append(env, names[upper]+"="+value)
	}
	sort.Strings(env)
	return remoteaccess.LocalScrawlEnvironment(env)
}

// hasNativeVariable reports whether env, a native host environment, sets name
// to a value. Names compare without case, as Windows compares them.
func hasNativeVariable(env []string, name string) bool {
	for _, entry := range env {
		if entryName, value, _ := strings.Cut(entry, "="); strings.EqualFold(entryName, name) {
			return value != ""
		}
	}
	return false
}

func (s Service) userEnvironment() ([]string, error) {
	if s.UserEnvironment != nil {
		return s.UserEnvironment()
	}
	return UserEnvironment()
}

// UserEnvironment is the environment Windows gives a new process of this
// user: the user's and the machine's configured variables, and nothing this
// process inherited from whatever runs it.
func UserEnvironment() ([]string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	return token.Environ(false)
}

// IsSessionMarker reports whether name is one of the variables a session
// marks its own processes with, which a harness the fleet starts must never
// inherit.
func IsSessionMarker(name string) bool {
	upper := strings.ToUpper(name)
	for _, inherited := range inheritedSessionVariables {
		if upper == inherited || (strings.HasSuffix(inherited, "_") && strings.HasPrefix(upper, inherited)) {
			return true
		}
	}
	return false
}

// containedNotice warns, for a native terminal whose host could not leave the
// job of the process that launched it, that the terminal ends when that job
// closes; it is empty for a host that left.
func containedNotice(record host.Record) string {
	if !record.Contained {
		return ""
	}
	return "warning: native terminal " + record.ID + " could not leave the job of the process that ran cfo, so it ends when that job closes (see its host log)"
}
