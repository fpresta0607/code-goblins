package codegoblins

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/train"
	"gopkg.in/yaml.v3"
)

// ownRun is how the go workflow says a run is a pull request's own: the only
// kind of run that may leave a job out. A merge train's run comes from a
// branch the train names with its own prefix, whatever event starts it, so
// the expression is built from that prefix and from nothing written twice.
const ownRun = "${{ github.event_name == 'pull_request' && !startsWith(github.head_ref, '" + train.BranchPrefix + "') }}"

// The workflow knows a merge train's run by the start of its branch's name,
// which is text in a YAML file, while the train names its branches from a
// constant of its own. Nothing but this ties the two: were the prefix ever
// renamed in one place, a train's run would read as a pull request's own,
// could leave jobs out, and every check would still pass. So each place the
// workflow tells the two kinds of run apart holds exactly the train's
// prefix, and the workflow names that prefix nowhere else in an expression.
func TestGoWorkflowKnowsATrainsRunByTheTrainsOwnBranchPrefix(t *testing.T) {
	// Arrange
	source, err := os.ReadFile(filepath.Join(".github", "workflows", "go.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if train.BranchPrefix == "" {
		t.Fatal("the train names its branches with no prefix, so no run could be told from a pull request's own")
	}

	// Act
	told := strings.Count(string(source), "startsWith(github.head_ref,")
	holds := strings.Count(string(source), ownRun)

	// Assert
	if told != 2 || holds != 2 {
		t.Errorf("the go workflow tells a train's run by its branch %d times, %d of them as %q: want the plan job and the test job, both by the train's own prefix %q", told, holds, ownRun, train.BranchPrefix)
	}
}

// The plan job says which jobs a run starts, and each other job starts only
// as it says. A job that did not wait for the plan, or read another job's
// answer, would run or be skipped whatever the change, and a job the plan
// has no answer for could be left out of the required check's reckoning. So
// the jobs are exactly the plan, the three it answers for and the check, each
// of the three reads its own answer, and the plan and the check agree on
// which runs may leave a job out.
func TestGoWorkflowJobsStartOnlyAsThePlanSays(t *testing.T) {
	// Arrange
	source, err := os.ReadFile(filepath.Join(".github", "workflows", "go.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Env   map[string]string `yaml:"env"`
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	jobs := goWorkflowJobs(t)
	starts := map[string]string{
		"frontend": "needs.plan.outputs.frontend == 'true'",
		"browser":  "needs.plan.outputs.browser == 'true'",
		"go":       "needs.plan.outputs.go != '[]'",
	}

	// Act
	names := slices.Sorted(maps.Keys(jobs))

	// Assert
	if want := []string{"browser", "frontend", "go", "plan", "test"}; !slices.Equal(names, want) {
		t.Fatalf("the go workflow's jobs are %v, want %v: a new job needs an answer from the plan, a rule in JOBS and a place in the check", names, want)
	}
	for name, condition := range starts {
		job := jobs[name]
		if !slices.Equal(job.Needs, goWorkflowNeeds{"plan"}) || job.If != condition {
			t.Errorf("job %s needs %v and starts if %q, want it to need plan and start if %q", name, job.Needs, job.If, condition)
		}
		if want := "${{ steps.plan.outputs." + name + " }}"; jobs["plan"].Outputs[name] != want {
			t.Errorf("the plan job's output %s is %q, want %q", name, jobs["plan"].Outputs[name], want)
		}
	}
	if include := jobs["go"].Strategy.Matrix.Include; include != "${{ fromJSON(needs.plan.outputs.go) }}" {
		t.Errorf("the go job runs %q, want the go jobs the plan started", include)
	}
	if len(jobs["plan"].Needs) != 0 || jobs["plan"].If != "" {
		t.Errorf("the plan job needs %v and starts if %q, want it to start with every run", jobs["plan"].Needs, jobs["plan"].If)
	}
	checks := ""
	for _, step := range workflow.Jobs["test"].Steps {
		if step.Name == "Require every job to have passed" {
			checks = step.Env["OWN_RUN"]
		}
	}
	if plans := workflow.Jobs["plan"].Env["OWN_RUN"]; plans != ownRun || checks != ownRun {
		t.Errorf("the plan says a run is a pull request's own by %q and the check by %q, want both %q", plans, checks, ownRun)
	}
}

// In a run that is not a pull request's own, the plan starts every job and
// reads nothing: the go jobs exactly as JOBS lists them, the frontend and the
// browser tests.
func TestGoWorkflowsPlanStartsEveryJobOfARunThatIsNotAPullRequestsOwn(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("the step runs in PowerShell 7, which is not installed")
	}
	// Arrange: the step as GitHub Actions runs a pwsh step, which stops on
	// an error, in a folder with no checkout.
	step := workflowStep(t, filepath.Join(".github", "workflows", "go.yml"), "plan", "Choose the jobs this run starts")
	table := goWorkflowTable(t)
	source, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script, outputs := filepath.Join(dir, "step.ps1"), filepath.Join(dir, "outputs.txt")
	if err := os.WriteFile(script, []byte("$ErrorActionPreference = 'stop'\r\n"+step), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OWN_RUN=false", "JOBS="+string(source), "GITHUB_OUTPUT="+outputs)

	// Act
	out, err := cmd.CombinedOutput()

	// Assert
	if err != nil {
		t.Fatalf("the step failed: %v\n%s", err, out)
	}
	written, err := os.ReadFile(outputs)
	if err != nil {
		t.Fatal(err)
	}
	said := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(written), "\r\n", "\n"), "\n") {
		if name, value, found := strings.Cut(line, "="); found {
			said[name] = value
		}
	}
	var shards []gatetest.Shard
	if err := json.Unmarshal([]byte(said["go"]), &shards); err != nil || !slices.Equal(shards, table.Go) || len(shards) == 0 {
		t.Errorf("the plan started the go jobs %q (%v), want every one of JOBS: %+v", said["go"], err, table.Go)
	}
	if len(said) != 3 || said["frontend"] != "true" || said["browser"] != "true" {
		t.Errorf("the plan said %v, want go, frontend true and browser true and nothing else", said)
	}
}
