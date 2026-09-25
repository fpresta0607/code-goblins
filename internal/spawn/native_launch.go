package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
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
)

// maxDialogMoves bounds the focus moves one dialog takes.
const maxDialogMoves = 8

// startNativeHarness starts the harness in a native terminal of its own, the
// task's id, and delivers its instruction. It reads the terminal's screen
// throughout: it answers a startup dialog only once it recognizes it, types
// the instruction only at the harness's composer, and submits it only once the
// composer shows it. It returns the record of the host it launched, even when
// it fails afterwards, and the zero record when it launched none.
func (s Service) startNativeHarness(ctx context.Context, id string, kind harness.Kind, launch harness.Launch, credentials map[string]string) (host.Record, error) {
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
	env := s.nativeHostEnvironment(launch, credentials)
	record, err := host.Launch(s.StateDir, s.HostCommand, env, host.Spec{ID: id, Args: program, Dir: launch.Dir, Cols: nativeCols, Rows: nativeRows})
	if err != nil {
		return host.Record{}, fmt.Errorf("spawn: start native terminal %s: %w", id, err)
	}
	if err := s.awaitNativeReady(ctx, record, screens); err != nil {
		return record, err
	}
	return record, s.deliverNativeInstruction(ctx, record, screens, launch.PromptInstruction())
}

