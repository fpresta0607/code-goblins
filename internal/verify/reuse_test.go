package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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
// build, git's version and one variable, for the fast tier.
func reuseDeclared() project.Reuse {
	return project.Reuse{Tiers: []string{"fast"}, Inputs: []string{"build"}, Tools: []project.Command{{"git", "version"}}, Environment: []string{"CFO_REUSE_TEST_SETTING"}}
}

// namedKey is the part of a key its caller names.
func namedKey() ReuseKey {
	return ReuseKey{Project: "demo", Scope: "fast", Commands: [][]string{{"demo", "check"}}, LimitSeconds: 900, Executor: "main-0123abcd"}
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
	if key.Project != "demo" || key.Scope != "fast" || key.Executor != "main-0123abcd" || key.LimitSeconds != 900 || len(key.Commands) != 1 {
		t.Errorf("key = %+v; want what its caller named kept", key)
	}
	if key.Source != (Source{Commit: head, CommitTree: tree, Tree: tree, IsComplete: true}) || key.Base != head {
		t.Errorf("key source %+v and base %q; want the clean head and the base origin/main names", key.Source, key.Base)
	}
	sum := sha256.Sum256([]byte("one"))
	if key.Inputs == "" || len(key.Tools) != 1 || key.Tools[0] == "" || len(key.Environment) != 1 || key.Environment[0] != "CFO_REUSE_TEST_SETTING=sha256:"+hex.EncodeToString(sum[:]) || key.Platform == "" {
		t.Errorf("key = %+v; want the inputs' digest, git's version, the variable's digest and the platform", key)
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
	for name, row := range map[string]struct {
		arrange   func(t *testing.T, dir string) (ReuseKey, project.Reuse)
		wantError string
	}{
		"an unstamped build": {wantError: "an unstamped cfo build", arrange: func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			key := namedKey()
			key.Executor = "dev"
			return key, reuseDeclared()
		}},
		// A sparse checkout leaves a.txt's neighbours off the disk, which the
		// tree then holds as its commit has them.
		"an incomplete source": {wantError: "the tree does not hold all of what is on disk", arrange: func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			if err := os.RemoveAll(filepath.Join(dir, "build")); err != nil {
				t.Fatal(err)
			}
			gitIn(t, dir, "sparse-checkout", "set", "--no-cone", "/a.txt")
			return namedKey(), reuseDeclared()
		}},
		// With .gitignore off the disk, the build is a file outside the
		// sparse checkout, which git will not stage.
		"a source that cannot be read": {wantError: "read the source", arrange: func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			gitIn(t, dir, "sparse-checkout", "set", "--no-cone", "/a.txt")
			return namedKey(), reuseDeclared()
		}},
		"no default branch": {wantError: "no default branch", arrange: func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			gitIn(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
			return namedKey(), reuseDeclared()
		}},
		"a tool that fails": {wantError: "read the tool git no-such-command", arrange: func(t *testing.T, dir string) (ReuseKey, project.Reuse) {
			declared := reuseDeclared()
			declared.Tools = []project.Command{{"git", "no-such-command"}}
			return namedKey(), declared
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := reuseWorktree(t)
			key, declared := row.arrange(t, dir)

			// Act
			read, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, key, declared)

			// Assert
			if err == nil || !strings.Contains(err.Error(), row.wantError) {
				t.Errorf("ReadReuseKey = %+v, %v; want an error that says %q", read, err, row.wantError)
			}
		})
	}
}

// The base is read in the worktree a key is read for, whatever repository
// the git variables it inherits name.
func TestReadReuseKeyReadsTheBaseOfTheWorktreeItIsGiven(t *testing.T) {
	// Arrange
	dir := reuseWorktree(t)
	other := reuseWorktree(t)
	gitIn(t, other, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "elsewhere")
	gitIn(t, other, "update-ref", "refs/remotes/origin/main", "HEAD")
	want := gitIn(t, dir, "rev-parse", "HEAD")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)

	// Act
	key, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, namedKey(), reuseDeclared())

	// Assert
	if err != nil || key.Base != want {
		t.Errorf("key base = %q (%v); want the worktree's own %q", key.Base, err, want)
	}
}

// A declared variable's value is kept only as a digest, so a secret named to
// key passes is never written down.
func TestReadReuseKeyKeepsNoVariableValue(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	dir := reuseWorktree(t)
	t.Setenv("CFO_REUSE_TEST_SETTING", "s3cret-value")
	key, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, namedKey(), reuseDeclared())
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = KeepPass(key, []Result{passedRun(t, key, key.Commands[0])}, time.Now())

	// Assert
	kept, readError := os.ReadFile(keptAt(key))
	if err != nil || readError != nil || strings.Contains(string(kept), "s3cret-value") {
		t.Errorf("KeepPass: %v; the pass (%v) holds the value = %v; want a pass without it", err, readError, strings.Contains(string(kept), "s3cret-value"))
	}
}

