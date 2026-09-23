package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestWorkspaceMetadataReportsNamesAndModelEvidenceWithoutSecrets(t *testing.T) {
	s, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	meta.Model = "default"
	_ = state.WriteTaskMeta(h.State, meta)
	dir := filepath.Dir(auth.ManifestPath(h.Data, meta.Project))
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"project":"work","services":[{"name":"declared","method":"env","env":["DECLARED_TOKEN"]}]}`), 0600)
	_ = os.WriteFile(filepath.Join(dir, "worktree.json"), []byte(`{"project":"work","env":{"SCOPED_NAME":"synthetic-value-must-not-appear"}}`), 0600)
	_ = os.WriteFile(filepath.Join(meta.Worktree, ".mcp.json"), []byte(`{"mcpServers":{"repo-tools":{"command":"secret-command","headers":{"Authorization":"synthetic-authorization"},"env":{"INTERNAL_SECRET":"synthetic-secret"}}}}`), 0600)
	s.db.TaskSessions[meta.ID] = "reported"
	s.db.Sessions["reported"] = Session{Generation: meta.SpawnGen, Model: "gpt-6-astra"}
	service := &Service{Store: s}
	details, err := service.workspaceDetail(context.Background(), meta.ID, meta.SpawnGen)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(details)
	text := string(data)
	for _, forbidden := range []string{"synthetic-", "secret-command", "Authorization", "INTERNAL_SECRET"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("workspace leaked config values")
		}
	}
	if details.Model != "Reported: gpt-6-astra" || len(details.MCP) != 1 || details.MCP[0].Status != "Configured" || len(details.Environment) != 2 {
		t.Fatal("wrong scoped metadata", details)
	}
	node := s.db.Sessions["reported"]
	node.Generation = "previous"
	s.db.Sessions["reported"] = node
	details, err = service.workspaceDetail(context.Background(), meta.ID, meta.SpawnGen)
	if err != nil || details.Model != "Configured: default" {
		t.Fatal("stale model presented as live", details.Model, err)
	}
}
