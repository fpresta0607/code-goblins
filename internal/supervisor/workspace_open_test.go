package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type editorStarter struct{ requests []execx.Request }

func (s *editorStarter) Start(_ context.Context, r execx.Request) error {
	s.requests = append(s.requests, r)
	return nil
}

func TestOpenWorkspaceRequiresLocalIdentityAndExactTaskDirectory(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	meta.Worktree = filepath.Join(h.Root, "review 日本語 & echo injected")
	meta.Project = gitFixture(t)
	cmd := exec.Command("git", "-C", meta.Project, "worktree", "add", "--detach", meta.Worktree, "HEAD")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("linked fixture: %s %v", output, err)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(t.TempDir(), "VS Code", "Code.exe")
	if err := os.MkdirAll(filepath.Join(filepath.Dir(editor), "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(editor, []byte("test-only not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	starter := &editorStarter{}
	handler := NewHTTP(&Service{Store: store, Instance: "instance"}, "board.local", nil)
	handler.editor = starter
	handler.editorLookup = func(string) (string, error) { return filepath.Join(filepath.Dir(editor), "bin", "code.cmd"), nil }
	post := func(body, token, origin string) int {
		r := httptest.NewRequest("POST", "http://board.local/api/workspace/open", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CFO-Token", token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	body := `{"task":"task-1","generation":"g1","target":"vscode"}`
	if got := post(body, "wrong", "http://board.local"); got != 403 {
		t.Fatal(got)
	}
	if got := post(body, "instance", "https://evil.invalid"); got != 403 {
		t.Fatal(got)
	}
	for _, invalid := range []string{
		`{"task":"../task-1","generation":"g1","target":"vscode"}`,
		`{"task":"task-1","generation":"","target":"vscode"}`,
		`{"task":"task-1","generation":"old","target":"vscode"}`,
		`{"task":"task-1","generation":"g1","target":"powershell"}`,
		`{"task":"task-1","generation":"g1","target":"vscode","path":"C:/"}`,
	} {
		if got := post(invalid, "instance", "http://board.local"); got < 400 {
			t.Fatal("unsafe request accepted", invalid)
		}
	}
	if len(starter.requests) != 0 {
		t.Fatal("refused request launched an editor")
	}
	t.Setenv("ELECTRON_RUN_AS_NODE", "1")
	t.Setenv("VSCODE_DEV", "1")
	if got := post(body, "instance", "http://board.local"); got != 200 {
		t.Fatal("valid workspace refused", got)
	}
	if len(starter.requests) != 1 || starter.requests[0].Name != editor {
		t.Fatal("did not launch Code.exe exactly once")
	}
	args, _ := json.Marshal(starter.requests[0].Args)
	want, _ := json.Marshal([]string{"--new-window", "--", meta.Worktree})
	if string(args) != string(want) {
		t.Fatal("path not preserved as one literal argument", string(args))
	}
	if starter.requests[0].Env == nil {
		t.Fatal("editor inherited unsanitized Electron environment")
	}
	for _, entry := range starter.requests[0].Env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "ELECTRON_RUN_AS_NODE") || strings.EqualFold(key, "VSCODE_DEV") {
			t.Fatal("editor inherited Node/development mode")
		}
	}
	work := meta.Worktree
	for _, primary := range []string{meta.Project, filepath.Join(meta.Project, ".")} {
		meta.Worktree = primary
		_ = state.WriteTaskMeta(h.State, meta)
		if got := post(body, "instance", "http://board.local"); got < 400 {
			t.Fatal("primary checkout opened as goblin workspace")
		}
	}
	meta.Worktree = work
	// A folder that becomes a nested directory must not fall back to its parent.
	meta.Worktree = filepath.Join(meta.Worktree, "nested")
	_ = os.Mkdir(meta.Worktree, 0700)
	_ = state.WriteTaskMeta(h.State, meta)
	if got := post(body, "instance", "http://board.local"); got < 400 {
		t.Fatal("nested path accepted")
	}
	meta.Worktree = filepath.Join(h.Root, "missing")
	_ = state.WriteTaskMeta(h.State, meta)
	if got := post(body, "instance", "http://board.local"); got < 400 {
		t.Fatal("missing directory accepted")
	}
	if len(starter.requests) != 1 {
		t.Fatal("invalid directories opened")
	}
}
