package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/services"
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
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
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
	if details.Model != "Reported: gpt-6-astra" || strings.Contains(text, "Configured") || strings.Contains(text, "Declared") {
		t.Fatal("wrong scoped metadata", details)
	}
	node := s.db.Sessions["reported"]
	node.Generation = "previous"
	s.db.Sessions["reported"] = node
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	details, err = service.workspaceDetail(context.Background(), meta.ID, meta.SpawnGen)
	if err != nil || details.Model != "Configured: default" {
		t.Fatal("stale model presented as live", details.Model, err)
	}
}

// A goblin's Workspace names the local services it holds, who it shares them
// with and the memory they take, and the CFO's names every stack held, so
// the board shows what a full-stack check costs while it runs.
func TestWorkspaceNamesTheLocalServicesHeldAndTheirMemory(t *testing.T) {
	s, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	record := services.Record{
		Engine: services.Engine{StartedByCFO: true},
		Stacks: map[string]services.Stack{
			"PrecisionDocs-AI": {Project: "PrecisionDocs-AI", Owned: true, Holders: []services.Hold{{Task: "task-1"}, {Task: "task-2"}}, Cost: services.Cost{Bytes: 2560 << 20}},
			"siqsermon":        {Project: "siqsermon", Holders: []services.Hold{{Task: "task-2"}}},
			"peakCraftsman":    {Project: "peakCraftsman", Cost: services.Cost{Bytes: 1 << 30}},
		},
	}
	if err := services.WriteRecord(h.State, record); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: s}

	details, err := service.workspaceDetail(context.Background(), meta.ID, meta.SpawnGen)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Holds PrecisionDocs-AI's local services with task-2, which take 2.5 GB of memory."}
	if !slices.Equal(details.Notes, want) {
		t.Errorf("task notes = %q, want %q", details.Notes, want)
	}

	details, err = service.workspaceDetail(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{
		"PrecisionDocs-AI's local services are up for task-1 and task-2, which take 2.5 GB of memory.",
		"siqsermon's local services are up for task-2, whose memory is not measured yet.",
		"cfo started the Docker engine for them and stops it once none of them runs.",
	}
	if !slices.Equal(details.Notes, want) {
		t.Errorf("CFO notes = %q, want %q", details.Notes, want)
	}
}

// A services record the board cannot read says so, rather than reading as
// nothing held.
func TestWorkspaceSaysWhenTheLocalServicesCannotBeRead(t *testing.T) {
	s, h := testStore(t)
	if err := os.WriteFile(filepath.Join(h.State, services.RecordName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	details, err := (&Service{Store: s}).workspaceDetail(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(details.Notes, []string{"The local services cfo holds could not be read."}) {
		t.Errorf("notes = %q, want the unreadable record named", details.Notes)
	}
}
