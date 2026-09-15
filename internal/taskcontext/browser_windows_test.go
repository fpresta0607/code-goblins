//go:build windows

package taskcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// Opt-in executable compatibility evidence stays outside source and test temp
// cleanup. Only this newly created task session is stopped; profiles are kept.
func TestWindowsInstalledBrowserActions(t *testing.T) {
	evidenceRoot := os.Getenv("CFO_BROWSER_SMOKE_ROOT")
	if evidenceRoot == "" {
		t.Skip("set CFO_BROWSER_SMOKE_ROOT to private retained evidence storage")
	}
	if !filepath.IsAbs(evidenceRoot) {
		t.Fatal("absolute private evidence root required")
	}
	if err := os.MkdirAll(evidenceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(evidenceRoot, "browser smoke ")
	if err != nil {
		t.Fatal(err)
	}
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	paths := PathsFor(h, "windows-browser-smoke")
	if err := os.MkdirAll(paths.Browser.Evidence, 0700); err != nil {
		t.Fatal(err)
	}
	browserEnv := BrowserEnv(h, "windows-browser-smoke")
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ownership.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(os.Getenv("APPDATA"), "npm", "node_modules")
	for name, version := range map[string]string{"chrome-devtools-axi": "0.1.34", "chrome-devtools-mcp": "1.9.0"} {
		data, err := os.ReadFile(filepath.Join(installed, name, "package.json"))
		var pkg struct {
			Version string `json:"version"`
		}
		if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Version != version {
			t.Fatalf("unverified installed pair %s: %s %v", name, pkg.Version, err)
		}
	}
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "CHROME_DEVTOOLS_AXI_") {
			continue
		}
		env = append(env, entry)
	}
	for key, value := range browserEnv {
		env = append(env, key+"="+value)
	}
	cli := filepath.Join(installed, "chrome-devtools-axi", "dist", "bin", "chrome-devtools-axi.js")
	log, err := os.OpenFile(filepath.Join(root, "actions.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", append([]string{cli}, args...)...)
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		fmt.Fprintf(log, "command %q\n%s\nerror=%v\n", args, output, err)
		if err != nil {
			t.Fatalf("Chrome action %v failed: %s %v; retained evidence %s", args, output, err, root)
		}
		return string(output)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><title>CFO Windows compatibility smoke</title><body><h1>Isolated task browser</h1><button onclick="localStorage.setItem('cfoSmoke','retained'); document.getElementById('result').textContent='Action verified'">Verify action</button><p id="result">Awaiting action</p></body></html>`)
	}))
	defer server.Close()
	stop := func() {
		run("stop")
		// Verify all profile-bound Chrome/MCP processes, independently of whether
		// AXI has already returned after terminating only its bridge PID.
		stoppedAt := time.Now().UTC()
		deadline := time.Now().Add(10 * time.Second)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			processes, e := (reap.CIMProcesses{Commands: execx.OSRunner{}}).List(ctx)
			cancel()
			if e != nil {
				t.Fatal(e)
			}
			var owned []int
			for _, p := range processes {
				if strings.Contains(strings.ToLower(p.CommandLine), strings.ToLower(paths.Browser.Profile)) {
					owned = append(owned, p.PID)
				}
			}
			fmt.Fprintf(log, "stop verification after %s: profile-bound process IDs %v\n", time.Since(stoppedAt), owned)
			if len(owned) == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("AXI returned before profile-bound processes stopped: %v", owned)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	defer stop()
	run("open", server.URL)
	if got := run("snapshot"); !strings.Contains(got, "Verify action") {
		t.Fatalf("snapshot did not inspect page: %s", got)
	}
	run("eval", "document.querySelector('button').click()")
	if got := run("eval", "document.getElementById('result').textContent"); !strings.Contains(got, "Action verified") {
		t.Fatalf("action not verified: %s", got)
	}
	run("screenshot", filepath.Join(paths.Browser.Evidence, "verified action with spaces.png"))
	stop()
	run("open", server.URL)
	persisted := strings.Contains(run("eval", "localStorage.getItem('cfoSmoke')"), "retained")
	if !persisted {
		if err := os.WriteFile(filepath.Join(root, "persistence-unresolved.txt"), []byte("Immediate-stop localStorage loss reproduced. AXI stop confirms bridge exit, not a durable storage flush. No supported graceful browser-close command was found in this AXI interface.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("UNRESOLVED: recent localStorage was lost after immediate AXI stop; action compatibility does not establish persistence")
		if os.Getenv("CFO_BROWSER_PERSISTENCE_REPRO") == "1" {
			t.Fatal("profile persistence lost after immediate stop")
		}
	}
	run("eval", "localStorage.removeItem('cfoSmoke')")
	stop()
	if err := os.WriteFile(filepath.Join(root, "passed.txt"), []byte(fmt.Sprintf("AXI 0.1.34 / MCP 1.9.0: open, snapshot, eval action, screenshot with spaces, named stop and zero profile-bound processes passed. Immediate-stop marker survived this invocation: %t. Previous loss remains an unresolved durability limitation.\n", persisted)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("retained evidence: %s", root)
}
