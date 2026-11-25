package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
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

// goblinKeyAlarm consumes output only in memory. Its durable notice contains
// a hashed call identity and the detector's fixed names, never output text.
// getenv reads the goblin's environment.
func goblinKeyAlarm(harnessName, stateDir string, getenv func(string) string, stdin io.Reader, stderr io.Writer) int {
	if getenv(harness.RoleVariable) != harness.RoleGoblin {
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
	var output any
	decoder := json.NewDecoder(bytes.NewReader(payload.Output))
	decoder.UseNumber()
	if err := decoder.Decode(&output); err != nil && err != io.EOF {
		fmt.Fprintln(stderr, "key alarm output is invalid")
		return 1
	}
	kinds := exposedKeysIn(output, nil)
	if len(kinds) == 0 {
		return 0
	}
	task := getenv("GOBLINS_TASK_ID")
	if state.ValidTaskID(task) != nil || payload.Session == "" || payload.Call == "" || len(payload.Session) > 4096 || len(payload.Call) > 4096 {
		fmt.Fprintln(stderr, "key alarm needs a task, session and tool call identity")
		return 1
	}
	if stateDir == "" {
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, "key alarm could not resolve its home")
			return 1
		}
		stateDir = h.State
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
		fmt.Fprintln(stderr, "key alarm could not notify the CEO")
		return 1
	}
	return 0
}

// exposedKeysIn adds to kinds the kinds of key the strings of a decoded tool
// output hold, reading an array in order and an object's members by name. It
// skips an image or PDF block, such as a Read of a picture or a PDF, and a
// notebook's image output, since their base64 bytes are no text the tool
// returned and match a key's shape by chance.
func exposedKeysIn(value any, kinds []string) []string {
	switch value := value.(type) {
	case string:
		for _, kind := range auth.ExposedKeys(value) {
			if !slices.Contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
		}
	case []any:
		for _, item := range value {
			kinds = exposedKeysIn(item, kinds)
		}
	case map[string]any:
		mediaType, _ := value["media_type"].(string)
		if value["type"] == "image" || value["type"] == "pdf" || (strings.HasPrefix(mediaType, "image/") && value["image_data"] != nil) {
			return kinds
		}
		for _, name := range slices.Sorted(maps.Keys(value)) {
			kinds = exposedKeysIn(value[name], exposedKeysIn(name, kinds))
		}
	}
	return kinds
}
