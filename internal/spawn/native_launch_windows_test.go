package spawn

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The test binary plays every part of a native spawn: with nativeSpawnHost
// first it is the terminal's host, and copied onto PATH as codex.exe or
// claude.exe it is the harness, which cmd /c or the spawn finds there.
const nativeSpawnHost = "native-spawn-host"

// The fake harness records what it sees to the file fakeCodexRecord names, and
// fakeCodexMode picks what it shows: codex's update prompt, then its trust
// prompt and composer by default, its composer at once ("ready"), or its hook
// review prompt ("hooks"), nothing
// a spawn would recognize ("silent"), or no console at all ("detached"). Half
// drawn ("halfdrawn"), its update prompt shows its header alone at first, and
// no focus for a moment after a move. At its composer, a prompt can open as
// the typing starts ("late"), a submitted line can leave it looking idle
// ("unmoved"), each turn can end a moment after it starts ("turns"), typed
// text can show only once its console is resized ("undrawn"), or the first
// Enter can be taken as part of the text ("swallow"), where
// by default a turn never ends. With "exitmenu", /exit opens the menu Claude
// Code shows over background work before it exits.
const (
	fakeCodexRecord = "SPAWN_TEST_CODEX_RECORD"
	fakeCodexMode   = "SPAWN_TEST_CODEX_MODE"
)

func TestMain(m *testing.M) {
	switch {
	case len(os.Args) > 1 && os.Args[1] == nativeSpawnHost:
		if err := host.RunArgs(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case slices.Contains([]string{"codex", "claude"}, strings.ToLower(strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])))):
		fakeHarness()
	default:
		os.Exit(m.Run())
	}
}

// codexEvent is one line of the fake codex's record.
type codexEvent struct {
	Event string             `json:"event"`
	Text  string             `json:"text,omitempty"`
	PID   int                `json:"pid,omitempty"`
	Env   map[string]*string `json:"env,omitempty"`
}

// recordedEnv is what the fake codex records of its environment.
var recordedEnv = []string{"CFO_TASK_ID", "CFO_ROLE", "GOTMPDIR", "TEMP", "TMP", "CFO_STATE_OVERRIDE", "CFO_HOST_ID", "FIXTURE_TOKEN", "PLAYWRIGHT_BROWSERS_PATH", "LOCALAPPDATA", "XDG_CACHE_HOME", "HOME", "UV_CACHE_DIR", "DATABASE_URL", "OPENAI_API_KEY", "HERDR_PANE_ID", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_GIT_BASH_PATH", "CODEX_SANDBOX_NETWORK_DISABLED", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_PID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "A_SESSION_ONLY_VARIABLE", "USERS_OWN_SETTING"}

// fakeHarness shows codex's own startup screens, as captured on this machine,
// and answers keys the way codex does. It records its environment, every key
// that reaches it before a screen that asks for one, each choice and each
// submitted line.
func fakeHarness() {
	file, err := os.OpenFile(os.Getenv(fakeCodexRecord), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	var mu sync.Mutex
	record := func(event codexEvent) {
		mu.Lock()
		defer mu.Unlock()
		line, _ := json.Marshal(event)
		_, _ = file.Write(append(line, '\n'))
	}
	env := map[string]*string{}
	for _, name := range recordedEnv {
		if value, found := os.LookupEnv(name); found {
			env[name] = &value
		}
	}
	record(codexEvent{Event: "env", PID: os.Getpid(), Env: env, Text: strings.Join(os.Args[1:], " ")})
	mode := os.Getenv(fakeCodexMode)
	if mode == "detached" {
		_, _, _ = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole").Call()
		record(codexEvent{Event: "detached"})
		time.Sleep(time.Minute)
		return
	}
	// Raw input, as codex's TUI reads it: keys arrive as they are typed,
	// unechoed, and the arrows as escape sequences.
	_ = windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	keys := make(chan string, 64)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			var key string
			var err error
			if strings.HasPrefix(mode, "daybreak") {
				// This offer takes a bare Escape, with no arrow sequence.
				var character rune
				character, _, err = reader.ReadRune()
				key = string(character)
			} else {
				key, err = readKey(reader)
			}
			if err != nil {
				close(keys)
				return
			}
			keys <- key
		}
	}()
	draw := func(rows ...string) {
		fmt.Print("\x1b[2J\x1b[H" + strings.Join(rows, "\r\n"))
	}
	if mode == "silent" {
		draw("Starting up, please wait.")
		for key := range keys {
			record(codexEvent{Event: "typed blind", Text: key})
		}
		return
	}
	composer := func(text string) {
		draw("", "› "+text, "", "  ? for shortcuts                                                                    100% context left")
	}
	if mode == "resumed-working" {
		if len(os.Args) > 1 && os.Args[1] == "resume" {
			draw("    +151 lines (ctrl+t to view transcript)", "", "Working (1m 51s • esc to interrupt) · 1 background terminal running · /ps to view · /stop to close", "", "› Ask Codex to do anything", "", "  GPT-6.1-Sol xhigh · "+mustGetwd())
			for key := range keys {
				record(codexEvent{Event: "typed into resumed turn", Text: key})
			}
			return
		}
		mode = "ready"
	}
	if mode != "ready" && !codexStartup(mode, keys, record, draw, composer) {
		return
	}
	composer("Ask Codex to do anything")
	var line strings.Builder
	late, swallowed := false, false
	resized := make(chan struct{}, 1)
	if mode == "undrawn" {
		go watchWidth(resized)
	}
	for {
		var key string
		select {
		case next, open := <-keys:
			if !open {
				return
			}
			key = next
		case <-resized:
			// As an idle Codex 0.154 does, typed text shows only at a redraw.
			if line.Len() > 0 {
				composer(line.String())
			}
			continue
		}
		// A burst of typing is drawn once, as a terminal program does.
		burst := []string{key}
		for more := true; more; {
			select {
			case next, open := <-keys:
				if open {
					burst = append(burst, next)
				}
				more = open
			default:
				more = false
			}
		}
		for _, key := range burst {
			switch {
			case mode == "late":
				// A prompt opens as the typing starts, and takes the keys.
				if key == "\r" {
					record(codexEvent{Event: "entered at a late prompt"})
				}
				late = true
			case key == "\r" && mode == "swallow" && !swallowed:
				// As Codex 0.154 did, the Enter ending a paste is taken as
				// part of it, and the text stays in the composer.
				swallowed = true
				record(codexEvent{Event: "swallowed enter"})
			case key == "\r":
				record(codexEvent{Event: "submitted", Text: line.String()})
				// Like codex, it ends at a submitted /exit.
				if line.String() == "/exit" {
					if mode == "exitmenu" {
						exitOptions := []string{"1. Exit and stop tasks", "2. Move to background and exit", "3. Stay"}
						chosen := choose(keys, record, func(focus int) {
							draw(append([]string{"", "  Background work is running", "  The following will stop when you exit:", "    npm run dev (shell 1)", ""}, focusRows(exitOptions, focus)...)...)
						})
						record(codexEvent{Event: "exit menu", Text: exitOptions[chosen]})
					}
					return
				}
				// Unmoved, codex takes the line, which leaves its composer as a
				// submitted line does, and never shows it working.
				if mode == "unmoved" {
					composer("Ask Codex to do anything")
				} else {
					draw("", "› "+line.String(), "", "• Working (0s • esc to interrupt)")
				}
				line.Reset()
				if mode == "turns" {
					time.Sleep(8 * time.Second)
					composer("Ask Codex to do anything")
				}
			case key == "\x1b[A" || key == "\x1b[B" || key == "\x1b[F":
			default:
				line.WriteString(key)
				if mode == "trickle" {
					// As a resumed Codex 0.154 did, typed text is taken
					// in a character at a time.
					time.Sleep(40 * time.Millisecond)
					composer(line.String())
				}
			}
		}
		switch {
		case late:
			draw("", "  Something needs an answer first. Continue? (y/n)")
		case line.Len() > 0 && mode != "undrawn":
			composer(line.String())
		}
	}
}

