package fleettree

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// at is the fixtures' clock: a moment on 2026-10-06.
var at = time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC)

// writeLines writes JSON lines to path, making its folder, and sets its
// write time.
func writeLines(t *testing.T, path string, written time.Time, entries ...any) {
	t.Helper()
	var lines []string
	for _, entry := range entries {
		switch entry := entry.(type) {
		case string:
			lines = append(lines, entry)
		default:
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(data))
		}
	}
	writeFile(t, path, strings.Join(lines, "\n")+"\n", written)
}

// appendLines adds JSON lines to the end of path, as a harness appends.
func appendLines(t *testing.T, path string, entries ...object) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string, written time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
}

type object = map[string]any

// call is an assistant entry with one tool call, as Claude Code writes it.
func call(id, name string, input object, when time.Time) object {
	return object{"type": "assistant", "timestamp": when.Format(time.RFC3339Nano), "message": object{"role": "assistant", "content": []object{{"type": "tool_use", "id": id, "name": name, "input": input}}}}
}

// result is a user entry with one tool result and what Claude Code recorded
// of it.
func result(id string, content string, recorded object, when time.Time) object {
	return object{"type": "user", "timestamp": when.Format(time.RFC3339Nano), "message": object{"role": "user", "content": []object{{"type": "tool_result", "tool_use_id": id, "content": content}}}, "toolUseResult": recorded}
}

// queued is a task-notification as Claude Code queues it, and delivered the
// same notification as it reaches the conversation.
func queued(notification string, when time.Time) object {
	return object{"type": "queue-operation", "operation": "enqueue", "timestamp": when.Format(time.RFC3339Nano), "content": notification}
}

func delivered(notification string, when time.Time) object {
	return object{"type": "user", "timestamp": when.Format(time.RFC3339Nano), "message": object{"role": "user", "content": notification}}
}

func notification(task, call, status, summary, event string) string {
	text := "<task-notification>\n<task-id>" + task + "</task-id>\n"
	if call != "" {
		text += "<tool-use-id>" + call + "</tool-use-id>\n"
	}
	if status != "" {
		text += "<status>" + status + "</status>\n"
	}
	text += "<summary>" + summary + "</summary>\n"
	if event != "" {
		text += "<event>" + event + "</event>\n"
	}
	return text + "</task-notification>"
}

// said is an assistant entry with text, as a sub-agent's transcript holds it.
func said(text string, when time.Time) object {
	return object{"type": "assistant", "timestamp": when.Format(time.RFC3339Nano), "message": object{"role": "assistant", "content": []object{{"type": "text", "text": text}}}}
}

// child finds a child by id, failing the test when it is missing.
func child(t *testing.T, tree Tree, id string) Node {
	t.Helper()
	for _, node := range tree.Children {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("no child %s among %+v", id, tree.Children)
	return Node{}
}