// A declared input that is missing is told from one that is an empty folder.
func TestInputsDigestTellsAMissingInputFromAnEmptyOne(t *testing.T) {
	// Arrange
	missing, empty := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(empty, "build"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	ofMissing, missingError := inputsDigest(context.Background(), missing, []string{"build"})
	ofEmpty, emptyError := inputsDigest(context.Background(), empty, []string{"build"})

	// Assert
	if missingError != nil || emptyError != nil || ofMissing == ofEmpty {
		t.Errorf("digests %q (%v) and %q (%v); want two different digests", ofMissing, missingError, ofEmpty, emptyError)
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
	return Result{Command: command, Status: "passed", Scope: key.Scope, Task: "t1", Execution: path, Source: record.Source}
}

// sealedKey is a key as ReadReuseKey would read it.
func sealedKey() ReuseKey {
	key := namedKey()
	key.Source = Source{Commit: "c1", CommitTree: "t1", Tree: "t1", IsComplete: true}
	key.Base, key.Inputs, key.Platform = "b1", "i1", "windows/amd64"
	key.Tools, key.Environment = []string{"git version 2"}, []string{"CFO_REUSE_TEST_SETTING=sha256:1"}
	return key
}

// keptAt is where the pass of key is kept.
func keptAt(key ReuseKey) string {
	return filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "passes", key.ID()+".json")
}

// A pass kept for a key is found for that key, with each of its results
// naming the pass, when and for which task it was taken again, and keeping
// the original run's own fields.
func TestFindPassReusesThePassKeptForItsKey(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	key := sealedKey()
	now := time.Now()
	if err := KeepPass(key, []Result{passedRun(t, key, key.Commands[0])}, now); err != nil {
		t.Fatal(err)
	}
	taken := now.Add(time.Hour)

	// Act
	pass, err := FindPass(key, taken, 24*time.Hour, "t2")

	// Assert
	if err != nil || len(pass.Results) != 1 {
		t.Fatalf("FindPass = %+v, %v; want the kept pass", pass, err)
	}
	result := pass.Results[0]
	if !fsx.SamePath(result.Reused, keptAt(key)) || !result.ReusedAt.Equal(taken.UTC()) || result.ReusedFor != "t2" || result.Task != "t1" || result.Status != "passed" {
		t.Errorf("result = %+v; want it taken from %s at %s for t2, its own task t1", result, keptAt(key), taken.UTC())
	}
}

// Only the exact key finds a pass: one with any part changed finds none.
func TestFindPassFindsNothingForAnotherKey(t *testing.T) {
	for name, change := range map[string]func(key *ReuseKey){
		"another tree":       func(key *ReuseKey) { key.Source.Tree = "t2" },
		"another commit":     func(key *ReuseKey) { key.Source.Commit = "c2" },
		"another base":       func(key *ReuseKey) { key.Base = "b2" },
		"other inputs":       func(key *ReuseKey) { key.Inputs = "i2" },
		"another tool":       func(key *ReuseKey) { key.Tools = []string{"git version 3"} },
		"another variable":   func(key *ReuseKey) { key.Environment = []string{"CFO_REUSE_TEST_SETTING=sha256:2"} },
		"another platform":   func(key *ReuseKey) { key.Platform = "linux/amd64" },
		"another build":      func(key *ReuseKey) { key.Executor = "main-4567cdef" },
		"another command":    func(key *ReuseKey) { key.Commands = [][]string{{"demo", "check", "--all"}} },
		"another tier":       func(key *ReuseKey) { key.Scope = "full" },
		"another project":    func(key *ReuseKey) { key.Project = "other" },
		"another time limit": func(key *ReuseKey) { key.LimitSeconds = 90 },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key := sealedKey()
			now := time.Now()
			if err := KeepPass(key, []Result{passedRun(t, key, key.Commands[0])}, now); err != nil {
				t.Fatal(err)
			}
			other := sealedKey()
			change(&other)

			// Act
			pass, err := FindPass(other, now, 24*time.Hour, "t1")

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
		age   time.Duration
		spoil func(t *testing.T, record string)
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
			if err := KeepPass(key, []Result{result}, now.Add(-row.age)); err != nil {
				t.Fatal(err)
			}
			if row.spoil != nil {
				row.spoil(t, result.Execution)
			}

			// Act
			pass, err := FindPass(key, now, 24*time.Hour, "t1")

			// Assert
			if !errors.Is(err, ErrNoPass) {
				t.Errorf("FindPass = %+v, %v; want ErrNoPass", pass, err)
			}
		})
	}
}

// A kept pass file that is not a whole pass of its key, as one edited by
// hand, is no pass: one that holds no result, or a result that failed.
func TestFindPassFindsNothingInAPassThatIsNotWhole(t *testing.T) {
	for name, spoil := range map[string]func(pass *Pass){
		"no result":            func(pass *Pass) { pass.Results = nil },
		"a result that failed": func(pass *Pass) { pass.Results[0].Status = "failed" },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key := sealedKey()
			now := time.Now()
			pass := Pass{Key: key, Results: []Result{passedRun(t, key, key.Commands[0])}, Kept: now.UTC()}
			spoil(&pass)
			if err := Save(keptAt(key), pass); err != nil {
				t.Fatal(err)
			}

			// Act
			found, err := FindPass(key, now, 24*time.Hour, "t1")

			// Assert
			if !errors.Is(err, ErrNoPass) {
				t.Errorf("FindPass = %+v, %v; want ErrNoPass", found, err)
			}
		})
	}
}