// codexStartup shows codex's own startup screens, as captured on this
// machine, and reports whether they ended at its composer.
func codexStartup(mode string, keys <-chan string, record func(codexEvent), draw func(rows ...string), composer func(text string)) bool {
	if strings.HasPrefix(mode, "daybreak") {
		var draft strings.Builder
		offer := func() {
			text := draft.String()
			if text == "" {
				text = "Ask Codex to do anything"
			}
			draw("Set up security for Daybreak mode", "Set up Advanced Account Security with a hardware security key. You can keep using Codex while you finish setup.", "", "› 1. Set up security", "", "Press a number to choose · esc to dismiss · type to continue", "", "› "+text, "100% context left")
		}
		offer()
		for key := range keys {
			switch key {
			case "\x1b":
				record(codexEvent{Event: "security dismissal attempted", Text: key})
				if mode == "daybreak-stuck" {
					continue
				}
				record(codexEvent{Event: "security dismissed", Text: key})
				return true
			case "\r":
				record(codexEvent{Event: "security enrollment", Text: key})
			default:
				draft.WriteString(key)
				record(codexEvent{Event: "typed into security offer", Text: key})
				offer()
			}
		}
		return false
	}
	// Nothing may be typed before a screen asks for a key.
	pause := time.Second
	if mode == "halfdrawn" {
		draw("", "  ✨ Update available! 0.154.0 -> 0.157.0")
		pause = 1500 * time.Millisecond
	}
	time.Sleep(pause)
	for waiting := true; waiting; {
		select {
		case key := <-keys:
			record(codexEvent{Event: "typed blind", Text: key})
		default:
			waiting = false
		}
	}
	options := []string{"1. Update now (runs `npm install -g @openai/codex`)", "2. Skip", "3. Skip until next version"}
	update := func(focus int) {
		rows := []string{"", "  ✨ Update available! 0.154.0 -> 0.157.0", "", "  Release notes: https://github.com/openai/codex/releases/latest", ""}
		rows = append(rows, focusRows(options, focus)...)
		draw(append(rows, "", "  Press enter to continue")...)
	}
	chosen := choose(keys, record, func(focus int) {
		if mode == "halfdrawn" && focus > 0 {
			update(-1)
			time.Sleep(1500 * time.Millisecond)
		}
		update(focus)
	})
	record(codexEvent{Event: "update prompt", Text: options[chosen]})
	if chosen == 0 {
		return false
	}
	// hookReview shows the hook review and reports whether it was answered
	// with Continue without trusting.
	hookReview := func() bool {
		hooks := []string{"1. Review hooks", "2. Trust all and continue", "3. Continue without trusting (hooks won't run)"}
		review := func(focus int) {
			rows := []string{"", "  Hooks need review", "  2 hooks are new or changed.", "  Hooks can run outside the sandbox after you trust them.", ""}
			rows = append(rows, focusRows(hooks, focus)...)
			draw(append(rows, "", "  Press enter to confirm or esc to go back")...)
		}
		if mode == "hooks-deaf" {
			// As Codex 0.154 does, the review shows a moment before keys
			// reach it, and a key sent meanwhile is lost.
			review(0)
			for deaf := time.After(1500 * time.Millisecond); deaf != nil; {
				select {
				case key := <-keys:
					record(codexEvent{Event: "lost", Text: key})
				case <-deaf:
					deaf = nil
				}
			}
		}
		chosen := choose(keys, record, review)
		record(codexEvent{Event: "hook prompt", Text: hooks[chosen]})
		return chosen == 2
	}
	if (mode == "hooks" || mode == "hooks-deaf") && !hookReview() {
		return false
	}
	trust := []string{"1. Yes, continue", "2. No, quit"}
	chosen = choose(keys, record, func(focus int) {
		rows := []string{"> You are in " + mustGetwd(), "", "  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt",
			"  injection. Trusting the directory allows project-local config, hooks, and exec policies to load.", ""}
		rows = append(rows, focusRows(trust, focus)...)
		draw(append(rows, "", "  Press enter to continue")...)
	})
	record(codexEvent{Event: "trust prompt", Text: trust[chosen]})
	if chosen != 0 {
		return false
	}
	if mode == "hooks-late" {
		// As Codex 0.154 does, the composer shows a moment before the hook
		// review is drawn over it, and keys typed meanwhile reach the review.
		composer("Ask Codex to do anything")
		time.Sleep(time.Second)
		if !hookReview() {
			return false
		}
	}
	return true
}

