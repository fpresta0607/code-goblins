package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A task cleanup finds delivered leaves ## Queued for ## Done in the cleanup
// that retires it, with its detail lines and every other line as it was, so
// the board stops listing finished work as Not started; one it finds
// undelivered keeps its row. On 2026-10-07 the CFO had left 24 finished rows
// under Queued.
func TestCleanupMovesADeliveredTasksQueuedRowToDone(t *testing.T) {
	const backlog = "# Backlog\n\n## Queued\n\n- **other** - Other work\n  detail: keep me\n- **g1** - Ship it (repo: project)\n  detail: line one\n\n  detail: line two\n- **after** - After\n\n## Done\n\n- [x] old - Old (done 2026-10-01)\n"
	for _, testCase := range []struct {
		name               string
		delivered, isForce bool
	}{
		{name: "delivered", delivered: true},
		{name: "delivered and force-archived", delivered: true, isForce: true},
		{name: "not delivered", delivered: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			fixture := newCleanupFixture(t)
			data := t.TempDir()
			fixture.service.Data = data
			if err := os.WriteFile(filepath.Join(data, "backlog.md"), []byte(backlog), 0o600); err != nil {
				t.Fatal(err)
			}
			if testCase.delivered {
				if err := os.WriteFile(filepath.Join(fixture.stateDir, "g1.status"), []byte("2026-10-07T10:00:00Z done: PR https://github.com/owner/project/pull/42\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.isForce {
				fixture.git.top = fixture.project
				fixture.service.ForceArchive = true
			}

			// Act
			result, err := fixture.service.Cleanup(context.Background(), "g1")

			// Assert
			if err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(data, "backlog.md"))
			if err != nil {
				t.Fatal(err)
			}
			want := backlog
			if testCase.delivered {
				outcome, err := state.ReadOutcome(fixture.stateDir, "g1")
				if err != nil {
					t.Fatal(err)
				}
				want = "# Backlog\n\n## Queued\n\n- **other** - Other work\n  detail: keep me\n- **after** - After\n\n## Done\n- [x] g1 - Ship it (repo: project) https://github.com/owner/project/pull/42 (done " + outcome.At.UTC().Format("2006-01-02") + ")\n  detail: line one\n\n  detail: line two\n\n- [x] old - Old (done 2026-10-01)\n"
			}
			if string(got) != want {
				t.Errorf("backlog =\n%s\nwant\n%s\noutput: %s", got, want, result.Output)
			}
		})
	}
}
