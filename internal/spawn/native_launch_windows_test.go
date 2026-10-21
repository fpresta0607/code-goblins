package spawn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The test binary plays every part of a native spawn: with nativeSpawnHost
// first it is the terminal's host, and copied onto PATH as codex.exe,
// claude.exe or pi.exe it is the harness, which cmd /c or the spawn finds
// there. With sleepChild first it is a process that only waits, a minute at
// most.
const (
	nativeSpawnHost = "native-spawn-host"
	sleepChild      = "sleep-child"
)

// The fake harness records what it sees to the file fakeCodexRecord names, and
// fakeCodexMode picks what it shows: codex's update prompt, then its trust
// prompt and composer by default, its composer at once ("ready"), or its hook
// review prompt ("hooks"), nothing
// a spawn would recognize ("silent"), or a screen nothing can read, the
// console's input waker ended ("unreadable"). Half
// drawn ("halfdrawn"), its update prompt shows its header alone at first, and
// no focus for a moment after a move. At its composer, a prompt can open as
// the typing starts ("late"), a submitted line can leave it looking idle
// ("unmoved"), each turn can end a moment after it starts ("turns"), typed
// text can show only once its console is resized ("undrawn"), an Enter with
// no End before it can be taken as part of the text ("swallow"), or the first
// submit key can be taken seconds after it arrived ("slow"), where
// by default a turn never ends. With "exitmenu", /exit opens the menu Claude
// Code shows over background work before it exits. With "claude-lost-enter"
// it is Claude Code's composer taking the first Enter that ends typed text
// as a new line, with "claude-enters-lost" every one, and with
// "claude-late-enter" taking the first seconds late. With "pi-starting" it is
// pi 0.85.1 at its editor while its startup completes for a few seconds,
// with "pi-stuck" one whose startup never completes, and with "pi-stale" one
// whose first turn ends with the startup row still in its chat and which then
// leaves whatever is submitted in its editor.
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
	case len(os.Args) > 1 && os.Args[1] == sleepChild:
		time.Sleep(time.Minute)
	case slices.Contains([]string{"codex", "claude", "pi"}, strings.ToLower(strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])))):
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
	Home  *home.Home         `json:"home,omitempty"`
}

