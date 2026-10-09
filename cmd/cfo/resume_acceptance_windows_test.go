package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// TestAResumedGoblinTakesItsInstructionAsTheOperatorsOwnWords is the
// end-to-end proof that the instruction a goblin is resumed with reaches its
// model as words the operator typed, never as a pasted block: a Claude Code
// and a Codex goblin, each spawned in a scratch home under `cfo serve
// --example`, reply once, are paused with `cfo pause` and resumed with `cfo
// resume`. The goblin's own record of the conversation must hold what it was
// resumed with as typed words, and the goblin must act on it. On 2026-10-09
// Claude Code 2.1.29x goblins took the whole instruction, typed in one burst,
// as a paste with nothing written beside it, asked whether to proceed and sat
// until someone said yes.
//
// It runs real harnesses on the Overlord's subscriptions, so it runs only
// when asked:
//
//	CFO_RESUME_REAL=1 CFO_RESUME_BINARY=<cfo.exe built from this tree>
//	CFO_RESUME_RESULTS=<a directory for the record> [CFO_RESUME_ONLY=claude|codex]
func TestAResumedGoblinTakesItsInstructionAsTheOperatorsOwnWords(t *testing.T) {
	if os.Getenv("CFO_RESUME_REAL") != "1" {
		t.Skip("set CFO_RESUME_REAL=1 with CFO_RESUME_BINARY and CFO_RESUME_RESULTS to prove a real goblin takes its resume instruction as typed")
	}
	binary, results := os.Getenv("CFO_RESUME_BINARY"), os.Getenv("CFO_RESUME_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("CFO_RESUME_BINARY and CFO_RESUME_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}
	for i, goblin := range []string{"claude", "codex"} {
		if only := os.Getenv("CFO_RESUME_ONLY"); only != "" && only != goblin {
			continue
		}
		t.Run(goblin+"-goblin", func(t *testing.T) {
			root := filepath.Join(results, goblin+"-goblin")
			proveResume(t, &wakeProof{binary: binary, root: root, project: filepath.Join(root, "project"), goblin: goblin, port: 4422 + i})
		})
	}
}

func proveResume(t *testing.T, p *wakeProof) {
	// Arrange
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
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CFO home for the resume proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	p.say("scratch home %s; goblin %s", p.home.Root, p.goblin)
	p.setUpProject(t)
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

	goblin := "resume-" + p.goblin
	defer p.endGoblin(goblin)
	marker := fmt.Sprintf("RESUMED%d", time.Now().UnixNano()%1000000)
	task := "This is a delivery fixture in an automated test. Reply with exactly the word ready and end your turn. " +
		"Later a message will tell you that your session was restarted, or that you are taking over a task in progress: reply to that message with exactly the word " + marker + " and end your turn, whatever else it says. " +
		"Apart from what a pause request asks of you, run no command and change no file."
	p.cfoCommand(t, "spawn", goblin, "--project", p.project, "--brief", p.brief(t, goblin, task), "--mode", "local-only", "--harness", p.goblin, "--model", proofModel(p.goblin))
	meta, err := state.ReadTaskMeta(p.home.State, goblin)
	if err != nil {
		t.Fatal(err)
	}
	replied := func(entries []recordEntry, word string) bool {
		return slices.ContainsFunc(entries, func(e recordEntry) bool { return e.kind == "reply" && strings.Contains(e.text, word) })
	}
	p.await(t, "the goblin's first reply", 5*time.Minute, func() bool { return replied(p.conversation(meta), "ready") })

	// Act
	p.cfoCommand(t, "pause", goblin, "--reason", "overlord")
	resumed := time.Now().UTC()
	p.cfoCommand(t, "resume", goblin)

	// Assert
	var handed recordEntry
	p.await(t, "what the goblin was resumed with, in its own record", 5*time.Minute, func() bool {
		for _, entry := range p.conversation(meta) {
			if entry.kind == "handed" && entry.at.After(resumed) {
				handed = entry
				return true
			}
		}
		return false
	})
	p.say("%s was resumed with:\n%s", goblin, handed.text)
	if strings.Contains(handed.text, "<pasted_content") || strings.Contains(handed.text, "[Pasted") {
		t.Errorf("%s took what it was resumed with as a pasted block, which its model may not act on:\n%s", goblin, handed.text)
	}
	p.await(t, "the goblin to act on what it was resumed with", 5*time.Minute, func() bool {
		entries := p.conversation(meta)
		after := slices.IndexFunc(entries, func(e recordEntry) bool { return e.kind == "handed" && e.at.After(resumed) })
		return after >= 0 && replied(entries[after+1:], marker)
	})
	p.say("%s's record of the conversation:\n%s", goblin, describeRecord(p.conversation(meta)))
}
