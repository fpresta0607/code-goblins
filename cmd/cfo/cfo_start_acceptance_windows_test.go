package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// TestACFOStartedAsGoblinsStartsItRegistersIsWokenAndComesBack is the
// end-to-end proof that a real Codex and a real pi CFO, started the way
// goblins and the first-run page start one, work: the command line and the
// first prompt are the product's own, the terminal opens in the home, and
// nothing but that first prompt tells the CFO to register. Each runs in a
// scratch home that holds this tree's AGENTS.md, under `cfo serve --example`.
// The CFO must register itself as its harness, the board must say a CFO
// runs, and a wake queued afterwards must reach it through the line the
// supervisor types and be acknowledged by its own cfo drain. Its terminal is
// then closed, and a harness the capability table says resumes must come
// back, with the arguments goblins brings a closed CFO back with, on the
// conversation it registered with.
//
// A Claude Code CFO, which its SessionStart hook registers and its Stop hook
// wakes, runs the closing and coming back alone: it is given one line to
// answer, so it has a conversation, and the line must be on its screen once
// it is back.
//
// It runs real harnesses on the Overlord's subscriptions, so it runs only
// when asked:
//
//	CFO_START_REAL=1 CFO_START_BINARY=<cfo.exe built from this tree>
//	CFO_START_RESULTS=<a directory for the record> [CFO_START_ONLY=claude|codex|pi]
//	[CFO_START_CODEX_MODEL=<a model to name when the configured one is refused>]
func TestACFOStartedAsGoblinsStartsItRegistersIsWokenAndComesBack(t *testing.T) {
	if os.Getenv("CFO_START_REAL") != "1" {
		t.Skip("set CFO_START_REAL=1 with CFO_START_BINARY and CFO_START_RESULTS to prove the CFO's start with real harnesses")
	}
	binary, results := os.Getenv("CFO_START_BINARY"), os.Getenv("CFO_START_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("CFO_START_BINARY and CFO_START_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}
	instructions, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for i, agent := range []string{"codex", "pi", "claude"} {
		if only := os.Getenv("CFO_START_ONLY"); only != "" && only != agent {
			continue
		}
		t.Run(agent+"-cfo", func(t *testing.T) {
			proveCFOStart(t, &wakeProof{binary: binary, root: filepath.Join(results, agent+"-cfo"), cfo: agent, port: 4396 + i}, string(instructions))
		})
	}
}

