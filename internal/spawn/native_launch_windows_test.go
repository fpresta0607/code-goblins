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
)

// The test binary plays every part of a native spawn: with nativeSpawnHost
// first it is the terminal's host, and copied onto PATH as codex.exe or
// claude.exe it is the harness, which cmd /c or the spawn finds there.
const nativeSpawnHost = "native-spawn-host"

// The fake harness records what it sees to the file fakeCodexRecord names, and
// fakeCodexMode picks what it shows: codex's update prompt, then its trust
// prompt and composer by default or its hook review prompt ("hooks"), nothing
// a spawn would recognize ("silent"), or no console at all ("detached"). At
// its composer, a prompt can open as the typing starts ("late"), or a
// submitted line can leave it looking idle ("unmoved").
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
var recordedEnv = []string{"CFO_TASK_ID", "CFO_ROLE", "GOTMPDIR", "CFO_STATE_OVERRIDE", "CFO_HOST_ID", "FIXTURE_TOKEN", "OPENAI_API_KEY", "HERDR_PANE_ID", "CLAUDECODE"}

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
	record(codexEvent{Event: "env", PID: os.Getpid(), Env: env})
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
			key, err := readKey(reader)
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
	// Nothing may be typed before a screen asks for a key.
	time.Sleep(time.Second)
	for waiting := true; waiting; {
		select {
		case key := <-keys:
			record(codexEvent{Event: "typed blind", Text: key})
		default:
			waiting = false
		}
	}
	options := []string{"1. Update now (runs `npm install -g @openai/codex`)", "2. Skip", "3. Skip until next version"}
	chosen := choose(keys, record, func(focus int) {
		rows := []string{"", "  ✨ Update available! 0.154.0 -> 0.157.0", "", "  Release notes: https://github.com/openai/codex/releases/latest", ""}
		rows = append(rows, focusRows(options, focus)...)
		draw(append(rows, "", "  Press enter to continue")...)
	})
	record(codexEvent{Event: "update prompt", Text: options[chosen]})
	if chosen == 0 {
		return
	}
	if mode == "hooks" {
		draw("", "  Hooks need review", "  1 hook is new or changed.", "  Hooks can run outside the sandbox after you trust them.", "",
			"› 1. Review hooks", "  2. Trust all and continue", "  3. Continue without trusting (hooks won't run)", "", "  Press enter to confirm or esc to go back")
		for key := range keys {
			record(codexEvent{Event: "answered the hook prompt", Text: key})
		}
		return
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
		return
	}
	composer := func(text string) {
		draw("", "› "+text, "", "  ? for shortcuts                                                                    100% context left")
	}
	composer("Ask Codex to do anything")
	var line strings.Builder
	late := false
	for key := range keys {
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
			case key == "\r":
				record(codexEvent{Event: "submitted", Text: line.String()})
				// Unmoved, codex takes the line and never shows it working.
				if mode != "unmoved" {
					draw("", "› "+line.String(), "", "• Working (0s • esc to interrupt)")
					line.Reset()
				}
			case key == "\x1b[A" || key == "\x1b[B":
			default:
				line.WriteString(key)
			}
		}
		switch {
		case late:
			draw("", "  Something needs an answer first. Continue? (y/n)")
		case line.Len() > 0:
			composer(line.String())
		}
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
	// terminal is the host record the spawn started, once it recorded itself.
	terminal func() (host.Record, bool)
}

