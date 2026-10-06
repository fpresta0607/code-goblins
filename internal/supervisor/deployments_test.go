package supervisor

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// pushRun is one push run on main as gh run list reports it: workflow's run
// for commit, titled title, in status and conclusion, with its own page.
func pushRun(id int, workflow, title, commit, status, conclusion string) string {
	run, _ := json.Marshal(map[string]any{
		"databaseId": id, "workflowName": workflow, "displayTitle": title, "status": status, "conclusion": conclusion,
		"headSha": commit, "url": "https://github.com/o/r/actions/runs/" + strconv.Itoa(id),
		"startedAt": "2026-09-30T12:20:00Z", "updatedAt": "2026-09-30T12:25:00Z",
	})
	return string(run)
}

const merged210 = "Merge pull request #210 from o/feat/ship"

// cardDeployment reads the deployment the board's snapshot shows on a task's
// card, as the JSON the board reads, after one CI poll of runs: a live goblin
// keeps the repository watched, and task cg-shipped, finished with pull
// request 210 merged, is a card in Completed.
func cardDeployment(t *testing.T, runs ...string) map[string]any {
	t.Helper()
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "cg-shipped", Title: "Ship the export", Project: project, Phase: "done", Reason: "merged", Evidence: "merged", PR: "https://github.com/o/r/pull/210", At: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.runs, forge.jobs = "feat/wakes", "[]", "["+strings.Join(runs, ",")+"]", `{"jobs":[]}`
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 31, 0, 0, time.UTC)
	if err := s.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	s.cycle(context.Background(), false)
	if err := s.refreshHistory(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return strings.HasSuffix(task.ID, "cg-shipped") })
	if index < 0 {
		t.Fatalf("tasks = %+v, want cg-shipped's card", snapshot.Tasks)
	}
	data, err := json.Marshal(snapshot.Tasks[index])
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		t.Fatal(err)
	}
	deployment, _ := card["deployment"].(map[string]any)
	if deployment != nil {
		delete(deployment, "at")
	}
	return deployment
}

// A merged task's card says how the deploy its merge started stands, read
// from the default branch's push runs: the newest run of each workflow whose
// name says deploy, for its merge commit, merged or squashed. CI workflows
// are not deploys, and another pull request's deploy is not this one's.
func TestDeploymentsReachTheCardOfTheMergedTask(t *testing.T) {
	for _, test := range []struct {
		name string
		runs []string
		want string
	}{
		{"still deploying", []string{pushRun(71, "Deploy", merged210, "aaa111", "in_progress", "")},
			`{"commit":"aaa111","link":"https://github.com/o/r/actions/runs/71","state":"deploying","workflows":["Deploy"]}`},
		{"deployed, its CI apart", []string{pushRun(72, "go", merged210, "aaa111", "completed", "failure"), pushRun(71, "Deploy", merged210, "aaa111", "completed", "success")},
			`{"commit":"aaa111","link":"https://github.com/o/r/actions/runs/71","state":"deployed","workflows":["Deploy"]}`},
		{"one of two deploys failed", []string{pushRun(73, "Deploy worker", merged210, "aaa111", "completed", "failure"), pushRun(71, "Deploy API", merged210, "aaa111", "in_progress", "")},
			`{"commit":"aaa111","link":"https://github.com/o/r/actions/runs/73","state":"failed","workflows":["Deploy worker","Deploy API"]}`},
		{"cancelled", []string{pushRun(71, "deploy", merged210, "aaa111", "completed", "cancelled")},
			`{"commit":"aaa111","link":"https://github.com/o/r/actions/runs/71","state":"cancelled","workflows":["deploy"]}`},
		{"a squashed merge, and only the newest run of a workflow", []string{pushRun(75, "Deploy", "Ship the export (#210)", "bbb222", "completed", "success"), pushRun(74, "Deploy", "Ship the export (#210)", "bbb222", "completed", "failure")},
			`{"commit":"bbb222","link":"https://github.com/o/r/actions/runs/75","state":"deployed","workflows":["Deploy"]}`},
		{"another pull request's deploy", []string{pushRun(76, "Deploy", "Merge pull request #211 from o/feat/other", "ccc333", "completed", "failure")}, `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			deployment := cardDeployment(t, test.runs...)

			// Assert
			got, _ := json.Marshal(deployment)
			if string(got) != test.want {
				t.Errorf("the card's deployment = %s, want %s", got, test.want)
			}
		})
	}
}