func proveCFOStart(t *testing.T, p *wakeProof, instructions string) {
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
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), instructions)
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	p.say("scratch home %s; CFO %s", p.home.Root, p.cfo)
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
	p.say("cfo serve --example pid %d", serve.Process.Pid)
	p.await(t, "the board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })

	// The start is startNativeCFO's own: the product's command line and first
	// prompt, in the home, in native terminal cfo. Only the host binary and
	// the environment are the proof's, so that cfo in the terminal is this
	// tree's build rather than the one on this machine's PATH.
	var named []string
	if model := os.Getenv("CFO_START_CODEX_MODEL"); model != "" && p.cfo == "codex" {
		named = []string{"-m", model}
	}
	if p.cfo == "claude" {
		named = p.claudeArguments(t)
	}
	start := func(args []string) host.Record {
		program, err := nativeCFOProgram(p.cfo, cfoStartArguments(p.cfo, args)...)
		if err != nil {
			t.Fatal(err)
		}
		p.say("the CFO's command line: %q", program)
		launched, err := host.Launch(p.home.State, []string{p.binary, "host"}, p.env, host.Spec{ID: supervisor.NativeCFOTerminal, Args: program, Dir: p.home.Root, Cols: 120, Rows: 40})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if process, err := os.FindProcess(launched.HostPID); err == nil {
				_ = process.Kill()
			}
		})
		p.cfoTerminal = &launched
		p.say("CFO %s runs in native terminal cfo, host pid %d, harness pid %d", p.cfo, launched.HostPID, launched.ChildPID)
		return launched
	}
	screen := func(terminal host.Record, when string) {
		lines, err := host.ReadScreen(terminal)
		p.say("the CFO's screen %s (read error %v):\n%s", when, err, host.ScreenTail(lines, 40))
	}
	launched := start(named)
	p.settle(t, launched)
	screen(launched, "once its startup settled")

	p.await(t, "the CFO to register itself", 5*time.Minute, func() bool {
		primary, live := livePrimaryFile(p.home.State)
		return live && primary == p.cfo
	})
	primary, err := os.ReadFile(filepath.Join(p.home.State, "primary.json"))
	p.say("the CFO registered (read error %v): %s", err, primary)
	screen(launched, "once it registered")

	setup := p.boardSetup(t)
	p.say("the board's first-run answer: %s", setup)
	if !strings.Contains(setup, `"cfo_runs":true`) {
		t.Errorf("the board does not say a CFO runs: %s", setup)
	}

	p.await(t, "the CFO's first turn to end", 10*time.Minute, func() bool { return p.idle(launched) })
	screen(launched, "once its first turn ended")
	// said is on the screen of a Claude Code CFO that came back on its
	// conversation: the one line it was given before its terminal closed.
	const said = "scratch-proof-7421"
	if supervisor.CFOWakeFor(p.cfo) == supervisor.CFOWakeTyped {
		// The wake is queued once the CFO's first turn has ended, so that
		// only the typed line can tell it.
		record, err := wake.Append(p.home.State, "notify", "scratch-proof", "done: a test report from the scratch-home proof of the CFO's start; nothing to do but acknowledge it")
		if err != nil {
			t.Fatal(err)
		}
		p.say("queued wake %d %s %s: %s", record.Seq, record.Kind, record.Key, record.Detail)
		p.expectAcked(t, record)
		screen(launched, "after it acknowledged the wake")
	} else {
		p.typeLine(t, launched, "Reply with the single word "+said+" and end your turn. Run no command and change no file.")
		p.await(t, "the CFO to answer its one line", 5*time.Minute, func() bool { return p.shows(launched, said, 2) && p.idle(launched) })
		screen(launched, "after it answered its one line")
	}

	// A closed CFO comes back as goblins brings one back: on the conversation
	// its registration recorded, where the table says its harness resumes.
	capability, _ := supervisor.CFOCapabilityFor(p.cfo)
	resume, why := cfoResume(p.home, p.cfo)
	p.say("goblins brings a closed %s CFO back with %q (%s)", p.cfo, resume, why)
	if capability.Resumes != (len(resume) > 0) {
		t.Fatalf("the table says a %s CFO resumes: %v, but goblins would bring it back with %q (%s)", p.cfo, capability.Resumes, resume, why)
	}
	if !capability.Resumes {
		return
	}
	before, err := supervisor.ReadCFOConversation(p.home.State)
	if err != nil {
		t.Fatal(err)
	}
	p.say("the conversation it registered with: %+v", before)
	p.await(t, "the CFO's turn to end before its terminal is closed", 5*time.Minute, func() bool { return p.idle(launched) })
	// Ending the terminal's host ends the harness, as closing its window does.
	if process, err := os.FindProcess(launched.HostPID); err == nil {
		p.say("closing the CFO's terminal, host pid %d: %v", launched.HostPID, process.Kill())
	}
	p.await(t, "the closed CFO's terminal to end", time.Minute, func() bool {
		_, err := host.ReadScreen(launched)
		return err != nil
	})
	reopened := start(append(slices.Clone(named), resume...))
	p.settle(t, reopened)
	var after supervisor.CFOConversation
	p.await(t, "the reopened CFO to register itself", 5*time.Minute, func() bool {
		conversation, err := supervisor.ReadCFOConversation(p.home.State)
		after = conversation
		return err == nil && conversation.PID != before.PID
	})
	p.say("the reopened CFO registered with: %+v", after)
	screen(reopened, "once it registered again")
	if after.Harness != p.cfo {
		t.Fatalf("the reopened CFO registered as %+v, want %s", after, p.cfo)
	}
	if supervisor.CFOWakeFor(p.cfo) == supervisor.CFOWakeTyped {
		if after.Session != before.Session {
			t.Fatalf("the reopened CFO registered with %+v, want the conversation it had, %s", after, before.Session)
		}
		return
	}
	p.await(t, "the reopened CFO to show the conversation it had", time.Minute, func() bool { return p.shows(reopened, said, 1) })
	screen(reopened, "showing the conversation it had")
}

// typeLine types text into the CFO's terminal and submits it.
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

// shows reports whether the CFO's screen holds text at least times times.
func (p *wakeProof) shows(record host.Record, text string, times int) bool {
	screen, err := host.ReadScreen(record)
	return err == nil && strings.Count(strings.Join(screen, "\n"), text) >= times
}

// idle reports whether the CFO's harness sits at its composer, in five reads
// a second apart.
func (p *wakeProof) idle(record host.Record) bool {
	kind, _ := harness.NativeScreens(harness.Kind(p.cfo))
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