// newNativeFixture readies a native spawn of task-7 in the fake harness, as
// kind, shown mode, with a project credential and fleet variables a goblin
// must not get.
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
	f.request.Backend = "native"

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
	native := &nativeFixture{fixture: f, record: record, terminal: func() (host.Record, bool) {
		mu.Lock()
		defer mu.Unlock()
		return started, found
	}}
	// Everything the spawn started ends before the fake's folder goes.
	t.Cleanup(func() {
		stop()
		if err := closeNativeTerminal(f.stateDir, "task-7"); err != nil {
			t.Errorf("close the native terminal: %v", err)
		}
		if terminal, found := native.terminal(); found && !ended(terminal.HostPID) {
			t.Errorf("host pid %d is still running", terminal.HostPID)
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

// A native spawn answers codex's update prompt with Skip and its trust prompt
// with Yes, each only once it shows, then types the instruction once and
// submits it once codex's composer shows it. The goblin gets its project
// credentials and the launch's variables, and nothing of the fleet's own:
// no billing key, no Herdr pane, no Claude Code session marker.
func TestANativeSpawnAnswersCodexsStartupAndDeliversItsInstructionOnce(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")

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
	instruction := spawnInstruction(f.brief, "task-7")
	if submitted := named(events, "submitted"); len(submitted) != 1 || submitted[0].Text != instruction {
		t.Errorf("submitted = %+v, want the instruction once:\n%s", submitted, instruction)
	}
	env := named(events, "env")[0].Env
	want := map[string]string{"CFO_TASK_ID": "task-7", "CFO_ROLE": harness.RoleGoblin, "GOTMPDIR": goTmpDir(t, f.stateDir, "task-7"), "CFO_STATE_OVERRIDE": f.stateDir, "CFO_HOST_ID": "task-7", "FIXTURE_TOKEN": "t0ken"}
	for name, value := range want {
		if got := env[name]; got == nil || *got != value {
			t.Errorf("the goblin's %s = %v, want %q", name, got, value)
		}
	}
	for _, name := range []string{"OPENAI_API_KEY", "HERDR_PANE_ID", "CLAUDECODE"} {
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
}

// A prompt no spawn may answer, here codex's hook review, stops the spawn,
// naming the terminal and the prompt, without a key typed at it; the spawn's
// teardown ends the terminal, codex and its host, and retires the task.
func TestANativeSpawnStopsAtAPromptItMayNotAnswer(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "hooks")

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "native terminal task-7") || !strings.Contains(err.Error(), "the hook review prompt") {
		t.Fatalf("Spawn error = %v, want the hook review prompt in native terminal task-7", err)
	}
	events := f.events(t)
	if answered := named(events, "answered the hook prompt"); len(answered) != 0 {
		t.Errorf("keys typed at the hook review prompt: %+v", answered)
	}
	terminal, found := f.terminal()
	codex := named(events, "env")[0].PID
	if !found || !ended(terminal.HostPID) || !ended(codex) {
		t.Errorf("host %+v (recorded %v) and codex pid %d, want both ended", terminal, found, codex)
	}
	if _, err := os.Stat(filepath.Join(f.stateDir, "task-7.meta")); !os.IsNotExist(err) {
		t.Errorf("the task record is still there: %v", err)
	}
	if f.git.returned != 1 {
		t.Errorf("worktree returned %d times, want once", f.git.returned)
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
	writeFile(t, filepath.Join(bin, "claude.cmd"), "@echo off\r\n")
	t.Setenv("PATH", bin)
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)

	if program, err := nativeProgram(harness.Claude, harness.Launch{Args: []string{"--x"}}); err == nil {
		t.Errorf("claude as a script shim = %q, want refused", program)
	}
	program, err := nativeProgram(harness.Codex, harness.Launch{TypedLaunch: true, Executable: "codex", Args: []string{"--model", "gpt-6-astra", "-c", "model_reasoning_effort=high"}})
	if err != nil || !slices.Equal(program, []string{`C:\Windows\System32\cmd.exe`, "/c", "codex", "--model", "gpt-6-astra", "-c", "model_reasoning_effort=high"}) {
		t.Errorf("codex = %q, %v; want it through cmd /c", program, err)
	}
	for _, arg := range []string{"a&b", `"quoted"`, "50%", "a|b"} {
		if program, err := nativeProgram(harness.Pi, harness.Launch{TypedLaunch: true, Executable: "pi", Args: []string{arg}}); err == nil {
			t.Errorf("pi with %q = %q, want refused", arg, program)
		}
	}
}

// fixtureCredentials is a project's credentials preflight that returns them.
type fixtureCredentials map[string]string

func (c fixtureCredentials) Preflight(context.Context, string) (auth.Result, error) {
	return auth.Result{Env: c}, nil
}

// nativeAdapter builds kind's launch the way its adapter does: codex typed
// through its shim, claude started as its own program.
type nativeAdapter struct {
	kind harness.Kind
}

func (a nativeAdapter) Kind() harness.Kind { return a.kind }

func (nativeAdapter) Validate(context.Context, execx.Runner) error { return nil }

func (nativeAdapter) Control() harness.Control { return harness.Control{} }

func (a nativeAdapter) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	launch := harness.Launch{
		Args:       []string{"--dangerously-skip-permissions"},
		Env:        map[string]string{"GOTMPDIR": spec.GoTmp, harness.RoleVariable: harness.RoleGoblin},
		PromptFile: spec.BriefPath,
	}
	if a.kind == harness.Codex {
		launch.Args, launch.TypedLaunch, launch.Executable = []string{"--dangerously-bypass-approvals-and-sandbox"}, true, "codex"
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
