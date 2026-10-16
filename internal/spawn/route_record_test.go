package spawn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnKeepsRoutingOnTaskRecord(t *testing.T) {
	request := Request{ID: "routed", RouteLane: "mechanical", RouteReason: "narrow change"}
	result := partialResult(request, "project", "temporary", "worktree", "scratch")
	directory := t.TempDir()
	if err := state.WriteTaskMeta(directory, result.Meta); err != nil {
		t.Fatal(err)
	}
	record, err := state.ReadTaskMeta(directory, request.ID)
	if err != nil || record.RouteLane != request.RouteLane || record.RouteReason != request.RouteReason {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}

func TestSpawnPassesFleetSubagentsToTheHarness(t *testing.T) {
	f := newFixture(t)
	f.request.Model = "claude-opus-5-5"
	policy, err := os.ReadFile(filepath.Join("..", "..", "data", "routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.dataDir, "routing.json"), string(policy))
	stop := errors.New("stop after observing the launch")
	f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, specs: &f.specs, buildErr: stop}
	_, err = f.service.Spawn(context.Background(), f.request)
	if !errors.Is(err, stop) || len(f.specs) != 1 {
		t.Fatalf("launches=%v err=%v", f.specs, err)
	}
	roles := f.specs[0].Subagents
	if roles["search"].Model != "haiku" || roles["review"].Model != f.request.Model || roles["verify"].Model != "sonnet" {
		t.Fatalf("launch roles=%+v", roles)
	}
}

func TestSpawnRefusesUnknownVerificationModelBeforeAcquiringWork(t *testing.T) {
	f := newFixture(t)
	f.request.Model = "default"
	writeFile(t, filepath.Join(f.dataDir, "routing.json"), `{"subagents":{"claude":{}}}`)
	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || len(mutations(f.events)) != 0 {
		t.Fatalf("events=%v err=%v", f.events, err)
	}
}
