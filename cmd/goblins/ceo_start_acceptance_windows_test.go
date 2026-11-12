package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// TestACEOStartedAsGoblinsStartsItRegistersIsWokenAndComesBack is the
// end-to-end proof that a real Codex and a real pi CEO, started the way
// goblins and the first-run page start one, work: the command line and the
// first prompt are the product's own, the terminal opens in the home, and
// nothing but that first prompt tells the CEO to register. Each runs in a
// scratch home that holds this tree's AGENTS.md, under `goblins serve --example`.
// The CEO must register itself as its harness, the board must say a CEO
// runs, and a wake queued afterwards must reach it through the line the
// supervisor types and be acknowledged by its own goblins drain. Its terminal is
// then closed, and a harness the capability table says resumes must come
// back, with the arguments goblins brings a closed CEO back with, on the
// conversation it registered with.
//
// A Claude Code CEO, which its SessionStart hook registers and its Stop hook
// wakes, runs the closing and coming back alone: it is given one line to
// answer, so it has a conversation, and the line must be on its screen once
// it is back.
//
// It runs real harnesses on the Overlord's subscriptions, so it runs only
// when asked:
//
//	GOBLINS_START_REAL=1 GOBLINS_START_BINARY=<goblins.exe built from this tree>
//	GOBLINS_START_RESULTS=<a directory for the record> [GOBLINS_START_ONLY=claude|codex|pi]
//	[GOBLINS_START_CODEX_MODEL=<a model to name when the configured one is refused>]
func TestACEOStartedAsGoblinsStartsItRegistersIsWokenAndComesBack(t *testing.T) {
	if os.Getenv("GOBLINS_START_REAL") != "1" {
		t.Skip("set GOBLINS_START_REAL=1 with GOBLINS_START_BINARY and GOBLINS_START_RESULTS to prove the CEO's start with real harnesses")
	}
	binary, results := os.Getenv("GOBLINS_START_BINARY"), os.Getenv("GOBLINS_START_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("GOBLINS_START_BINARY and GOBLINS_START_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset GOBLINS_HOME and GOBLINS_STATE_OVERRIDE first", root, state)
	}
	instructions, err := fs.ReadFile(codegoblins.Contract, "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	for i, agent := range []string{"codex", "pi", "claude"} {
		if only := os.Getenv("GOBLINS_START_ONLY"); only != "" && only != agent {
			continue
		}
		t.Run(agent+"-ceo", func(t *testing.T) {
			proveCEOStart(t, &wakeProof{binary: binary, root: filepath.Join(results, agent+"-ceo"), ceo: agent, port: 4396 + i}, string(instructions))
		})
	}
}

