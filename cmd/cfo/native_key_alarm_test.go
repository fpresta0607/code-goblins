package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestNativeGoblinKeyAlarmsWakeOncePerCallWithoutPersistingOutput(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			stateDir := goblinHookHome(t)
			key := keyShaped("sk" + "_test_")
			args := []string{harness, "--home", filepath.Dir(stateDir), "--state", stateDir}
			if harness == "claude" {
				args = append(args, "--key-alarm")
			}
			var stdout, stderr bytes.Buffer

			for _, call := range []string{"call-1", "call-1", "call-2"} {
				payload := alarmEvent(t, harness, call, key)
				if code := runNativeHook(args, bytes.NewReader(payload), &stdout, &stderr, commandRuntime{}); code != 0 {
					t.Fatalf("hook exited %d", code)
				}
			}

			pending, err := wake.Pending(stateDir)
			if err != nil || len(pending) != 2 {
				t.Fatalf("got %d wakes, error %v; want two calls", len(pending), err)
			}
			for _, record := range pending {
				if record.Key != "pd-keys" || !strings.Contains(record.Detail, "pd-keys's Read output held a Stripe secret key") {
					t.Fatal("alarm did not name task, tool and key kind")
				}
			}
			if stdout.String() != "{}\n{}\n{}\n" || stderr.Len() != 0 {
				t.Fatal("hook changed its reply or wrote diagnostics")
			}
			assertAlarmFilesExclude(t, stateDir, key)
		})
	}
}

func TestNativeGoblinKeyAlarmUsesTheExplicitHome(t *testing.T) {
	stateDir := goblinHookHome(t)
	other := t.TempDir()
	t.Setenv("CFO_HOME", other)
	t.Setenv("CFO_STATE_OVERRIDE", filepath.Join(other, "state"))
	var stdout, stderr bytes.Buffer

	code := runNativeHook([]string{"codex", "--home", filepath.Dir(stateDir), "--state", stateDir}, bytes.NewReader(alarmEvent(t, "codex", "call-1", keyShaped("sk"+"_test_"))), &stdout, &stderr, commandRuntime{})

	pending, err := wake.Pending(stateDir)
	if code != 0 || err != nil || len(pending) != 1 {
		t.Fatal("the hook did not wake the explicitly selected home")
	}
}

func TestNativeGoblinKeyAlarmReportsQueueFailureWithoutRepeatingPayload(t *testing.T) {
	stateDir := goblinHookHome(t)
	key := keyShaped("sk" + "_test_")
	if err := os.Mkdir(filepath.Join(stateDir, ".wake-queue"), 0700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := runNativeHook([]string{"codex", "--home", filepath.Dir(stateDir), "--state", stateDir}, bytes.NewReader(alarmEvent(t, "codex", "call-1", key)), &stdout, &stderr, commandRuntime{})

	if code == 0 || stderr.Len() == 0 || strings.Contains(stdout.String()+stderr.String(), key) {
		t.Fatal("alarm failure must be reported without payload content")
	}
	assertAlarmFilesExclude(t, stateDir, key)
}

func TestNativeGoblinKeyAlarmHandlesOutputShapes(t *testing.T) {
	key := keyShaped("sk" + "_test_")
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, test := range []struct {
			name, output string
			want         int
		}{
			{"missing", "", 0},
			{"null", "null", 0},
			{"empty", `""`, 0},
			{"ordinary text", `"safe output"`, 0},
			{"nested result", `{"content":[{"type":"text","text":"` + key + `"}]}`, 1},
			{"error output", `{"stdout":"","stderr":"` + key + `","isError":true}`, 1},
			{"escaped key", `"\u0073` + key[1:] + `"`, 1},
			{"large number before key", `{"number":1e1000,"text":"` + key + `"}`, 1},
			{"large output with key at end", `"` + strings.Repeat("safe ", 1<<18) + key + `"`, 1},
		} {
			t.Run(harness+"/"+test.name, func(t *testing.T) {
				stateDir := goblinHookHome(t)
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(alarmEvent(t, harness, "call-1", ""), &payload); err != nil {
					t.Fatal(err)
				}
				delete(payload, "tool_response")
				if test.output != "" {
					payload["tool_response"] = json.RawMessage(test.output)
				}
				input, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer

				code := runNativeHook([]string{harness, "--home", filepath.Dir(stateDir), "--state", stateDir, "--key-alarm"}, bytes.NewReader(input), &stdout, &stderr, commandRuntime{})

				pending, err := wake.Pending(stateDir)
				if code != 0 || err != nil || len(pending) != test.want || stderr.Len() != 0 {
					t.Fatalf("exit %d, wakes %d, error %v", code, len(pending), err)
				}
				assertAlarmFilesExclude(t, stateDir, key)
			})
		}
	}
}

