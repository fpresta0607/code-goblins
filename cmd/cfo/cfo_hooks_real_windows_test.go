package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// TestARealClaudeCFOGetsItsHooksThroughItsTerminalAlone is the proof, with
// real Claude Code, that a CFO started the way the product starts it
// (startNativeCFO) runs the CFO's hooks from the settings its start hands it
// and from nowhere else. In a scratch home it sees three things arrive: the
// digest at the session's start, which registers the CFO, a guard refusing a
// Bash call, and a rewake for a goblin's report, which the CFO drains and
// acknowledges. The CFO is told to use its Bash tool: asked only for a shell
// command, Claude Code on Windows took its PowerShell tool, which the Bash
// guards do not see.
//
// The user's own settings are kept out with --setting-sources project,local,
// which is passed through the start as any argument is: on a machine an older
// build installed they still hold the CFO's hooks, naming the fleet's own
// cfo.exe, and their copies would fire beside these. The scratch home's
// project settings hold no hook, so what fires came through --settings.
//
// It runs real Claude Code on the Overlord's subscription for two short
// turns, so it runs only when asked:
//
//	CFO_HOOKS_REAL=1 CFO_HOOKS_BINARY=<cfo.exe built from this tree>
//	CFO_HOOKS_RESULTS=<an empty directory for the scratch home and the record>
func TestARealClaudeCFOGetsItsHooksThroughItsTerminalAlone(t *testing.T) {
	if os.Getenv("CFO_HOOKS_REAL") != "1" {
		t.Skip("set CFO_HOOKS_REAL=1 with CFO_HOOKS_BINARY and CFO_HOOKS_RESULTS to prove the hooks with real Claude Code")
	}
	binary, results := os.Getenv("CFO_HOOKS_BINARY"), os.Getenv("CFO_HOOKS_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("CFO_HOOKS_BINARY and CFO_HOOKS_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}

	// Arrange: a scratch home with its own cfo.exe and one goblin in flight.
	h := home.Home{Root: filepath.Join(results, "home"), State: filepath.Join(results, "home", "state"), Data: filepath.Join(results, "home", "data")}
	for _, dir := range []string{h.State, h.Data, filepath.Join(h.Root, home.BinDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	logFile, err := os.Create(filepath.Join(results, "proof.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	p := &wakeProof{cfo: "claude", root: results, home: h, log: logFile}
	say := func(format string, args ...any) {
		p.say(format, args...)
		t.Logf(format, args...)
	}
	writeProofFile(t, filepath.Join(h.Root, "AGENTS.md"), "# Scratch CFO home for the hooks proof\n")
	writeProofFile(t, filepath.Join(h.Root, home.InstalledMarker), "")
	cfo := filepath.Join(h.Root, home.BinDir, "cfo.exe")
	copyFile(t, binary, cfo)
	writeMetaFixture(t, h.State, "g1.meta")
	// The scratch home's watcher sweeps no machine for orphans: this one
	// runs a fleet of its own.
	if err := reap.WriteRecord(h.State, reap.Record{Time: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	prompt := "You stand in for the CFO of a scratch Code Goblins home in an automated test. Use the shell only as told. First use your Bash tool, never PowerShell, to run the command cd /c/Windows exactly once, reply with the bracketed code its refusal begins with, and end your turn. After that, every time a line starting with cfo watcher wake reaches you, run " + cfo + " drain, then run the WAKE_ACK_REQUIRED command it prints with " + cfo + " in place of cfo, then reply with one short line naming each drained record by kind and key, and end your turn. Run no other command and change no file."

	// Act: the product's own start of the CFO.
	if err := startNativeCFO(h, h.Root, "claude", []string{"--setting-sources", "project,local", "--dangerously-skip-permissions", prompt}); err != nil {
		t.Fatalf("startNativeCFO: %v", err)
	}
	t.Cleanup(func() { closeNativeTerminal(t, h.State, supervisor.NativeCFOTerminal) })
	record, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	p.cfoTerminal = &record
	if identity, err := proc.Identify(record.ChildPID, record.ChildStart); err == nil {
		say("the CFO's harness, pid %d, started as: %s", record.ChildPID, strings.Join(identity.Arguments[:min(5, len(identity.Arguments))], " "))
	}
	settings, err := os.ReadFile(filepath.Join(h.State, cfoSettingsFile))
	if err != nil {
		t.Fatal(err)
	}
	say("%s names %s in %d hook entries", cfoSettingsFile, cfo, strings.Count(string(settings), strings.ReplaceAll(cfo, `\`, `\\`)))
	p.settle(t, record)
	await := func(what string, within time.Duration, done func() bool) {
		t.Helper()
		for deadline := time.Now().Add(within); !done(); time.Sleep(time.Second) {
			if time.Now().After(deadline) {
				screen, _ := host.ReadScreen(record)
				t.Fatalf("%s did not happen within %s; the CFO's screen ends:\n%s", what, within, host.ScreenTail(screen, 20))
			}
		}
		say("seen: %s", what)
	}

	// Assert
	await("the digest at the session's start, which registered the CFO (state\\primary.json names a live claude)", 3*time.Minute, func() bool {
		harness, isLive := livePrimaryFile(h.State)
		return isLive && harness == "claude"
	})
	if holder, err := lock.Read(h.State); err != nil || holder.PID != record.ChildPID {
		t.Errorf("the session lock names %+v (%v), want the CFO's harness pid %d", holder, err, record.ChildPID)
	}
	await("a guard: the pretool hook refused the CFO's cd with [cwd-relocation]", 5*time.Minute, func() bool {
		screen, err := host.ReadScreen(record)
		return err == nil && strings.Contains(host.ScreenTail(screen, 400), "cwd-relocation")
	})
	await("the auto-arm hook holding recovery after the turn ended (state\\.claude-autoarm.lock)", 5*time.Minute, func() bool {
		holder, err := lock.ReadNamed(h.State, autoarmLockName)
		if err != nil || !holder.Alive() {
			return false
		}
		if identity, err := proc.Identify(holder.PID, holder.Start); err == nil {
			say("the auto-arm hook, pid %d, is %s %s", holder.PID, identity.Image, strings.Join(identity.Arguments[1:], " "))
		}
		return true
	})
	if err := os.WriteFile(filepath.Join(h.State, "g1.status"), []byte("needs-decision: which way\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	say("the goblin g1 reported needs-decision")
	var signal wake.Record
	await("the goblin's report queued as a wake", 3*time.Minute, func() bool {
		pending, err := wake.Pending(h.State)
		for _, queued := range pending {
			if wake.DecisionSignal(queued, "g1") {
				signal = queued
			}
		}
		return err == nil && signal.Seq != 0
	})
	await(fmt.Sprintf("the rewake: the CFO drained and acknowledged wake %d", signal.Seq), 5*time.Minute, func() bool {
		isAcked, err := wake.Acked(h.State, signal.Seq)
		return err == nil && isAcked
	})
	if _, err := os.Stat(filepath.Join(h.State, custodyAudit)); err == nil {
		t.Errorf("state\\%s is there: the session lock changed hands during the proof", custodyAudit)
	}
}