func proveCEOStart(t *testing.T, p *wakeProof, instructions string) {
	if err := os.MkdirAll(p.root, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(p.root, "proof.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	p.log = logFile
	p.home = home.Home{Root: filepath.Join(p.root, "home"), State: filepath.Join(p.root, "home", "state"), Data: filepath.Join(p.root, "home", "data")}
	for _, dir := range []string{p.home.State, p.home.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := fs.WalkDir(codegoblins.Contract, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(codegoblins.Contract, name)
		if err == nil {
			writeProofFile(t, filepath.Join(p.home.Root, filepath.FromSlash(name)), string(data))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), instructions)
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	p.say("scratch home %s; CEO %s", p.home.Root, p.ceo)
	p.seen = map[int]wake.Record{}
	watching, stopWatching := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() { defer close(watched); p.watchQueue(watching) }()
	defer func() { stopWatching(); <-watched }()
	p.env = p.environment()

	serve := exec.Command(p.binary, "serve", "--example", "--listen", fmt.Sprintf("127.0.0.1:%d", p.port))
	serve.Env = p.env
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	p.say("goblins serve --example pid %d", serve.Process.Pid)
	p.await(t, "the board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })

	// The start is startNativeCEO's own: the product's command line and first
	// prompt, in the home, in native terminal ceo. Only the host binary and
	// the environment are the proof's, so that goblins in the terminal is this
	// tree's build rather than the one on this machine's PATH.
	var named []string
	if model := os.Getenv("GOBLINS_START_CODEX_MODEL"); model != "" && p.ceo == "codex" {
		named = []string{"-m", model}
	}
	if p.ceo == "claude" {
		named = p.claudeArguments(t)
	}
	start := func(args []string) host.Record {
		screen, err := ceoScreenArguments(p.ceo)
		if err != nil {
			t.Fatal(err)
		}
		program, err := nativeCEOProgram(p.ceo, ceoStartArguments(p.ceo, slices.Concat(args, screen), "")...)
		if err != nil {
			t.Fatal(err)
		}
		p.say("the CEO's command line: %q", program)
		launched, err := host.Launch(p.home.State, []string{p.binary, "host"}, p.env, host.Spec{ID: supervisor.NativeCEOTerminal, Args: program, Dir: p.home.Root, Cols: 120, Rows: 40})
		if err != nil {
			t.Fatal(err)
		}
		// Found while it runs, so the end below reaches it, and no later
		// process that took its pid once the proof closed the terminal.
		hostProcess, err := os.FindProcess(launched.HostPID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = hostProcess.Kill()
			_ = hostProcess.Release()
		})
		p.ceoTerminal = &launched
		p.say("CEO %s runs in native terminal ceo, host pid %d, harness pid %d", p.ceo, launched.HostPID, launched.ChildPID)
		return launched
	}
	screen := func(terminal host.Record, when string) {
		lines, err := host.ReadScreen(terminal)
		p.say("the CEO's screen %s (read error %v):\n%s", when, err, host.ScreenTail(lines, 40))
	}
	launched := start(named)
	p.settle(t, launched)
	screen(launched, "once its startup settled")

	p.await(t, "the CEO to register itself", 5*time.Minute, func() bool {
		primary, live := livePrimaryFile(p.home.State)
		return live && primary == p.ceo
	})
	primary, err := os.ReadFile(filepath.Join(p.home.State, "primary.json"))
	p.say("the CEO registered (read error %v): %s", err, primary)
	screen(launched, "once it registered")

	setup := p.boardSetup(t)
	p.say("the board's first-run answer: %s", setup)
	if !strings.Contains(setup, `"ceo_runs":true`) {
		t.Errorf("the board does not say a CEO runs: %s", setup)
	}

	p.await(t, "the CEO's first turn to end", 10*time.Minute, func() bool { return p.idle(launched) })
	screen(launched, "once its first turn ended")
	// said is on the screen of a Claude Code CEO that came back on its
	// conversation: the one line it was given before its terminal closed.
	const said = "scratch-proof-7421"
	if supervisor.CEOWakeFor(p.ceo) == supervisor.CEOWakeTyped {
		// The wake is queued once the CEO's first turn has ended, so that
		// only the typed line can tell it.
		record, err := wake.Append(p.home.State, "notify", "scratch-proof", "done: a test report from the scratch-home proof of the CEO's start; nothing to do but acknowledge it")
		if err != nil {
			t.Fatal(err)
		}
		p.say("queued wake %d %s %s: %s", record.Seq, record.Kind, record.Key, record.Detail)
		p.expectAcked(t, record)
		screen(launched, "after it acknowledged the wake")
	} else {
		p.typeLine(t, launched, "Reply with the single word "+said+" and end your turn. Run no command and change no file.")
		p.await(t, "the CEO to answer its one line", 5*time.Minute, func() bool { return p.shows(launched, said, 2) && p.idle(launched) })
		screen(launched, "after it answered its one line")
	}

	// A closed CEO comes back as goblins brings one back: on the conversation
	// its registration recorded, where the table says its harness resumes.
	capability, _ := supervisor.CEOCapabilityFor(p.ceo)
	resume, why := ceoResume(p.home, p.ceo)
	p.say("goblins brings a closed %s CEO back with %q (%s)", p.ceo, resume, why)
	if capability.Resumes != (len(resume) > 0) {
		t.Fatalf("the table says a %s CEO resumes: %v, but goblins would bring it back with %q (%s)", p.ceo, capability.Resumes, resume, why)
	}
	if !capability.Resumes {
		return
	}
	before, err := supervisor.ReadCEOConversation(p.home.State)
	if err != nil {
		t.Fatal(err)
	}
	p.say("the conversation it registered with: %+v", before)
	p.await(t, "the CEO's turn to end before its terminal is closed", 5*time.Minute, func() bool { return p.idle(launched) })
	// Ending the terminal's host ends the harness, as closing its window does.
	if process, err := os.FindProcess(launched.HostPID); err == nil {
		p.say("closing the CEO's terminal, host pid %d: %v", launched.HostPID, process.Kill())
	}
	p.await(t, "the closed CEO's terminal to end", time.Minute, func() bool {
		_, err := host.ReadScreen(launched)
		return err != nil
	})
	reopened := start(append(slices.Clone(named), resume...))
	p.settle(t, reopened)
	var after supervisor.CEOConversation
	p.await(t, "the reopened CEO to register itself", 5*time.Minute, func() bool {
		conversation, err := supervisor.ReadCEOConversation(p.home.State)
		after = conversation
		return err == nil && conversation.PID != before.PID
	})
	p.say("the reopened CEO registered with: %+v", after)
	screen(reopened, "once it registered again")
	if after.Harness != p.ceo {
		t.Fatalf("the reopened CEO registered as %+v, want %s", after, p.ceo)
	}
	if supervisor.CEOWakeFor(p.ceo) == supervisor.CEOWakeTyped {
		if after.Session != before.Session {
			t.Fatalf("the reopened CEO registered with %+v, want the conversation it had, %s", after, before.Session)
		}
		return
	}
	p.await(t, "the reopened CEO to show the conversation it had", time.Minute, func() bool { return p.shows(reopened, said, 1) })
	screen(reopened, "showing the conversation it had")
}

// typeLine types text into the CEO's terminal and submits it.
func (p *wakeProof) typeLine(t *testing.T, record host.Record, text string) {
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Input([]byte(text)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := client.Input([]byte("\r")); err != nil {
		t.Fatal(err)
	}
}

// shows reports whether the CEO's screen holds text at least times times.
func (p *wakeProof) shows(record host.Record, text string, times int) bool {
	screen, err := host.ReadScreen(record)
	return err == nil && strings.Count(strings.Join(screen, "\n"), text) >= times
}

// idle reports whether the CEO's harness sits at its composer, in five reads
// a second apart. An offer drawn above the composer, such as Codex's Daybreak
// security offer, live on 2026-10-22 after a CEO's first turn, holds nothing
// back: a wake dismisses a live one before it types (#1556).
func (p *wakeProof) idle(record host.Record) bool {
	kind, _ := harness.NativeScreens(harness.Kind(p.ceo))
	for range 5 {
		screen, err := host.ReadScreen(record)
		if err != nil || !kind.IsReady(screen) || kind.IsWorking(screen) {
			return false
		}
		time.Sleep(time.Second)
	}
	return true
}

// boardSetup is what the scratch board's first-run page is told.
func (p *wakeProof) boardSetup(t *testing.T) string {
	response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/setup?root=", p.port))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