func TestNativeGoblinKeyAlarmRejectsMalformedAndUnreadableInputSafely(t *testing.T) {
	key := keyShaped("sk" + "_test_")
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, test := range []struct {
			name  string
			input func() io.Reader
		}{
			{"empty", func() io.Reader { return strings.NewReader("") }},
			{"null", func() io.Reader { return strings.NewReader("null") }},
			{"malformed output", func() io.Reader { return strings.NewReader(`{"tool_response":"` + key) }},
			{"unknown event holding key", func() io.Reader { return strings.NewReader(`{"hook_event_name":"` + key + `"}`) }},
			{"stream error", func() io.Reader { return iotest.ErrReader(errors.New(key)) }},
		} {
			t.Run(harness+"/"+test.name, func(t *testing.T) {
				stateDir := goblinHookHome(t)
				var stdout, stderr bytes.Buffer

				code := runNativeHook([]string{harness, "--home", filepath.Dir(stateDir), "--state", stateDir, "--key-alarm"}, test.input(), &stdout, &stderr, commandRuntime{})

				if code == 0 || stderr.Len() == 0 || strings.Contains(stdout.String()+stderr.String(), key) {
					t.Fatal("invalid input was accepted or repeated")
				}
				if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 0 {
					t.Fatal("invalid input manufactured a wake")
				}
			})
		}
	}
}

func TestNativeGoblinKeyAlarmDoesNotAlarmForOtherRolesOrEvents(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, role := range []string{"", "cfo", "worker", "subagent", "goblin"} {
			t.Run(harness+"/"+role, func(t *testing.T) {
				stateDir := goblinHookHome(t)
				t.Setenv("CFO_ROLE", role)
				if role == "cfo" || role == "" {
					t.Setenv("CFO_TASK_ID", "")
				}
				payload := alarmEvent(t, harness, "call-1", keyShaped("sk"+"_test_"))
				if role == "goblin" {
					payload = bytes.ReplaceAll(payload, []byte("PostToolUse"), []byte("UserPromptSubmit"))
					payload = bytes.ReplaceAll(payload, []byte("tool_execution_end"), []byte("agent_start"))
				}
				var stdout, stderr bytes.Buffer

				code := runNativeHook([]string{harness, "--home", filepath.Dir(stateDir), "--state", stateDir, "--key-alarm"}, bytes.NewReader(payload), &stdout, &stderr, commandRuntime{})

				if code != 0 {
					t.Fatal("non-alarm event failed")
				}
				if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 0 {
					t.Fatal("non-goblin or non-tool event raised an alarm")
				}
			})
		}
	}
}

