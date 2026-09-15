package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
)

func TestTaskBrowserBackendMissingAndExecutableCompatibility(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is required for the executable backend probe")
	}
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("CFO_CHROME_MCP_PATH", "")
	env := taskcontext.BrowserEnv(home.Home{State: filepath.Join(root, "state")}, "fresh-task")
	backend := env["CHROME_DEVTOOLS_AXI_MCP_PATH"]
	want := filepath.Join(root, "npm", "node_modules", "chrome-devtools-mcp", "build", "src", "bin", "chrome-devtools-mcp.js")
	if backend != want {
		t.Fatalf("launch did not retain explicit missing backend: %q", backend)
	}
	check := CheckBrowserBackend(backend)
	if Healthy([]Check{check}) || !strings.Contains(check.Err, "unavailable") || !strings.Contains(check.Hint, "chrome-devtools-mcp@1.9.0") {
		t.Fatalf("fresh home falsely ready: %+v", check)
	}
	if err := os.MkdirAll(filepath.Dir(backend), 0700); err != nil {
		t.Fatal(err)
	}
	for _, trial := range []struct {
		script    string
		supported bool
	}{
		{"process.exit(1)", false},
		{"console.log('1.6.0')", false},
		{"console.log('1.9.0')", true},
	} {
		if err := os.WriteFile(backend, []byte(trial.script), 0600); err != nil {
			t.Fatal(err)
		}
		check := CheckBrowserBackend(backend)
		if Healthy([]Check{check}) != trial.supported {
			t.Fatalf("backend executable compatibility: %+v", check)
		}
	}
	t.Setenv("CFO_CHROME_MCP_PATH", filepath.Join(root, "operator override", "missing.js"))
	configured := taskcontext.BrowserEnv(home.Home{}, "fresh-task")["CHROME_DEVTOOLS_AXI_MCP_PATH"]
	if CheckBrowserBackend(configured).Err == "" {
		t.Fatal("missing override silently fell back to installed backend")
	}
}