// recordedEnv is what the fake codex records of its environment.
var recordedEnv = []string{"CFO_TASK_ID", "CFO_ROLE", "GOTMPDIR", "TEMP", "TMP", "TMPDIR", "CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_PROJECTS_ROOT", "CFO_HOST_ID", "FIXTURE_TOKEN", "PLAYWRIGHT_BROWSERS_PATH", "LOCALAPPDATA", "XDG_CACHE_HOME", "HOME", "UV_CACHE_DIR", "DATABASE_URL", "OPENAI_API_KEY", "HERDR_PANE_ID", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_GIT_BASH_PATH", "CODEX_SANDBOX_NETWORK_DISABLED", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_PID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "A_SESSION_ONLY_VARIABLE", "USERS_OWN_SETTING", standInDatabase, standInPayments, standInSource, standInStray}

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
	// The home every cfo command run in the goblin's terminal resolves.
	if resolved, err := home.Resolve(); err != nil {
		record(codexEvent{Event: "home", Text: err.Error()})
	} else {
		record(codexEvent{Event: "home", Home: &resolved})
	}
	mode := os.Getenv(fakeCodexMode)
	if mode == "unreadable" {
		// The console's input waker, which reads its screen, is the one
		// other process attached to it.
		pids := make([]uint32, 16)
		count, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
		for _, pid := range pids[:min(int(count), len(pids))] {
			if int(pid) == os.Getpid() {
				continue
			}
			if process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid); err == nil {
				_ = windows.TerminateProcess(process, 1)
				_, _ = windows.WaitForSingleObject(process, 60000)
				_ = windows.CloseHandle(process)
			}
		}
		record(codexEvent{Event: "unreadable"})
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
	if mode == "pi-ready" || mode == "pi-starting" || mode == "pi-stuck" || mode == "pi-stale" {
		startup := 8 * time.Second
		switch mode {
		case "pi-ready":
			startup = 0
		case "pi-stuck":
			startup = time.Hour
		}
		piStartup(startup, mode == "pi-stale", keys, record, draw) // stall-accepted: the stand-in pi's startup refuses the first Enter however short a stall cuts it, so a test that needs a refusal still gets one.
		return
	}
	if mode == "claude-lost-enter" || mode == "claude-enters-lost" || mode == "claude-late-enter" {
		claudeComposer(mode, keys, record, draw)
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
	if mode == "working-at-start" {
		// As cg-cards-stay-open was on 2026-10-15, when a cfo send reached it
		// before its spawn typed the instruction: in a turn from its first
		// screen, it takes what is typed as a message queued for the turn.
		working := func(text string) {
			draw("• Working (5s • esc to interrupt)", "", "› "+text, "", "  GPT-6.1-Sol xhigh · "+mustGetwd())
		}
		working("Ask Codex to do anything")
		var line strings.Builder
		for key := range keys {
			switch key {
			case "\r":
				record(codexEvent{Event: "queued into the turn", Text: line.String()})
				line.Reset()
				working("Ask Codex to do anything")
			case "\x1b[A", "\x1b[B", "\x1b[F":
			default:
				line.WriteString(key)
				working(line.String())
			}
		}
		return
	}
	if mode != "ready" && !codexStartup(mode, keys, record, draw, composer) {
		return
	}
	composer("Ask Codex to do anything")
	var line strings.Builder
	late, hasPasteEnded, slow := false, false, mode == "slow"
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
			case key == "\r" && mode == "swallow" && !hasPasteEnded:
				// As Codex 0.154 does, an Enter that no End ended the paste
				// before is taken as part of it, and the text stays in the
				// composer.
				hasPasteEnded = true
				record(codexEvent{Event: "swallowed enter"})
			case key == "\r":
				if slow {
					// A loaded runner can leave an idle Codex taking its
					// submit key seconds after it arrived, the text still
					// showing in its composer.
					time.Sleep(3 * time.Second)
					slow = false
				}
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
			case key == "\x1b[F":
				// End ends a paste and leaves the text as it is.
				hasPasteEnded = true
			case key == "\x1b[A" || key == "\x1b[B":
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
			for deaf := time.After(1500 * time.Millisecond); deaf != nil; { // stall-accepted: the stand-in's deaf moment, which the spawn's longer settle outlasts on the same clock.
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

// piStartup shows pi 0.85.1's editor as it shows while its startup completes,
// which on a machine without fd and rg includes downloading them: it takes
// typed text at once, but until startup has run that long it answers Enter by
// putting the text back in its editor and saying "Startup is still in
// progress". After that, Enter submits the text and pi works on it. With
// isTurnEnding its first turn ends a second later, its chat still showing that
// row, and every later Enter leaves the text in its editor.
func piStartup(startup time.Duration, isTurnEnding bool, keys <-chan string, record func(codexEvent), draw func(rows ...string)) {
	rule := strings.Repeat("─", 40)
	footer := []string{`~\source\demo (detached)`, "0.0%/128k (auto)                                        stand-in"}
	status := ""
	editor := func(text string) {
		draw(append([]string{"", status, rule, " " + text, rule}, footer...)...)
	}
	editor("")
	started := time.Now().Add(startup)
	hasTurned, hasRefused := false, false
	var line strings.Builder
	for key := range keys {
		switch {
		case key == "\r" && (time.Now().Before(started) || (startup > 0 && !hasRefused)):
			record(codexEvent{Event: "refused while starting", Text: line.String()})
			status = " Startup is still in progress"
			hasRefused = true
		case key == "\r" && line.Len() > 0 && hasTurned:
			record(codexEvent{Event: "left in the editor", Text: line.String()})
		case key == "\r" && line.Len() > 0:
			record(codexEvent{Event: "submitted", Text: line.String()})
			draw(append([]string{"", " " + line.String(), "", "── ⠸ Working ──", "", rule}, footer...)...)
			line.Reset()
			if !isTurnEnding {
				continue
			}
			time.Sleep(time.Second)
			hasTurned = true
		case key == "\r":
			continue
		default:
			line.WriteString(key)
		}
		editor(line.String())
	}
}

// claudeComposer shows Claude Code's composer, between its rules above its
// permission mode, as a resumed goblin's was on a loaded machine on
// 2026-10-20: in mode "claude-lost-enter" the first Enter that ends typed
// text, and in "claude-enters-lost" every one, is taken as a new line,
// leaving the text in the composer over an empty row. In
// "claude-late-enter" the first Enter that ends typed text is taken seconds
// after it arrived, the text showing in the composer as it was until then,
// and submits it. An Enter that submits the text starts a turn that never
// ends.
func claudeComposer(mode string, keys <-chan string, record func(codexEvent), draw func(rows ...string)) {
	rule := strings.Repeat("─", 80)
	footer := "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	composer := func(rows ...string) {
		draw(append(append([]string{"", rule}, rows...), rule, footer)...)
	}
	composer(">")
	var line strings.Builder
	lost, isLate := 0, mode == "claude-late-enter"
	for key := range keys {
		switch {
		case key == "\r" && line.Len() > 0 && (mode == "claude-enters-lost" || (mode == "claude-lost-enter" && lost == 0)):
			lost++
			record(codexEvent{Event: "enter taken as a new line", Text: line.String()})
		case key == "\r" && line.Len() > 0:
			if isLate {
				time.Sleep(3 * time.Second)
				isLate = false
			}
			record(codexEvent{Event: "submitted", Text: line.String()})
			draw("", "> "+line.String(), "", "✽ Reticulating… (esc to interrupt)", rule, ">", rule, footer)
			line.Reset()
			continue
		case key == "\r":
			record(codexEvent{Event: "enter on an empty composer"})
			continue
		default:
			line.WriteString(key)
		}
		rows := []string{"> " + line.String()}
		for range lost {
			rows = append(rows, "")
		}
		composer(rows...)
	}
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
	return newQuickFixtureOf(t, harness.Codex)
}

// newQuickFixtureOf is newQuickFixture with its fake harness standing in for
// kind; a fake pi shows its editor as it does once its startup has ended.
func newQuickFixtureOf(t *testing.T, kind harness.Kind) *nativeFixture {
	t.Helper()
	previous := nativeReadySettle
	nativeReadySettle = 2 * nativePoll
	t.Cleanup(func() { nativeReadySettle = previous })
	mode := "ready"
	if kind == harness.Pi {
		mode = "pi-ready"
	}
	return newNativeFixture(t, kind, mode)
}

// A check the caller ran beside the start, such as who else works in the
// task's area, refuses it before its terminal launches, and the start leaves
// no trace: no task record, no worktree, no terminal and no failure in its
// status log, which the board would show as a finished task.
func TestACheckThatRefusesBeforeLaunchLeavesNoTrace(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	refusal := errors.New("a teammate works in this area")
	f.request.BeforeLaunch = func() error { return refusal }

	// Act
	_, err := f.service.Spawn(t.Context(), f.request)

	// Assert
	if !errors.Is(err, refusal) {
		t.Fatalf("spawn = %v, want the check's refusal", err)
	}
	if _, readErr := state.ReadTaskMeta(f.stateDir, f.request.ID); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("the refused start left its task record: %v", readErr)
	}
	if _, readErr := host.ReadRecord(f.stateDir, f.request.ID); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("the refused start launched a terminal: %v", readErr)
	}
	if lines, _ := state.TailStatus(f.stateDir, f.request.ID, 10); slices.ContainsFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, "failed:") || strings.Contains(line, " failed:")
	}) {
		t.Fatalf("status log = %q, want no failure for a refused start", lines)
	}
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

