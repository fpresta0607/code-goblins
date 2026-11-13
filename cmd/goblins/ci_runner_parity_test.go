package main

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/ciparity"
	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// apiAnswering answers a GET of GitHub's API with the body each path, with its
// query, names, and any other request as GitHub answers a path that does not
// exist.
type apiAnswering map[string]string

func (a apiAnswering) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	body, isKnown := a[strings.TrimPrefix(request.URL.RequestURI(), "/")]
	if request.Method != http.MethodGet || !isKnown {
		githubtest.Refuse(w, http.StatusNotFound, "Not Found")
		return
	}
	_, _ = w.Write([]byte(body))
}

func TestCIRunnerParityRecordsEachMirroredJobOnceAndSaysWhereItStands(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	runtime := defaultCommandRuntime()
	runtime.resolveHome = func() (home.Home, error) { return h, nil }
	merge := "Merge " + strings.Repeat("a", 40) + " into " + strings.Repeat("b", 40)
	answering := apiAnswering{
		"repos/o/r/actions/workflows/desktop-mirror.yml/runs?status=completed&per_page=30": `{"workflow_runs":[{"id":200,"display_title":"Desktop mirror of CI run 100","status":"completed","conclusion":"success"}]}`,
		"repos/o/r/actions/runs/200/jobs?per_page=100":                                     `{"jobs":[{"id":21,"name":"desktop-backend-shard (1)","conclusion":"success","started_at":"2026-10-21T23:33:00Z","completed_at":"2026-10-21T23:45:00Z","runner_name":"cg-desktop-a"}]}`,
		"repos/o/r/actions/runs/100/jobs?per_page=100":                                     `{"jobs":[{"id":11,"name":"backend-shard (1)","conclusion":"success","started_at":"2026-10-21T22:33:00Z","completed_at":"2026-10-21T22:42:00Z","runner_name":"GitHub Actions 7"}]}`,
		"repos/o/r/actions/jobs/21/logs":                                                   "desktop mirror: " + merge + ", tree 0123\n===== 10 passed in 1.00s =====",
		"repos/o/r/actions/jobs/11/logs":                                                   "HEAD is now at f04064ade " + merge + "\n===== 10 passed in 1.00s =====",
	}
	runtime.githubAPI = githubtest.Client(answering)

	// Act
	var first, again, stderr bytes.Buffer
	exit := runCIRunnerParity([]string{"--repo", "o/r"}, &first, &stderr, runtime)
	againExit := runCIRunnerParity([]string{"--repo", "o/r"}, &again, &stderr, runtime)

	// Assert
	if exit != 0 || againExit != 0 {
		t.Fatalf("exits = %d, %d, stderr %q: want 0", exit, againExit, stderr.String())
	}
	path := ciparity.Path(watch.CIRunnerFolder(h.State), "o/r")
	if !strings.Contains(first.String(), "1 new pair(s), 1 in the record "+path) || !strings.Contains(first.String(), "backend-shard (1): 1 run(s) in a row matched, of 1 pair(s)\n") {
		t.Fatalf("first = %q, want the new pair recorded and the job's standing", first.String())
	}
	if !strings.Contains(again.String(), "0 new pair(s), 1 in the record") {
		t.Fatalf("again = %q, want the pair the record holds not read again", again.String())
	}
	record, err := ciparity.Read(path, "o/r")
	if err != nil || len(record.Pairs) != 1 || record.Pairs[0].CIRun != 100 || record.Pairs[0].Desktop.Runner != "cg-desktop-a" {
		t.Fatalf("record = %+v, %v: want the one pair kept", record, err)
	}
}

func TestCIRunnerParityRefusesWhatItCannotRead(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--repo", "r"},
		{"--repo", "o/r", "--runs", "0"},
		{"--repo", "o/r", "--runs", "101"},
		{"--repo", "o/r", "--workflow", "../other.yml"},
		{"--repo", "o/r", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			exit := runCIRunnerParity(args, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), "usage: goblins ci-runner parity") {
				t.Fatalf("exit = %d, stderr %q: want the usage and 2", exit, stderr.String())
			}
		})
	}
}
