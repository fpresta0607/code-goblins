package spawn

import (
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
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A native terminal starts at the size goblins --native starts the CFO at.
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
	// a turn waits for a hook to report it taken, in case the turn was ending.
	nativeQueuedProof = 5 * time.Second
)

// maxDialogMoves bounds the focus moves one dialog takes.
const maxDialogMoves = 8

// startNativeHarness starts the harness in a native terminal of its own, the
// task's id, and delivers its instruction. It reads the terminal's screen
// throughout: it answers a startup dialog only once it recognizes it, types
// the instruction only at the harness's composer, and submits it only once the
// composer shows it. It returns the record of the host it launched, even when
// it fails afterwards, and the zero record when it launched none.
func (s Service) startNativeHarness(ctx context.Context, id string, kind harness.Kind, launch harness.Launch, userEnv []string, credentials map[string]string) (host.Record, error) {
	screens, ok := harness.NativeScreens(kind)
	if !ok {
		return host.Record{}, fmt.Errorf("spawn: %s cannot run in a native terminal yet", kind)
	}
	program, err := nativeProgram(kind, launch)
	if err != nil {
		return host.Record{}, err
	}
	if len(s.HostCommand) == 0 {
		return host.Record{}, errors.New("spawn: the command that runs a native terminal's host is required")
	}
	record, err := host.Launch(s.StateDir, s.HostCommand, s.nativeHostEnvironment(userEnv, launch, credentials), host.Spec{ID: id, Args: program, Dir: launch.Dir, Cols: nativeCols, Rows: nativeRows})
	if err != nil {
		return host.Record{}, fmt.Errorf("spawn: start native terminal %s: %w", id, err)
	}
	untrusted, err := s.awaitNativeReady(ctx, record, screens)
	if err != nil {
		return record, err
	}
	if untrusted != "" {
		if err := s.reportUntrusted(id, kind, launch.Dir, untrusted); err != nil {
			return record, err
		}
	}
	instruction, err := s.typedInstruction(id, screens, launch.PromptInstruction())
	if err != nil {
		return record, err
	}
	return record, s.deliverNativeInstruction(ctx, record, screens, instruction, launch.Env["CFO_SPAWN_GEN"])
}

// typedInstructionLimit is the longest instruction typed whole into a harness
// that takes typed text in slowly.
const typedInstructionLimit = 400

