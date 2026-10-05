package codegoblins

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installtest"
	"gopkg.in/yaml.v3"
)

// workflowStep is the run script of the step named step in job of the
// workflow at path.
func workflowStep(t *testing.T, path, job, step string) string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range workflow.Jobs[job].Steps {
		if candidate.Name == step {
			return candidate.Run
		}
	}
	t.Fatalf("%s has no step %q in job %s", path, step, job)
	return ""
}

// goWorkflowJobs is the jobs of the go workflow, as far as its tests read
// them.
func goWorkflowJobs(t *testing.T) map[string]goWorkflowJob {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(".github", "workflows", "go.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]goWorkflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow.Jobs
}

type goWorkflowJob struct {
	If       string   `yaml:"if"`
	Needs    []string `yaml:"needs"`
	Strategy struct {
		Matrix struct {
			Include []goWorkflowShard `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

// goWorkflowShard is one job of the go job's matrix: the packages it tests,
// or for the rest job the packages it leaves to the others, and the pattern
// naming the tests it runs or skips when it shares a package.
type goWorkflowShard struct {
	Shard    string `yaml:"shard"`
	Packages string `yaml:"packages"`
	Except   string `yaml:"except"`
	Run      string `yaml:"run"`
	Skip     string `yaml:"skip"`
}

// The go workflow tests the slow packages in jobs of their own and every
// other package in the rest job, which runs whatever its except does not
// name. A package except names that no job of its own runs would be tested
// nowhere, and so would the tests of a package whose job runs only some of
// them, and the workflow would still pass. So except names exactly the
// packages the other jobs run, each a package with tests, and a job runs all
// of its package's tests unless one other job runs the rest: one runs the
// tests a pattern matches, and the other skips exactly those.
func TestGoWorkflowRunsEveryPackageOnce(t *testing.T) {
	// Arrange
	shards := goWorkflowJobs(t)["go"].Strategy.Matrix.Include
	if len(shards) == 0 {
		t.Fatal("the go job has no matrix of packages")
	}

	// Act
	jobs := map[string][]goWorkflowShard{}
	var except []string
	rest := 0
	for _, shard := range shards {
		if (shard.Packages == "") == (shard.Except == "") {
			t.Errorf("job %q names packages %q and except %q, want exactly one of them", shard.Shard, shard.Packages, shard.Except)
		}
		if shard.Except != "" {
			rest++
			except = strings.Fields(shard.Except)
			if shard.Run != "" || shard.Skip != "" {
				t.Errorf("the rest job %q runs %q and skips %q, want it to run every test of its packages", shard.Shard, shard.Run, shard.Skip)
			}
		}
		for _, dir := range strings.Fields(shard.Packages) {
			jobs[dir] = append(jobs[dir], shard)
		}
	}

	// Assert
	if rest != 1 {
		t.Fatalf("%d jobs run the packages no other job names, want one", rest)
	}
	own := slices.Sorted(maps.Keys(jobs))
	slices.Sort(except)
	if !slices.Equal(own, except) {
		t.Errorf("the rest job leaves out %v, and the other jobs run %v: want the same packages", except, own)
	}
	for _, dir := range own {
		if tests, err := filepath.Glob(filepath.Join(filepath.FromSlash(dir), "*_test.go")); err != nil || len(tests) == 0 {
			t.Errorf("%s has a job of its own and no tests (%v)", dir, err)
		}
		switch in := jobs[dir]; len(in) {
		case 1:
			if in[0].Run != "" || in[0].Skip != "" {
				t.Errorf("job %q runs %q and skips %q of %s, and no other job runs its other tests", in[0].Shard, in[0].Run, in[0].Skip, dir)
			}
		case 2:
			runs, skips := in[0], in[1]
			if runs.Run == "" {
				runs, skips = skips, runs
			}
			if runs.Run == "" || runs.Skip != "" || skips.Run != "" || skips.Skip != runs.Run {
				t.Errorf("jobs %q (run %q, skip %q) and %q (run %q, skip %q) share %s: want one to run a pattern and the other to skip the same one", runs.Shard, runs.Run, runs.Skip, skips.Shard, skips.Run, skips.Skip, dir)
			}
		default:
			t.Errorf("%d jobs test %s, want one, or two that split its tests by one pattern", len(in), dir)
		}
	}
}

// A newer commit on a pull request supersedes the run validating the older
// one, so that run is cancelled. A run on main or started by hand is never
// cancelled or held: in a shared group it would wait behind another, and a
// waiting run is cancelled when a newer one joins, leaving its commit
// unvalidated. A release is never cancelled halfway. Every workflow says
// which it is, so a new one cannot join a group by accident.
func TestWorkflowsCancelOnlyASupersededPullRequestRun(t *testing.T) {
	// Arrange
	const superseded = "${{ github.workflow }}-${{ github.event_name == 'pull_request' && format('pr-{0}', github.event.pull_request.number) || github.run_id }}"
	groups := map[string]string{"go.yml": superseded, "install.yml": superseded, "release.yml": ""}
	files, err := filepath.Glob(filepath.Join(".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range files {
		names = append(names, filepath.Base(file))
	}
	if want := slices.Sorted(maps.Keys(groups)); !slices.Equal(names, want) {
		t.Fatalf("workflows %v, want %v: say whether a new workflow's pull request runs are cancelled when superseded", names, want)
	}

	for name, group := range groups {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(".github", "workflows", name))
			if err != nil {
				t.Fatal(err)
			}

			// Act
			var workflow struct {
				Concurrency struct {
					Group            string `yaml:"group"`
					CancelInProgress bool   `yaml:"cancel-in-progress"`
				} `yaml:"concurrency"`
			}
			if err := yaml.Unmarshal(source, &workflow); err != nil {
				t.Fatal(err)
			}

			// Assert
			if workflow.Concurrency.Group != group || workflow.Concurrency.CancelInProgress != (group != "") {
				t.Errorf("concurrency group %q, cancel-in-progress %t; want group %q, cancelling only when it is set", workflow.Concurrency.Group, workflow.Concurrency.CancelInProgress, group)
			}
		})
	}
}

// Branch protection requires the one check named test, so test must wait
// for every other job and must run whatever became of them: a job test does
// not need could fail unseen, and a test that is skipped counts as a pass.
func TestGoWorkflowsRequiredCheckWaitsForEveryJob(t *testing.T) {
	// Arrange
	jobs := goWorkflowJobs(t)
	var others []string
	for name := range jobs {
		if name != "test" {
			others = append(others, name)
		}
	}
	slices.Sort(others)

	// Act
	check := jobs["test"]
	needs := slices.Sorted(slices.Values(check.Needs))

	// Assert
	if len(others) == 0 || !slices.Equal(needs, others) {
		t.Errorf("test needs %v, want every other job: %v", needs, others)
	}
	if check.If != "always()" {
		t.Errorf("test runs if %q, want always(), so a failed or cancelled job cannot leave it skipped", check.If)
	}
}

// The required check passes only when every job it needs succeeded: one
// that failed, was cancelled or was skipped fails it, and so does a check
// that needs nothing.
func TestGoWorkflowsRequiredCheckPassesOnlyWhenEveryJobSucceeded(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("the step runs in PowerShell 7, which is not installed")
	}
	step := workflowStep(t, filepath.Join(".github", "workflows", "go.yml"), "test", "Require every job to have passed")
	for name, test := range map[string]struct {
		// results is the needs context as GitHub hands it to the step.
		results string
		wantOK  bool
	}{
		"every job succeeded": {`{"frontend":{"result":"success","outputs":{}},"go":{"result":"success","outputs":{}}}`, true},
		"a job failed":        {`{"frontend":{"result":"success","outputs":{}},"go":{"result":"failure","outputs":{}}}`, false},
		"a job was cancelled": {`{"frontend":{"result":"cancelled","outputs":{}},"go":{"result":"success","outputs":{}}}`, false},
		"a job was skipped":   {`{"frontend":{"result":"success","outputs":{}},"go":{"result":"skipped","outputs":{}}}`, false},
		"no job at all":       {`{}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: as GitHub Actions runs a pwsh step, which stops on
			// an error.
			script := filepath.Join(t.TempDir(), "step.ps1")
			if err := os.WriteFile(script, []byte("$ErrorActionPreference = 'stop'\r\n"+step), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", script)
			cmd.Env = append(os.Environ(), "RESULTS="+test.results)

			// Act
			out, err := cmd.CombinedOutput()

			// Assert
			if test.wantOK && err != nil {
				t.Errorf("the check failed with every job passed: %v\n%s", err, out)
			}
			if !test.wantOK && err == nil {
				t.Errorf("the check passed:\n%s", out)
			}
		})
	}
}

// The go workflow installs SQLite with Chocolatey, whose feed once answered
// 503 and failed the run: the install is tried again after a wait, and gives
// up after its third attempt.
func TestGoWorkflowRetriesTheSQLiteInstall(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("the step runs in PowerShell 7, which is not installed")
	}
	step := workflowStep(t, filepath.Join(".github", "workflows", "go.yml"), "go", "Ensure SQLite integration tests can run")
	for name, test := range map[string]struct {
		// failures is how many installs fail before one succeeds; -1 is
		// every one.
		failures     int
		wantAttempts int
		wantOK       bool
	}{
		"two failures, then the install": {2, 3, true},
		"a feed that stays down":         {-1, 3, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: choco says each attempt, fails as many times as
			// asked, and then installs a sqlite3 beside itself.
			choco := "@echo choco %*\r\n"
			if test.failures < 0 {
				choco += "@exit /b 1\r\n"
			}
			for failure := 1; failure <= test.failures; failure++ {
				choco += fmt.Sprintf("@if not exist \"%%~dp0failure-%d\" (type nul>\"%%~dp0failure-%d\" & exit /b 1)\r\n", failure, failure)
			}
			choco += "@(echo @echo sqlite 3.46.0)>\"%~dp0sqlite3.cmd\"\r\n"
			// As GitHub Actions runs a pwsh step: stop on an error, and end
			// with the last native command's exit code. A wait is said, not
			// taken.
			script := filepath.Join(t.TempDir(), "step.ps1")
			body := "$ErrorActionPreference = 'stop'\r\n" +
				"function Start-Sleep { param([int]$Seconds) Write-Output \"wait $Seconds\" }\r\n" +
				step + "\r\nif ((Test-Path -LiteralPath variable:\\LASTEXITCODE)) { exit $LASTEXITCODE }\r\n"
			if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd, _, _ := installtest.StrippedCommand(t, "", map[string]string{"choco": choco}, pwsh, "-NoProfile", "-NonInteractive", "-Command", ". '"+script+"'")

			// Act
			out, err := cmd.CombinedOutput()

			// Assert
			output := string(out)
			attempts, waits := strings.Count(output, "choco install sqlite --yes --no-progress"), strings.Count(output, "wait ")
			if attempts != test.wantAttempts {
				t.Errorf("choco install ran %d times, want %d:\n%s", attempts, test.wantAttempts, output)
			}
			if waits != attempts-1 {
				t.Errorf("the step waited %d times between %d attempts, want once between each two:\n%s", waits, attempts, output)
			}
			if test.wantOK && (err != nil || !strings.Contains(output, "sqlite 3.46.0")) {
				t.Errorf("step = %v, want SQLite installed and its version printed:\n%s", err, output)
			}
			if !test.wantOK && err == nil {
				t.Errorf("the step succeeded with no SQLite installed:\n%s", output)
			}
		})
	}
}
