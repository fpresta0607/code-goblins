package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// This opt-in proof uses a scratch CODEX_HOME and invocation-only trust for
// the hook commands just installed there. It never changes user hook trust.
func TestInstalledCodexHooksReportTurnsAndWatcherReceipts(t *testing.T) {
	if os.Getenv("CFO_CODEX_HOOKS_REAL") != "1" {
		t.Skip("set CFO_CODEX_HOOKS_REAL=1, CFO_CODEX_HOOKS_BINARY and CFO_CODEX_HOOKS_RESULTS for the real scratch Codex hook proof")
	}
	binary, root := os.Getenv("CFO_CODEX_HOOKS_BINARY"), os.Getenv("CFO_CODEX_HOOKS_RESULTS")
	if !filepath.IsAbs(binary) || !filepath.IsAbs(root) || exists(root) {
		t.Fatal("the proof needs an absolute binary and an unoccupied absolute results directory")
	}
	if inheritedHome, inheritedState := home.Inherited(); inheritedHome != "" || inheritedState != "" {
		t.Fatal("clear CFO_HOME and CFO_STATE_OVERRIDE before running the proof")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(root, "proof.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	p := &wakeProof{binary: binary, root: root, project: filepath.Join(root, "project"), cfo: "codex", log: logFile}
	p.home = home.Home{Root: filepath.Join(root, "home"), State: filepath.Join(root, "home", "state"), Data: filepath.Join(root, "home", "data")}
	for _, dir := range []string{p.home.State, p.home.Data, p.project} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch native hook proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	configDir := filepath.Join(root, "codex")
	config := "check_for_update_on_startup = false\nmodel_reasoning_effort = \"low\"\n[projects." + strconv.Quote(p.project) + "]\ntrust_level = \"trusted\"\n"
	writeProofFile(t, filepath.Join(configDir, "config.toml"), config)
	writeProofFile(t, filepath.Join(configDir, "hooks.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"cmd.exe /c exit 0"}]}],"SessionEnd":[{"hooks":[{"type":"command","command":"cmd.exe /c exit 0"}]}]}}`)
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := os.ReadFile(filepath.Join(userHome, ".codex", "auth.json"))
	if err != nil {
		t.Fatal("the real proof needs an existing Codex file login")
	}
	authPath := filepath.Join(configDir, "auth.json")
	if err := os.WriteFile(authPath, auth, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(authPath) })
	p.env = slices.DeleteFunc(p.environment(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.ContainsFunc([]string{"CODEX_HOME", "CFO_ROLE", "CFO_TASK_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID", "NO_MISTAKES_GATE"}, func(owned string) bool { return strings.EqualFold(name, owned) })
	})
	p.env = append(p.env, "CODEX_HOME="+configDir, "CFO_ROLE=cfo")
	p.cfoCommand(t, "hooks", "check", "codex", "--config-dir", configDir)
	type hookCommand struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type hookSettings struct {
		Hooks map[string][]struct {
			Hooks []hookCommand `json:"hooks"`
		} `json:"hooks"`
	}
	before, err := os.ReadFile(filepath.Join(configDir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var baseline hookSettings
	if err := json.Unmarshal(before, &baseline); err != nil || len(baseline.Hooks) != 2 || len(baseline.Hooks["UserPromptSubmit"]) != 0 {
		t.Fatal("the baseline must contain only unrelated hooks")
	}
	p.cfoCommand(t, "hooks", "install", "codex", "--config-dir", configDir)
	after, err := os.ReadFile(filepath.Join(configDir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var installed hookSettings
	if err := json.Unmarshal(after, &installed); err != nil {
		t.Fatal(err)
	}
	command := `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "` + filepath.ToSlash(filepath.Join(configDir, "cfo-native-hook.ps1")) + `"`
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd", "SubagentStart", "SubagentStop", "Interrupt"} {
		owned := 0
		for _, group := range installed.Hooks[event] {
			for _, hook := range group.Hooks {
				if hook.Command == command && hook.Type == "command" && hook.Timeout > 0 && hook.Timeout <= 3 {
					owned++
				}
			}
		}
		if owned != 1 {
			t.Fatalf("%s has %d usable owned hooks, want one", event, owned)
		}
	}
	for _, event := range []string{"SessionStart", "SessionEnd"} {
		if installed.Hooks[event][0].Hooks[0] != baseline.Hooks[event][0].Hooks[0] {
			t.Fatalf("the installer changed the unrelated %s handler", event)
		}
	}
	p.say("definition: installed eight owned events; trust: explicit invocation-only bypass, no persisted trust proof")
	prompt := "You are the CFO of an isolated scratch home for a hook proof. First run cfo register then reply standing by and end your turn. On each cfo watcher wake line run cfo drain and the WAKE_ACK_REQUIRED command exactly as printed, then reply acknowledged and end your turn. Run no other commands and change no files."
	args, err := spawn.NativeProgram("codex", "--no-daemon", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "-m", proofModel("codex"), prompt)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	record, err := host.Launch(p.home.State, []string{binary, "host"}, p.env, host.Spec{ID: "cfo", Args: args, Dir: p.project, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if client, err := host.Dial(record); err == nil {
			_ = client.CloseTerminal()
			_ = client.Close()
		}
	})
	p.cfoTerminal = &record
	p.say("scratch native host %d, child %d", record.HostPID, record.ChildPID)
	p.settle(t, record)
	p.await(t, "native SessionStart, prompt, tool and Stop events", 3*time.Minute, func() bool {
		seen := map[string]bool{}
		entries, _ := os.ReadDir(nativehook.SpoolDir(p.home.State))
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(p.home.State), entry.Name()))
			if err != nil {
				continue
			}
			var event nativehook.Event
			if err := json.Unmarshal(data, &event); err != nil || event.HostID != "cfo" {
				continue
			}
			seen[fmt.Sprintf("%s:%t", event.Kind, event.Prompt)] = true
		}
		return seen["started:false"] && seen["active:true"] && seen["active:false"] && seen["settled:false"]
	})
	if received, err := supervisor.NativeHostPromptSince(p.home.State, "cfo", started); err != nil || !received {
		t.Fatalf("initial prompt receipt = %v, %v", received, err)
	}
	p.say("observed lifecycle: SessionStart, UserPromptSubmit, PostToolUse, Stop; native host prompt receipt true")
	serve := exec.Command(binary, "serve", "--example", "--listen", "127.0.0.1:0")
	serve.Env, serve.Stdout, serve.Stderr = p.env, logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	p.await(t, "scratch board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })
	for _, key := range []string{"post-turn-proof", "continued-supervision-proof"} {
		sent := time.Now()
		wakeRecord, err := wake.Append(p.home.State, "heartbeat", key, "scratch hook parity evidence")
		if err != nil {
			t.Fatal(err)
		}
		p.await(t, "acknowledgement of "+key, 3*time.Minute, func() bool { acked, err := wake.Acked(p.home.State, wakeRecord.Seq); return err == nil && acked })
		if received, err := supervisor.NativeHostPromptSince(p.home.State, "cfo", sent); err != nil || !received {
			t.Fatalf("watcher prompt receipt for %s = %v, %v", key, received, err)
		}
		p.say("wake %d %s acknowledged with native prompt receipt", wakeRecord.Seq, key)
	}
	busyRequested := time.Now()
	p.typeLine(t, record, "Run Start-Sleep -Seconds 20, then reply busy turn finished and end your turn.")
	screens, _ := harness.NativeScreens("codex")
	p.await(t, "the deliberate busy turn", time.Minute, func() bool {
		screen, err := host.ReadScreen(record)
		received, receiptError := supervisor.NativeHostPromptSince(p.home.State, "cfo", busyRequested)
		return err == nil && receiptError == nil && received && screens.IsWorking(screen)
	})
	queued := time.Now()
	p.typeLine(t, record, "Reply queued prompt accepted and end your turn. Run no command.")
	time.Sleep(3 * time.Second)
	earlyReceipt, err := supervisor.NativeHostPromptSince(p.home.State, "cfo", queued)
	if err != nil {
		t.Fatal(err)
	}
	p.say("busy input queued at %s; prompt receipt after 3s=%v", queued.UTC().Format(time.RFC3339Nano), earlyReceipt)
	p.await(t, "the queued prompt turn to settle", 3*time.Minute, func() bool {
		data, err := os.ReadFile(filepath.Join(p.home.State, ".supervisor.json"))
		if err != nil {
			return false
		}
		var database supervisor.Database
		if err := json.Unmarshal(data, &database); err != nil {
			return false
		}
		for _, session := range database.Sessions {
			if session.HostID == "cfo" && session.PromptAt.After(queued) && session.Phase == "settled" {
				p.say("queued prompt receipt at %s, turn %s", session.PromptAt.UTC().Format(time.RFC3339Nano), session.TurnID)
				return true
			}
		}
		return false
	})
	digestRequested := time.Now()
	p.typeLine(t, record, "Run only cfo session-start, then reply digest checked and end your turn.")
	p.await(t, "the manual digest turn to settle", 3*time.Minute, func() bool {
		data, err := os.ReadFile(filepath.Join(p.home.State, ".supervisor.json"))
		if err != nil {
			return false
		}
		var database supervisor.Database
		if err := json.Unmarshal(data, &database); err != nil {
			return false
		}
		for _, session := range database.Sessions {
			if session.HostID == "cfo" && session.PromptAt.After(digestRequested) && session.Phase == "settled" {
				return true
			}
		}
		return false
	})
	holder, err := lock.Read(p.home.State)
	if err != nil {
		t.Fatal(err)
	}
	owner, isComplete := digest.ReadCompleteMarker(p.home.State)
	if !isComplete || owner != holder.PID {
		t.Fatalf("Codex primary holds custody as pid %d, but its manual digest did not complete under that owner: marker=%d complete=%v", holder.PID, owner, isComplete)
	}
	p.say("manual digest completed under the registered custody pid %d", owner)
}
