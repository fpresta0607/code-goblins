package cleanup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestCleanupRecordsStoppedWithoutDeliveredWork(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.meta.Title = "Check launch behavior"
	if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Cleanup(context.Background(), fixture.meta.ID); err != nil {
		t.Fatal(err)
	}
	outcome, err := state.ReadOutcome(fixture.stateDir, fixture.meta.ID)
	if err != nil || outcome.Phase != "stopped" || outcome.Title != fixture.meta.Title || outcome.Project != fixture.meta.Project || outcome.Evidence != "" {
		t.Fatalf("outcome=%+v %v", outcome, err)
	}
}

type deliveredCommitsRunner struct {
	head       string
	remoteHead string
	count      string
}

func (runner deliveredCommitsRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch strings.Join(request.Args, " ") {
	case "symbolic-ref --short HEAD":
		return execx.Result{Stdout: []byte("feat/task")}, nil
	case "rev-parse HEAD":
		return execx.Result{Stdout: []byte(runner.head)}, nil
	case "ls-remote origin refs/heads/feat/task":
		return execx.Result{Stdout: []byte(runner.remoteHead + "\trefs/heads/feat/task")}, nil
	case "rev-list --count origin/HEAD..HEAD":
		return execx.Result{Stdout: []byte(runner.count)}, nil
	default:
		return execx.Result{}, fmt.Errorf("unexpected git request: %v", request.Args)
	}
}

func TestCleanupCountsOnlyPushedTaskCommitsAsDelivery(t *testing.T) {
	head := strings.Repeat("a", 40)
	for _, test := range []struct {
		name, remote, count, phase string
	}{
		{"pushed task commits", head, "2", "done"},
		{"unchanged base", head, "0", "stopped"},
		{"unpushed commits", strings.Repeat("b", 40), "2", "stopped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := Service{StateDir: t.TempDir(), Commands: deliveredCommitsRunner{head: head, remoteHead: test.remote, count: test.count}}
			outcome := service.outcome(t.Context(), state.TaskMeta{ID: "task", Kind: "ship"}, "Task cleaned up")
			if outcome.Phase != test.phase || (outcome.Evidence != "") != (test.phase == "done") {
				t.Fatalf("delivery=%+v", outcome)
			}
		})
	}
}

func TestCleanupRequiresCurrentDeliveryAndKeepsPRDetailsOnStop(t *testing.T) {
	for _, kind := range []string{"pull request", "stopped pull request", "old generation", "report"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newCleanupFixture(t)
			fixture.meta.SpawnGen = fmt.Sprintf("s%d", time.Now().Add(-time.Minute).UnixNano())
			if kind == "report" {
				fixture.meta.Kind = "scout"
				fixture.meta.Brief = filepath.Join(t.TempDir(), "brief.md")
				if err := os.WriteFile(fixture.meta.Brief, []byte("Deliver report.md with the findings."), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(fixture.meta.Brief), "report.md"), []byte("Verified findings."), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				at := time.Now()
				if kind == "old generation" {
					at = at.Add(-time.Hour)
				}
				if err := os.WriteFile(filepath.Join(fixture.stateDir, fixture.meta.ID+".status"), []byte(at.UTC().Format(time.RFC3339)+" done: PR https://github.com/owner/project/pull/42\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "stopped pull request" {
				if err := state.WriteLifecycle(fixture.stateDir, state.Lifecycle{ID: fixture.meta.ID, Generation: fixture.meta.SpawnGen, Operation: "stop-1", Action: "stop", Phase: "stopping", Reason: "Requested from the board"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.service.Cleanup(context.Background(), fixture.meta.ID); err != nil {
				t.Fatal(err)
			}
			outcome, err := state.ReadOutcome(fixture.stateDir, fixture.meta.ID)
			want := "done"
			if kind == "old generation" || kind == "stopped pull request" {
				want = "stopped"
			}
			if err != nil || outcome.Phase != want || want == "done" && outcome.Evidence == "" || kind == "stopped pull request" && (outcome.PR == "" || outcome.Reason != "Requested from the board") {
				t.Fatalf("outcome=%+v %v", outcome, err)
			}
		})
	}
}