// A pass file under a key's name that holds another key's pass is no pass of
// that key, though the other key has the same source and its pass is whole,
// as only the key itself shows what else the pass depended on.
func TestFindPassFindsNothingInAPassOfAnotherKey(t *testing.T) {
	for name, differ := range map[string]func(key *ReuseKey){
		"another input":    func(key *ReuseKey) { key.Inputs = "i2" },
		"another base":     func(key *ReuseKey) { key.Base = "b2" },
		"another tool":     func(key *ReuseKey) { key.Tools = []string{"git version 3"} },
		"another platform": func(key *ReuseKey) { key.Platform = "windows/arm64" },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key, other := sealedKey(), sealedKey()
			differ(&other)
			now := time.Now()
			if err := KeepPass(other, []Result{passedRun(t, other, other.Commands[0])}, now); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(keptAt(other), keptAt(key)); err != nil {
				t.Fatal(err)
			}

			// Act
			pass, err := FindPass(key, now, 24*time.Hour, "t1")

			// Assert
			if !errors.Is(err, ErrNoPass) || !strings.Contains(err.Error(), "is not this key's") {
				t.Errorf("FindPass = %+v, %v; want ErrNoPass for a pass of another key", pass, err)
			}
		})
	}
}

// Only a whole pass on the key's own source is kept: a tier with a command
// that failed, did not run, ran on another or no one source, or kept no
// record in the store is not.
func TestKeepPassKeepsOnlyAWholePassOnItsSource(t *testing.T) {
	other := Source{Commit: "c1", CommitTree: "t1", Tree: "t9", IsComplete: true}
	incomplete := Source{Commit: "c1", CommitTree: "t1", Tree: "t1"}
	for name, spoil := range map[string]func(result *Result){
		"a failed command":           func(result *Result) { result.Status = "failed" },
		"a command not run":          func(result *Result) { result.Status = "not_run" },
		"a command with no record":   func(result *Result) { result.Execution = "" },
		"a record outside the store": func(result *Result) { result.Execution = filepath.Join(os.TempDir(), "elsewhere.json") },
		"another command":            func(result *Result) { result.Command = []string{"demo", "other"} },
		"a command of another source": func(result *Result) {
			result.Source = SourceReads{Start: &other, End: &other}
		},
		"reads that differed": func(result *Result) {
			result.Source = SourceReads{Start: result.Source.Start, End: &other}
		},
		"a read that failed": func(result *Result) { result.Source.Failure = "git failed" },
		"an incomplete source": func(result *Result) {
			result.Source = SourceReads{Start: &incomplete, End: &incomplete}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key := sealedKey()
			result := passedRun(t, key, key.Commands[0])
			spoil(&result)

			// Act
			err := KeepPass(key, []Result{result}, time.Now())

			// Assert
			if err == nil {
				t.Error("KeepPass kept the pass; want it refused")
			}
			if _, err := FindPass(key, time.Now(), 24*time.Hour, "t1"); !errors.Is(err, ErrNoPass) {
				t.Errorf("FindPass: %v; want ErrNoPass", err)
			}
		})
	}
}

// A tier with no command, or with a command that left no result, is no
// whole pass.
func TestKeepPassKeepsNoPassMissingACommand(t *testing.T) {
	for name, commands := range map[string][][]string{
		"a command with no result": {{"demo", "check"}, {"demo", "second"}},
		"no command":               nil,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			key := sealedKey()
			var results []Result
			if len(commands) > 0 {
				results = []Result{passedRun(t, key, commands[0])}
			}
			key.Commands = commands

			// Act
			err := KeepPass(key, results, time.Now())

			// Assert
			if err == nil {
				t.Error("KeepPass kept the pass; want it refused")
			}
		})
	}
}

// Pruning removes the passes kept more than a week before, and no other.
func TestPrunePassesRemovesPassesOlderThanAWeek(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	old, young := sealedKey(), sealedKey()
	old.Scope = "full"
	now := time.Now()
	for _, key := range []ReuseKey{old, young} {
		if err := KeepPass(key, []Result{passedRun(t, key, key.Commands[0])}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(keptAt(old), now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Act
	err := PrunePasses(now)

	// Assert
	if _, statError := os.Stat(keptAt(old)); err != nil || !errors.Is(statError, os.ErrNotExist) {
		t.Errorf("PrunePasses: %v, and the week-old pass is there (%v); want it removed", err, statError)
	}
	if _, statError := os.Stat(keptAt(young)); statError != nil {
		t.Errorf("the young pass: %v; want it kept", statError)
	}
}
