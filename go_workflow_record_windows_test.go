package codegoblins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// recordStep is one step of the go workflow, as far as the tests of the
// record of tests that failed once read it.
type recordStep struct {
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]string `yaml:"with"`
}

// recordSteps is every step of job in the go workflow.
func recordSteps(t *testing.T, job string) []recordStep {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(".github", "workflows", "go.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []recordStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow.Jobs[job].Steps
}

// A test that fails and passes on its second try leaves its job green, and
// is named only because its job wrote it to a record, kept that record
// whatever became of the job, and the required check fetched every record. A
// job that wrote to one file and kept another, or a check that fetched under
// another name, would name nothing and say that no test failed once. So each
// job that runs tests writes the record it keeps, under a name the check
// fetches, and the check names what it fetched.
func TestGoWorkflowKeepsAndFetchesTheRecordOfEachTestThatFailedOnce(t *testing.T) {
	// Arrange
	const record = "${{ runner.temp }}/failed-once.jsonl"
	writes := map[string]func(recordStep) bool{
		"go": func(step recordStep) bool {
			return strings.Contains(step.Run, "& $citest ") && strings.Contains(step.Run, "-record (Join-Path $env:RUNNER_TEMP failed-once.jsonl)")
		},
		"browser": func(step recordStep) bool {
			return strings.Contains(step.Run, "npm run test:browser") && step.Env["FAILED_ONCE_RECORD"] == record && step.Env["FAILED_ONCE_JOB"] != ""
		},
	}

	for job, writesRecord := range writes {
		t.Run(job, func(t *testing.T) {
			// Act
			wrote, kept := -1, -1
			for index, step := range recordSteps(t, job) {
				if writesRecord(step) {
					wrote = index
				}
				if strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.With["path"] == record {
					kept = index
					if want := "failed-once-" + job + "-${{ matrix.shard }}"; step.If != "always()" || step.With["name"] != want || step.With["if-no-files-found"] != "ignore" {
						t.Errorf("the step that keeps the record runs if %q, as %q, with if-no-files-found %q: want always(), %q and ignore", step.If, step.With["name"], step.With["if-no-files-found"], want)
					}
				}
				for _, line := range strings.Split(step.Run, "\n") {
					if job == "go" && strings.HasPrefix(strings.TrimSpace(line), "go test ") {
						t.Errorf("job go runs go test itself, where a test that fails gets no second try and no record: %s", line)
					}
				}
			}

			// Assert
			if wrote < 0 || kept < wrote {
				t.Errorf("job %s writes its record in step %d and keeps %s in step %d, want it written and then kept", job, wrote, record, kept)
			}
		})
	}

	// Act
	fetched, named, folder := -1, -1, ""
	for index, step := range recordSteps(t, "test") {
		switch {
		case strings.HasPrefix(step.Uses, "actions/download-artifact@") && step.With["pattern"] == "failed-once-*" && step.If == "always()":
			fetched, folder = index, step.With["path"]
		case fetched >= 0 && step.If == "always()" && strings.Contains(step.Run, "Get-ChildItem -Path "+folder+" "):
			named = index
		}
	}

	// Assert
	if fetched < 0 || named < 0 || folder == "" {
		t.Errorf("test fetches the records in step %d into %q and names them in step %d, want both whatever became of the jobs, the fetch first", fetched, folder, named)
	}
}

// The required check names each test that failed once as a warning of its
// own, which GitHub shows on the run and gh run view prints, and in the
// run's summary. GitHub shows ten warnings of a step, so past ten the last
// says how many more the log names.
func TestGoWorkflowsRequiredCheckNamesEachTestThatFailedOnce(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("the step runs in PowerShell 7, which is not installed")
	}
	step := workflowStep(t, filepath.Join(".github", "workflows", "go.yml"), "test", "Name each test that failed once")
	twelve := map[string][]string{}
	for number := 1; number <= 12; number++ {
		twelve[fmt.Sprintf("failed-once-go-shard-%02d", number)] = []string{fmt.Sprintf(`{"job":"go (shard-%02d)","suite":"example.test/internal/train","test":"TestTrainLands"}`, number)}
	}
	for name, test := range map[string]struct {
		// records is each fetched record's lines, by the artifact it came in.
		records      map[string][]string
		wantWarnings []string
		wantSummary  []string
	}{
		"no test failed once": {nil, nil, nil},
		"a go test and a browser test": {
			map[string][]string{
				"failed-once-go-rest":   {`{"job":"go (rest)","suite":"example.test/internal/train","test":"TestTrainLands"}`},
				"failed-once-browser-2": {`{"job":"browser (2/4)","suite":"afk.spec.ts","test":"a 100% answer > stays"}`, ``},
			},
			[]string{
				"::warning title=Failed once::browser (2/4): afk.spec.ts a 100%25 answer > stays failed once and passed on the second try",
				"::warning title=Failed once::go (rest): example.test/internal/train TestTrainLands failed once and passed on the second try",
			},
			[]string{"- browser (2/4): afk.spec.ts a 100% answer > stays failed once and passed on the second try", "- go (rest): example.test/internal/train TestTrainLands failed once and passed on the second try"},
		},
		"more than GitHub shows": {
			twelve,
			[]string{
				"::warning title=Failed once::go (shard-01): example.test/internal/train TestTrainLands failed once and passed on the second try",
				"::warning title=Failed once::go (shard-09): example.test/internal/train TestTrainLands failed once and passed on the second try",
				"::warning title=Failed once::and 3 more, named in the log of this step",
			},
			[]string{"- go (shard-12): example.test/internal/train TestTrainLands failed once and passed on the second try"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: the records as the fetch leaves them, each artifact
			// in a folder of its own, and the step as GitHub Actions runs a
			// pwsh step, which stops on an error.
			dir := t.TempDir()
			for artifact, lines := range test.records {
				folder := filepath.Join(dir, "failed-once", artifact)
				if err := os.MkdirAll(folder, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, "failed-once.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			script, summary := filepath.Join(dir, "step.ps1"), filepath.Join(dir, "summary.md")
			if err := os.WriteFile(script, []byte("$ErrorActionPreference = 'stop'\r\n"+step), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GITHUB_STEP_SUMMARY="+summary)

			// Act
			out, err := cmd.CombinedOutput()

			// Assert
			if err != nil {
				t.Fatalf("the step failed: %v\n%s", err, out)
			}
			output := strings.ReplaceAll(string(out), "\r\n", "\n")
			if warnings, want := strings.Count(output, "::warning "), min(len(test.records), 10); warnings != want {
				t.Errorf("the step wrote %d warnings, want %d:\n%s", warnings, want, output)
			}
			for _, warning := range test.wantWarnings {
				if !strings.Contains(output, warning+"\n") {
					t.Errorf("the step did not write %q:\n%s", warning, output)
				}
			}
			written, err := os.ReadFile(summary)
			if len(test.records) == 0 {
				if !strings.Contains(output, "No test failed once in this run.") || err == nil {
					t.Errorf("with no record the step wrote a summary (%v) or did not say that no test failed once:\n%s", err, output)
				}
				return
			}
			for _, line := range test.wantSummary {
				if !strings.Contains(string(written), line) {
					t.Errorf("the summary does not hold %q (%v):\n%s", line, err, written)
				}
			}
		})
	}
}