// ended reports whether pid has ended within thirty seconds.
func ended(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	event, _ := windows.WaitForSingleObject(handle, 30000)
	return event == windows.WAIT_OBJECT_0
}

func TestANativeSpawnDismissesTheOptionalDaybreakOfferWithoutEnrollment(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "daybreak")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
// again until its focus shows, and answered as one drawn at once. A codex
// that takes its submit key seconds late, the text still in its composer, is
// sent that key once, so no second Enter reaches it after the instruction
// (#1171).
func TestANativeSpawnAnswersCodexsStartupAndDeliversItsInstructionOnce(t *testing.T) {
	for name, mode := range map[string]string{"drawn at once": "", "half drawn": "halfdrawn", "typing drawn only at a redraw": "undrawn", "a paste that only End ends": "swallow", "a submit key taken late": "slow"} {
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
			if instruction := spawnInstruction(f.brief, state.TaskMeta{ID: "task-7", Kind: "ship", TaskTmp: filepath.Join(f.stateDir, "tasktmp", "task-7")}, nil) + credentialsInstruction(f.project, auth.Grant{}); written != instruction+"\n" {
				t.Errorf("instruction.md = %q, want the whole instruction:\n%s", written, instruction)
			}
			env := named(events, "env")[0].Env
			want := map[string]string{"CFO_TASK_ID": "task-7", "CFO_ROLE": harness.RoleGoblin, "GOTMPDIR": taskScratch(f.stateDir, "task-7"), "TEMP": taskScratch(f.stateDir, "task-7"), "TMPDIR": taskScratch(f.stateDir, "task-7"), "TMP": home.SharedTempBeside(taskScratch(f.stateDir, "task-7")), "CFO_STATE_OVERRIDE": f.stateDir, "CFO_HOST_ID": "task-7", "FIXTURE_TOKEN": "t0ken", "CLAUDE_CODE_GIT_BASH_PATH": gitBash}
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

// An instruction longer than a harness keeps as typed words, or of more than
// one line, is never typed whole into any harness: it is written to the
// task's instruction.md and one short line pointing at it is typed. Typed
// whole in one burst, Claude Code 2.1.29x collapsed it as "[Pasted text #1]"
// and handed its model a pasted block with nothing written beside it, which
// the model does not take as the operator's own words: on 2026-10-09 Uma,
// Wanda and Lyle each asked whether to proceed with what they were resumed
// with, and sat until someone said yes.
func TestAnInstructionIsTypedAsOneShortLineIntoEveryHarness(t *testing.T) {
	meta := state.TaskMeta{ID: "task-7", Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh"}
	resume := resumeInstruction(meta, switchTarget{Harness: harness.Claude, Model: "claude-opus-5-5", Effort: "xhigh"})
	if len([]rune(resume)) <= typedInstructionLimit {
		t.Fatalf("the resume instruction is %d characters, so it proves nothing of a long one", len([]rune(resume)))
	}
	for _, kind := range []harness.Kind{harness.Claude, harness.Codex, harness.Pi} {
		for name, testCase := range map[string]struct {
			instruction string
			isPointed   bool
		}{
			"what a goblin is resumed with":             {resume, true},
			"a short instruction with a note under it":  {"Continue the task.\nThe Overlord answered: Blue", true},
			"a short instruction of one line":           {harness.BriefInstruction(`C:\home\data\task-7\brief.md`), false},
			"a line as long as the longest typed whole": {strings.Repeat("x", typedInstructionLimit), false},
		} {
			t.Run(string(kind)+": "+name, func(t *testing.T) {
				// Arrange
				service := Service{StateDir: t.TempDir()}

				// Act
				typed, err := service.typedInstruction("task-7", testCase.instruction)

				// Assert
				if err != nil {
					t.Fatal(err)
				}
				if !testCase.isPointed {
					if typed != testCase.instruction {
						t.Errorf("typed %q, want the instruction itself", typed)
					}
					return
				}
				path := filepath.Join(service.StateDir, "tasktmp", "task-7", "instruction.md")
				written, readErr := os.ReadFile(path)
				if typed != instructionPointer(path) || strings.ContainsAny(typed, "\r\n") || len([]rune(typed)) > typedInstructionLimit {
					t.Errorf("typed %d characters, want the one short line pointing at %s:\n%s", len([]rune(typed)), path, typed)
				}
				if readErr != nil || string(written) != testCase.instruction+"\n" {
					t.Errorf("instruction.md = %q, %v; want the whole instruction", written, readErr)
				}
			})
		}
	}
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
	// Found while it runs, so the end below reaches it and no later process
	// that took its pid.
	existingHost, err := os.FindProcess(existing.HostPID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(f.stateDir, existing, nativeCloseWait); err != nil {
			t.Errorf("close the terminal that already ran: %v", err)
			_ = existingHost.Kill()
		}
		_ = existingHost.Release()
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

// A harness already in a turn when its spawn first reads it has started: a
// cfo send that reached it first started the turn. Its instruction is still
// handed to it, queued for the turn, once (#959).
func TestANativeSpawnCountsAHarnessAlreadyWorkingAsStarted(t *testing.T) {
	// Arrange
	previous := nativeStartup
	nativeStartup = 10 * time.Second
	t.Cleanup(func() { nativeStartup = previous })
	f := newNativeFixture(t, harness.Codex, "working-at-start")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v, want a harness at work counted as started", err)
	}
	queued := named(f.events(t), "queued into the turn")
	if len(queued) != 1 || !strings.Contains(delivered(t, queued[0].Text), "follow it exactly") {
		t.Fatalf("queued = %+v, want the instruction handed to the turn once", queued)
	}
	status, err := os.ReadFile(filepath.Join(f.stateDir, f.request.ID+".status"))
	if err != nil && !os.IsNotExist(err) || strings.Contains(string(status), "failed:") {
		t.Errorf("status = %q, %v; want no failed start", status, err)
	}
}

// pi 0.85 takes typed text while its startup completes, which on a machine
// without fd and rg includes downloading them, but answers Enter by putting
// the text back in its editor and saying "Startup is still in progress". On
// 2026-10-14 a spawn on a clean runner pressed Enter once, and pi sat with its
// instruction unsent until the spawn gave up (issue 919). The spawn presses
// Enter again while pi says so, for longer than a submitted instruction is
// given to show pi working, and the instruction is submitted once.
func TestANativeSpawnSubmitsAgainWhilePiIsStillStarting(t *testing.T) {
	// Arrange
	previous := nativeAccepted
	nativeAccepted = 3 * time.Second
	t.Cleanup(func() { nativeAccepted = previous })
	f := newNativeFixture(t, harness.Pi, "pi-starting")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	events := f.events(t)
	if refused := named(events, "refused while starting"); len(refused) == 0 {
		t.Fatal("no Enter reached pi while it was starting, so this proves nothing")
	}
	if submitted := named(events, "submitted"); len(submitted) != 1 || submitted[0].Text != spawnPointer(t, f.fixture) {
		t.Errorf("submitted = %+v, want the instruction once", submitted)
	}
}

// A pi whose startup never completes fails the spawn once it has said it is
// still starting for as long as a harness's startup is given, and the error
// shows what its screen says.
func TestANativeSpawnFailsWhenPiNeverFinishesStarting(t *testing.T) {
	// Arrange
	previousAccepted, previousStartup := nativeAccepted, nativeStartup
	nativeAccepted, nativeStartup = 3*time.Second, 15*time.Second
	t.Cleanup(func() { nativeAccepted, nativeStartup = previousAccepted, previousStartup })
	f := newNativeFixture(t, harness.Pi, "pi-stuck")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "native terminal task-7") || !strings.Contains(err.Error(), "still starting") || !strings.Contains(err.Error(), "Startup is still in progress") {
		t.Fatalf("Spawn error = %v, want native terminal task-7 named as still starting, with its screen", err)
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 0 {
		t.Errorf("submitted = %+v, want nothing taken", submitted)
	}
}

// pi keeps "Startup is still in progress" in its chat after it takes the
// instruction, so a later cfo send can find that row already on its screen.
// That row turned an earlier submit away, not this one: Enter is pressed once,
// and a pi that never shows itself working fails the send once its acceptance
// window has run, not its startup budget.
func TestANativeSendIgnoresAStartupRowLeftFromAnEarlierSubmit(t *testing.T) {
	// Arrange
	previousAccepted, previousStartup := nativeAccepted, nativeStartup
	nativeAccepted, nativeStartup = 3*time.Second, 2*time.Minute
	t.Cleanup(func() { nativeAccepted, nativeStartup = previousAccepted, previousStartup })
	f := newNativeFixture(t, harness.Pi, "pi-stale")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	record, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := harness.NativeScreens(harness.Pi)
	if _, err := f.service.awaitScreen(context.Background(), record, 30*time.Second, func(screen []string) bool {
		return screens.IsStarting(screen) && screens.ComposerEmpty(screen) && !screens.IsWorking(screen)
	}); err != nil {
		t.Fatalf("the fake pi never ended its turn with the startup row on its screen: %v", err)
	}

	// Act
	began := time.Now()
	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")
	took := time.Since(began)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "never showed its harness working") {
		t.Errorf("SendNative = %v, want the acceptance window's error", err)
	}
	if took >= time.Minute {
		t.Errorf("SendNative took %s, want it back once the %s acceptance window ran", took, nativeAccepted)
	}
	if left := named(f.events(t), "left in the editor"); len(left) != 1 || left[0].Text != "CFO: run the tests" {
		t.Errorf("Enter presses on the send = %+v, want one", left)
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
// never counts as a screen with no dialog on it. The console's input waker
// reads its screen, so with the waker ended nothing can.
func TestANativeSpawnStopsWhenItCannotReadTheScreen(t *testing.T) {
	previous := nativeReadGrace
	nativeReadGrace = 2 * time.Second
	t.Cleanup(func() { nativeReadGrace = previous })
	f := newNativeFixture(t, harness.Claude, "unreadable")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "terminal task-7") || !strings.Contains(err.Error(), "input waker") {
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

// A native terminal starts claude.exe itself, and codex and pi by the path
// PATH finds them at, through cmd /c with arguments cmd reads as plain text; a
// claude found only as a script shim, or an argument cmd would interpret, is
// refused.
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
	if err != nil || !slices.Equal(program, []string{`C:\Windows\System32\cmd.exe`, "/c", filepath.Join(bin, "codex.cmd"), "--model", "gpt-6-astra", "-c", "model_reasoning_effort=high"}) {
		t.Errorf("codex = %q, %v; want the codex.cmd on PATH through cmd /c", program, err)
	}
	for _, arg := range []string{"a&b", `"quoted"`, "50%", "a|b"} {
		if program, err := nativeProgram(harness.Pi, harness.Launch{Executable: "pi", Args: []string{arg}}); err == nil {
			t.Errorf("pi with %q = %q, want refused", arg, program)
		}
	}
}

// A harness installed as a script shim that this process's PATH, where its
// spawn validated it, does not find is refused, never left to the terminal's
// PATH to find another.
func TestAShimHarnessNotOnThisProcessPathIsRefused(t *testing.T) {
	// Arrange
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)

	// Act
	program, err := nativeProgram(harness.Codex, harness.Launch{Executable: "codex", Args: []string{"--model", "gpt-6-astra"}})

	// Assert
	if err == nil {
		t.Errorf("codex not on PATH = %q, want refused", program)
	}
}

// NativeProgram, which starts the CFO's harness and the connection checks',
// starts a script shim by the path this process's PATH finds it at, so a
// terminal or command whose PATH is the user's never starts another (#1155),
// and refuses a name this PATH does not find.
func TestNativeProgramStartsAShimByThePathThisProcessFinds(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "codex.cmd"), "@echo off\r\n")
	t.Setenv("PATH", bin)
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)

	// Act
	program, err := NativeProgram("codex", "mcp", "list")
	missing, missingErr := NativeProgram("pi", "--help")

	// Assert
	if err != nil || !slices.Equal(program, []string{`C:\Windows\System32\cmd.exe`, "/c", filepath.Join(bin, "codex.cmd"), "mcp", "list"}) {
		t.Errorf("codex = %q, %v; want the codex.cmd on PATH through cmd /c", program, err)
	}
	if missingErr == nil {
		t.Errorf("pi not on PATH = %q, want refused", missing)
	}
}

// A shim whose path has a space in it is quoted on cmd's command line, and
// once another argument is quoted too, cmd drops the path's opening quote and
// runs the path's first word instead. Such a shim starts by its short name,
// which has no space, and one on a volume that keeps no short names, such as
// a Dev Drive, is refused, never left to cmd to start another program.
func TestAShimAtAPathWithASpaceStartsAsItselfOrIsRefused(t *testing.T) {
	for name, program := range map[string]func() ([]string, error){
		"a goblin's harness": func() ([]string, error) {
			return nativeProgram(harness.Codex, harness.Launch{Executable: "codex", Args: []string{"--prompt", "a prompt with spaces"}})
		},
		"a named program": func() ([]string, error) { return NativeProgram("codex", "--prompt", "a prompt with spaces") },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			bin := filepath.Join(t.TempDir(), "npm shims")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(bin, "codex.cmd"), "@echo args=[%*]\r\n")
			t.Setenv("PATH", bin)
			from, err := windows.UTF16PtrFromString(filepath.Join(bin, "codex.cmd"))
			if err != nil {
				t.Fatal(err)
			}
			buffer := make([]uint16, windows.MAX_LONG_PATH)
			length, err := windows.GetShortPathName(from, &buffer[0], uint32(len(buffer)))
			if err != nil {
				t.Fatal(err)
			}
			hasShortName := !strings.ContainsAny(windows.UTF16ToString(buffer[:length]), " \t")

			// Act
			command, err := program()

			// Assert
			if !hasShortName {
				if err == nil {
					t.Errorf("a shim at %s, which has no short name = %q, want refused", bin, command)
				}
				return
			}
			if err != nil {
				t.Fatalf("a shim at %s: %v", bin, err)
			}
			output, err := execx.Command(command[0], command[1:]...).CombinedOutput()
			if want := `args=[--prompt "a prompt with spaces"]`; err != nil || strings.TrimSpace(string(output)) != want {
				t.Errorf("%q printed %q, %v; want the shim at %s to print %s", command, output, err, bin, want)
			}
		})
	}
}