// watchWidth signals resized each time its console's width changes.
func watchWidth(resized chan<- struct{}) {
	out := windows.Handle(os.Stdout.Fd())
	var last int16
	for {
		var info windows.ConsoleScreenBufferInfo
		if windows.GetConsoleScreenBufferInfo(out, &info) == nil {
			width := info.Window.Right - info.Window.Left
			if last != 0 && width != last {
				select {
				case resized <- struct{}{}:
				default:
				}
			}
			last = width
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// choose shows a list with the focus on its first option and moves the focus
// with the arrow keys until Enter chooses the focused option.
func choose(keys <-chan string, record func(codexEvent), show func(focus int)) int {
	focus := 0
	show(focus)
	for key := range keys {
		switch key {
		case "\x1b[B":
			focus = min(focus+1, 2)
		case "\x1b[A":
			focus = max(focus-1, 0)
		case "\r":
			return focus
		default:
			record(codexEvent{Event: "typed into a list", Text: key})
		}
		show(focus)
	}
	os.Exit(0)
	return focus
}

func focusRows(options []string, focus int) []string {
	rows := make([]string, len(options))
	for i, option := range options {
		rows[i] = "  " + option
		if i == focus {
			rows[i] = "› " + option
		}
	}
	return rows
}

// readKey reads one key: an escape sequence whole, or one character.
func readKey(reader *bufio.Reader) (string, error) {
	first, _, err := reader.ReadRune()
	if err != nil {
		return "", err
	}
	if first != '\x1b' {
		return string(first), nil
	}
	sequence := string(first)
	for {
		next, _, err := reader.ReadRune()
		if err != nil {
			return sequence, err
		}
		sequence += string(next)
		if next >= '@' && next <= '~' && next != '[' {
			return sequence, nil
		}
	}
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// nativeFixture is a native spawn of the fake codex.
type nativeFixture struct {
	*fixture
	record string
	// userEnv is what Windows gives a new process of this user: what a
	// native goblin starts from, where this test process's own environment
	// stands for the session that runs cfo spawn.
	userEnv []string
	// terminal is the host record the spawn started, once it recorded itself.
	terminal func() (host.Record, bool)
}

// newNativeFixture readies a native spawn of task-7 in the fake harness, as
// kind, shown mode, with a project credential and fleet variables a goblin
// must not get, both in the spawner's environment and in the user's.
func newNativeFixture(t *testing.T, kind harness.Kind, mode string) *nativeFixture {
	t.Helper()
	f := newFixture(t)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fake := filepath.Join(bin, string(kind)+".exe")
	copyFile(t, program, fake)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The premise: the harness this spawn starts is the fake, never the real
	// one on this machine.
	if found, err := exec.LookPath(string(kind)); err != nil || !strings.EqualFold(found, fake) {
		t.Fatalf("%s resolves to %q, %v; want the fake %s", kind, found, err, fake)
	}
	record := filepath.Join(t.TempDir(), "codex.jsonl")
	t.Setenv(fakeCodexRecord, record)
	t.Setenv(fakeCodexMode, mode)
	userEnv := append(os.Environ(), "OPENAI_API_KEY=a user-scope billing key", "ANTHROPIC_API_KEY=a user-scope billing key", "CLAUDECODE=1", "HERDR_SOCKET_PATH=a user-scope herdr socket")
	t.Setenv("OPENAI_API_KEY", "a billing key of the CFO's")
	t.Setenv("HERDR_PANE_ID", "w1:p9")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CFO_STATE_OVERRIDE", t.TempDir())
	f.service.Sleep = nil
	f.service.HostCommand = []string{program, nativeSpawnHost}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{kind: nativeAdapter{kind: kind}}}
	f.service.Auth = fixtureCredentials{"FIXTURE_TOKEN": "t0ken"}
	f.request.Harness = kind
	f.request.Model = ""
	f.request.Effort = ""

	// The host's record is gone once it ends, so its pid is kept while it runs.
	var mu sync.Mutex
	var started host.Record
	found := false
	watching, stop := context.WithCancel(context.Background())
	go func() {
		for watching.Err() == nil {
			if running, err := host.ReadRecord(f.stateDir, "task-7"); err == nil {
				mu.Lock()
				started, found = running, true
				mu.Unlock()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	native := &nativeFixture{fixture: f, record: record, userEnv: userEnv, terminal: func() (host.Record, bool) {
		mu.Lock()
		defer mu.Unlock()
		return started, found
	}}
	f.service.UserEnvironment = func() ([]string, error) { return native.userEnv, nil }
	// Everything the spawn started ends before the fake's folder goes.
	t.Cleanup(func() {
		stop()
		if terminal, found := native.terminal(); found {
			if err := host.Close(f.stateDir, terminal, nativeCloseWait); err != nil {
				t.Errorf("close the native terminal: %v", err)
			}
			if !ended(terminal.HostPID) {
				t.Errorf("host pid %d is still running", terminal.HostPID)
			}
		}
		if data, err := os.ReadFile(record); err == nil {
			var first codexEvent
			if json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &first) == nil && first.PID != 0 && !ended(first.PID) {
				t.Errorf("the fake harness, pid %d, is still running", first.PID)
			}
		}
	})
	return native
}

// newQuickFixture readies a native spawn of task-7 whose fake codex shows its
// composer at once, for a test about anything but how a spawn meets a
// harness's startup screens, with the composer's settle cut to two reads.
func newQuickFixture(t *testing.T) *nativeFixture {
	t.Helper()
	previous := nativeReadySettle
	nativeReadySettle = 2 * nativePoll
	t.Cleanup(func() { nativeReadySettle = previous })
	return newNativeFixture(t, harness.Codex, "ready")
}

// events reads what the fake codex recorded.
func (f *nativeFixture) events(t *testing.T) []codexEvent {
	t.Helper()
	data, err := os.ReadFile(f.record)
	if err != nil {
		t.Fatalf("the fake codex recorded nothing: %v", err)
	}
	var events []codexEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event codexEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("record line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func named(events []codexEvent, name string) []codexEvent {
	var matched []codexEvent
	for _, event := range events {
		if event.Event == name {
			matched = append(matched, event)
		}
	}
	return matched
}

// ended reports whether pid has ended within ten seconds.
func ended(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	event, _ := windows.WaitForSingleObject(handle, 10000)
	return event == windows.WAIT_OBJECT_0
}

func TestANativeSpawnDismissesTheOptionalDaybreakOfferWithoutEnrollment(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "daybreak")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := f.service.Spawn(ctx, f.request)

	if err != nil {
		t.Errorf("native spawn: %v", err)
	}
	events := f.events(t)
	if dismissals := named(events, "security dismissed"); len(dismissals) != 1 || dismissals[0].Text != "\x1b" {
		t.Errorf("dismissals %v, want exactly one Escape", dismissals)
	}
	for _, event := range []string{"security enrollment", "typed into security offer"} {
		if got := named(events, event); len(got) != 0 {
			t.Errorf("unexpected %s: %d keys", event, len(got))
		}
	}
	if submissions := named(events, "submitted"); len(submissions) != 1 {
		t.Errorf("submissions %v, want exactly one instruction", submissions)
	}
}

func TestANativeSpawnDoesNotContinueUntilTheDaybreakOfferDisappears(t *testing.T) {
	previous := nativeKeyEffect
	nativeKeyEffect = 3 * time.Second
	t.Cleanup(func() { nativeKeyEffect = previous })
	f := newNativeFixture(t, harness.Codex, "daybreak-stuck")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "still shows the optional Daybreak security setup offer after Escape") {
		t.Errorf("native spawn: %v, want an unconfirmed dismissal error", err)
	}
	events := f.events(t)
	if attempts := named(events, "security dismissal attempted"); len(attempts) != 1 || attempts[0].Text != "\x1b" {
		t.Errorf("dismissal attempts %v, want exactly one Escape", attempts)
	}
	for _, event := range []string{"security dismissed", "security enrollment", "typed into security offer", "submitted"} {
		if got := named(events, event); len(got) != 0 {
			t.Errorf("unexpected %s: %d events", event, len(got))
		}
	}
}

// A native spawn answers codex's update prompt with Skip and its trust prompt
// with Yes, each only once it shows, then types the instruction once and
// submits it once codex's composer shows it. The goblin gets its project
// credentials, the launch's variables and Claude Code's own settings, and
// nothing that names the fleet's own session: no billing key, no Herdr pane,
// no Claude Code or Codex session marker. A prompt still drawing, its options
// not shown yet or its focus not shown for a moment after a move, is read
// again until its focus shows, and answered as one drawn at once.
func TestANativeSpawnAnswersCodexsStartupAndDeliversItsInstructionOnce(t *testing.T) {
	for name, mode := range map[string]string{"drawn at once": "", "half drawn": "halfdrawn", "typing drawn only at a redraw": "undrawn", "an Enter taken as part of the paste": "swallow"} {
		t.Run(name, func(t *testing.T) {
			f := newNativeFixture(t, harness.Codex, mode)
			t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
			gitBash := filepath.Join(t.TempDir(), "bash.exe")
			f.userEnv = append(f.userEnv, "CLAUDE_CODE_GIT_BASH_PATH="+gitBash)
			t.Setenv("CODEX_SANDBOX_NETWORK_DISABLED", "1")

			result, err := f.service.Spawn(context.Background(), f.request)

			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil || meta.Backend != "native" || meta.HerdrPaneID != "" || result.Meta.Backend != "native" {
				t.Fatalf("task record = %+v, %v; want a native task with no Herdr pane", meta, err)
			}
			events := f.events(t)
			if blind := append(named(events, "typed blind"), named(events, "typed into a list")...); len(blind) != 0 {
				t.Errorf("keys typed where no screen asked for them: %+v", blind)
			}
			if update := named(events, "update prompt"); len(update) != 1 || update[0].Text != "2. Skip" {
				t.Errorf("update prompt answers = %+v, want 2. Skip once", update)
			}
			if trust := named(events, "trust prompt"); len(trust) != 1 || trust[0].Text != "1. Yes, continue" {
				t.Errorf("trust prompt answers = %+v, want 1. Yes, continue once", trust)
			}
			pointer, written := typedBrief(t, f.fixture, "task-7")
			if submitted := named(events, "submitted"); len(submitted) != 1 || submitted[0].Text != pointer {
				t.Errorf("submitted = %+v, want the line pointing at the instruction once:\n%s", submitted, pointer)
			}
			if instruction := spawnInstruction(f.brief, "task-7"); written != instruction+"\n" {
				t.Errorf("instruction.md = %q, want the whole instruction:\n%s", written, instruction)
			}
			env := named(events, "env")[0].Env
			want := map[string]string{"CFO_TASK_ID": "task-7", "CFO_ROLE": harness.RoleGoblin, "GOTMPDIR": taskScratch(f.stateDir, "task-7"), "TEMP": taskScratch(f.stateDir, "task-7"), "TMP": taskScratch(f.stateDir, "task-7"), "CFO_STATE_OVERRIDE": f.stateDir, "CFO_HOST_ID": "task-7", "FIXTURE_TOKEN": "t0ken", "CLAUDE_CODE_GIT_BASH_PATH": gitBash}
			for name, value := range want {
				if got := env[name]; got == nil || *got != value {
					t.Errorf("the goblin's %s = %v, want %q", name, got, value)
				}
			}
			for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CODEX_SANDBOX_NETWORK_DISABLED"} {
				if got := env[name]; got != nil {
					t.Errorf("the goblin got %s = %q, want it unset", name, *got)
				}
			}
			terminal, found := f.terminal()
			if !found {
				t.Fatal("the spawn's host never recorded itself")
			}
			screen, err := host.ReadScreen(terminal)
			if err != nil || !strings.Contains(host.ScreenTail(screen, 0), "Working") {
				t.Errorf("screen = %q, %v; want codex working", host.ScreenTail(screen, 0), err)
			}
		})
	}
}

// A harness that takes typed text in slowly keeps its delivery waiting while
// the text keeps arriving, however long the text: here each character takes
// 40 ms against a wait of a few seconds.
func TestTypedTextStillArrivingIsWaitedFor(t *testing.T) {
	previousEffect, previousPace := nativeKeyEffect, nativeTypedPace
	nativeKeyEffect, nativeTypedPace = 3*time.Second, 0
	t.Cleanup(func() { nativeKeyEffect, nativeTypedPace = previousEffect, previousPace })
	f := newNativeFixture(t, harness.Codex, "trickle")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
		t.Errorf("submitted = %+v, want the pointer once", submitted)
	}
}

// typedBrief is the line a native Codex spawn types for task id, pointing at
// the task's instruction.md, and what that file holds.
func typedBrief(t *testing.T, f *fixture, id string) (string, string) {
	t.Helper()
	path := filepath.Join(f.stateDir, "tasktmp", id, "instruction.md")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written instruction: %v", err)
	}
	return instructionPointer(path), string(written)
}

// delivered is what a typed line delivered: the instruction a pointer line
// points at, or the line itself.
func delivered(t *testing.T, line string) string {
	t.Helper()
	path, found := strings.CutPrefix(line, "Read ")
	path, pointed := strings.CutSuffix(path, " and follow it exactly: it is your instruction from the CFO.")
	if !found || !pointed {
		return line
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the instruction %s points at: %v", line, err)
	}
	return string(written)
}

// spawnPointer is the line a native Codex spawn of task-7 typed.
func spawnPointer(t *testing.T, f *fixture) string {
	t.Helper()
	pointer, _ := typedBrief(t, f, "task-7")
	return pointer
}

// A harness that can take typed text in slowly, as an idle Codex 0.154 took a
// 2,940-character brief at about 17 characters a second, is given time for
// each character; any other is given a key's effect.
func TestTypedTextIsGivenTimeByItsLengthWhereAHarnessTakesItInSlowly(t *testing.T) {
	codex, _ := harness.NativeScreens(harness.Codex)
	claude, _ := harness.NativeScreens(harness.Claude)
	brief := strings.Repeat("x", 2940)

	if got, want := typedWait(codex, brief), nativeKeyEffect+2940*nativeTypedPace; got != want {
		t.Errorf("codex: wait = %s, want %s", got, want)
	}
	if got := typedWait(claude, brief); got != nativeKeyEffect {
		t.Errorf("claude: wait = %s, want %s", got, nativeKeyEffect)
	}
}

// Codex asks at every start to review hooks that are new or changed. Trusting
// a hook is the Overlord's decision, never a spawn's, yet the goblin must not
// stop there: the spawn continues without trusting them, so they do not run,
// takes its brief on, and tells the CFO, as cfo notify does, which hooks the
// session loads.
func TestANativeSpawnContinuesPastTheHookReviewWithoutTrusting(t *testing.T) {
	for _, mode := range []string{"hooks", "hooks-deaf", "hooks-late"} {
		t.Run(mode, func(t *testing.T) { continuesPastTheHookReview(t, mode) })
	}
}

// continuesPastTheHookReview runs one spawn past the hook review: in mode
// "hooks-deaf" at a review that loses the keys it gets in its first second, and
// in mode "hooks-late" at a review drawn a second after the composer, both as
// Codex 0.154 did live.
func continuesPastTheHookReview(t *testing.T, mode string) {
	previous := nativeKeyEffect
	nativeKeyEffect = 5 * time.Second
	t.Cleanup(func() { nativeKeyEffect = previous })
	f := newNativeFixture(t, harness.Codex, mode)
	hooks := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"siqshift-hook --event session-start"}]}]}}`
	if err := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json"), []byte(hooks), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	events := f.events(t)
	if answered := named(events, "hook prompt"); len(answered) != 1 || answered[0].Text != "3. Continue without trusting (hooks won't run)" {
		t.Errorf("hook prompt answers = %+v, want Continue without trusting once", answered)
	}
	if blind := append(named(events, "typed blind"), named(events, "typed into a list")...); len(blind) != 0 {
		t.Errorf("keys typed where no screen asked for them: %+v", blind)
	}
	if submitted := named(events, "submitted"); len(submitted) != 1 {
		t.Errorf("submitted = %+v, want the instruction once", submitted)
	}
	status, err := os.ReadFile(filepath.Join(f.stateDir, "task-7.status"))
	if err != nil || !strings.Contains(string(status), "working: codex started without trusting its hooks, so they do not run (2 hooks are new or changed); the hooks it loads: SessionStart: siqshift-hook --event session-start") {
		t.Errorf("status = %q, %v; want the untrusted hooks reported", status, err)
	}
	pending, err := wake.Pending(f.stateDir)
	if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool {
		return record.Kind == "notify" && record.Key == "task-7" && strings.Contains(record.Detail, "siqshift-hook --event session-start")
	}) {
		t.Errorf("wake queue = %+v, %v; want a notify naming the untrusted hook", pending, err)
	}
}

// A hooks file that cannot be parsed never stops a spawn whose Codex already
// runs past the hook review: the report still reaches the CFO, naming the file
// in place of its hooks.
func TestANativeSpawnReportsHooksItCannotNameWithoutStopping(t *testing.T) {
	previous := nativeKeyEffect
	nativeKeyEffect = 5 * time.Second
	t.Cleanup(func() { nativeKeyEffect = previous })
	f := newNativeFixture(t, harness.Codex, "hooks")
	broken := filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json")
	if err := os.WriteFile(broken, []byte(`{"hooks":{"SessionStart":{"type":"command"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
		t.Errorf("submitted = %+v, want the instruction once", submitted)
	}
	status, err := os.ReadFile(filepath.Join(f.stateDir, "task-7.status"))
	if err != nil || !strings.Contains(string(status), "working: codex started without trusting its hooks") || !strings.Contains(string(status), "not every hook could be named: harness: read Codex hooks in "+broken) {
		t.Errorf("status = %q, %v; want the untrusted hooks reported with the unreadable file named", status, err)
	}
	pending, err := wake.Pending(f.stateDir)
	if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool {
		return record.Kind == "notify" && record.Key == "task-7" && strings.Contains(record.Detail, broken)
	}) {
		t.Errorf("wake queue = %+v, %v; want a notify naming the unreadable hooks file", pending, err)
	}
}