func TestNativeGoblinKeyAlarmKeepsAcknowledgedCallsDeduplicated(t *testing.T) {
	stateDir := goblinHookHome(t)
	key := keyShaped("sk" + "_test_")
	for _, harness := range []string{"codex", "pi"} {
		payload := alarmEvent(t, harness, "same-call", key)
		for attempt := 0; attempt < 2; attempt++ {
			var stdout, stderr bytes.Buffer
			code := runNativeHook([]string{harness, "--home", filepath.Dir(stateDir), "--state", stateDir}, bytes.NewReader(payload), &stdout, &stderr, commandRuntime{})
			pending, err := wake.Pending(stateDir)
			if code != 0 || err != nil || len(pending) != 1-attempt {
				t.Fatal("duplicate calls or different harnesses shared the wrong identity")
			}
			if attempt == 0 {
				if err := wake.AckThrough(stateDir, pending[0].Seq); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	assertAlarmFilesExclude(t, stateDir, key)
}

func TestNativeGoblinKeyAlarmBoundsInput(t *testing.T) {
	stateDir := goblinHookHome(t)
	input := strings.NewReader(strings.Repeat(" ", nativehook.MaxInputBytes+2))
	var stdout, stderr bytes.Buffer

	code := runNativeHook([]string{"codex", "--home", filepath.Dir(stateDir), "--state", stateDir}, input, &stdout, &stderr, commandRuntime{})

	if code == 0 || input.Len() != 1 || !strings.Contains(stderr.String(), "exceeds 64 MiB") {
		t.Fatal("input was not stopped at the size bound")
	}
}

func TestNativeGoblinKeyAlarmKeepsToolLabelsAndCallIDsSafe(t *testing.T) {
	key := keyShaped("sk" + "_test_")
	for _, tool := range []string{key, "Read\n" + key, "", strings.Repeat("x", 257)} {
		stateDir := goblinHookHome(t)
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(alarmEvent(t, "codex", key, key), &payload); err != nil {
			t.Fatal(err)
		}
		payload["tool_name"], _ = json.Marshal(tool)
		input, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer

		code := runNativeHook([]string{"codex", "--home", filepath.Dir(stateDir), "--state", stateDir}, bytes.NewReader(input), &stdout, &stderr, commandRuntime{})

		pending, err := wake.Pending(stateDir)
		if code != 0 || err != nil || len(pending) != 1 || !strings.Contains(pending[0].Detail, "pd-keys's tool output held") || strings.Contains(stdout.String()+stderr.String(), key) {
			t.Fatal("unsafe tool label or call ID escaped into the alarm")
		}
		assertAlarmFilesExclude(t, stateDir, key)
	}
}

func TestNativeGoblinKeyAlarmRefusesMissingCallIdentity(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			stateDir := goblinHookHome(t)
			var stdout, stderr bytes.Buffer

			code := runNativeHook([]string{harness, "--home", filepath.Dir(stateDir), "--state", stateDir, "--key-alarm"}, bytes.NewReader(alarmEvent(t, harness, "", keyShaped("sk"+"_test_"))), &stdout, &stderr, commandRuntime{})

			if code == 0 || stderr.Len() == 0 {
				t.Fatal("missing identity silently combined unrelated calls")
			}
			if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 0 {
				t.Fatal("a call without identity manufactured a notice")
			}
		})
	}
}

func TestNativeGoblinKeyAlarmRetriesFailedPublication(t *testing.T) {
	stateDir := goblinHookHome(t)
	marker := filepath.Join(stateDir, ".watcher-down")
	if err := os.Mkdir(marker, 0700); err != nil {
		t.Fatal(err)
	}
	payload := alarmEvent(t, "codex", "call-1", keyShaped("sk"+"_test_"))
	for attempt := 0; attempt < 2; attempt++ {
		var stdout, stderr bytes.Buffer
		code := runNativeHook([]string{"codex", "--home", filepath.Dir(stateDir), "--state", stateDir}, bytes.NewReader(payload), &stdout, &stderr, commandRuntime{})
		if (code != 0) != (attempt == 0) {
			t.Fatal("publication failure was not reported or recovered")
		}
		if attempt == 0 {
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
		}
	}
	episode, err := wake.ReadEpisode(stateDir)
	if err != nil || !episode.Pending {
		t.Fatal("retry did not publish the queued alarm")
	}
	if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 1 {
		t.Fatal("retry changed the queued alarm")
	}
}

func alarmEvent(t *testing.T, harness, call, output string) []byte {
	t.Helper()
	event := "PostToolUse"
	if harness == "pi" {
		event = "tool_execution_end"
	}
	data, err := json.Marshal(struct {
		Event   string `json:"hook_event_name"`
		Session string `json:"session_id"`
		CWD     string `json:"cwd"`
		Tool    string `json:"tool_name"`
		Call    string `json:"tool_use_id"`
		Output  string `json:"tool_response"`
	}{event, "fixture-session", t.TempDir(), "Read", call, output})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertAlarmFilesExclude(t *testing.T, root, secret string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if bytes.Contains(data, []byte(secret)) {
			t.Error("raw key was persisted")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(nativehook.SpoolDir(root))
	if err != nil || len(files) == 0 {
		t.Fatal("native lifecycle event was not preserved")
	}
}
