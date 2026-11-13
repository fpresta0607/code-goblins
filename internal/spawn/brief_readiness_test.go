package spawn

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

type briefEditingAdapter struct {
	harness.Adapter
	path    string
	content string
}

func (a briefEditingAdapter) Validate(ctx context.Context, runner execx.Runner) error {
	if err := os.WriteFile(a.path, []byte(a.content), 0o600); err != nil {
		return err
	}
	return a.Adapter.Validate(ctx, runner)
}

func TestSpawnPreflightsTheValidatedBriefSnapshot(t *testing.T) {
	// Arrange
	fixture := newFixture(t)
	adapter := fixture.service.Harness.Adapters[fixture.request.Harness]
	fixture.service.Harness.Adapters[fixture.request.Harness] = briefEditingAdapter{Adapter: adapter, path: fixture.brief, content: strings.ReplaceAll(completedSpawnBrief, "credentials: none", "credentials: github")}
	preflight := &stubPreflight{}
	fixture.service.Auth = preflight
	refusal := errors.New("fixture stops before publishing")
	fixture.service.Admit = func() error { return refusal }

	// Act: the harness check races with an author's credential edit.
	_, err := fixture.service.Spawn(t.Context(), fixture.request)

	// Assert
	if !errors.Is(err, refusal) || len(preflight.needs) != 1 || len(preflight.needs[0].Services) != 0 {
		t.Fatalf("preflight needs = %+v, error = %v; want the validated none choice", preflight.needs, err)
	}
}

func TestSpawnRejectsDraftBeforeFreezingPolicy(t *testing.T) {
	for _, content := range []string{"{TASK - describe work}", "{ACCEPTANCE - outcomes}", "{CONSTRAINTS - boundaries}", "## Delivery\nready: false\n"} {
		t.Run(content, func(t *testing.T) {
			// Arrange
			fixture := newFixture(t)
			fixture.service.PolicyPath = filepath.Join("..", "..", "config", "pipeline.json")
			writeFile(t, fixture.request.BriefPath, content)

			// Act
			_, err := fixture.service.Spawn(t.Context(), fixture.request)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "brief") {
				t.Fatalf("draft error = %v", err)
			}
			if len(fixture.events) != 0 {
				t.Fatalf("draft reached collaborators: %v", fixture.events)
			}
			if _, err := os.Stat(filepath.Join(fixture.stateDir, "tasktmp", fixture.request.ID, "pipeline.json")); !os.IsNotExist(err) {
				t.Fatalf("draft froze policy: %v", err)
			}
		})
	}
}

func TestSpawnBindsCLIChoicesToTheBriefItRead(t *testing.T) {
	// Arrange
	fixture := newFixture(t)
	fixture.request.BriefHash = sha256.Sum256([]byte("An earlier version of the brief."))

	// Act
	_, err := fixture.service.Spawn(t.Context(), fixture.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "brief changed after routing") {
		t.Fatalf("changed CLI brief = %v", err)
	}
	if len(fixture.events) != 0 {
		t.Fatalf("changed CLI brief reached collaborators: %v", fixture.events)
	}
}

func TestSpawnRefusesABriefChangedDuringAdmission(t *testing.T) {
	// Arrange
	fixture := newFixture(t)
	fixture.service.Admit = func() error {
		return os.WriteFile(fixture.request.BriefPath, []byte("## Task\nDifferent work.\n## Delivery\nready: false\n"), 0o600)
	}

	// Act
	_, err := fixture.service.Spawn(t.Context(), fixture.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "brief") {
		t.Fatalf("changed brief error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.stateDir, fixture.request.ID+".meta")); !os.IsNotExist(err) {
		t.Fatalf("changed brief published identity: %v", err)
	}
}

func TestSpawnAdmitsAHelpersBriefOnlyForAHelper(t *testing.T) {
	for _, test := range []struct {
		name, parent, id, mode, brief, reason string
	}{
		{name: "a task spawned without a parent", mode: "no-mistakes", brief: helperOfferBrief, reason: "author the Acceptance criteria section"},
		{name: "a helper without an mcp line", parent: "g1", id: "g1-h1", mode: "local-only", brief: strings.ReplaceAll(helperOfferBrief, "mcp: none\n", ""), reason: "helper contract"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			fixture := newFixture(t)
			fixture.request.Parent, fixture.request.Mode = test.parent, test.mode
			if test.id != "" {
				fixture.request.ID = test.id
			}
			writeFile(t, fixture.request.BriefPath, test.brief)

			// Act
			_, err := fixture.service.Spawn(t.Context(), fixture.request)

			// Assert
			if !errors.Is(err, fleet.ErrBriefNotReady) || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("spawn = %v, want a refusal that says %q", err, test.reason)
			}
			if len(fixture.events) != 0 {
				t.Fatalf("refused brief reached collaborators: %v", fixture.events)
			}
			if _, err := os.Stat(filepath.Join(fixture.stateDir, fixture.request.ID+".meta")); !os.IsNotExist(err) {
				t.Fatalf("refused brief published a task: %v", err)
			}
		})
	}
}
