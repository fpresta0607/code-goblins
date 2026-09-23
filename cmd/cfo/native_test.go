package main

import (
	"bytes"
	"encoding/json"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeHookEntrySpoolsIdentityWithoutPayloadSecrets(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	t.Setenv("CFO_ROLE", "goblin")
	t.Setenv("CFO_TASK_ID", "fixture")
	t.Setenv("CFO_SPAWN_GEN", "g1")
	t.Setenv("CFO_PARENT_SESSION_ID", "")
	t.Setenv("CFO_PARENT_HARNESS", "")
	payload, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": "native-session", "turn_id": "t1", "cwd": root, "prompt": "secret-should-not-persist"})
	var out, errs bytes.Buffer
	if exit := runNativeHook([]string{"codex", "--home", root, "--state", dir}, bytes.NewReader(payload), &out, &errs, commandRuntime{}); exit != 0 {
		t.Fatalf("exit=%d %s", exit, errs.String())
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatal(out.String())
	}
	files, err := os.ReadDir(nativehook.SpoolDir(dir))
	if err != nil || len(files) != 1 {
		t.Fatalf("spool %v %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var event nativehook.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.TaskID != "fixture" || event.Kind != "settled" || event.Generation != "g1" || bytes.Contains(data, []byte("secret-should")) {
		t.Fatalf("wrong event %s", data)
	}
}
