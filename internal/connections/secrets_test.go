package connections

import (
	"bytes"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// plantedSecrets are values put where a check reads, each of which must never
// leave the check: not into a snapshot file, the view or a log.
var plantedSecrets = map[string]string{
	"credential store":      "store-secret-4f9a1c",
	"process environment":   "env-secret-71bc02",
	"claude user config":    "claude-json-secret-5d2e90",
	"codex user config":     "codex-secret-a81c37",
	"mcp entry value":       "mcp-entry-secret-3b7d51",
	"mcp entry address":     "mcp-url-secret-e9f0a4",
	"transcript sign-in":    "pw-secret-b2d1f6",
	"transcript query":      "query-secret-0c3a88",
	"transcript bearer":     "bearer-secret-aa11bb22cc",
	"status command output": "status-output-secret-9e77d3",
	"probe command output":  "probe-output-secret-6a40ce",
}

// Nothing a check reads by value reaches a snapshot file, the view, a log or a
// reported error: a credential in the store, the environment, the user's own
// settings, a project's .mcp.json, a session record's failure, or a tool's
// output.
func TestNoCredentialValueReachesSnapshotFilesTheViewOrLogs(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[
		{"name":"db","method":"env","env":["ACME_DB_TOKEN"],"probe":["acme-cli","status"]},
		{"name":"hosting","method":"cli","probe":["gh","api","user"]}
	]`)
	path := e.checkout(t, "acme")
	e.store(t, "acme", "ACME_DB_TOKEN", plantedSecrets["credential store"])
	t.Setenv("ACME_ENV_TOKEN", plantedSecrets["process environment"])
	e.userEnv = []string{"ACME_ENV_TOKEN", "ACME_DB_TOKEN"}
	writeFile(t, filepath.Join(e.UserHome, ".claude.json"), `{"mcpServers":{"alpha":{"command":"alpha","env":{"ALPHA_KEY":"`+plantedSecrets["claude user config"]+`"}}},"claudeAiMcpEverConnected":["Acme Docs"]}`)
	writeFile(t, filepath.Join(e.UserHome, ".codex", "config.toml"), "[mcp_servers.beta]\ncommand = \"beta\"\n[mcp_servers.beta.env]\nBETA_KEY = \""+plantedSecrets["codex user config"]+"\"\n")
	writeFile(t, path+"/.mcp.json", `{"mcpServers":{
		"valued": {"command": "acme-valued", "env": {"ACME_MODE": "`+plantedSecrets["mcp entry value"]+`"}},
		"remote": {"url": "https://mcp.acme.example/mcp?apikey=`+plantedSecrets["mcp entry address"]+`", "bearerTokenEnvVar": "ACME_DB_TOKEN"}
	}}`)
	transcript := filepath.Join(filepath.Dir(e.DataDir), "session.jsonl")
	writeFile(t, transcript, deltaLine(t, []string{"mcp__acme-docs__search"}, nil, []map[string]string{{
		"name":  "globex-db",
		"error": "cannot reach https://acme-user:" + plantedSecrets["transcript sign-in"] + "@db.globex.example/mcp?token=" + plantedSecrets["transcript query"] + " with Authorization: Bearer " + plantedSecrets["transcript bearer"],
	}}, nil))
	var received []execx.Request
	var receivedMu sync.Mutex
	e.runner.respond = func(request execx.Request) (execx.Result, error) {
		receivedMu.Lock()
		received = append(received, request)
		receivedMu.Unlock()
		output := []byte("status said " + plantedSecrets["status command output"] + " and " + plantedSecrets["probe command output"])
		return execx.Result{Stdout: output, Stderr: output}, nil
	}
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	var failures []string
	folder := t.TempDir()
	snapshots := NewSnapshots(e.Environment, folder)
	t.Cleanup(snapshots.Close)
	snapshots.Failed = func(err error) { failures = append(failures, err.Error()) }
	snapshots.Transcript = func(state.TaskMeta) string { return transcript }
	meta := state.TaskMeta{ID: "acme-task", Project: path, Worktree: path, Harness: "claude", SpawnGen: "g1", HasCredentials: true, Credentials: []string{"db"}}

	// Act
	snapshots.tick([]state.TaskMeta{meta})
	snapshots.work.Wait()
	view := asJSON(t, snapshots.Read())
	var files strings.Builder
	walkErr := filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		files.Write(data)
		return readErr
	})

	// Assert: the premise first, that the checks ran and the secrets were there to be leaked.
	if walkErr != nil || files.Len() == 0 {
		t.Fatalf("snapshot files unreadable or empty: %v", walkErr)
	}
	if got := find(t, snapshots.Read().Projects[0].Items, "service:db"); got.Status != StatusVerified {
		t.Fatalf("db = %q, want its probe to have run and passed", got.Status)
	}
	sawStoreValue := false
	receivedMu.Lock()
	for _, request := range received {
		if request.Name == "acme-cli" && strings.Contains(strings.Join(request.Env, "\n"), plantedSecrets["credential store"]) {
			sawStoreValue = true
		}
	}
	receivedMu.Unlock()
	if !sawStoreValue {
		t.Fatal("the probe was not given the stored value, so the test proves nothing")
	}
	if !strings.Contains(asJSON(t, snapshots.Read().Goblins), "failed:globex-db") {
		t.Fatal("the goblin's failed server was not read, so the test proves nothing")
	}
	// Assert: nowhere it could leave by.
	where := map[string]string{"snapshot files": files.String(), "view": view, "logs": logged.String(), "failures": strings.Join(failures, "\n")}
	for place, text := range where {
		for kind, secret := range plantedSecrets {
			if strings.Contains(text, secret) {
				t.Errorf("the %s value reached the %s", kind, place)
			}
		}
	}
}
