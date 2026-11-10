package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	handbackRun    = "01M70HANDBACKRUNOFTHISTEST"
	handbackRepo   = "c0ffee000001"
	handbackBranch = "feat/handback"
	handbackHead   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	handbackShip   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// versionSeven is version 6 whose gate repairs no Test, Lint or CI failure.
func versionSeven(t *testing.T) Policy {
	t.Helper()
	policy := versionSix(t, Reviewer{})
	policy.Version = 7
	policy.AutoFix = AutoFix{Rebase: 1}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestOnlyVersionSevenHandsBackTestLintAndCI(t *testing.T) {
	for _, policy := range []Policy{versionSix(t, Reviewer{}), versionSeven(t)} {
		for _, step := range []string{"review", "test", "lint", "document", "rebase", "push", "ci"} {
			want := policy.Version == 7 && (step == "test" || step == "lint" || step == "ci")
			if got := policy.HandsBack(step); got != want {
				t.Errorf("version %d hands back %s = %v, want %v", policy.Version, step, got, want)
			}
		}
	}
}

func TestVersionSevenTellsTheGoblinHowToSubmitItsCorrection(t *testing.T) {
	for _, policy := range []Policy{versionSix(t, Reviewer{}), versionSeven(t)} {
		selection, err := policy.Select("ordinary")
		if err != nil {
			t.Fatal(err)
		}
		instruction := selection.Instruction("task", "snapshot")
		isNamed := strings.Contains(instruction, "ceo pipeline validate-authored task --run <run> --findings <ids>") && strings.Contains(instruction, "ceo pipeline handback task --run <run>")
		if isNamed != (policy.Version == 7) {
			t.Errorf("version %d instruction names the owner's correction = %v: %s", policy.Version, isNamed, instruction)
		}
	}
}

// handbackDatabase is a gate database whose branch holds an older run parked
// at a Test failure and, newest, a run whose step is at status.
func handbackDatabase(t *testing.T, runStatus, step, stepStatus, findings string) Reader {
	t.Helper()
	root := t.TempDir()
	schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,created_at INTEGER);
CREATE TABLE step_results(run_id TEXT,step_name TEXT,step_order INTEGER,status TEXT,findings_json TEXT);
INSERT INTO repos VALUES('%[1]s','C:\project');
INSERT INTO runs VALUES('01M70OLDERRUNOFTHISBRANCH0','%[1]s','%[2]s','running','%[3]s','%[3]s',1);
INSERT INTO step_results VALUES('01M70OLDERRUNOFTHISBRANCH0','test',3,'awaiting_approval','{"findings":[{"id":"older","severity":"error","description":"older"}]}');
INSERT INTO runs VALUES('%[4]s','%[1]s','%[2]s','%[5]s','%[3]s','%[6]s',2);
INSERT INTO step_results VALUES('%[4]s','review',1,'completed',NULL);
INSERT INTO step_results VALUES('%[4]s','%[7]s',3,'%[8]s','%[9]s');`, handbackRepo, handbackBranch, handbackHead, handbackRun, runStatus, handbackShip, step, stepStatus, strings.ReplaceAll(findings, "'", "''"))
	writeFixtureSQLite(t, filepath.Join(root, "state.sqlite"), schema)
	return Reader{Root: root, Commands: fixtureCommands{execx.OSRunner{}}}
}

const handbackFindings = `{"findings":[{"id":"test-failure","severity":"error","description":"go test ./internal/c failed"},{"id":"flaky-note","action":"no-op","severity":"info","description":"a note"}]}`

// noTurnFindings is a Test step whose command native gave no turn, as for a
// missing required environment.
const noTurnFindings = `{"findings":[{"id":"test-command-no-turn","action":"ask-user","severity":"error","description":"The wrapper reported: ceo gate turn: the command did not start: required test environment: .venv is missing. Respond with fix to run it again."},{"id":"flaky-note","action":"no-op","severity":"info","description":"a note"}]}`

// A run is handed back only while it is parked at a Test, Lint or CI failure
// of a version 7 task with an open finding other than a test command given no
// turn, and it is handed back with every open finding.
func TestHandedBackReadsOnlyAFailureTheTasksPolicyHandsBack(t *testing.T) {
	for _, test := range []struct {
		name, runStatus, step, stepStatus, findings string
		isVersionSeven                              bool
		want                                        []string
	}{
		{"a parked version 7 test failure", "running", "test", "awaiting_approval", handbackFindings, true, []string{"test-failure"}},
		{"a version 7 lint failure whose run was stopped", "cancelled", "lint", "failed", handbackFindings, true, nil},
		{"a version 7 ci failure handed to the goblin", "running", "ci", "awaiting_agent", handbackFindings, true, []string{"test-failure"}},
		{"a version 6 test failure", "running", "test", "awaiting_approval", handbackFindings, false, nil},
		{"a version 7 review decision", "running", "review", "awaiting_approval", handbackFindings, true, nil},
		{"a version 7 run that failed outright", "failed", "test", "failed", handbackFindings, true, nil},
		{"a version 7 test failure whose findings are all no-ops", "running", "test", "awaiting_approval", `{"findings":[{"id":"n","action":"no-op"}]}`, true, nil},
		{"a version 7 test step that recorded no findings", "running", "test", "awaiting_approval", "", true, nil},
		{"a version 7 test command given no turn", "running", "test", "awaiting_approval", noTurnFindings, true, nil},
		{"a version 7 test failure beside a command given no turn", "running", "test", "awaiting_approval", `{"findings":[{"id":"test-command-no-turn","action":"ask-user"},{"id":"test-failure","severity":"error","description":"go test ./internal/c failed"}]}`, true, []string{"test-command-no-turn", "test-failure"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reader := handbackDatabase(t, test.runStatus, test.step, test.stepStatus, test.findings)
			policy := versionSix(t, Reviewer{})
			if test.isVersionSeven {
				policy = versionSeven(t)
			}

			// Act
			run, isHandedBack, err := reader.HandedBack(context.Background(), `C:\project`, handbackBranch, policy)

			// Assert
			if err != nil || isHandedBack != (test.want != nil) {
				t.Fatalf("handed back = %v (%v), want %v", isHandedBack, err, test.want != nil)
			}
			if test.want == nil {
				return
			}
			if run.RunID != handbackRun || run.Step != test.step || run.Head != handbackHead || run.SubmittedHead != handbackShip {
				t.Fatalf("run = %+v, want run %s at %s", run, handbackRun, test.step)
			}
			if !slices.Equal(run.FindingIDs(), test.want) {
				t.Fatalf("findings = %v, want %v, every one that is not a no-op", run.FindingIDs(), test.want)
			}
			if want := filepath.Join(reader.Root, "logs", handbackRun, test.step+".log"); run.Log != want {
				t.Fatalf("log = %s, want %s", run.Log, want)
			}
		})
	}
}

func TestHandedBackRefusesUnreadableFindings(t *testing.T) {
	// Arrange
	reader := handbackDatabase(t, "running", "test", "awaiting_approval", "not a document")

	// Act
	_, isHandedBack, err := reader.HandedBack(context.Background(), `C:\project`, handbackBranch, versionSeven(t))

	// Assert
	if err == nil || isHandedBack || !strings.Contains(err.Error(), "findings cannot be read") {
		t.Fatalf("handed back = %v, error = %v, want the findings refused", isHandedBack, err)
	}
}

// commitFiles writes each file and commits them all in dir.
func commitFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		writeFixtureFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	runPipelineGit(t, dir, "add", "--all")
	runPipelineGit(t, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "fixture")
	return runPipelineGit(t, dir, "rev-parse", "HEAD")
}

// correctionWorktree is a branch that changes a.go from origin's default
// branch, at the head a run failed its Test step on: its log names b.go and
// its finding internal/c/c.go, with Windows separators.
func correctionWorktree(t *testing.T) (string, string, Handback) {
	t.Helper()
	worktree := t.TempDir()
	runPipelineGit(t, worktree, "init", "--quiet", "--initial-branch=main")
	trusted := commitFiles(t, worktree, map[string]string{"a.go": "a\n", "b.go": "b\n", "internal/c/c.go": "c\n", "docs/guide.md": "guide\n"})
	runPipelineGit(t, worktree, "checkout", "--quiet", "-b", handbackBranch)
	head := commitFiles(t, worktree, map[string]string{"a.go": "a2\n"})
	log := filepath.Join(t.TempDir(), "test.log")
	writeFixtureFile(t, log, "ok  \tinternal/a\n--- FAIL: TestB (0.00s)\n    b.go:12: want 2, got 3\nFAIL\n")
	run := Handback{RunID: handbackRun, RepoID: handbackRepo, Branch: handbackBranch, Head: head, SubmittedHead: head, Step: "test", Log: log,
		Findings: []HandbackFinding{{ID: "test-failure", Severity: "error", Description: `go vet: internal\c\c.go:3: unreachable code`}}}
	return worktree, trusted, run
}

func TestCheckCorrectionAdmitsOnlyOneCommitInTheFindingsScope(t *testing.T) {
	for _, test := range []struct {
		name    string
		commits []map[string]string
		stray   string
		refusal string
	}{
		{"a fix of a file the branch changes", []map[string]string{{"a.go": "a3\n"}}, "", ""},
		{"a fix of a file the step's log names", []map[string]string{{"b.go": "b2\n"}}, "", ""},
		{"a fix of a file a finding names", []map[string]string{{"internal/c/c.go": "c2\n"}}, "", ""},
		{"a change of a file nothing names", []map[string]string{{"a.go": "a3\n", "docs/guide.md": "guide2\n"}}, "", "the correction changes docs/guide.md, which the branch does not change"},
		{"two commits", []map[string]string{{"a.go": "a3\n"}, {"b.go": "b2\n"}}, "", "a correction is one commit on run"},
		{"no commit", nil, "", "a correction is one commit on run"},
		{"a file left uncommitted", []map[string]string{{"a.go": "a3\n"}}, "notes.txt", "commit the correction, and leave nothing else in the worktree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktree, trusted, run := correctionWorktree(t)
			for _, files := range test.commits {
				commitFiles(t, worktree, files)
			}
			if test.stray != "" {
				writeFixtureFile(t, filepath.Join(worktree, test.stray), "stray\n")
			}

			// Act
			err := Reader{Commands: execx.OSRunner{}}.CheckCorrection(context.Background(), worktree, trusted, run)

			// Assert
			if test.refusal == "" {
				if err != nil {
					t.Fatalf("a correction in scope was refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("error = %v, want one saying %q", err, test.refusal)
			}
		})
	}
}

// A step that left no log is scoped by its findings alone.
func TestCheckCorrectionScopesAStepWithoutALogByItsFindings(t *testing.T) {
	// Arrange
	worktree, trusted, run := correctionWorktree(t)
	run.Log = filepath.Join(t.TempDir(), "missing.log")
	commitFiles(t, worktree, map[string]string{"internal/c/c.go": "c2\n"})

	// Act
	err := Reader{Commands: execx.OSRunner{}}.CheckCorrection(context.Background(), worktree, trusted, run)

	// Assert
	if err != nil {
		t.Fatalf("a correction its finding names was refused: %v", err)
	}
}

// A failure names a file by its path only where the path stands on its own,
// as a compiler, linter or stack trace prints it: from the repository's root,
// `./` or an absolute worktree the failure ran in, and a root-level file only
// from `./` or such a worktree. A base name, as a Go test failure prints it,
// names a file only in a directory the branch itself already changes,
// whatever packages the rest of the log names.
func TestCheckCorrectionHoldsANamedFileToItsPathBoundaries(t *testing.T) {
	const branchChange = "internal/pipeline/policy.go"
	// goTest is a ceo gate test log: its plan, a package that passed, and the
	// failing package's test failure and result.
	const goTest = "- github.com/fpresta0607/code-goblins/internal/gatetest (changed)\n- github.com/fpresta0607/code-goblins/internal/pipeline (changed)\nok  \tgithub.com/fpresta0607/code-goblins/internal/gatetest\t2.1s\n--- FAIL: TestPolicy (0.00s)\n    policy_test.go:42: want 2, got 3\nFAIL\nFAIL\tgithub.com/fpresta0607/code-goblins/internal/pipeline\t0.012s\n"
	for _, test := range []struct {
		name, log, finding, own, file string
		isNamed                       bool
	}{
		{"a base name in a package the branch does not change", goTest, "", branchChange, "internal/gatetest/policy_test.go", false},
		{"a root-level file whose name ends a logged path", "internal/x/data.go:5:2: undefined: y\n", "", branchChange, "a.go", false},
		{"a root-level file whose name ends a finding's path", "", `go vet: internal\c\c.go:3: unreachable code`, branchChange, "c.go", false},
		{"a root-level base name while the branch changes no root-level file", "--- FAIL: TestC (0.00s)\n    c.go:3: want 1\nFAIL\n", "", branchChange, "c.go", false},
		{"a root-level file one directory up", "../main.go:5:2: undefined: x\n", "", branchChange, "main.go", false},
		{"a path under a directory the failure did not run in", "panic: boom\n\t/elsewhere/internal/x/data.go:12 +0x1d\n", "", branchChange, "internal/x/data.go", false},
		{"a path that begins a longer file name", "internal/x/data.go.orig:3: x\n", "", branchChange, "internal/x/data.go", false},
		{"a base name that ends a longer base name", "--- FAIL: TestMetadata (0.00s)\n    metadata.go:5: want 1\nFAIL\n", "", branchChange, "internal/pipeline/data.go", false},
		{"a base name with no line after it", "--- FAIL: TestPolicy (0.00s)\n    policy_test.go is flaky\nFAIL\n", "", branchChange, "internal/pipeline/policy_test.go", false},
		{"a compiler's repository path", "# github.com/fpresta0607/code-goblins/internal/x\ninternal/x/data.go:12:3: undefined: x\n", "", branchChange, "internal/x/data.go", true},
		{"a finding's quoted path", "", "`internal/x/data.go` drops the error", branchChange, "internal/x/data.go", true},
		{"a stack trace's path in the task's worktree", "panic: boom\n\t{worktree}/internal/x/data.go:12 +0x1d\n", "", branchChange, "internal/x/data.go", true},
		{"a stack trace's path in the run's gate worktree", "panic: boom\n\t{gate}/internal/x/data.go:12 +0x1d\n", "", branchChange, "internal/x/data.go", true},
		{"a TypeScript compiler's path", "src/foo.ts(12,3): error TS2322: Type 'string' is not assignable to type 'number'.\n", "", branchChange, "src/foo.ts", true},
		{"a Go compiler's path in its own directory", "./internal/x/data.go:5:2: undefined: y\n", "", branchChange, "internal/x/data.go", true},
		{"a Go compiler's root-level path", "./main.go:5:2: undefined: x\n", "", branchChange, "main.go", true},
		{"a Go test failure's base name in a package the branch changes", goTest, "", branchChange, "internal/pipeline/policy_test.go", true},
		{"an MSBuild base name in a project the branch changes", "File.cs(12,3): error CS0029: Cannot implicitly convert type 'string' to 'int'\n", "", "src/App/Program.cs", "src/App/File.cs", true},
		{"a root-level base name while the branch changes a root-level file", "--- FAIL: TestC (0.00s)\n    c.go:3: want 1\nFAIL\n", "", "own.go", "c.go", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root, worktree := t.TempDir(), t.TempDir()
			runPipelineGit(t, worktree, "init", "--quiet", "--initial-branch=main")
			trusted := commitFiles(t, worktree, map[string]string{"own.go": "own\n", "a.go": "a\n", "c.go": "c\n", "main.go": "m\n", "internal/x/data.go": "x\n", "internal/c/c.go": "c\n", "internal/pipeline/policy.go": "p\n", "internal/pipeline/policy_test.go": "t\n", "internal/gatetest/policy_test.go": "g\n", "src/foo.ts": "f\n", "src/App/File.cs": "f\n", "src/App/Program.cs": "p\n"})
			runPipelineGit(t, worktree, "checkout", "--quiet", "-b", handbackBranch)
			head := commitFiles(t, worktree, map[string]string{test.own: "changed by the branch\n"})
			run := Handback{RunID: handbackRun, RepoID: handbackRepo, Branch: handbackBranch, Head: head, SubmittedHead: head, Step: "test", Log: filepath.Join(t.TempDir(), "test.log"), Findings: []HandbackFinding{{ID: "test-failure", Severity: "error", Description: test.finding}}}
			log := strings.NewReplacer("{worktree}", worktree, "{gate}", filepath.Join(root, "worktrees", handbackRepo, handbackRun)).Replace(test.log)
			writeFixtureFile(t, run.Log, log)
			commitFiles(t, worktree, map[string]string{test.file: "corrected\n"})

			// Act
			err := Reader{Root: root, Commands: execx.OSRunner{}}.CheckCorrection(context.Background(), worktree, trusted, run)

			// Assert
			if test.isNamed {
				if err != nil {
					t.Fatalf("a correction of %s, which the failure names, was refused: %v", test.file, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "the correction changes "+test.file+", which the branch does not change") {
				t.Fatalf("error = %v, want %s refused as outside the failure's scope", err, test.file)
			}
		})
	}
}

// gateRepository is a gate's bare repository under root and a task worktree
// of it whose branch is at the head the run was submitted from, while the
// gate's own clone added a commit, the run's head, on top.
func gateRepository(t *testing.T, root string) (string, Handback) {
	t.Helper()
	bare := filepath.Join(root, "repos", handbackRepo+".git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, bare, "init", "--quiet", "--bare")
	worktree := t.TempDir()
	runPipelineGit(t, worktree, "init", "--quiet", "--initial-branch=main")
	commitFiles(t, worktree, map[string]string{"a.go": "a\n"})
	runPipelineGit(t, worktree, "checkout", "--quiet", "-b", handbackBranch)
	submitted := commitFiles(t, worktree, map[string]string{"a.go": "a2\n"})
	runPipelineGit(t, worktree, "push", "--quiet", bare, "HEAD:refs/heads/"+handbackBranch)
	gate := t.TempDir()
	runPipelineGit(t, gate, "clone", "--quiet", "--branch", handbackBranch, bare, ".")
	head := commitFiles(t, gate, map[string]string{"a.go": "a3\n"})
	runPipelineGit(t, gate, "push", "--quiet", "origin", "HEAD:refs/heads/"+handbackBranch)
	return worktree, Handback{RunID: handbackRun, RepoID: handbackRepo, Branch: handbackBranch, Head: head, SubmittedHead: submitted, Step: "test"}
}

func TestTakeRunHeadMovesOnlyABranchStillAtItsSubmittedHead(t *testing.T) {
	for _, test := range []struct {
		name      string
		move      func(t *testing.T, worktree, bare string, run Handback)
		isMoved   bool
		isRefused bool
	}{
		{"a branch at the submitted head", func(*testing.T, string, string, Handback) {}, true, false},
		{"a branch already at the run's head", func(t *testing.T, worktree, bare string, run Handback) {
			runPipelineGit(t, worktree, "fetch", "--quiet", "--no-tags", bare, handbackBranch)
			runPipelineGit(t, worktree, "reset", "--quiet", "--keep", run.Head)
		}, false, false},
		{"a branch the goblin moved on", func(t *testing.T, worktree, _ string, _ Handback) {
			commitFiles(t, worktree, map[string]string{"b.go": "b\n"})
		}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			worktree, run := gateRepository(t, root)
			bare := filepath.Join(root, "repos", handbackRepo+".git")
			reader := Reader{Root: root, Commands: execx.OSRunner{}}
			test.move(t, worktree, bare, run)
			before := runPipelineGit(t, worktree, "rev-parse", "HEAD")

			// Act
			ref, err := reader.TakeRunHead(context.Background(), worktree, run)

			// Assert
			after := runPipelineGit(t, worktree, "rev-parse", "HEAD")
			if test.isRefused {
				if err == nil || after != before || !strings.Contains(err.Error(), "was not submitted from") {
					t.Fatalf("ref = %q, error = %v, head %s -> %s, want the branch left as it was", ref, err, before, after)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !test.isMoved {
				if ref != "" || after != before {
					t.Fatalf("ref = %q, head %s -> %s, want nothing done", ref, before, after)
				}
				return
			}
			if after != run.Head || runPipelineGit(t, worktree, "symbolic-ref", "--short", "HEAD") != handbackBranch {
				t.Fatalf("head = %s, want the run's head %s on %s", after, run.Head, handbackBranch)
			}
			if pinned := runPipelineGit(t, bare, "rev-parse", ref); pinned != run.Head {
				t.Fatalf("%s = %s, want the run's head %s pinned", ref, pinned, run.Head)
			}
		})
	}
}

func TestDocumentFieldsReadsTopLevelScalars(t *testing.T) {
	// Arrange
	document := "predecessor_id: 01M70HANDBACKRUNOFTHISTEST\r\npredecessor_status: cancelled\r\npublished_head_sha: null\r\npredecessor_launch_assertion_json: \"{\\\"trusted_sha\\\":\\\"abc\\\"}\"\r\nsource_anchors:\r\n  review: nested\r\n"

	// Act
	fields, err := documentFields([]byte(document))

	// Assert
	want := map[string]string{"predecessor_id": handbackRun, "predecessor_status": "cancelled", "published_head_sha": "", "predecessor_launch_assertion_json": `{"trusted_sha":"abc"}`}
	if err != nil || len(fields) != len(want) {
		t.Fatalf("fields = %v (%v), want %v", fields, err, want)
	}
	for key, value := range want {
		if fields[key] != value {
			t.Fatalf("%s = %q, want %q", key, fields[key], value)
		}
	}
}

func TestDocumentFieldsRefusesABrokenQuotedValue(t *testing.T) {
	// Act
	_, err := documentFields([]byte("intent_digest: \"unterminated\n"))

	// Assert
	if err == nil || !strings.Contains(err.Error(), "intent_digest") {
		t.Fatalf("error = %v, want the field named", err)
	}
}

// ownerRunner is a native engine that stops a run and prints its terminal
// preflight.
type ownerRunner struct {
	preflight string
	requests  []string
}

func (runner *ownerRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "no-mistakes" {
		return execx.Result{}, fmt.Errorf("unexpected command %s", request.Name)
	}
	runner.requests = append(runner.requests, strings.Join(request.Args, " "))
	if slices.Contains(request.Args, "terminal-preflight") {
		return execx.Result{Stdout: []byte(runner.preflight)}, nil
	}
	return execx.Result{}, nil
}

func ownerPreflight(status, competing, clean string) string {
	return strings.Join([]string{
		"predecessor_id: " + handbackRun,
		"repo_id: " + handbackRepo,
		"branch: " + handbackBranch,
		"predecessor_status: " + status,
		"has_competing_run: " + competing,
		"snapshot_digest: sha256:snapshot",
		"candidate_sha: cccccccccccccccccccccccccccccccccccccccc",
		"candidate_tree: dddddddddddddddddddddddddddddddddddddddd",
		"caller_is_clean: " + clean,
		"predecessor_launch_nonce: ceo-launch",
		"predecessor_validation_generation: policy-hash",
		"intent_digest: sha256:intent",
		`predecessor_launch_assertion_json: "{\"trusted_sha\":\"old\",\"apply\":true}"`,
		"published_head_sha: null",
		"push_generation: 0",
		"push_target_fingerprint: null",
		"",
	}, "\n")
}

// The parked run is stopped first, and its correction is submitted under the
// run's own launch with today's trusted commit.
func TestOwnerAdmissionSubmitsTheRunsOwnLaunchWithTodaysTrustedCommit(t *testing.T) {
	// Arrange
	worktree, dir := t.TempDir(), t.TempDir()
	runner := &ownerRunner{preflight: ownerPreflight("cancelled", "false", "true")}
	run := Handback{RunID: handbackRun, RepoID: handbackRepo, Branch: handbackBranch, Head: handbackHead, Step: "lint", Findings: []HandbackFinding{{ID: "lint-failure"}}}
	selection, err := versionSeven(t).Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	file, err := Reader{Commands: runner}.OwnerAdmission(context.Background(), worktree, handbackShip, dir, run, selection, nil)

	// Assert
	if want := []string{"axi abort --run " + handbackRun, "axi terminal-preflight --predecessor " + handbackRun}; err != nil || !slices.Equal(runner.requests, want) {
		t.Fatalf("requests = %q (%v), want %q", runner.requests, err, want)
	}
	data, err := fsx.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got admission
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	caller, err := fsx.Canonical(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if got.PredecessorID != handbackRun || got.CallerDir != caller || got.CandidateSHA != strings.Repeat("c", 40) || got.LaunchNonce != "ceo-launch" || got.ValidationGeneration != "policy-hash" || got.PublishedHeadSHA != "" {
		t.Fatalf("admission = %+v, want the predecessor's own launch", got)
	}
	if got.ControllerPolicy.SHA256 != selection.Hash || got.ControllerPolicy.Class != "high-risk" || got.ControllerPolicy.ReviewCycles != 3 {
		t.Fatalf("controller policy = %+v, want the task's frozen selection", got.ControllerPolicy)
	}
	var assertion map[string]any
	if err := json.Unmarshal(got.LaunchAssertion, &assertion); err != nil || assertion["trusted_sha"] != handbackShip || assertion["apply"] != true {
		t.Fatalf("launch assertion = %v (%v), want the run's own with today's trusted commit", assertion, err)
	}
	if !strings.Contains(got.Reason, "lint-failure") || !strings.Contains(got.Reason, "version 7") {
		t.Fatalf("reason = %q, want the findings and the policy named", got.Reason)
	}
}

func TestOwnerAdmissionSubmitsNothingTheNativePreflightDoesNotClear(t *testing.T) {
	for _, test := range []struct {
		name, status, competing, clean, refusal string
	}{
		{"a run that did not stop", "running", "false", "true", "not stopped for its correction"},
		{"a competing run", "cancelled", "true", "true", "another run owns"},
		{"a worktree that changed", "cancelled", "false", "false", "the worktree changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			runner := &ownerRunner{preflight: ownerPreflight(test.status, test.competing, test.clean)}
			run := Handback{RunID: handbackRun, RepoID: handbackRepo, Branch: handbackBranch, Head: handbackHead, Step: "test", Findings: []HandbackFinding{{ID: "test-failure"}}}
			selection, err := versionSeven(t).Select("ordinary")
			if err != nil {
				t.Fatal(err)
			}

			// Act
			_, err = Reader{Commands: runner}.OwnerAdmission(context.Background(), t.TempDir(), handbackShip, dir, run, selection, nil)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("error = %v, want one saying %q", err, test.refusal)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("a refused correction wrote %v", entries)
			}
		})
	}
}