// typedInstruction is what is typed to deliver instruction to the harness in
// native terminal id: the instruction itself, or, for a harness that takes
// typed text in slowly and an instruction longer than typedInstructionLimit,
// a line pointing at the task's instruction.md, where the whole instruction is
// written. Live after the 2026-09-29 reboot an idle Codex 0.154 took a
// 2,940-character brief in at about 17 characters a second, and took an Enter
// pressed while the rest still arrived as part of the text.
func (s Service) typedInstruction(id string, screens harness.Screens, instruction string) (string, error) {
	if !screens.Undrawn || utf8.RuneCountInString(instruction) <= typedInstructionLimit {
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
// of the composer went into the review. It returns what a dialog answered
// without trust left untrusted, in the dialog's own words, or nothing.
func (s Service) awaitNativeReady(ctx context.Context, record host.Record, screens harness.Screens) (string, error) {
	deadline := time.Now().Add(nativeStartup)
	var untrusted string
	ready := 0
	for {
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return "", fmt.Errorf("spawn: %w", err)
		}
		if dialog, found := screens.Dialog(screen); found {
			if dialog.Summary != nil {
				if untrusted = dialog.Summary.FindString(strings.Join(screen, "\n")); untrusted == "" {
					untrusted = dialog.Name
				}
			}
			if err := s.answerDialog(ctx, record, dialog, screen); err != nil {
				return "", err
			}
			ready = 0
			continue
		}
		if !screens.IsReady(screen) {
			ready = 0
		} else if ready++; ready > readySettleReads() {
			return untrusted, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("spawn: native terminal %s showed neither a dialog it knows nor its harness's composer within %s; its screen ends:\n%s", record.ID, nativeStartup, host.ScreenTail(screen, 8))
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return "", err
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
// again until its focus shows. A dialog no spawn may answer stops the spawn.
func (s Service) answerDialog(ctx context.Context, record host.Record, dialog harness.Dialog, screen []string) error {
	if dialog.Accept == "" {
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
	if _, ok := dialog.Focused(screen); !ok {
		screen, err = s.awaitScreen(ctx, record, nativeKeyEffect, func(screen []string) bool {
			_, ok := dialog.Focused(screen)
			return ok || !dialog.Shows(screen)
		})
		if err != nil {
			return fmt.Errorf("spawn: native terminal %s shows %s, but not which option has the focus: %w", record.ID, dialog.Name, err)
		}
	}
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("spawn: type into native terminal %s: %w", record.ID, err)
	}
	defer client.Close()
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

// deliverNativeInstruction submits the instruction to the harness of
// generation generation and returns once the harness is proven to have taken
// it: its native hooks report a prompt taken since the submit, or its screen
// shows it working when it was not working before. Typed into a harness
// already in a turn, the text waits in its composer for that turn to end, so
// with no hook report soon after the submit it is not proven taken and the
// error says it waits behind the turn.
func (s Service) deliverNativeInstruction(ctx context.Context, record host.Record, screens harness.Screens, instruction, generation string) error {
	before, err := s.readNativeScreen(ctx, record)
	if err != nil {
		return err
	}
	busy := screens.IsWorking(before)
	submitted := time.Now()
	if err := s.submitNative(ctx, record, screens, instruction, launchSettle); err != nil {
		return err
	}
	within := nativeAccepted
	if busy {
		within = nativeQueuedProof
	}
	deadline := time.Now().Add(within)
	pressed, presses := time.Now(), 0
	for {
		if s.PromptSince != nil {
			if taken, err := s.PromptSince(record.ID, generation, submitted); err == nil && taken {
				return nil
			}
		}
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return err
		}
		if !busy && screens.IsWorking(screen) {
			return nil
		}
		// A harness that takes the Enter ending a paste as part of it leaves
		// the text in its composer: Enter is pressed again while the text
		// still shows and no turn has started, further apart each time. An
		// Enter on an empty composer submits nothing, so the text is never
		// handed over twice.
		if !busy && screens.PasteTakesEnter && presses < submitRetries && time.Since(pressed) >= time.Duration(presses+1)*time.Second && screens.Shows(screen, instruction) {
			if err := pressNativeEnter(record); err != nil {
				return err
			}
			pressed, presses = time.Now(), presses+1
		}
		if time.Now().After(deadline) {
			if busy {
				return fmt.Errorf("spawn: native terminal %s took the text while its harness was in a turn, and no hook reported the harness taking it within %s: %w", record.ID, within, fleet.ErrQueuedBehindTurn)
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
// would take the Enter as its answer.
func (s Service) submitNative(ctx context.Context, record host.Record, screens harness.Screens, instruction string, settle time.Duration) error {
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("spawn: type into native terminal %s: %w", record.ID, err)
	}
	defer client.Close()
	if err := client.Input([]byte(instruction)); err != nil {
		return fmt.Errorf("spawn: type the instruction into native terminal %s: %w", record.ID, err)
	}
	if err := s.awaitTyped(ctx, client, record, screens, instruction); err != nil {
		return fmt.Errorf("spawn: the instruction typed into native terminal %s never showed in its composer, so it was not submitted: %w", record.ID, err)
	}
	if err := s.sleep(ctx, settle); err != nil {
		return err
	}
	if err := client.Input([]byte("\r")); err != nil {
		return fmt.Errorf("spawn: submit the instruction in native terminal %s: %w", record.ID, err)
	}
	return nil
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

// nativeProgram is the command line a native terminal starts the harness with.
// A harness installed as a script shim names it as its Executable and runs
// through cmd /c, which finds it in the user environment the host runs with.
// Any other must be a program: one found only as a script shim is refused.
func nativeProgram(kind harness.Kind, launch harness.Launch) ([]string, error) {
	if launch.Executable != "" {
		return cmdProgram(launch.Executable, launch.Args...)
	}
	path, err := exec.LookPath(string(kind))
	if err != nil {
		return nil, fmt.Errorf("spawn: find %s: %w", kind, err)
	}
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return nil, fmt.Errorf("spawn: %s resolves to %s, which a native terminal cannot start without a shell", kind, path)
	}
	return append([]string{path}, launch.Args...), nil
}

// NativeProgram is the command line a native terminal starts name with,
// followed by args. CreateProcess runs only executables, so a .exe found on
// PATH runs as itself, and anything else, such as the npm .cmd shims codex and
// pi install, runs through cmd /c.
func NativeProgram(name string, args ...string) ([]string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s is not on PATH: %w", name, err)
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return append([]string{path}, args...), nil
	}
	return cmdProgram(name, args...)
}

// cmdProgram runs name with args through cmd /c, which exits with it. cmd
// reads its command line itself, so an argument it would interpret is
// refused.
func cmdProgram(name string, args ...string) ([]string, error) {
	for _, arg := range append([]string{name}, args...) {
		if arg == "" || strings.ContainsAny(arg, "\"&|<>^%!()\r\n") {
			return nil, fmt.Errorf("spawn: %s's argument %q is one cmd would read as more than text", name, arg)
		}
	}
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	return append([]string{shell, "/c", name}, args...), nil
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
// session marker; then the project's credentials, then the launch's variables
// (CFO_ROLE=goblin and the task's identity among them) and CFO_STATE_OVERRIDE,
// which win. This block is how its credentials reach the harness at start.
// Names compare without case, as Windows compares them.
func (s Service) nativeHostEnvironment(userEnv []string, launch harness.Launch, credentials map[string]string) []string {
	names := map[string]string{}
	values := map[string]string{}
	set := func(name, value string) {
		names[strings.ToUpper(name)] = name
		values[strings.ToUpper(name)] = value
	}
	for _, entry := range userEnv {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || auth.IsHarnessBillingKey(name) || IsSessionMarker(name) {
			continue
		}
		set(name, value)
	}
	for name, value := range credentials {
		if reservedLaunchName(launch.Env, name) || auth.IsHarnessBillingKey(name) {
			continue
		}
		set(name, value)
	}
	for name, value := range launch.Env {
		set(name, value)
	}
	set("CFO_STATE_OVERRIDE", s.StateDir)
	env := make([]string, 0, len(values))
	for upper, value := range values {
		env = append(env, names[upper]+"="+value)
	}
	sort.Strings(env)
	return env
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
