package worktree

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every goblin's uv, pip and npm resolve only releases a week old, so a
// hijacked release is pulled before it reaches one. uv's cutoff stays out of
// a worktree that tracks a uv.lock, wherever it is, since uv then sets the
// lock aside: `uv sync --locked` fails and `uv run` rewrites it.
func TestCooldownHoldsUvPipAndNpmToReleasesAWeekOld(t *testing.T) {
	now := time.Date(2026, 10, 21, 13, 30, 0, 0, time.FixedZone("CDT", -5*60*60))
	for _, test := range []struct {
		name    string
		tracked []string
		held    []string
		wantUV  bool
		locks   []string
	}{
		{name: "a worktree with no uv.lock", tracked: []string{"requirements.txt", "uv.lock.bak"}, wantUV: true},
		{name: "a uv.lock only the folder holds", tracked: []string{"pyproject.toml"}, held: []string{"uv.lock"}, wantUV: true},
		{name: "tracked uv.lock files at the root and in a subfolder", tracked: []string{"uv.lock", "apps/api/uv.lock", "pyproject.toml"}, locks: []string{"apps/api/uv.lock", "uv.lock"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktreePath := t.TempDir()
			gitRun(t, worktreePath, "init", "-q")
			for _, name := range append(slices.Clone(test.tracked), test.held...) {
				path := filepath.Join(worktreePath, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				writeFileLine(t, path, "x")
			}
			gitRun(t, worktreePath, append([]string{"add", "--"}, test.tracked...)...)

			// Act
			env, locks, err := (Service{Commands: execx.OSRunner{}}).Cooldown(context.Background(), worktreePath, now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"PIP_UPLOADED_PRIOR_TO": "P7D", "npm_config_before": "2026-10-14T18:30:00Z"}
			if test.wantUV {
				want["UV_EXCLUDE_NEWER"] = "7 days"
			}
			if !maps.Equal(env, want) || !slices.Equal(locks, test.locks) {
				t.Fatalf("env = %v, locks = %q; want %v and %q", env, locks, want, test.locks)
			}
		})
	}
}

// A worktree whose uv.lock files git cannot list fails the start that asks,
// rather than give uv a cutoff that would rewrite a lock it did not see.
func TestCooldownFailsWhereGitCannotListTheWorktree(t *testing.T) {
	// Arrange
	notARepository := t.TempDir()
	if err := os.WriteFile(filepath.Join(notARepository, "uv.lock"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(notARepository))

	// Act
	env, _, err := (Service{Commands: execx.OSRunner{}}).Cooldown(context.Background(), notARepository, time.Now())

	// Assert
	if err == nil || env != nil {
		t.Fatalf("env = %v, err = %v, want the listing's failure", env, err)
	}
}
