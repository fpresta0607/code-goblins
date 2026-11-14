package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/pipeline/pipelinetest"
)

func TestHandbackReadsTheExactPersistedPublicationAndReceipt(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is needed for persisted handback fixtures")
	}
	for _, test := range []struct {
		name, receipt, project, run, extraSQL, refusal string
		shouldRevalidatePublication                    bool
	}{
		{name: "release", run: "original-run"},
		{name: "explicit publication revalidation", run: "original-run", shouldRevalidatePublication: true},
		{name: "receipt binds continuation after accepted replay", run: "original-run", receipt: "native-receipt", extraSQL: "UPDATE runs SET head_sha='" + strings.Repeat("c", 40) + "',push_generation=8;"},
		{name: "explicit revalidation retains released identity on replay", run: "original-run", receipt: "native-receipt", shouldRevalidatePublication: true, extraSQL: "UPDATE runs SET head_sha='" + strings.Repeat("c", 40) + "',push_generation=8;"},
		{name: "wrong repository", run: "original-run", project: "other", refusal: "exactly one latest published run"},
		{name: "stale run", run: "earlier-run", refusal: "no longer the latest run"},
		{name: "unknown checkpoint", run: "original-run", receipt: "unknown-receipt", refusal: "exactly one latest published run"},
		{name: "a run that never published", run: "original-run", extraSQL: "UPDATE runs SET last_pushed_sha=NULL,push_generation=0 WHERE id='original-run';", refusal: "run original-run never published, so handback, which returns a published run's failed CI, does not apply to it; a run that failed before it published, such as one Review approved whose Test step then failed preparing, has no route on from its reviewed head that keeps its review until fpresta0607/no-mistakes#323 lands"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			profile, root, caller := t.TempDir(), t.TempDir(), t.TempDir()
			for _, name := range []string{"USERPROFILE", "HOME", "APPDATA", "LOCALAPPDATA"} {
				t.Setenv(name, profile)
			}
			t.Setenv("NM_HOME", root)
			t.Setenv("GOBLINS_HOME", profile)
			t.Setenv("GOBLINS_STATE_OVERRIDE", filepath.Join(profile, "state"))
			if effective, err := os.UserHomeDir(); err != nil || effective != profile {
				t.Fatalf("effective user home = %q, %v", effective, err)
			}
			caller, err := fsx.Canonical(caller)
			if err != nil {
				t.Fatal(err)
			}
			commands := execx.OSRunner{}
			git := func(args ...string) string {
				t.Helper()
				result, err := commands.Run(t.Context(), execx.Request{Dir: caller, Name: "git", Args: args})
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("git %q: %v %s", args, err, result.Stderr)
				}
				return strings.TrimSpace(string(result.Stdout))
			}
			git("init", "-b", "fix/handback")
			git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "test: retain authored history")
			head := git("rev-parse", "HEAD")
			published := strings.Repeat("a", 40)
			database := filepath.Join(root, "state.sqlite")
			schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,last_pushed_sha TEXT,push_generation INTEGER,created_at INTEGER);
CREATE TABLE ci_handbacks(id TEXT,run_id TEXT,caller_dir TEXT,released_head TEXT,push_generation INTEGER);
INSERT INTO repos VALUES('repo-1','%s');
INSERT INTO runs VALUES('earlier-run','repo-1','fix/handback','%s','%s',6,1);
INSERT INTO runs VALUES('original-run','repo-1','fix/handback','%s','%s',7,2);
INSERT INTO ci_handbacks VALUES('native-receipt','original-run','%s','%s',7);`, strings.ReplaceAll(filepath.ToSlash(caller), "'", "''"), published, published, published, published, strings.ReplaceAll(caller, "'", "''"), published) + test.extraSQL
			result, err := commands.Run(t.Context(), execx.Request{Name: "sqlite3", Args: []string{database, schema}})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("create database: %v %s", err, result.Stderr)
			}
			before, err := os.ReadFile(database)
			if err != nil {
				t.Fatal(err)
			}
			project := caller
			if test.project == "other" {
				project = t.TempDir()
			}
			calls := 0
			reader := Reader{Root: root, Commands: runnerFunc(func(ctx context.Context, request execx.Request) (execx.Result, error) {
				if request.Name != "no-mistakes" {
					return pipelinetest.FixtureCommands{Runner: commands}.Run(ctx, request)
				}
				calls++
				if len(request.Args) < 4 || strings.Join(request.Args[:3], " ") != "axi handback --expect" {
					t.Fatalf("unsupported native operation: %q", request.Args)
				}
				data, err := os.ReadFile(request.Args[3])
				if err != nil {
					t.Fatal(err)
				}
				var binding handbackBinding
				if err := json.Unmarshal(data, &binding); err != nil {
					t.Fatal(err)
				}
				if binding.RunID != "original-run" || binding.RepoID != "repo-1" || binding.Branch != "fix/handback" || binding.CallerDir != caller || binding.CallerHead != head || binding.CurrentHead != published || binding.PublishedHead != published || binding.PushGeneration != 7 {
					t.Fatalf("native expectation = %+v", binding)
				}
				wantArgs := []string{"axi", "handback", "--expect", request.Args[3]}
				if test.shouldRevalidatePublication {
					wantArgs = append(wantArgs, "--revalidate-publication")
				}
				if test.receipt != "" {
					wantArgs = append(wantArgs, "--resume", "--receipt", test.receipt)
				}
				if !slices.Equal(request.Args, wantArgs) || request.Dir != caller || !slices.Equal(request.Env, []string{"NM_HOME=" + root}) {
					t.Fatalf("native request = %+v, want %q in %s with unchanged environment", request, wantArgs, caller)
				}
				return execx.Result{}, nil
			})}

			// Act
			_, err = reader.Handback(t.Context(), project, caller, "fix/handback", test.run, test.receipt, test.shouldRevalidatePublication, []string{"NM_HOME=" + root})

			// Assert
			if test.refusal == "" {
				if err != nil || calls != 1 {
					t.Fatalf("handback = %v, native calls = %d", err, calls)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.refusal) || calls != 0 {
				t.Fatalf("handback = %v, calls = %d, want %q without mutation", err, calls, test.refusal)
			}
			after, err := os.ReadFile(database)
			if err != nil || !bytes.Equal(before, after) || git("rev-parse", "HEAD") != head {
				t.Fatalf("wrapper changed persisted state or caller history: %v", err)
			}
		})
	}
}
