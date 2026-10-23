package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func hookGoblinKeyAlarm(stdin io.Reader, stderr io.Writer) int {
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, "key alarm could not resolve its home")
		return 1
	}
	return goblinKeyAlarm("claude", h.State, stdin, stderr)
}

// goblinKeyAlarm consumes output only in memory. Its durable notice contains
// a hashed call identity and the detector's fixed names, never output text.
func goblinKeyAlarm(harnessName, stateDir string, stdin io.Reader, stderr io.Writer) int {
	if os.Getenv(harness.RoleVariable) != harness.RoleGoblin {
		return 0
	}
	data, err := io.ReadAll(io.LimitReader(stdin, nativehook.MaxInputBytes+1))
	if err != nil || len(data) > nativehook.MaxInputBytes {
		fmt.Fprintln(stderr, "key alarm input could not be read within 64 MiB")
		return 1
	}
	var payload struct {
		Event   string          `json:"hook_event_name"`
		Session string          `json:"session_id"`
		Call    string          `json:"tool_use_id"`
		Tool    string          `json:"tool_name"`
		Output  json.RawMessage `json:"tool_response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		fmt.Fprintln(stderr, "key alarm input is invalid")
		return 1
	}
	if (harnessName == "pi" && payload.Event != "tool_execution_end") || (harnessName != "pi" && payload.Event != "" && payload.Event != "PostToolUse") {
		return 0
	}
	var kinds []string
	decoder := json.NewDecoder(bytes.NewReader(payload.Output))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintln(stderr, "key alarm output is invalid")
			return 1
		}
		if text, ok := token.(string); ok {
			for _, kind := range auth.ExposedKeys(text) {
				if !slices.Contains(kinds, kind) {
					kinds = append(kinds, kind)
				}
			}
		}
	}
	if len(kinds) == 0 {
		return 0
	}
	task := os.Getenv("CFO_TASK_ID")
	if state.ValidTaskID(task) != nil || payload.Session == "" || payload.Call == "" || len(payload.Session) > 4096 || len(payload.Call) > 4096 {
		fmt.Fprintln(stderr, "key alarm needs a task, session and tool call identity")
		return 1
	}
	if payload.Tool == "" || len(payload.Tool) > 256 || strings.ContainsFunc(payload.Tool, unicode.IsControl) || len(auth.ExposedKeys(payload.Tool)) != 0 {
		payload.Tool = "tool"
	}
	held := kinds[len(kinds)-1]
	if len(kinds) > 1 {
		held = strings.Join(kinds[:len(kinds)-1], ", ") + " and " + held
	}
	identity, _ := json.Marshal([]string{harnessName, task, payload.Session, payload.Call})
	detail := task + "'s " + payload.Tool + " output held " + held + ", so that key is in its transcript now. Treat it as exposed and roll it if it is real. The value is not repeated here."
	if err := wake.AppendFirstAndPublish(stateDir, fmt.Sprintf("key-alarm %x", sha256.Sum256(identity)), "review", task, detail); err != nil {
		fmt.Fprintln(stderr, "key alarm could not notify the CFO")
		return 1
	}
	return 0
}
