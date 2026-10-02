package codegoblins

import (
	"fmt"
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
			Include []struct {
				Shard    string `yaml:"shard"`
				Packages string `yaml:"packages"`
				Except   string `yaml:"except"`
			} `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

// The go workflow tests the slow packages in jobs of their own and every
// other package in the rest job, which runs whatever its except does not
// name. A package except names that no job of its own runs would be tested
// nowhere, and the workflow would still pass: except names exactly the
// packages the other jobs run, each a package with tests, and each once.
func TestGoWorkflowRunsEveryPackageOnce(t *testing.T) {
	// Arrange
	shards := goWorkflowJobs(t)["go"].Strategy.Matrix.Include
	if len(shards) == 0 {
		t.Fatal("the go job has no matrix of packages")
	}

	// Act
	var own, except []string
	rest := 0
	for _, shard := range shards {
		if shard.Except != "" {
			rest++
			except = strings.Fields(shard.Except)
		}
		if (shard.Packages == "") == (shard.Except == "") {
			t.Errorf("job %q names packages %q and except %q, want exactly one of them", shard.Shard, shard.Packages, shard.Except)
		}
		own = append(own, strings.Fields(shard.Packages)...)
	}

	// Assert
	if rest != 1 {
		t.Fatalf("%d jobs run the packages no other job names, want one", rest)
	}
	slices.Sort(own)
	slices.Sort(except)
	if !slices.Equal(own, except) {
		t.Errorf("the rest job leaves out %v, and the other jobs run %v: want the same packages, each once", except, own)
	}
	for _, dir := range own {
		if tests, err := filepath.Glob(filepath.Join(filepath.FromSlash(dir), "*_test.go")); err != nil || len(tests) == 0 {
			t.Errorf("%s has a job of its own and no tests (%v)", dir, err)
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
