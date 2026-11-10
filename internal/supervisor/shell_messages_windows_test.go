package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A message that starts with ! runs the rest in Windows PowerShell 5.1 through
// the harness's own ! (the Overlord: "when I type in a PowerShell command ...
// it should run PowerShell natively. like it does in the terminals cloud
// code"). He had typed ! powershell ... into the CFO's box, and Claude Code ran
// it in Git Bash, where it failed on quoting. The board keeps the command as a
// script and the harness's ! runs that script with powershell.exe, a line with
// no quote or space for any shell to take apart, so the command's output is in
// the session's conversation as the harness records any ! command.
func TestAShellMessageRunsItsCommandInWindowsPowerShellThroughTheHarnessesOwnBang(t *testing.T) {
	const command = "Get-ChildItem -Name | Select-Object -First 3; echo \"a b\" 'c' $HOME `n | % { $_ }"
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			meta.Harness = harness
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			gitWorktree(t, meta.Worktree)
			goblinTerminal(t, h.State, meta.ID)
			var typed []string
			previous := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = previous })
			typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, line string, _, _, _ bool) error {
				typed = append(typed, line)
				return nil
			}
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
			messageTo(t, store, meta, "shell-1", "  !  "+command)

			// Act
			err := store.ProcessOne(context.Background(), service.execute)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(h.State, "shell-commands", shellScriptName("shell-1"))
			powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
			want := "! " + filepath.ToSlash(powershell) + " -NoProfile -ExecutionPolicy Bypass -File " + filepath.ToSlash(script)
			if len(typed) != 1 || typed[0] != want {
				t.Fatalf("typed %q, want %q once", typed, want)
			}
			for _, word := range strings.Fields(typed[0]) {
				if strings.ContainsAny(word, "\"'`$;&|<>(){}\\") {
					t.Fatalf("the line %q holds %q, which a shell would take apart", typed[0], word)
				}
			}
			data, err := os.ReadFile(script)
			if err != nil || !bytes.Equal(data, append([]byte("\xef\xbb\xbf"), command...)) {
				t.Fatalf("script = %q, %v; want the command after a UTF-8 byte order mark", data, err)
			}
			audit, err := os.ReadFile(filepath.Join(h.State, "runs.audit"))
			if err != nil || !strings.Contains(string(audit), "shell-1 "+runDigest(data)+" shell mode for task-1") {
				t.Fatalf("audit = %q, %v", audit, err)
			}
			if action := actionOf(store, "shell-1"); action.Text != "  !  "+command {
				t.Fatalf("the message's own text became %q; it keeps what he typed", action.Text)
			}
		})
	}
}

// A shell message nothing types, one handed to the CFO for a stopped goblin or
// kept for a paused goblin's resume, carries the command he typed: no line
// naming powershell.exe and a script, and no script kept or audited for it.
func TestAShellMessageNothingTypesKeepsHisCommand(t *testing.T) {
	for _, test := range []struct {
		phase, action string
	}{
		{"stopped", "stop"},
		{"paused", "pause"},
	} {
		t.Run(test.phase, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := gatedGoblin(t, h)
			atPhase(t, h.State, meta, test.action, test.phase)
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{}}}
			action := Action{ID: "shell-1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "!git status", Status: "queued", CreatedAt: time.Now()}

			// Act
			_, err := service.deliverMessage(context.Background(), action)

			// Assert
			record, readErr := state.ReadLifecycle(h.State, meta.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			told := toldCFO(t, h.State, meta.ID)
			switch test.phase {
			case "stopped":
				if !errors.Is(err, ErrRejected) || len(told) != 1 || !strings.HasSuffix(told[0], "The Overlord wrote: !git status") {
					t.Fatalf("delivery = %v, the CFO was told %q; want his command handed over as he typed it", err, told)
				}
			case "paused":
				if err != nil || record.ResumeNote != "!git status" || len(told) != 0 {
					t.Fatalf("delivery = %v, resume note %q, the CFO was told %q; want his command kept for the resume", err, record.ResumeNote, told)
				}
			}
			if _, statErr := os.Stat(filepath.Join(h.State, "shell-commands")); !os.IsNotExist(statErr) {
				t.Fatalf("a shell message nothing typed kept a script: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(h.State, "runs.audit")); !os.IsNotExist(statErr) {
				t.Fatalf("a shell message nothing typed was audited: %v", statErr)
			}
		})
	}
}

func TestOnlyABangWithACommandIsShellMode(t *testing.T) {
	for _, text := range []string{"/compact", "!", "  !   ", "use the staging database", "$ npm test"} {
		t.Run(text, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()

			// Act
			line, isShell, err := shellMessageLine(stateDir, "message-1", "task-1", text)

			// Assert
			if err != nil || isShell || line != "" {
				t.Fatalf("shellMessageLine(%q) = %q, %v, %v; want no shell mode", text, line, isShell, err)
			}
			if _, err := os.Stat(filepath.Join(stateDir, "shell-commands")); !os.IsNotExist(err) {
				t.Fatalf("a message that is not shell mode kept a script: %v", err)
			}
		})
	}
}

func TestAShellMessageIsRefusedWhenItsScriptsFolderHoldsACharacterAShellSplitsOn(t *testing.T) {
	// Arrange
	stateDir := filepath.Join(t.TempDir(), "Code Goblins")

	// Act
	line, isShell, err := shellMessageLine(stateDir, "message-1", "", "! Get-Date")

	// Assert
	if err == nil || !isShell || line != "" || !strings.Contains(err.Error(), "space") {
		t.Fatalf("shellMessageLine in %q = %q, %v, %v; want a refusal naming the space", stateDir, line, isShell, err)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, "shell-commands")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused shell message kept a script: %v", statErr)
	}
}