// On 2026-10-17 a rig proving Codex spawns with a stub codex first on its own
// PATH started six real Codex sessions (#1075): the spawn validated the stub,
// and its terminal ran cmd /c codex, which found the user's real codex on the
// PATH of the user's environment the terminal starts from. A spawn starts the
// codex it validated, the one first on its own PATH, whatever the terminal's
// PATH finds.
func TestANativeSpawnStartsTheHarnessItValidatedNotTheOneOnTheUsersPath(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	t.Setenv("CFO_CODEX_GUARD", "")
	users := t.TempDir()
	ran := filepath.Join(t.TempDir(), "users-codex-ran.txt")
	writeFile(t, filepath.Join(users, "codex.cmd"), "@echo ran> \""+ran+"\"\r\n")
	f.userEnv = slices.DeleteFunc(f.userEnv, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(name, "PATH")
	})
	f.userEnv = append(f.userEnv, "Path="+users)
	// The premise: codex started by name in the user's environment is the
	// user's own.
	premise := exec.Command(os.Getenv("ComSpec"), "/d", "/c", "codex")
	premise.Env = f.userEnv
	if output, err := premise.CombinedOutput(); err != nil {
		t.Fatalf("codex in the user's environment: %v: %s", err, output)
	}
	if err := os.Remove(ran); err != nil {
		t.Fatalf("codex in the user's environment is not the user's own: %v", err)
	}

	// Act
	_, err := f.service.Spawn(t.Context(), f.request)

	// Assert
	if _, statErr := os.Stat(ran); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the spawn started the codex on the user's PATH (%v)", statErr)
	}
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if started := named(f.events(t), "env"); len(started) != 1 {
		t.Errorf("the stub on the spawner's PATH started %d times, want once", len(started))
	}
}

