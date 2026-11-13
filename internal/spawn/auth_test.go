package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// stubPreflight stands in for the auth package so spawn's contribution -
// where the credentials go and what the goblin sees - is tested on its own.
type stubPreflight struct {
	result   auth.Result
	err      error
	projects []string
	needs    []auth.Need
}

func (p *stubPreflight) Preflight(_ context.Context, project string, need auth.Need) (auth.Result, error) {
	p.projects = append(p.projects, project)
	p.needs = append(p.needs, need)
	return p.result, p.err
}

func TestWriteAuthScriptSwapsAExistingScriptAtomically(t *testing.T) {
	taskTmp := t.TempDir()
	old := filepath.Join(taskTmp, "auth.ps1")
	if err := os.WriteFile(old, []byte("$env:OLD = 'stale'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, vars, err := writeAuthScript(taskTmp, map[string]string{"FLY_API_TOKEN": "fresh"})
	if err != nil {
		t.Fatalf("writeAuthScript: %v", err)
	}
	if path != old || vars != 1 {
		t.Fatalf("path = %q vars = %d, want %q and 1", path, vars, old)
	}
	script, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "$env:FLY_API_TOKEN = 'fresh'") || strings.Contains(string(script), "stale") {
		t.Errorf("script =\n%s\nwant the rewrite to fully replace the old content", script)
	}
	entries, err := os.ReadDir(taskTmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "auth.ps1" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("tasktmp = %v, want only auth.ps1 with no temp file left behind", names)
	}
}

func TestWriteAuthScriptFailureLeavesNoArtifacts(t *testing.T) {
	root := t.TempDir()
	// A tasktmp that cannot be created: the swap must fail before any temp
	// file or partial script appears.
	blocker := filepath.Join(root, "tasktmp")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := writeAuthScript(blocker, map[string]string{"FLY_API_TOKEN": "x"}); err == nil {
		t.Fatal("writeAuthScript = nil error, want a failure for an unusable tasktmp")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "tasktmp" {
		t.Errorf("root changed by the failed write: %d entries, want only the blocker", len(entries))
	}
}

func TestSpawnFailsLoudlyWhenThePreflightItselfBreaks(t *testing.T) {
	f := newFixture(t)
	f.service.Auth = &stubPreflight{err: errTestPreflight}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "project auth preflight") {
		t.Fatalf("err = %v, want the preflight failure surfaced", err)
	}
}

var errTestPreflight = &preflightError{}

type preflightError struct{}

func (*preflightError) Error() string { return "credential store is unreachable" }

func TestSpawnRefusesARedBlockingServiceAndPrintsTheFixCommand(t *testing.T) {
	f := newFixture(t)
	f.request.Yolo = false
	f.service.Auth = &stubPreflight{result: auth.Result{
		Warning: "auth: 0/1 services green for primary; BLOCKING: postgres (missing)",
		Refusal: "1 blocking service(s) for primary; fix these or pass --yolo to dispatch anyway\n  postgres (missing): did not resolve: DATABASE_URL\n    goblins auth store --project primary DATABASE_URL",
	}}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil {
		t.Fatal("Spawn = nil, want a red blocking service to stop the dispatch")
	}
	// The warning scrolled past every goblin spawned that night. A refusal
	// the operator cannot ignore is the control; the fix has to be in it.
	if !strings.Contains(err.Error(), "goblins auth store --project primary DATABASE_URL") {
		t.Errorf("err = %v, want the exact fix command printed", err)
	}
}