// A failed native spawn closes only a terminal it launched: one that already
// runs under the task's id refuses the spawn's host, and the spawn's teardown
// leaves it running while it returns the worktree and retires the task.
func TestAFailedNativeSpawnLeavesATerminalItDidNotStartRunning(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	fake, err := exec.LookPath(string(harness.Codex))
	if err != nil {
		t.Fatal(err)
	}
	existing, err := host.Launch(f.stateDir, f.service.HostCommand, append(os.Environ(), fakeCodexMode+"=silent"), host.Spec{ID: "task-7", Args: []string{fake}, Dir: t.TempDir(), Cols: nativeCols, Rows: nativeRows})
	if err != nil {
		t.Fatalf("start the terminal that already runs: %v", err)
	}
	t.Cleanup(func() {
		if err := host.Close(f.stateDir, existing, nativeCloseWait); err != nil {
			t.Errorf("close the terminal that already ran: %v", err)
			if process, err := os.FindProcess(existing.HostPID); err == nil {
				_ = process.Kill()
			}
		}
	})

	_, err = f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "native terminal task-7") {
		t.Fatalf("Spawn error = %v, want native terminal task-7 refused", err)
	}
	if log, _ := os.ReadFile(filepath.Join(f.stateDir, "hosts", "task-7.log")); !strings.Contains(string(log), fmt.Sprintf("terminal task-7 already runs in host pid %d", existing.HostPID)) {
		t.Errorf("host log = %q, want the terminal that already runs named", log)
	}
	if record, err := host.ReadRecord(f.stateDir, "task-7"); err != nil || record.HostPID != existing.HostPID {
		t.Fatalf("record = %+v, %v; want the terminal that already ran, host pid %d", record, err, existing.HostPID)
	}
	if _, err := host.ReadScreen(existing); err != nil {
		t.Errorf("read the terminal that already ran: %v", err)
	}
	if blind := named(f.events(t), "typed blind"); len(blind) != 0 {
		t.Errorf("keys typed into the terminal that already ran: %+v", blind)
	}
	if _, err := os.Stat(filepath.Join(f.stateDir, "task-7.meta")); !os.IsNotExist(err) {
		t.Errorf("the task record is still there: %v", err)
	}
	if f.git.returned != 1 {
		t.Errorf("worktree returned %d times, want once", f.git.returned)
	}
}

