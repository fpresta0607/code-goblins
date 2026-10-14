package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/project"
)

// reuseWorktree is a source worktree whose branch left a default branch, as
// origin/main names it, and that holds an ignored input, build/page.html.
func reuseWorktree(t *testing.T) string {
	t.Helper()
	dir := sourceWorktree(t)
	writeSource(t, dir, ".gitignore", "*.log\nbuild/\n")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-am", "ignore the build")
	gitIn(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	writeSource(t, dir, "build/page.html", "built\n")
	return dir
}

// reuseDeclared reads what reuse declares in a project's record: the ignored
// build, git's version and one variable.
func reuseDeclared() project.Reuse {
	return project.Reuse{Inputs: []string{"build"}, Tools: []project.Command{{"git", "version"}}, Environment: []string{"CFO_REUSE_TEST_SETTING"}}
}

// namedKey is the part of a key its caller names.
func namedKey() ReuseKey {
	return ReuseKey{Project: "demo", Scope: "fast", Commands: [][]string{{"demo", "check"}}, Executor: "main-0123abcd"}
}

// A key holds the source, where the branch left the default branch, the
// declared ignored inputs, tools and variables, the platform and the build
// that runs the checks, beside what its caller names.
func TestReadReuseKeyReadsEverythingAPassDependsOn(t *testing.T) {
	// Arrange
	dir := reuseWorktree(t)
	t.Setenv("CFO_REUSE_TEST_SETTING", "one")

	// Act
	key, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, namedKey(), reuseDeclared())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	head, tree := gitIn(t, dir, "rev-parse", "HEAD"), gitIn(t, dir, "rev-parse", "HEAD^{tree}")
	if key.Project != "demo" || key.Scope != "fast" || key.Executor != "main-0123abcd" || len(key.Commands) != 1 {
		t.Errorf("key = %+v; want what its caller named kept", key)
	}
	if key.Source != (Source{Commit: head, CommitTree: tree, Tree: tree, IsComplete: true}) || key.Base != head {
		t.Errorf("key source %+v and base %q; want the clean head and the base origin/main names", key.Source, key.Base)
	}
	if key.Inputs == "" || len(key.Tools) != 1 || key.Tools[0] == "" || len(key.Environment) != 1 || key.Environment[0] != "CFO_REUSE_TEST_SETTING=one" || key.Platform == "" {
		t.Errorf("key = %+v; want the inputs' digest, git's version, the variable and the platform", key)
	}
}

// Each thing a pass depends on changes the key: the ignored input's content,
// a variable, a file git sees, and where the branch left the default branch.
func TestReadReuseKeyChangesWithEachInput(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, dir string){
		"an ignored input":   func(t *testing.T, dir string) { writeSource(t, dir, "build/page.html", "rebuilt\n") },
		"a new ignored file": func(t *testing.T, dir string) { writeSource(t, dir, "build/more.html", "more\n") },
		"a variable":         func(t *testing.T, dir string) { t.Setenv("CFO_REUSE_TEST_SETTING", "two") },
		"a file git sees":    func(t *testing.T, dir string) { writeSource(t, dir, "a.txt", "changed\n") },
		"the base":           func(t *testing.T, dir string) { gitIn(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD~1") },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := reuseWorktree(t)
			t.Setenv("CFO_REUSE_TEST_SETTING", "one")
			before, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, namedKey(), reuseDeclared())
			if err != nil {
				t.Fatal(err)
			}
			change(t, dir)

			// Act
			after, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, namedKey(), reuseDeclared())

			// Assert
			if err != nil || after.ID() == before.ID() {
				t.Errorf("the key after %s = %+v (%v); want another key than %+v", name, after, err, before)
			}
		})
	}
}

// No key is read, so nothing is reused, when what a pass depends on cannot
// all be read: an unstamped build, an incomplete source, no default branch
// to measure the branch against, or a tool that fails.
func TestReadReuseKeyRefusesWhatItCannotRead(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, dir string) (ReuseKey, project.Reuse){
		"an unstamped build": func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			key := namedKey()
			key.Executor = "dev"
			return key, reuseDeclared()
		},
		"an incomplete source": func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			gitIn(t, dir, "sparse-checkout", "set", "--no-cone", "/a.txt")
			return namedKey(), reuseDeclared()
		},
		"no default branch": func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			gitIn(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
			return namedKey(), reuseDeclared()
		},
		"a tool that fails": func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			declared := reuseDeclared()
			declared.Tools = []project.Command{{"git", "no-such-command"}}
			return namedKey(), declared
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := reuseWorktree(t)
			key, declared := arrange(t, dir)

			// Act
			read, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, key, declared)

			// Assert
			if err == nil {
				t.Errorf("ReadReuseKey = %+v; want an error", read)
			}
		})
	}
}

// passedRun is the result of one command that passed on key's source, with
// its execution record in the store.
func passedRun(t *testing.T, key ReuseKey, command []string) Result {
	t.Helper()
	exit := 0
	record := ExecutionRecord{Version: 1, ID: "run-" + command[len(command)-1], Command: command, Status: "passed", ExitCode: &exit, Source: SourceReads{Start: &key.Source, End: &key.Source}}
	path := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "executions", record.ID+".json")
	if err := Save(path, record); err != nil {
		t.Fatal(err)
	}
	return Result{Command: command, Status: "passed", Scope: key.Scope, Execution: path, Source: record.Source}
}

// sealedKey is a key as ReadReuseKey would read it.
func sealedKey() ReuseKey {
	key := namedKey()
	key.Source = Source{Commit: "c1", CommitTree: "t1", Tree: "t1", IsComplete: true}
	key.Base, key.Inputs, key.Platform = "b1", "i1", "windows/amd64"
	key.Tools, key.Environment = []string{"git version 2"}, []string{"CFO_REUSE_TEST_SETTING=one"}
	return key
}

