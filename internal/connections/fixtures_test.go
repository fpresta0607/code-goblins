package connections

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fakeRunner answers every command from its respond function and records each
// command, with the environment it was given, so a test can say what ran.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []execx.Request
	respond func(execx.Request) (execx.Result, error)
}

func (r *fakeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, request)
	respond := r.respond
	r.mu.Unlock()
	if respond == nil {
		return execx.Result{}, nil
	}
	return respond(request)
}

// ran is how many times a program was started.
func (r *fakeRunner) ran(program string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(slices.DeleteFunc(slices.Clone(r.calls), func(request execx.Request) bool { return request.Name != program }))
}

func (r *fakeRunner) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// testEnvironment is an environment of made-up folders whose tools are all
// installed unless a test says otherwise and whose programs start nothing.
type testEnvironment struct {
	Environment
	runner      *fakeRunner
	credentials auth.Store
	userEnv     []string
	missing     map[string]bool
	userEnvErr  error
}

func newTestEnvironment(t *testing.T) *testEnvironment {
	t.Helper()
	root := t.TempDir()
	credentials, err := auth.OpenFileStore(filepath.Join(root, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &testEnvironment{runner: &fakeRunner{}, credentials: credentials, missing: map[string]bool{}}
	fixture.Environment = Environment{
		HomeRoot: filepath.Join(root, "home"),
		UserHome: filepath.Join(root, "user"),
		DataDir:  filepath.Join(root, "data"),
		StateDir: filepath.Join(root, "state"),
		Runner:   fixture.runner,
		OpenStore: func() (auth.Store, error) {
			return fixture.credentials, nil
		},
		LookPath: func(program string) (string, error) {
			if fixture.missing[program] {
				return "", os.ErrNotExist
			}
			return filepath.Join(root, "bin", program), nil
		},
		UserEnvNames: func() ([]string, error) { return fixture.userEnv, fixture.userEnvErr },
	}
	return fixture
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// manifest writes the auth manifest of a made-up project.
func (e *testEnvironment) manifest(t *testing.T, project, services string) {
	t.Helper()
	writeFile(t, auth.ManifestPath(e.DataDir, project), `{"project":"`+project+`","services":`+services+`}`)
}

// checkout makes a project's folder and returns it.
func (e *testEnvironment) checkout(t *testing.T, project string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(e.DataDir), "projects", project)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// store keeps a credential for a project in the fake store.
func (e *testEnvironment) store(t *testing.T, project, name, value string) {
	t.Helper()
	if err := e.credentials.Set(auth.Scoped(project, name), value); err != nil {
		t.Fatal(err)
	}
}

// find is the item with an ID, or the test's failure.
func find(t *testing.T, items []Item, id string) Item {
	t.Helper()
	index := slices.IndexFunc(items, func(item Item) bool { return item.ID == id })
	if index < 0 {
		t.Fatalf("no item %q among %v", id, ids(items))
	}
	return items[index]
}

func ids(items []Item) []string {
	list := make([]string, 0, len(items))
	for _, item := range items {
		list = append(list, item.ID)
	}
	return list
}

func asJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// transcriptLine is one line of a Claude session record.
func transcriptLine(t *testing.T, line map[string]any) string {
	t.Helper()
	return asJSON(t, line) + "\n"
}

// deltaLine is a session record's line naming the tools gained and lost and the
// servers that failed or were starting.
func deltaLine(t *testing.T, added, removed []string, failed []map[string]string, pending []string) string {
	t.Helper()
	return transcriptLine(t, map[string]any{
		"type": "attachment",
		"attachment": map[string]any{
			"type":               "deferred_tools_delta",
			"addedNames":         added,
			"removedNames":       removed,
			"failedMcpServers":   failed,
			"pendingMcpServers":  pending,
			"readdedNames":       []string{},
			"addedLines":         []string{},
			"unrelatedAttribute": strings.Repeat("x", 8),
		},
	})
}