// A native terminal whose host runs but does not answer the close stops the
// teardown, since its harness may still run: the error names the terminal,
// and the worktree and task record stay, so the task can still be reached. A
// host that has ended has nothing left to close, and the teardown completes.
func TestATeardownKeepsANativeTaskWhoseHostMayStillRun(t *testing.T) {
	previous := nativeCloseWait
	nativeCloseWait = time.Second
	t.Cleanup(func() { nativeCloseWait = previous })
	exited := exec.Command(os.Args[0], "-test.run=^$")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		hostPID       int
		isKeptInPlace bool
	}{
		"its host runs":      {os.Getpid(), true},
		"its host has ended": {exited.ProcessState.Pid(), false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			launched := host.Record{ID: "task-7", Pipe: `\\.\pipe\spawn-test-nobody-serves-this`, Token: "t0ken", Version: host.Version, HostPID: test.hostPID, Started: time.Now().UTC()}
			record, err := json.Marshal(launched)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(makeDir(t, filepath.Join(f.stateDir, "hosts")), "task-7.json"), string(record))
			meta := filepath.Join(f.stateDir, "task-7.meta")
			writeFile(t, meta, "{}")

			err = f.service.teardownLaunch(context.Background(), launched, f.project, f.worktree, filepath.Join(f.stateDir, "scratch-task-7"), "task-7")

			_, metaErr := os.Stat(meta)
			if test.isKeptInPlace {
				if err == nil || !strings.Contains(err.Error(), "native terminal task-7") || f.git.returned != 0 || metaErr != nil {
					t.Errorf("teardown = %v, worktree returned %d times, task record %v; want the terminal named and the task kept", err, f.git.returned, metaErr)
				}
				return
			}
			if err != nil || f.git.returned != 1 || !os.IsNotExist(metaErr) {
				t.Errorf("teardown = %v, worktree returned %d times, task record %v; want the task retired", err, f.git.returned, metaErr)
			}
		})
	}
}

