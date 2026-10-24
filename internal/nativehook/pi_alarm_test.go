package nativehook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestPiExtensionTransportsOnlyGoblinToolResultsOverStdin(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Pi requires Node")
	}
	for _, role := range []string{"goblin", "cfo"} {
		t.Run(role, func(t *testing.T) {
			directory := t.TempDir()
			configuration := InstallConfig{Harness: "pi", ConfigDir: directory, Executable: filepath.Join(directory, "hook.exe"), Home: directory, State: filepath.Join(directory, "state")}
			extension, err := Install(configuration)
			if err != nil {
				t.Fatal(err)
			}
			driver := filepath.Join(directory, "driver.mjs")
			if err := os.WriteFile(driver, []byte(`import childProcess from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
const calls = [];
childProcess.spawnSync = (program, args, options) => {
  calls.push({args, payload: JSON.parse(options.input), windowsHide: options.windowsHide, timeout: options.timeout});
  return {status: 0};
};
syncBuiltinESMExports();
const extension = (await import(pathToFileURL(process.argv[2]).href)).default;
const handlers = {};
extension({on: (name, handler) => {handlers[name] = handler;}});
const ctx = {cwd: process.cwd(), sessionManager: {getSessionId: () => "fixture-session"}, hasPendingMessages: () => false};
handlers.tool_execution_end({toolName: "read", toolCallId: "call-1", result: {content: [{type: "text", text: "fixture-output"}]}, isError: false}, ctx);
handlers.agent_start({}, ctx);
process.stdout.write(JSON.stringify(calls));
`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_ROLE", role)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := execx.CommandContext(ctx, node, driver, extension)
			var diagnostics bytes.Buffer
			command.Stderr = &diagnostics

			output, err := command.Output()

			if err != nil {
				t.Fatalf("offline extension failed: %v: %s", err, diagnostics.String())
			}
			var calls []struct {
				Args    []string `json:"args"`
				Payload struct {
					Event  string          `json:"hook_event_name"`
					Tool   string          `json:"tool_name"`
					Call   string          `json:"tool_use_id"`
					Output json.RawMessage `json:"tool_response"`
				} `json:"payload"`
				WindowsHide bool `json:"windowsHide"`
				Timeout     int  `json:"timeout"`
			}
			if err := json.Unmarshal(output, &calls); err != nil || len(calls) != 2 {
				t.Fatal("expected two native calls")
			}
			if role == "goblin" {
				if calls[0].Payload.Tool != "read" || calls[0].Payload.Call != "call-1" || !bytes.Contains(calls[0].Payload.Output, []byte("fixture-output")) {
					t.Fatal("Pi dropped the goblin's completed tool result")
				}
			} else if len(calls[0].Payload.Output) != 0 {
				t.Fatal("a non-goblin session transported tool output")
			}
			for index, call := range calls {
				if !call.WindowsHide || call.Timeout <= 0 || strings.Contains(strings.Join(call.Args, " "), "fixture-output") || (index > 0 && len(call.Payload.Output) != 0) {
					t.Fatal("tool output escaped stdin or the hook lost its process bounds")
				}
			}
		})
	}
}

func TestPiExtensionReportsTransportFailuresWithoutRepeatingOutput(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Pi requires Node")
	}
	for _, failure := range []string{"exit", "spawn", "cycle", "oversize"} {
		t.Run(failure, func(t *testing.T) {
			directory := t.TempDir()
			extension, err := Install(InstallConfig{Harness: "pi", ConfigDir: directory, Executable: filepath.Join(directory, "hook.exe"), Home: directory, State: filepath.Join(directory, "state")})
			if err != nil {
				t.Fatal(err)
			}
			driver := filepath.Join(directory, "driver.mjs")
			if err := os.WriteFile(driver, []byte(`import childProcess from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
const failure = process.argv[3];
const privateOutput = "fixture-output-must-not-be-logged";
let calls = 0;
childProcess.spawnSync = () => {
  calls++;
  return failure === "spawn" ? {error: new Error(privateOutput), stderr: privateOutput} : {status: 1, stdout: privateOutput, stderr: privateOutput};
};
syncBuiltinESMExports();
const extension = (await import(pathToFileURL(process.argv[2]).href)).default;
const handlers = {};
extension({on: (name, handler) => {handlers[name] = handler;}});
const ctx = {cwd: process.cwd(), sessionManager: {getSessionId: () => "fixture-session"}, hasPendingMessages: () => false};
const result = {text: privateOutput};
if (failure === "cycle") result.cycle = result;
if (failure === "oversize") result.text = "x".repeat(64 * 1024 * 1024);
handlers.tool_execution_end({toolName: "read", toolCallId: "call-1", result, isError: true}, ctx);
process.stdout.write(String(calls));
`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_ROLE", "goblin")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := execx.CommandContext(ctx, node, driver, extension, failure)
			var diagnostics bytes.Buffer
			command.Stderr = &diagnostics

			output, err := command.Output()

			if err != nil || diagnostics.Len() == 0 || bytes.Contains(diagnostics.Bytes(), []byte("fixture-output")) {
				t.Fatal("transport failure was not safely reported")
			}
			want := "1"
			if failure == "cycle" || failure == "oversize" {
				want = "0"
			}
			if string(output) != want {
				t.Fatal("invalid or oversized input reached the child process")
			}
		})
	}
}

// On 2026-10-24 a pi goblin's turn end showed "CFO native event could not be
// spooled" while its settled event had in fact reached the supervisor four
// seconds later. The message named a cause nobody could confirm, because the
// extension printed it for every failure of the hook: a hook that never ran, a
// hook killed at its limit, and a hook that ran and exited non-zero. Each now
// says which it was, and none repeats the child's output, which may hold a
// tool result. Issue recorded from the retained screen evidence of that turn
// end.
func TestPiExtensionNamesTheRealHookFailureWithoutClaimingALostEvent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Pi requires Node")
	}
	for _, c := range []struct {
		name    string
		failure string
		want    []string
	}{
		{name: "a hook that never ran or timed out", failure: "timeout", want: []string{"did not run (ETIMEDOUT)", "may not have been recorded"}},
		{name: "a hook that ran and failed", failure: "status", want: []string{"exited with status 3", "may not have been recorded"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			directory := t.TempDir()
			extension, err := Install(InstallConfig{Harness: "pi", ConfigDir: directory, Executable: filepath.Join(directory, "hook.exe"), Home: directory, State: filepath.Join(directory, "state")})
			if err != nil {
				t.Fatal(err)
			}
			driver := filepath.Join(directory, "driver.mjs")
			if err := os.WriteFile(driver, []byte(`import childProcess from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
const failure = process.argv[3];
const privateOutput = "fixture-output-must-not-be-logged";
childProcess.spawnSync = () => {
  if (failure === "timeout") {
    const error = new Error(privateOutput);
    error.code = "ETIMEDOUT";
    return {error, signal: "SIGTERM", stdout: privateOutput, stderr: privateOutput};
  }
  return {status: 3, signal: null, stdout: privateOutput, stderr: privateOutput};
};
syncBuiltinESMExports();
const extension = (await import(pathToFileURL(process.argv[2]).href)).default;
const handlers = {};
extension({on: (name, handler) => {handlers[name] = handler;}});
const ctx = {cwd: process.cwd(), sessionManager: {getSessionId: () => "fixture-session"}, hasPendingMessages: () => false};
handlers.agent_settled({}, ctx);
`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_ROLE", "goblin")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := execx.CommandContext(ctx, node, driver, extension, c.failure)
			var diagnostics bytes.Buffer
			command.Stderr = &diagnostics

			// Act
			_, err = command.Output()

			// Assert
			reported := diagnostics.String()
			if err != nil || reported == "" {
				t.Fatalf("the extension reported nothing of the failure: %v %q", err, reported)
			}
			if strings.Contains(reported, "could not be spooled") {
				t.Fatalf("the extension still claims the event was not spooled: %q", reported)
			}
			if bytes.Contains(diagnostics.Bytes(), []byte("fixture-output")) {
				t.Fatalf("the extension repeated the child's private output: %q", reported)
			}
			for _, want := range c.want {
				if !strings.Contains(reported, want) {
					t.Fatalf("reported %q, want it to name %q", reported, want)
				}
			}
		})
	}
}
