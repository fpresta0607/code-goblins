package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// goblinHookHome is a scratch home whose goblin pd-keys runs the goblin
// hooks, as its terminal names them.
func goblinHookHome(t *testing.T) string {
	t.Helper()
	root := useScratchHome(t)
	t.Setenv(harness.RoleVariable, harness.RoleGoblin)
	t.Setenv("CFO_TASK_ID", "pd-keys")
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return stateDir
}

func shellCall(t *testing.T, command string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{"session_id": "s1", "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func TestGoblinSealRefusesBrowserCommandsOnlyWhileItsTaskIsSealed(t *testing.T) {
	for _, test := range []struct {
		name      string
		arrange   func(t *testing.T, stateDir string)
		command   string
		isRefused bool
	}{
		{"a snapshot while sealed", sealTask("pd-keys", time.Hour), "chrome-devtools-axi snapshot", true},
		{"any spelling of the tool while sealed", sealTask("pd-keys", time.Hour), "npx Chrome-DevTools-AXI eval 'document.body.innerText'", true},
		{"another command while sealed", sealTask("pd-keys", time.Hour), "git status", false},
		{"a snapshot with no seal", func(*testing.T, string) {}, "chrome-devtools-axi snapshot", false},
		{"a snapshot after the seal ran out", sealTask("pd-keys", -time.Second), "chrome-devtools-axi snapshot", false},
		{"a snapshot while another task is sealed", sealTask("pd-other", time.Hour), "chrome-devtools-axi snapshot", false},
		{"a snapshot under a damaged seal", func(t *testing.T, stateDir string) {
			sealTask("pd-keys", time.Hour)(t, stateDir)
			if err := os.WriteFile(filepath.Join(stateDir, "browser-seals", "pd-keys.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "chrome-devtools-axi snapshot", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := goblinHookHome(t)
			test.arrange(t, stateDir)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runHook("goblin-seal", shellCall(t, test.command), &stdout, &stderr)

			// Assert
			if isRefused := exit == 2 && strings.Contains(stderr.String(), `"permissionDecision":"deny"`) && strings.Contains(stderr.String(), "key fetch is reading"); isRefused != test.isRefused {
				t.Fatalf("exit = %d, stderr = %q, want refused %t", exit, stderr.String(), test.isRefused)
			}
		})
	}
}

// The goblin hooks run on a goblin's tool calls all day, so a call that is
// no browser command, or whose output holds no key, costs them no file read
// and no look at the machine's processes.
func TestGoblinHooksReadNothingForAnOrdinaryCall(t *testing.T) {
	// Arrange
	goblinHookHome(t)
	opened, listed := fsx.Opens(), proc.Lists()
	var stdout, stderr bytes.Buffer

	// Act
	seal := runHook("goblin-seal", shellCall(t, "git status"), &stdout, &stderr)
	alarm := runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "commit 9f67ba752f1e637b1d58a0e13e1c07a692648c2c"), &stdout, &stderr)

	// Assert
	if seal != 0 || alarm != 0 {
		t.Fatalf("seal = %d, alarm = %d, want both calls left as they were", seal, alarm)
	}
	if opened, listed = fsx.Opens()-opened, proc.Lists()-listed; opened != 0 || listed != 0 {
		t.Fatalf("the hooks opened %d file(s) and listed processes %d time(s), want neither", opened, listed)
	}
}

func sealTask(task string, lasting time.Duration) func(*testing.T, string) {
	return func(t *testing.T, stateDir string) {
		t.Helper()
		if err := browserlogin.Seal(stateDir, task, time.Now().Add(lasting)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoblinHooksDoNothingOutsideAGoblin(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	t.Setenv(harness.RoleVariable, "")
	sealTask("pd-keys", time.Hour)(t, stateDir)
	var stdout, stderr bytes.Buffer

	// Act
	seal := runHook("goblin-seal", shellCall(t, "chrome-devtools-axi snapshot"), &stdout, &stderr)
	alarm := runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+keyShaped("sk"+"_test_")), &stdout, &stderr)

	// Assert
	if seal != 0 || alarm != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("seal = %d, alarm = %d, output %q %q, want nothing outside a goblin", seal, alarm, stdout.String(), stderr.String())
	}
	if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 0 {
		t.Fatalf("wakes = %+v, %v, want none", pending, err)
	}
}

// keyShaped is a value of a secret key's shape, made at run time so no
// source file holds one.
func keyShaped(prefix string) string {
	return prefix + "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
}

func toolOutput(t *testing.T, call, output string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{"session_id": "s1", "tool_name": "Read", "tool_use_id": call, "tool_response": map[string]any{"file": map[string]string{"content": output}}})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func TestGoblinKeyAlarmWakesTheCFOOnceACallWithTheKindAndNeverTheKey(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	key := keyShaped("sk" + "_test_")
	var stdout, stderr bytes.Buffer

	// Act
	exits := []int{
		runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+key+"\nDATABASE_URL=postgres://db"), &stdout, &stderr),
		runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+key), &stdout, &stderr),
		runHook("goblin-key-alarm", toolOutput(t, "toolu_2", "commit 9f67ba752f1e637b1d58a0e13e1c07a692648c2c"), &stdout, &stderr),
		runHook("goblin-key-alarm", toolOutput(t, "toolu_3", key+" and "+keyShaped("ghp"+"_")), &stdout, &stderr),
	}

	// Assert
	for index, exit := range exits {
		if exit != 0 {
			t.Fatalf("call %d exit = %d, want the tool call left as it was", index+1, exit)
		}
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output %q %q, want the goblin told nothing", stdout.String(), stderr.String())
	}
	pending, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("wakes = %+v, want one for each call that held a key", pending)
	}
	for index, want := range []string{"pd-keys's Read output held a Stripe secret key, so that key is in its transcript now", "held a Stripe secret key and a GitHub token"} {
		record := pending[index]
		if record.Kind != "review" || record.Key != "pd-keys" || !strings.Contains(record.Detail, want) || strings.Contains(record.Detail, key) || strings.Contains(record.Detail, "q7Lm2Xv9") {
			t.Fatalf("wake %d = %+v, want %q and no key", index+1, record, want)
		}
	}
}