// The instruction is submitted only once the composer shows it: a prompt that
// opens as the typing starts never gets the Enter.
func TestANativeSpawnSubmitsOnlyWhatTheComposerShows(t *testing.T) {
	previous := nativeKeyEffect
	nativeKeyEffect = 3 * time.Second
	t.Cleanup(func() { nativeKeyEffect = previous })
	f := newNativeFixture(t, harness.Codex, "late")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "native terminal task-7") || !strings.Contains(err.Error(), "so it was not submitted") {
		t.Fatalf("Spawn error = %v, want the instruction left unsubmitted in native terminal task-7", err)
	}
	if entered := named(f.events(t), "entered at a late prompt"); len(entered) != 0 {
		t.Errorf("Enter reached the prompt that opened: %+v", entered)
	}
}

// A spawn succeeds only once the harness shows it working on the instruction,
// and it never types the instruction a second time.
func TestANativeSpawnSucceedsOnlyOnceTheHarnessWorks(t *testing.T) {
	previous := nativeAccepted
	nativeAccepted = 3 * time.Second
	t.Cleanup(func() { nativeAccepted = previous })
	f := newNativeFixture(t, harness.Codex, "unmoved")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "native terminal task-7") || !strings.Contains(err.Error(), "never showed its harness working") {
		t.Fatalf("Spawn error = %v, want native terminal task-7 never seen working", err)
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
		t.Errorf("submitted = %+v, want the instruction submitted once", submitted)
	}
}