// awaitNativeReady reads the terminal's screen until the harness's composer
// waits for input, answering each startup dialog it recognizes on the way.
// Only a screen read successfully counts, so seeing no dialog can only come
// from a screen that shows none.
func (s Service) awaitNativeReady(ctx context.Context, record host.Record, screens harness.Screens) error {
	deadline := time.Now().Add(nativeStartup)
	for {
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return fmt.Errorf("spawn: %w", err)
		}
		if dialog, found := screens.Dialog(screen); found {
			if err := s.answerDialog(ctx, record, dialog, screen); err != nil {
				return err
			}
			continue
		}
		if screens.IsReady(screen) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("spawn: native terminal %s showed neither a dialog it knows nor its harness's composer within %s; its screen ends:\n%s", record.ID, nativeStartup, host.ScreenTail(screen, 8))
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return err
		}
	}
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
	if _, ok := dialog.Focused(screen); !ok {
		var err error
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

// deliverNativeInstruction types the instruction into the harness's composer
// and submits it once the composer shows it, since a dialog that opened
// meanwhile would take the Enter as its answer. It returns once the harness
// shows it working on the instruction, the native form of the Herdr path's
// proof that the agent accepted its prompt.
func (s Service) deliverNativeInstruction(ctx context.Context, record host.Record, screens harness.Screens, instruction string) error {
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("spawn: type into native terminal %s: %w", record.ID, err)
	}
	defer client.Close()
	if err := client.Input([]byte(instruction)); err != nil {
		return fmt.Errorf("spawn: type the instruction into native terminal %s: %w", record.ID, err)
	}
	if _, err := s.awaitScreen(ctx, record, nativeKeyEffect, func(screen []string) bool { return screens.Shows(screen, instruction) }); err != nil {
		return fmt.Errorf("spawn: the instruction typed into native terminal %s never showed in its composer, so it was not submitted: %w", record.ID, err)
	}
	if err := s.sleep(ctx, launchSettle); err != nil {
		return err
	}
	if err := client.Input([]byte("\r")); err != nil {
		return fmt.Errorf("spawn: submit the instruction in native terminal %s: %w", record.ID, err)
	}
	if _, err := s.awaitScreen(ctx, record, nativeAccepted, screens.IsWorking); err != nil {
		return fmt.Errorf("spawn: native terminal %s never showed its harness working on the instruction: %w", record.ID, err)
	}
	return nil
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
// CreateProcess runs only executables, so the npm .cmd shims codex and pi
// install as run through cmd /c, which exits with the harness. cmd reads its
// command line itself, so an argument it would interpret is refused.
func nativeProgram(kind harness.Kind, launch harness.Launch) ([]string, error) {
	if !launch.TypedLaunch {
		path, err := exec.LookPath(string(kind))
		if err != nil {
			return nil, fmt.Errorf("spawn: find %s: %w", kind, err)
		}
		if !strings.EqualFold(filepath.Ext(path), ".exe") {
			return nil, fmt.Errorf("spawn: %s resolves to %s, which a native terminal cannot start without a shell", kind, path)
		}
		return append([]string{path}, launch.Args...), nil
	}
	for _, arg := range append([]string{launch.Executable}, launch.Args...) {
		if arg == "" || strings.ContainsAny(arg, "\"&|<>^%!()\r\n") {
			return nil, fmt.Errorf("spawn: %s's argument %q is one cmd would read as more than text", kind, arg)
		}
	}
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	return append([]string{shell, "/c", launch.Executable}, launch.Args...), nil
}

// inheritedSessionVariables name what this process inherits from the session
// that runs it and a goblin must not: the harness that runs the CFO marks its
// own session (Claude Code refuses to start inside another), and a Herdr pane
// names itself to the hooks that report into it. Claude Code's own settings,
// such as CLAUDE_CODE_GIT_BASH_PATH, pass on, as they do to a Herdr goblin.
var inheritedSessionVariables = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CODEX_THREAD_ID", "CODEX_SANDBOX", "CODEX_SANDBOX_", "HERDR_", "CFO_SESSION_ID", "CFO_SESSION_HARNESS", host.IDVariable}

// nativeHostEnvironment is the whole environment a native task's host and
// harness run with: this process's own, without the harness billing keys or
// the session variables above, then the project's credentials, then the
// launch's variables and CFO_STATE_OVERRIDE, which win. A native task has no
// credentials script: this block is how its credentials reach the harness.
// Names compare without case, as Windows compares them.
func (s Service) nativeHostEnvironment(launch harness.Launch, credentials map[string]string) []string {
	names := map[string]string{}
	values := map[string]string{}
	set := func(name, value string) {
		names[strings.ToUpper(name)] = name
		values[strings.ToUpper(name)] = value
	}
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || auth.IsHarnessBillingKey(name) || inheritedSession(name) {
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

func inheritedSession(name string) bool {
	upper := strings.ToUpper(name)
	for _, inherited := range inheritedSessionVariables {
		if upper == inherited || (strings.HasSuffix(inherited, "_") && strings.HasPrefix(upper, inherited)) {
			return true
		}
	}
	return false
}

// closeNativeTerminal ends the native terminal whose host launched is, the
// harness and everything it started, and waits for its host to end. It closes
// only that host: a terminal its id names that another host runs is left
// alone. A zero record launched nothing, and a host that has ended has
// nothing left to close; a running host that does not answer, as one under
// load can be busy, is dialed again for at most nativeCloseWait.
func closeNativeTerminal(stateDir string, launched host.Record) error {
	if launched.HostPID == 0 {
		return nil
	}
	recorded := func() (bool, error) {
		record, err := host.ReadRecord(stateDir, launched.ID)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return err == nil && record.HostPID == launched.HostPID, err
	}
	if still, err := recorded(); err != nil || !still {
		return err
	}
	deadline := time.Now().Add(nativeCloseWait)
	client, err := host.Dial(launched)
	for err != nil {
		if !processRunning(launched.HostPID) {
			return nil
		}
		if still, readErr := recorded(); readErr == nil && !still {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the host of native terminal %s, pid %d, does not answer, so its harness may still be running: %w", launched.ID, launched.HostPID, err)
		}
		time.Sleep(100 * time.Millisecond)
		client, err = host.Dial(launched)
	}
	closeErr := client.CloseTerminal()
	_ = client.Close()
	if closeErr != nil {
		return closeErr
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if still, err := recorded(); err == nil && !still {
			return nil
		}
	}
	return fmt.Errorf("the host of native terminal %s did not end", launched.ID)
}

// processRunning reports whether pid may still run: only a process Windows
// shows as ended, or as never started, does not.
func processRunning(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == stillActive
}

// stillActive is the exit code Windows reports for a process that runs.
const stillActive = 259
