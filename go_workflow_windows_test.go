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
// packages the other jobs run, each a package with tests, and the jobs that
// share a package run each of its tests once: in the workflow's order, every
// job but the last runs the tests its pattern matches and skips those the
// jobs before it run, and the last skips every pattern, which leaves it the
// tests none matched. A test several patterns match runs in the first of
// their jobs alone.
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
		var earlier []string
		for index, job := range jobs[dir] {
			last := index == len(jobs[dir])-1
			if (job.Run == "") != last {
				t.Errorf("job %q, number %d of %d that test %s, runs %q: want every job but the last to name the tests it runs, and the last to name none", job.Shard, index+1, len(jobs[dir]), dir, job.Run)
			}
			if want := strings.Join(earlier, "|"); job.Skip != want {
				t.Errorf("job %q skips %q of %s, want %q, exactly what the jobs before it run", job.Shard, job.Skip, dir, want)
			}
			earlier = append(earlier, job.Run)
		}
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

// workflowSource reads and parses the workflow named name into into.
func workflowSource(t *testing.T, name string, into any) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(source, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// A push to a pull request makes its earlier run obsolete, so the workflows
// that check a pull request cancel that run and its runners go to the new
// one, and one pull request's run never cancels another's. Every other run
// is in a group of its own, the run's: a run for main shares none, since a
// run that waits in a group is cancelled by the next one to arrive, and
// every commit on main keeps its own verdict. The release workflow
// publishes, and nothing cancels it halfway.
func TestWorkflowsCancelOnlyAPullRequestsSupersededRun(t *testing.T) {
	type workflow struct {
		Concurrency struct {
			Group  string `yaml:"group"`
			Cancel any    `yaml:"cancel-in-progress"`
		} `yaml:"concurrency"`
	}
	for _, name := range []string{"go.yml", "install.yml"} {
		// Arrange
		var checks workflow

		// Act
		workflowSource(t, name, &checks)

		// Assert
		if want := "${{ github.workflow }}-${{ github.event.pull_request.number || github.run_id }}"; checks.Concurrency.Group != want {
			t.Errorf("%s groups its runs by %q, want %q: one group for each pull request, and for any other run a group of its own", name, checks.Concurrency.Group, want)
		}
		if want := "${{ github.event_name == 'pull_request' }}"; fmt.Sprint(checks.Concurrency.Cancel) != want {
			t.Errorf("%s cancels a run in progress when %v, want %q: a pull request's superseded run, never a run for main", name, checks.Concurrency.Cancel, want)
		}
	}
	var release workflow
	workflowSource(t, "release.yml", &release)
	if release.Concurrency.Cancel != nil && fmt.Sprint(release.Concurrency.Cancel) != "false" {
		t.Errorf("release.yml cancels a run in progress when %v, want never: a release is published once and whole", release.Concurrency.Cancel)
	}
}

// The browser suite runs as parallel jobs, each one shard of it. The shards
// are numbered from 1 to their count and each job runs its own shard of that
// count, so every browser test runs in exactly one job, and no other job runs
// the suite again.
func TestGoWorkflowRunsEveryBrowserTestOnce(t *testing.T) {
	// Arrange
	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Shard []int `yaml:"shard"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}

	// Act
	workflowSource(t, "go.yml", &workflow)

	// Assert
	shards := workflow.Jobs["browser"].Strategy.Matrix.Shard
	if len(shards) < 2 {
		t.Fatalf("the browser job runs in %d shard(s), want two or more jobs sharing the suite", len(shards))
	}
	for index, shard := range shards {
		if shard != index+1 {
			t.Errorf("the browser job's shards are %v, want 1 to %d in order", shards, len(shards))
			break
		}
	}
	// Playwright is called directly, with npx, so the shard does not depend on
	// how a shell and npm run hand a flag on to a script: a flag lost on the
	// way would have every job run the whole suite.
	want := fmt.Sprintf("npx playwright test --shard=${{ matrix.shard }}/%d\n", len(shards))
	for name, job := range workflow.Jobs {
		runs := 0
		for _, step := range job.Steps {
			runs += strings.Count(step.Run, "playwright test") + strings.Count(step.Run, "test:browser")
			if name == "browser" && strings.Contains(step.Run, "playwright test") && !strings.Contains(step.Run, want) {
				t.Errorf("the browser job runs %q, want it to run %q", step.Run, want)
			}
		}
		if (name == "browser") != (runs == 1) {
			t.Errorf("job %s runs the browser suite %d time(s), want the browser job alone to run it, once", name, runs)
		}
	}
}