// A screen the spawn recognizes nothing on is never typed into: the spawn
// fails, naming the terminal and quoting its screen. The budget leaves a
// loaded machine time to start the harness and draw it.
func TestANativeSpawnNeverTypesIntoAScreenItDoesNotKnow(t *testing.T) {
	previous := nativeStartup
	nativeStartup = 15 * time.Second
	t.Cleanup(func() { nativeStartup = previous })
	f := newNativeFixture(t, harness.Codex, "silent")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "native terminal task-7") || !strings.Contains(err.Error(), "Starting up, please wait.") {
		t.Fatalf("Spawn error = %v, want native terminal task-7 named with its screen", err)
	}
	if blind := named(f.events(t), "typed blind"); len(blind) != 0 {
		t.Errorf("keys typed into a screen the spawn did not know: %+v", blind)
	}
}

// A screen that cannot be read stops the spawn with the read's error: it
// never counts as a screen with no dialog on it. Claude is the terminal's
// own program, so its leaving the console leaves nothing to read it through.
func TestANativeSpawnStopsWhenItCannotReadTheScreen(t *testing.T) {
	previous := nativeReadGrace
	nativeReadGrace = 2 * time.Second
	t.Cleanup(func() { nativeReadGrace = previous })
	f := newNativeFixture(t, harness.Claude, "detached")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "terminal task-7") || !strings.Contains(err.Error(), "attach to the console") {
		t.Fatalf("Spawn error = %v, want the failed read of terminal task-7", err)
	}
}

// A screen read that fails for a moment, as a console does while it starts
// under load, is read again, and the read that succeeds is the screen; reads
// that keep failing past the grace are the error.
func TestAScreenReadThatFailsForAMomentIsReadAgain(t *testing.T) {
	previous := nativeReadGrace
	nativeReadGrace = 200 * time.Millisecond
	t.Cleanup(func() { nativeReadGrace = previous })
	refusal := fmt.Errorf("host: read the screen of terminal task-7: attach to the console: A device attached to the system is not functioning")
	for name, test := range map[string]struct {
		failures int
		want     error
	}{
		"fails twice":   {2, nil},
		"keeps failing": {1 << 30, refusal},
		"never fails":   {0, nil},
	} {
		t.Run(name, func(t *testing.T) {
			reads := 0
			service := Service{
				ReadScreen: func(host.Record) ([]string, error) {
					reads++
					if reads <= test.failures {
						return nil, refusal
					}
					return []string{"› Ask Codex to do anything"}, nil
				},
				Sleep: func(context.Context, time.Duration) error { return nil },
			}

			screen, err := service.readNativeScreen(context.Background(), host.Record{ID: "task-7"})

			if err != test.want || (err == nil) != (len(screen) == 1) {
				t.Errorf("readNativeScreen = %q, %v after %d reads; want error %v", screen, err, reads, test.want)
			}
		})
	}
}

// A native terminal starts claude.exe itself, and codex and pi through cmd /c
// with arguments cmd reads as plain text; a claude found only as a script shim,
// or an argument cmd would interpret, is refused.
func TestANativeTerminalStartsEachHarnessAsItsProgramNeeds(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude.cmd", "codex.cmd", "pi.cmd"} {
		writeFile(t, filepath.Join(bin, name), "@echo off\r\n")
	}
	t.Setenv("PATH", bin)
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)

	if program, err := nativeProgram(harness.Claude, harness.Launch{Args: []string{"--x"}}); err == nil {
		t.Errorf("claude as a script shim = %q, want refused", program)
	}
	program, err := nativeProgram(harness.Codex, harness.Launch{Executable: "codex", Args: []string{"--model", "gpt-6-astra", "-c", "model_reasoning_effort=high"}})
	if err != nil || !slices.Equal(program, []string{`C:\Windows\System32\cmd.exe`, "/c", "codex", "--model", "gpt-6-astra", "-c", "model_reasoning_effort=high"}) {
		t.Errorf("codex = %q, %v; want it through cmd /c", program, err)
	}
	for _, arg := range []string{"a&b", `"quoted"`, "50%", "a|b"} {
		if program, err := nativeProgram(harness.Pi, harness.Launch{Executable: "pi", Args: []string{arg}}); err == nil {
			t.Errorf("pi with %q = %q, want refused", arg, program)
		}
	}
}

// A harness installed as a script shim leaves finding its program to cmd, in
// the user environment the host runs with, so one not on this process's PATH
// still starts through cmd /c.
func TestAShimHarnessIsNotLookedUpOnThisProcessPath(t *testing.T) {
	// Arrange
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)

	// Act
	program, err := nativeProgram(harness.Codex, harness.Launch{Executable: "codex", Args: []string{"--model", "gpt-6-astra"}})

	// Assert
	if err != nil || !slices.Equal(program, []string{`C:\Windows\System32\cmd.exe`, "/c", "codex", "--model", "gpt-6-astra"}) {
		t.Errorf("codex not on PATH = %q, %v; want it through cmd /c", program, err)
	}
}

// fixtureCredentials is a project's credentials preflight that returns them.
type fixtureCredentials map[string]string

func (c fixtureCredentials) Preflight(context.Context, string) (auth.Result, error) {
	return auth.Result{Env: c}, nil
}

// nativeAdapter builds kind's launch the way its adapter does: codex through
// its shim, claude started as its own program.
type nativeAdapter struct {
	kind    harness.Kind
	control harness.Control
	// specs, when set, records every launch spec built, and buildErr refuses
	// the build.
	specs    *[]harness.LaunchSpec
	buildErr error
}

func (a nativeAdapter) Kind() harness.Kind { return a.kind }

func (nativeAdapter) Validate(context.Context, execx.Runner) error { return nil }

func (a nativeAdapter) Control() harness.Control { return a.control }

