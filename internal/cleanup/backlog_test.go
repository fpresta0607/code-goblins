package cleanup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A task cleanup retires leaves ## Queued for ## Done in the cleanup that
// retires it, with its detail lines and every other line as it was, whatever
// its last status was, so nothing starts it again from its row: closed as
// done when cleanup finds it delivered and as retired when it does not. On
// 2026-10-07 the CFO had left 24 finished rows under Queued, and on
// 2026-10-09 the cleanup of cg-afk-left-in-command-center, paused for memory
// with its pull request merged and no done report, left its row there.
func TestCleanupMovesARetiredTasksQueuedRowToDone(t *testing.T) {
	const backlog = "# Backlog\n\n## Queued\n\n- **other** - Other work\n  detail: keep me\n- **g1** - Ship it (repo: project)\n  detail: line one\n\n  detail: line two\n- **after** - After\n\n## Done\n\n- [x] old - Old (done 2026-10-01)\n"
	const delivered = "2026-10-07T10:00:00Z done: PR https://github.com/owner/project/pull/42\n"
	for _, testCase := range []struct {
		name, status, closed string
		isPaused, isForce    bool
	}{
		{name: "delivered", status: delivered, closed: " https://github.com/owner/project/pull/42 (done "},
		{name: "delivered and force-archived", status: delivered, closed: " https://github.com/owner/project/pull/42 (done ", isForce: true},
		{name: "paused for memory with no done report", status: "2026-10-09T14:21:11Z working: writing the tests\n2026-10-09T14:39:15Z lifecycle-paused: memory; task session and branch\n", closed: " (retired ", isPaused: true},
		{name: "last reported failed", status: "2026-10-09T14:21:11Z failed: the build broke\n", closed: " (retired "},
		{name: "last reported blocked", status: "2026-10-09T14:21:11Z blocked: which base branch?\n", closed: " (retired "},
		{name: "reported nothing", closed: " (retired "},
		{name: "reported nothing and force-archived", closed: " (retired ", isForce: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			fixture := newCleanupFixture(t)
			data := t.TempDir()
			fixture.service.Data = data
			if err := os.WriteFile(filepath.Join(data, "backlog.md"), []byte(backlog), 0o600); err != nil {
				t.Fatal(err)
			}
			if testCase.status != "" {
				if err := os.WriteFile(filepath.Join(fixture.stateDir, "g1.status"), []byte(testCase.status), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.isPaused {
				paused := time.Date(2026, 10, 9, 14, 38, 48, 0, time.UTC)
				if err := state.WriteLifecycle(fixture.stateDir, state.Lifecycle{ID: "g1", Generation: fixture.meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Reason: "memory", Pause: &state.PauseCondition{Reason: "memory", At: paused}, Started: paused, Updated: paused}); err != nil {
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
			outcome, err := state.ReadOutcome(fixture.stateDir, "g1")
			if err != nil {
				t.Fatal(err)
			}
			want := "# Backlog\n\n## Queued\n\n- **other** - Other work\n  detail: keep me\n- **after** - After\n\n## Done\n- [x] g1 - Ship it (repo: project)" + testCase.closed + outcome.At.UTC().Format("2006-01-02") + ")\n  detail: line one\n\n  detail: line two\n\n- [x] old - Old (done 2026-10-01)\n"
			if string(got) != want || !strings.Contains(result.Output, "its backlog row is under ## Done") {
				t.Errorf("backlog =\n%s\nwant\n%s\noutput: %s", got, want, result.Output)
			}
		})
	}
}

// A row cleanup cannot move stays where it is, the task is retired all the
// same, and cleanup says what becomes of the row: the supervisor moves a
// delivered task's row once it can, and the scheduler never starts any other
// retired task's row and tells the CFO of it.
func TestCleanupSaysWhatBecomesOfARowItCannotMove(t *testing.T) {
	const backlog = "## Queued\n- **g1** - Ship it\n- **g1** - Ship it twice\n"
	for _, testCase := range []struct {
		name, status, want string
	}{
		{name: "delivered", status: "2026-10-07T10:00:00Z done: PR https://github.com/owner/project/pull/42\n", want: "the supervisor moves it to ## Done once it can"},
		{name: "not delivered", want: "the scheduler does not start it again and wakes you about the row"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			fixture := newCleanupFixture(t)
			data := t.TempDir()
			fixture.service.Data = data
			if err := os.WriteFile(filepath.Join(data, "backlog.md"), []byte(backlog), 0o600); err != nil {
				t.Fatal(err)
			}
			if testCase.status != "" {
				if err := os.WriteFile(filepath.Join(fixture.stateDir, "g1.status"), []byte(testCase.status), 0o600); err != nil {
					t.Fatal(err)
				}
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
			if string(got) != backlog || !strings.Contains(result.Output, "warning: its backlog row stays under ## Queued") || !strings.Contains(result.Output, testCase.want) {
				t.Errorf("backlog =\n%s\noutput: %s\nwant the row left and a warning saying %q", got, result.Output, testCase.want)
			}
		})
	}
}

// A local-only task opens no pull request: its done line delivers it when it
// names the report the home keeps for it, data/<id>/report.md, and the report
// is there, so its cleanup moves its row to ## Done as a pull request's does.
// On 2026-10-09 Bruno's local-only scout reported done with his report and
// no pull request, cleanup left his row under ## Queued, and the scheduler
// started the same scout again as Trudy.
func TestCleanupDeliversALocalOnlyTaskWhoseDoneLineNamesItsReport(t *testing.T) {
	const backlog = "# Backlog\n\n## Queued\n\n- **g1** - Prove the strays (repo: project)\n- **after** - After\n\n## Done\n\n- [x] old - Old (done 2026-10-01)\n"
	for _, testCase := range []struct {
		name, mode, report string
		done               func(data string) string
		isEarlier          bool
		isDelivered        bool
	}{
		{
			name: "names its report by its full path", mode: "local-only", report: "Nineteen strays, each proven.",
			done: func(data string) string {
				return "done: PR none (local-only scout). Report: " + filepath.Join(data, "g1", "report.md") + ". All 19 strays proven safe to clear."
			},
			isDelivered: true,
		},
		{
			name: "names its report by its path in the home", mode: "local-only", report: "Nineteen strays, each proven.",
			done:        func(string) string { return "done: PR none. Report: data/g1/report.md" },
			isDelivered: true,
		},
		{
			name: "names a report that is not there", mode: "local-only",
			done: func(data string) string { return "done: PR none. Report: " + filepath.Join(data, "g1", "report.md") },
		},
		{
			name: "names an empty report", mode: "local-only", report: " ",
			done: func(data string) string { return "done: PR none. Report: " + filepath.Join(data, "g1", "report.md") },
		},
		{
			name: "names no report", mode: "local-only", report: "Nineteen strays, each proven.",
			done: func(string) string { return "done: PR none (local-only scout). All 19 strays proven safe to clear." },
		},
		{
			name: "named it in an earlier generation", mode: "local-only", report: "Nineteen strays, each proven.", isEarlier: true,
			done: func(data string) string { return "done: PR none. Report: " + filepath.Join(data, "g1", "report.md") },
		},
		{
			name: "a direct-PR task that opened no pull request", mode: "direct-PR", report: "Nineteen strays, each proven.",
			done: func(data string) string { return "done: PR none. Report: " + filepath.Join(data, "g1", "report.md") },
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			fixture := newCleanupFixture(t)
			data := filepath.Join(t.TempDir(), "data")
			fixture.service.Data = data
			if err := os.MkdirAll(filepath.Join(data, "g1"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, "backlog.md"), []byte(backlog), 0o600); err != nil {
				t.Fatal(err)
			}
			if testCase.report != "" {
				if err := os.WriteFile(filepath.Join(data, "g1", "report.md"), []byte(strings.TrimSpace(testCase.report)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fixture.meta.Kind, fixture.meta.Mode = "ship", testCase.mode
			fixture.meta.Brief = filepath.Join(data, "g1", "brief.md")
			fixture.meta.SpawnGen = fmt.Sprintf("s%d", time.Now().Add(-time.Minute).UnixNano())
			if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
				t.Fatal(err)
			}
			reported := time.Now()
			if testCase.isEarlier {
				reported = reported.Add(-time.Hour)
			}
			if err := os.WriteFile(filepath.Join(fixture.stateDir, "g1.status"), []byte(reported.UTC().Format(time.RFC3339)+" "+testCase.done(data)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			result, err := fixture.service.Cleanup(context.Background(), "g1")

			// Assert
			if err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			outcome, err := state.ReadOutcome(fixture.stateDir, "g1")
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(data, "backlog.md"))
			if err != nil {
				t.Fatal(err)
			}
			closed := "retired"
			if testCase.isDelivered {
				closed = "done"
			}
			want := "# Backlog\n\n## Queued\n\n- **after** - After\n\n## Done\n- [x] g1 - Prove the strays (repo: project) (" + closed + " " + outcome.At.UTC().Format("2006-01-02") + ")\n\n- [x] old - Old (done 2026-10-01)\n"
			if !testCase.isDelivered {
				if outcome.Phase != "stopped" || string(got) != want {
					t.Errorf("outcome=%+v backlog=\n%s\nwant\n%s\noutput: %s\nwant it undelivered with its row closed as retired", outcome, got, want, result.Output)
				}
				return
			}
			if outcome.Phase != "done" || !strings.Contains(outcome.Evidence, filepath.Join(data, "g1", "report.md")) {
				t.Errorf("outcome=%+v, want done with its report as the evidence", outcome)
			}
			if string(got) != want || !strings.Contains(result.Output, "its backlog row is under ## Done") {
				t.Errorf("backlog =\n%s\nwant\n%s\noutput: %s", got, want, result.Output)
			}
		})
	}
}