// fixtureCredentials is a project's credentials preflight that returns them.
type fixtureCredentials map[string]string

func (c fixtureCredentials) Preflight(context.Context, string, auth.Need) (auth.Result, error) {
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
		Env:        map[string]string{"GOTMPDIR": spec.Scratch, "TEMP": spec.Scratch, "TMPDIR": spec.Scratch, "TMP": home.SharedTempBeside(spec.Scratch), harness.RoleVariable: harness.RoleGoblin},
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
	env := service.nativeHostEnvironment(userEnv, harness.Launch{Env: map[string]string{harness.RoleVariable: harness.RoleGoblin, "CFO_TASK_ID": "g1"}}, auth.Result{})

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

// Every cfo command a goblin runs acts on the home that spawned it, root and
// state alike, whatever home the user's environment names. A second home on
// the machine spawns its goblins from a user environment whose CFO_HOME is the
// first install's: on 2026-10-07 a scratch home's goblin resolved that home's
// root with its own home's state, so it merged its helper but could not
// retire it, and its cfo kill was refused as needing a primary home.
func TestANativeGoblinResolvesTheHomeThatSpawnedIt(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	f.userEnv = append(f.userEnv, "CFO_HOME="+t.TempDir())
	spawningRoot := filepath.Dir(f.stateDir)

	// Act
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Assert
	resolved := named(f.events(t), "home")
	if len(resolved) != 1 || resolved[0].Home == nil {
		t.Fatalf("the goblin's home.Resolve = %+v, want one resolved home", resolved)
	}
	if got := *resolved[0].Home; !strings.EqualFold(got.Root, spawningRoot) || !strings.EqualFold(got.State, f.stateDir) {
		t.Errorf("the goblin resolves root %s and state %s, want the spawning home's root %s and state %s", got.Root, got.State, spawningRoot, f.stateDir)
	}
}

// recordedValue is a recorded variable's value, or that it was not set.
func recordedValue(value *string) string {
	if value == nil {
		return "not set"
	}
	return *value
}

// A goblin's terminal names the projects root its spawner names, as the CFO's
// terminal does, so a home started with a projects root of its own never
// gives its goblins the machine's, which the user's environment names; a
// spawner that names none leaves the user's.
func TestANativeGoblinKeepsTheProjectsRootItsSpawnerNames(t *testing.T) {
	machineRoot := `C:\dev`
	for name, test := range map[string]struct {
		spawners string
		want     string
	}{
		"a projects root of its own": {`C:\scratch\projects`, `C:\scratch\projects`},
		"none of its own":            {"", machineRoot},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newQuickFixture(t)
			f.userEnv = append(f.userEnv, install.ProjectsRootVariable+"="+machineRoot)
			f.service.ProjectsRoot = test.spawners

			// Act
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}

			// Assert
			got := named(f.events(t), "env")[0].Env[install.ProjectsRootVariable]
			if got == nil || *got != test.want {
				t.Errorf("the goblin's %s is %v, want %q", install.ProjectsRootVariable, recordedValue(got), test.want)
			}
		})
	}
}