func (a nativeAdapter) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	if a.specs != nil {
		*a.specs = append(*a.specs, spec)
	}
	if a.buildErr != nil {
		return harness.Launch{}, a.buildErr
	}
	launch := harness.Launch{
		Args:       []string{"--dangerously-skip-permissions"},
		Env:        map[string]string{"GOTMPDIR": spec.Scratch, "TEMP": spec.Scratch, "TMP": spec.Scratch, harness.RoleVariable: harness.RoleGoblin},
		PromptFile: spec.BriefPath,
	}
	if a.kind == harness.Codex {
		launch.Args, launch.Executable = []string{"--dangerously-bypass-approvals-and-sandbox"}, "codex"
	}
	return launch, nil
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	source, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}

// On 2026-09-26 a CFO running in Claude Code spawned pd-chat-turn-died
// natively, and the goblin started with the CFO's whole session: Claude
// Code's child-session marker, so the goblin saved no transcript, the CFO's
// session id and messaging socket and token, its pid, and the CFO's own
// Herdr pane. A native goblin starts from the user's environment, as a Herdr
// goblin does from its pane, never from the process that ran cfo spawn.
func TestANativeGoblinStartsFromTheUsersEnvironmentNotTheSpawners(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	f.userEnv = append(f.userEnv, "USERS_OWN_SETTING=kept")
	spawner := map[string]string{
		"CLAUDECODE":                   "1",
		"CLAUDE_CODE_CHILD_SESSION":    "1",
		"CLAUDE_CODE_SESSION_ID":       "the-cfos-session",
		"CLAUDE_CODE_MESSAGING_SOCKET": `\\.\pipe\the-cfos-socket`,
		"CLAUDE_CODE_MESSAGING_TOKEN":  "the-cfos-token",
		"CLAUDE_PID":                   "4242",
		"HERDR_PANE_ID":                "w9:p0",
		"HERDR_TAB_ID":                 "w9:t0",
		"HERDR_WORKSPACE_ID":           "w9",
		"CFO_ROLE":                     "cfo",
		"A_SESSION_ONLY_VARIABLE":      "the spawner's",
	}
	for name, value := range spawner {
		t.Setenv(name, value)
	}

	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	env := named(f.events(t), "env")[0].Env
	for name := range spawner {
		if name == "CFO_ROLE" {
			continue
		}
		if got := env[name]; got != nil {
			t.Errorf("the goblin started with the spawner's %s = %q", name, *got)
		}
	}
	if got := env["CFO_ROLE"]; got == nil || *got != harness.RoleGoblin {
		t.Errorf("the goblin's CFO_ROLE = %v, want %q", got, harness.RoleGoblin)
	}
	if got := env["USERS_OWN_SETTING"]; got == nil || *got != "kept" {
		t.Errorf("the goblin's USERS_OWN_SETTING = %v, want the user's own setting kept", got)
	}
}

// A server that authenticates by bearerTokenEnvVar reaches a native goblin
// only when the environment its host starts with sets that variable: one set
// only in the spawning process, as a CFO's session exports it, is withheld,
// and one the user configured is handed on.
func TestANativeSpawnHandsATokenServerOnlyWhenTheGoblinStartsWithItsToken(t *testing.T) {
	for _, test := range []struct {
		name         string
		isUserScoped bool
	}{
		{name: "set only in the spawning process"},
		{name: "set in the user's environment", isUserScoped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeFixture(t, harness.Codex, "")
			t.Setenv("FIXTURE_MCP_TOKEN", "the-cfos-token")
			if test.isUserScoped {
				f.userEnv = append(f.userEnv, "FIXTURE_MCP_TOKEN=the-users-token")
			}
			writeFile(t, filepath.Join(f.project, ".mcp.json"), `{"mcpServers":{"neon":{"url":"https://mcp.neon.tech/mcp","bearerTokenEnvVar":"FIXTURE_MCP_TOKEN"}}}`)

			result, err := f.service.Spawn(context.Background(), f.request)

			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			withheld := strings.Contains(result.Output, "withheld servers whose token variable is not set for the goblin: neon (FIXTURE_MCP_TOKEN)")
			if withheld == test.isUserScoped {
				t.Errorf("output names neon withheld = %v, want %v:\n%s", withheld, !test.isUserScoped, result.Output)
			}
		})
	}
}

// A native terminal whose host could not leave its launcher's job is named in
// the spawn's output, since it ends when that job closes; one that left is not.
func TestAContainedNativeTerminalIsReported(t *testing.T) {
	for contained, want := range map[bool]string{
		true:  "warning: native terminal task-7 could not leave the job of the process that ran cfo, so it ends when that job closes (see its host log)",
		false: "",
	} {
		if got := containedNotice(host.Record{ID: "task-7", Contained: contained}); got != want {
			t.Errorf("contained %v: notice = %q, want %q", contained, got, want)
		}
	}
}

// On 2026-09-30 the CFO's terminal rendered white: it had been started from a
// Codex tool shell carrying NO_COLOR, TERM=dumb and CODEX_CI. A goblin that
// cfo spawn, switch or resume starts from such a shell starts from the user's
// own logon environment instead, so none of those settings reaches it, while
// the user's PATH and the task's own variables do.
func TestANativeGoblinDoesNotInheritTheCallersTerminalSettings(t *testing.T) {
	// Arrange
	caller := map[string]string{"NO_COLOR": "1", "TERM": "dumb", "CODEX_CI": "1"}
	for name, value := range caller {
		t.Setenv(name, value)
	}
	service := Service{StateDir: t.TempDir()}

	// Act
	userEnv, err := service.userEnvironment()
	if err != nil {
		t.Fatalf("userEnvironment: %v", err)
	}
	env := service.nativeHostEnvironment(userEnv, harness.Launch{Env: map[string]string{harness.RoleVariable: harness.RoleGoblin, "CFO_TASK_ID": "g1"}}, nil)

	// Assert
	for name := range caller {
		if hasNativeVariable(env, name) {
			t.Errorf("the goblin starts with the caller's %s", name)
		}
	}
	for _, name := range []string{"PATH", harness.RoleVariable, "CFO_TASK_ID", "CFO_STATE_OVERRIDE"} {
		if !hasNativeVariable(env, name) {
			t.Errorf("the goblin starts without %s", name)
		}
	}
}
