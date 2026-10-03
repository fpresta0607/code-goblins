//go:build windows

package codegoblins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHostedGoEvidenceBindsTheActualCheckoutAndRejectsIncompleteResults(t *testing.T) {
	producer, err := filepath.Abs(filepath.Join(".github", "go-evidence.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(producer)
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	git := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s (%v)", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	write("go.mod", "module example.com/evidence\n\ngo 1.22\n")
	write("go.sum", "fixture sum\n")
	write("config/verify.json", `{"version":1}`)
	write(".no-mistakes.yaml", "commands:\n  test: cfo gate test\n")
	write(".github/workflows/go.yml", "name: fixture\n")
	write(".github/go-evidence.ps1", string(script))
	write("a/a.go", "package a\n")
	git("init", "-q", "--initial-branch=main")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "Fixture Tester")
	git("add", ".")
	git("commit", "-qm", "test(fixture): establish evidence inputs")
	git("switch", "-qc", "feature")
	write("a/a.go", "package a\nfunc Value() int { return 2 }\n")
	git("add", ".")
	git("commit", "-qm", "test(fixture): change package input")
	head := git("rev-parse", "HEAD")
	git("switch", "main")
	write("upstream.txt", "a concurrent main change\n")
	git("add", ".")
	git("commit", "-qm", "test(fixture): advance the tested main")
	main := git("rev-parse", "HEAD")
	git("merge", "--no-ff", "feature", "-m", "test(fixture): check the combined result")
	checkout, tree := git("rev-parse", "HEAD"), git("rev-parse", "HEAD^{tree}")
	inputs := map[string]string{}
	for _, name := range []string{"go.mod", "go.sum", "config/verify.json", ".no-mistakes.yaml", ".github/workflows/go.yml", ".github/go-evidence.ps1"} {
		inputs[name] = git("rev-parse", "HEAD:"+name)
	}
	baseEnvironment := map[string]string{
		"GOVERSION": "go1.26.5", "GOOS": "windows", "GOARCH": "amd64", "CGO_ENABLED": "0",
		"GOAMD64": "v1", "GOARM64": "", "GOEXPERIMENT": "", "GOFLAGS": "fixture-sensitive-flag", "GOWORK": "C:/private/workspace.work",
	}
	var fingerprint strings.Builder
	for _, name := range slices.Sorted(maps.Keys(baseEnvironment)) {
		fingerprint.WriteString(name + "\x00" + baseEnvironment[name] + "\x00")
	}
	digest := sha256.Sum256([]byte(fingerprint.String()))
	wantEnvironment := hex.EncodeToString(digest[:])
	environment, err := json.Marshal(baseEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	toolchain := filepath.Join(t.TempDir(), "toolchain.json")
	if err := os.WriteFile(toolchain, environment, 0o600); err != nil {
		t.Fatal(err)
	}
	allPackages := filepath.Join(t.TempDir(), "packages.json")
	if err := os.WriteFile(allPackages, []byte(`["example.com/evidence/a","example.com/evidence/b"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_REPOSITORY", "fixture/evidence")
	t.Setenv("GITHUB_RUN_ID", "123")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")
	for _, test := range []struct {
		name, events, suppliedHead string
		isValid                    bool
	}{
		{"passed", `{"Action":"start","Package":"example.com/evidence/a"}
{"Action":"pass","Package":"example.com/evidence/a","Elapsed":0.125}
`, head, true},
		{"no tests", `{"Action":"start","Package":"example.com/evidence/a"}
{"Action":"skip","Package":"example.com/evidence/a"}
`, head, true},
		{"test skip recorded", `{"Action":"start","Package":"example.com/evidence/a"}
{"Action":"skip","Package":"example.com/evidence/a","Test":"TestOptional"}
{"Action":"pass","Package":"example.com/evidence/a","Elapsed":0.125}
`, head, true},
		{"missing package", "", head, false},
		{"unfinished", "{\"Action\":\"start\",\"Package\":\"example.com/evidence/a\"}\n", head, false},
		{"failed", "{\"Action\":\"fail\",\"Package\":\"example.com/evidence/a\"}\n", head, false},
		{"duplicate result", "{\"Action\":\"pass\",\"Package\":\"example.com/evidence/a\"}\n{\"Action\":\"pass\",\"Package\":\"example.com/evidence/a\"}\n", head, false},
		{"unexpected package", "{\"Action\":\"pass\",\"Package\":\"example.com/evidence/b\"}\n", head, false},
		{"wrong head", "{\"Action\":\"pass\",\"Package\":\"example.com/evidence/a\"}\n", main, false},
		{"malformed events", "not JSON\n", head, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			events, output := filepath.Join(dir, "events.jsonl"), filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(events, []byte(test.events), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", producer,
				"-Events", events, "-Output", output, "-Toolchain", toolchain, "-AllPackages", allPackages,
				"-Packages", "example.com/evidence/a", "-Shard", "rest", "-Head", test.suppliedHead)
			command.Dir = project
			text, err := command.CombinedOutput()
			if (err == nil) != test.isValid {
				t.Fatalf("producer: %s (%v); valid=%v", text, err, test.isValid)
			}
			if !test.isValid {
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatalf("invalid proof left a manifest: %v", err)
				}
				return
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var proof struct {
				Version     int               `json:"version"`
				RunID       int64             `json:"run_id"`
				Attempt     int               `json:"attempt"`
				Repository  string            `json:"repository"`
				Head        string            `json:"head"`
				Main        string            `json:"main"`
				Checkout    string            `json:"checkout"`
				Tree        string            `json:"tree"`
				Shard       string            `json:"shard"`
				RunTests    string            `json:"run_tests"`
				SkipTests   string            `json:"skip_tests"`
				Environment string            `json:"environment"`
				Inputs      map[string]string `json:"inputs"`
				AllPackages []string          `json:"all_packages"`
				Vet         bool              `json:"vet"`
				Results     []struct {
					Package, Status string
					Skipped         []string
				} `json:"results"`
			}
			if err := json.Unmarshal(data, &proof); err != nil {
				t.Fatal(err)
			}
			if proof.Version != 1 || proof.RunID != 123 || proof.Attempt != 2 || proof.Repository != "fixture/evidence" || proof.Head != head || proof.Main != main || proof.Checkout != checkout || proof.Tree != tree || proof.Shard != "rest" || !proof.Vet {
				t.Fatalf("proof is not bound to its actual checkout and job: %+v", proof)
			}
			for name, hash := range inputs {
				if proof.Inputs[name] != hash {
					t.Errorf("input %s=%q, want %q", name, proof.Inputs[name], hash)
				}
			}
			if proof.Environment != wantEnvironment || len(proof.Results) != 1 || proof.Results[0].Package != "example.com/evidence/a" || !slices.Equal(proof.AllPackages, []string{"example.com/evidence/a", "example.com/evidence/b"}) {
				t.Fatalf("incomplete environment or package proof: %+v", proof)
			}
			if strings.Contains(string(data), "fixture-sensitive-flag") || strings.Contains(string(data), "C:/private/workspace.work") {
				t.Fatal("raw build settings reached the reusable proof")
			}
			if test.name == "test skip recorded" && !slices.Equal(proof.Results[0].Skipped, []string{"TestOptional"}) {
				t.Fatalf("test skip was lost: %+v", proof.Results)
			}
			wantStatus := "passed"
			if test.name == "no tests" {
				wantStatus = "no_tests"
			}
			if proof.Results[0].Status != wantStatus {
				t.Fatalf("status=%q, want %q", proof.Results[0].Status, wantStatus)
			}
		})
	}
}

func TestGoWorkflowKeepsTheTestExitAndClearsTheInheritedEnvironment(t *testing.T) {
	step := workflowStep(t, filepath.Join(".github", "workflows", "go.yml"), "go", "Run Go tests with isolated CFO state")
	for _, test := range []struct {
		name, exit, run, skip, packages, except string
	}{
		{"run shard", "0", "Fixture", "", "example.com/evidence/a", ""},
		{"skip shard", "0", "", "Fixture", "example.com/evidence/a", ""},
		{"rest shard", "0", "", "", "", "example.com/evidence/b"},
		{"failed shard", "7", "", "", "example.com/evidence/a", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("RUNNER_TEMP", dir)
			t.Setenv("GITHUB_EVENT_NAME", "push")
			t.Setenv("PACKAGES", test.packages)
			t.Setenv("EXCEPT", test.except)
			t.Setenv("RUN", test.run)
			t.Setenv("SKIP", test.skip)
			t.Setenv("FIXTURE_GO_EXIT", test.exit)
			for _, name := range []string{"CODEX_THREAD_ID", "CFO_HOME", "CFO_STATE_OVERRIDE", "NO_MISTAKES_GATE"} {
				t.Setenv(name, "fixture-inherited-value")
			}
			prefix := `$ErrorActionPreference = 'Stop'
function go {
    $global:LASTEXITCODE = 0
    [string[]]$arguments = $args | ForEach-Object { $_ }
    switch ($arguments[0]) {
        'env' { '{}' }
        'list' {
            if ($arguments[1] -eq './...') { 'example.com/evidence/a'; 'example.com/evidence/b' }
            else { $arguments[1..($arguments.Count - 1)] }
        }
        'test' {
            foreach ($name in 'CODEX_THREAD_ID', 'CFO_HOME', 'CFO_STATE_OVERRIDE', 'NO_MISTAKES_GATE') {
                if ([Environment]::GetEnvironmentVariable($name)) { throw 'An inherited variable reached go test.' }
            }
            $expected = @('test', 'example.com/evidence/a')
            if ($env:RUN) { $expected += '-run', $env:RUN }
            if ($env:SKIP) { $expected += '-skip', $env:SKIP }
            $expected += '-json', '-count=1', '-timeout', '20m'
            if (($arguments -join '|') -cne ($expected -join '|')) { throw "Wrong test scope or flags: $arguments" }
            $global:LASTEXITCODE = [int]$env:FIXTURE_GO_EXIT
            '{"Action":"pass","Package":"example.com/evidence/a"}'
        }
    }
}
`
			script := filepath.Join(dir, "step.ps1")
			if err := os.WriteFile(script, []byte(prefix+step), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
			output, err := command.CombinedOutput()
			if test.exit == "0" && err != nil {
				t.Fatalf("successful Go step failed: %s (%v)", output, err)
			}
			if test.exit != "0" {
				exitError, ok := err.(*exec.ExitError)
				if !ok || exitError.ExitCode() != 7 || !strings.Contains(string(output), `"Package":"example.com/evidence/a"`) {
					t.Fatalf("Go failure was lost: %s (%v)", output, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "cfo-go-evidence", "manifest.json")); !os.IsNotExist(err) {
				t.Fatalf("a main-push step produced PR evidence: %v", err)
			}
		})
	}
}

func TestGoWorkflowKeepsEvidenceBoundToSuccessfulPullRequestShards(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(".github", "workflows", "go.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name, If, Uses string
				With, Env      map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	var uploadStep, depth string
	for _, step := range workflow.Jobs["go"].Steps {
		if step.Uses == "actions/checkout@v6" {
			depth = step.With["fetch-depth"]
		}
		if step.Name == "Run Go tests with isolated CFO state" {
			if step.Env["SHARD"] != "${{ matrix.shard }}" || step.Env["HEAD_SHA"] != "${{ github.event.pull_request.head.sha }}" || step.Env["RUN"] != "${{ matrix.run }}" || step.Env["SKIP"] != "${{ matrix.skip }}" {
				t.Errorf("evidence identity and filters are not bound to the shard and PR head: %+v", step.Env)
			}
		}
		if step.Name == "Keep reusable Go evidence" {
			if step.Uses != "actions/upload-artifact@v4" || step.If != "success() && github.event_name == 'pull_request'" {
				t.Errorf("evidence upload is not limited to successful PR tests: %+v", step)
			}
			if step.With["name"] != "cfo-go-evidence-${{ github.run_attempt }}-${{ matrix.shard }}" || step.With["if-no-files-found"] != "error" {
				t.Errorf("artifact is not bound to its attempt and shard: %+v", step.With)
			}
			uploadStep = step.With["path"]
		}
	}
	if depth != "2" || uploadStep != "${{ runner.temp }}/cfo-go-evidence/manifest.json" {
		t.Errorf("checkout depth=%q, uploaded path=%q; want actual parents and a manifest without raw environment values", depth, uploadStep)
	}
}