// A pass kept for a key is found for that key, with each of its results
// naming the pass it was reused from.
func TestFindPassReusesThePassKeptForItsKey(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	key := sealedKey()
	now := time.Now()
	kept, err := KeepPass(key, []Result{passedRun(t, key, key.Commands[0])}, now)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	pass, err := FindPass(key, now.Add(time.Hour), 24*time.Hour)

	// Assert
	if err != nil || len(pass.Results) != 1 || pass.Results[0].Reused != kept || pass.Results[0].Status != "passed" {
		t.Errorf("FindPass = %+v, %v; want the kept pass, its result reused from %s", pass, err, kept)
	}
}

// Only the exact key finds a pass: one with any part changed finds none.
func TestFindPassFindsNothingForAnotherKey(t *testing.T) {
	for name, change := range map[string]func(key *ReuseKey){
		"another tree":     func(key *ReuseKey) { key.Source.Tree = "t2" },
		"another commit":   func(key *ReuseKey) { key.Source.Commit = "c2" },
		"another base":     func(key *ReuseKey) { key.Base = "b2" },
		"other inputs":     func(key *ReuseKey) { key.Inputs = "i2" },
		"another tool":     func(key *ReuseKey) { key.Tools = []string{"git version 3"} },
		"another variable": func(key *ReuseKey) { key.Environment = []string{"CFO_REUSE_TEST_SETTING=two"} },
		"another platform": func(key *ReuseKey) { key.Platform = "linux/amd64" },
		"another build":    func(key *ReuseKey) { key.Executor = "main-4567cdef" },
		"another command":  func(key *ReuseKey) { key.Commands = [][]string{{"demo", "check", "--all"}} },
		"another tier":     func(key *ReuseKey) { key.Scope = "full" },
		"another project":  func(key *ReuseKey) { key.Project = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key := sealedKey()
			now := time.Now()
			if _, err := KeepPass(key, []Result{passedRun(t, key, key.Commands[0])}, now); err != nil {
				t.Fatal(err)
			}
			other := sealedKey()
			change(&other)

			// Act
			pass, err := FindPass(other, now, 24*time.Hour)

			// Assert
			if !errors.Is(err, ErrNoPass) {
				t.Errorf("FindPass = %+v, %v; want ErrNoPass", pass, err)
			}
		})
	}
}

// A pass is reused only while it is fresh and its evidence stands: an old
// pass, or one whose execution record is gone or no longer a pass, is none.
func TestFindPassFindsNothingWithoutFreshEvidence(t *testing.T) {
	for name, row := range map[string]struct {
		age    time.Duration
		spoil  func(t *testing.T, record string)
		isKept bool
	}{
		"a pass older than its limit": {age: 25 * time.Hour},
		"a pass from the future":      {age: -time.Hour},
		"a record that is gone": {spoil: func(t *testing.T, record string) {
			if err := os.Remove(record); err != nil {
				t.Fatal(err)
			}
		}},
		"a record that failed": {spoil: func(t *testing.T, record string) {
			loaded, err := LoadExecution(record)
			if err != nil {
				t.Fatal(err)
			}
			loaded.Status = "failed"
			if err := Save(record, loaded); err != nil {
				t.Fatal(err)
			}
		}},
		"a record of another source": {spoil: func(t *testing.T, record string) {
			loaded, err := LoadExecution(record)
			if err != nil {
				t.Fatal(err)
			}
			other := Source{Commit: "c1", CommitTree: "t1", Tree: "t9", IsComplete: true}
			loaded.Source = SourceReads{Start: &other, End: &other}
			if err := Save(record, loaded); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key := sealedKey()
			now := time.Now()
			result := passedRun(t, key, key.Commands[0])
			if _, err := KeepPass(key, []Result{result}, now.Add(-row.age)); err != nil {
				t.Fatal(err)
			}
			if row.spoil != nil {
				row.spoil(t, result.Execution)
			}

			// Act
			pass, err := FindPass(key, now, 24*time.Hour)

			// Assert
			if !errors.Is(err, ErrNoPass) {
				t.Errorf("FindPass = %+v, %v; want ErrNoPass", pass, err)
			}
		})
	}
}

// Only a whole pass on the key's own source is kept: a tier with a command
// that failed, did not run, ran on another source, or kept no record is not.
func TestKeepPassKeepsOnlyAWholePassOnItsSource(t *testing.T) {
	for name, spoil := range map[string]func(result *Result){
		"a failed command":         func(result *Result) { result.Status = "failed" },
		"a command not run":        func(result *Result) { result.Status = "not_run" },
		"a command with no record": func(result *Result) { result.Execution = "" },
		"a command of another source": func(result *Result) {
			other := Source{Commit: "c1", CommitTree: "t1", Tree: "t9", IsComplete: true}
			result.Source = SourceReads{Start: &other, End: &other}
		},
		"another command": func(result *Result) { result.Command = []string{"demo", "other"} },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key := sealedKey()
			result := passedRun(t, key, key.Commands[0])
			spoil(&result)

			// Act
			kept, err := KeepPass(key, []Result{result}, time.Now())

			// Assert
			if err == nil {
				t.Errorf("KeepPass kept %s; want it refused", kept)
			}
			if _, err := FindPass(key, time.Now(), 24*time.Hour); !errors.Is(err, ErrNoPass) {
				t.Errorf("FindPass: %v; want ErrNoPass", err)
			}
		})
	}
}
