package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/tickets"
)

func TestARedRunOfMainIsARootIssueOfItsWorkflowAndJobs(t *testing.T) {
	cases := []struct {
		name, jobs string
		want       []string
	}{
		{name: "its failed jobs read", jobs: `{"jobs":[{"name":"go (rest)","conclusion":"failure"},{"name":"plan","conclusion":"success"}]}`, want: []string{"ci:go: job go (rest)"}},
		{name: "its failed jobs unread", jobs: `not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			_, h := fleetService(t)
			var watched fleetWakes
			checkout := t.TempDir()
			forge := &fakeForge{repo: checkout, runs: `[{"databaseId":37563097409,"workflowName":"go","status":"completed","conclusion":"failure","headSha":"7c94ef3aaa","url":"https://github.com/o/r/actions/runs/37563097409"}]`, jobs: tc.jobs}
			var found []string
			record := func(project string, root tickets.Root) {
				if project != checkout || root.Evidence != "https://github.com/o/r/actions/runs/37563097409" {
					t.Errorf("found %q %+v, want the checkout and the run", project, root)
				}
				found = append(found, root.RootKey())
			}

			// Act: the same run polled twice.
			now := time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC)
			for range 2 {
				if _, err := pollMain(context.Background(), forge, h.State, &watched, checkout, now, record); err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Hour)
			}

			// Assert
			if len(found) != len(tc.want) || len(found) > 0 && found[0] != tc.want[0] {
				t.Fatalf("found %v, want %v: once a run, and only with its jobs read", found, tc.want)
			}
		})
	}
}
