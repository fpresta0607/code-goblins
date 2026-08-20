package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const defaultReturnRetries = 3

var indexLockCollision = regexp.MustCompile(`Unable to create ['"].*index\.lock['"]: File exists`)

// RunnerGit implements Git through the injected CFO subprocess runner.
type RunnerGit struct {
	Commands execx.Runner
	Retries  int
	Sleep    func(context.Context, time.Duration) error
}

// Acquire creates a detached worktree at <project>/.worktrees/<holder> based
// on origin's current default-branch commit. The .worktrees directory is
// registered in the clone's info/exclude first - the per-clone ignore
// mechanism, so no project's tracked .gitignore has to change - and an
// existing path is refused rather than reused, because a worktree path is
// handed to exactly one task.
func (g RunnerGit) Acquire(ctx context.Context, project, holder string) (string, error) {
	if strings.TrimSpace(holder) == "" || holder != filepath.Base(holder) {
		return "", fmt.Errorf("worktree: holder %q is not a usable directory name", holder)
	}
	target, _, err := g.fetchDefault(ctx, project)
	if err != nil {
		return "", err
	}
	if err := g.ensureExcluded(ctx, project, ".worktrees/"); err != nil {
		return "", err
	}
	path := filepath.Join(project, ".worktrees", holder)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("worktree: %q already exists; refusing to reuse another task's worktree", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("worktree: inspect %q: %w", path, err)
	}
	if _, err := g.required(ctx, project, "git", "worktree", "add", "--detach", path, target); err != nil {
		return "", fmt.Errorf("worktree: add worktree for %q: %w", holder, err)
	}
	return path, nil
}

// WorktreeTop reports dir's Git worktree root.
func (g RunnerGit) WorktreeTop(ctx context.Context, dir string) (string, error) {
	result, err := g.command(ctx, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", commandFailure("git rev-parse --show-toplevel", result)
	}
	top := strings.TrimSpace(string(result.Stdout))
	if top == "" {
		return "", errors.New("worktree: git rev-parse --show-toplevel returned an empty path")
	}
	return top, nil
}

// fetchDefault fetches origin, resolves its default branch, fetches that
// branch's refspec, and returns the remote ref and its verified commit,
// so Acquire detaches a new worktree onto a base it has just proven exists.
func (g RunnerGit) fetchDefault(ctx context.Context, dir string) (target, expected string, err error) {
	if _, err := g.required(ctx, dir, "git", "fetch", "--quiet", "origin"); err != nil {
		return "", "", fmt.Errorf("worktree: fetch origin: %w", err)
	}
	if _, err := g.required(ctx, dir, "git", "remote", "set-head", "origin", "--auto"); err != nil {
		return "", "", fmt.Errorf("worktree: resolve origin default branch: %w", err)
	}
	branch, err := g.defaultBranch(ctx, dir)
	if err != nil {
		return "", "", err
	}
	target = "origin/" + branch
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	if _, err := g.required(ctx, dir, "git", "fetch", "--quiet", "origin", refspec); err != nil {
		return "", "", fmt.Errorf("worktree: fetch %q: %w", target, err)
	}
	expectedResult, err := g.required(ctx, dir, "git", "rev-parse", "--verify", "--quiet", target+"^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("worktree: resolve %q commit: %w", target, err)
	}
	expected = strings.TrimSpace(string(expectedResult.Stdout))
	if expected == "" {
		return "", "", fmt.Errorf("worktree: resolve %q commit: empty output", target)
	}
	return target, expected, nil
}

// EnsureSeeded makes an unborn or empty primary project a real repository with
// one commit on its current branch pushed to origin, so a worktree can be
// based on refs/remotes/origin/<branch>. A freshly created empty GitHub repo
// has no commits, so there is no remote branch to detach onto. It reports true
// only when it seeded a commit. A repo that already has a commit is left
// untouched; a repo with files but no commit is refused with the fix named
// rather than silently committed.
func (g RunnerGit) EnsureSeeded(ctx context.Context, project string) (bool, error) {
	result, err := g.command(ctx, project, "git", "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return false, fmt.Errorf("worktree: inspect %q for an existing commit: %w", project, err)
	}
	if result.ExitCode == 0 && strings.TrimSpace(string(result.Stdout)) != "" {
		return false, nil
	}

	// Unborn HEAD: only a genuinely empty repository is safe to seed. A repo
	// that already carries files must not be committed over by the fleet.
	status, err := g.command(ctx, project, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, fmt.Errorf("worktree: inspect %q contents: %w", project, err)
	}
	if strings.TrimSpace(string(status.Stdout)) != "" {
		return false, fmt.Errorf("worktree: project %q has no commits and contains files; commit them (or seed an initial commit) before spawning so a worktree can be based on one", project)
	}

	if !g.hasOrigin(ctx, project) {
		return false, fmt.Errorf("worktree: project %q has no commits and no origin remote; add an origin (or commit and push) before spawning so a worktree can be based on one", project)
	}
	branch, err := g.unbornBranch(ctx, project)
	if err != nil {
		return false, err
	}

	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# "+repoName(project)+"\n"), 0o644); err != nil {
		return false, fmt.Errorf("worktree: seed %q: %w", project, err)
	}
	if _, err := g.required(ctx, project, "git", "add", "README.md"); err != nil {
		return false, fmt.Errorf("worktree: stage seeded commit in %q: %w", project, err)
	}
	if _, err := g.required(ctx, project, "git", "-c", "user.name=Code Goblins", "-c", "user.email=code-goblins@localhost", "commit", "-m", "Initial commit"); err != nil {
		return false, fmt.Errorf("worktree: seed initial commit in %q: %w", project, err)
	}
	if _, err := g.required(ctx, project, "git", "push", "-u", "origin", branch); err != nil {
		return false, fmt.Errorf("worktree: push seeded commit in %q: %w", project, err)
	}
	return true, nil
}

// hasOrigin reports whether the project lists an origin remote, so seeding can
// push before a worktree is based on origin/<branch>.
func (g RunnerGit) hasOrigin(ctx context.Context, project string) bool {
	result, err := g.command(ctx, project, "git", "remote")
	if err != nil || result.ExitCode != 0 {
		return false
	}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		if strings.TrimSpace(line) == "origin" {
			return true
		}
	}
	return false
}

// unbornBranch returns the branch HEAD points at before its first commit.
func (g RunnerGit) unbornBranch(ctx context.Context, project string) (string, error) {
	result, err := g.command(ctx, project, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("worktree: read %q current branch: %w", project, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("worktree: project %q has no commits and no current branch; create a branch, commit, and push before spawning", project)
	}
	branch := strings.TrimSpace(string(result.Stdout))
	if branch == "" || strings.ContainsAny(branch, "\r\n") {
		return "", fmt.Errorf("worktree: project %q current branch %q is malformed", project, branch)
	}
	return branch, nil
}

func repoName(project string) string {
	name := filepath.Base(filepath.Clean(project))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "repository"
	}
	return name
}

// Return removes one worktree and prunes its administrative entry, leaving no
// `git worktree list` residue. The worktree's own `git status --porcelain`
// must be empty first: ignored provisioned artifacts (node_modules, .venv,
// shared config) never show, but any uncommitted goblin work does, and Return
// refuses rather than destroy it. A refusal changes nothing at all, so the
// operator it tells to commit still has a worktree that builds and tests.
// Only once the removal is certain to run are the shared links provisioned
// into the worktree unlinked, still before any git removal: Git for Windows
// does not treat a junction as a link during recursive deletion, and removing
// a worktree that still holds one deletes the primary checkout's files
// through it. `git worktree remove --force` then runs - force because the
// ignored artifacts keep the directory non-empty, which plain remove refuses;
// it retries only the narrow transient index-lock collision and never touches
// the lock or worktree directory directly.
func (g RunnerGit) Return(ctx context.Context, project, worktree string) error {
	status, err := g.command(ctx, worktree, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("worktree: check %q for uncommitted work: %w", worktree, err)
	}
	if status.ExitCode != 0 {
		return commandFailure("git status --porcelain", status)
	}
	if dirty := strings.TrimSpace(string(status.Stdout)); dirty != "" {
		return fmt.Errorf("worktree: %q has uncommitted work; refusing to remove it:\n%s", worktree, dirty)
	}
	if err := removeSharedLinks(worktree, project); err != nil {
		return fmt.Errorf("worktree: unlink shared directories in %q: %w", worktree, err)
	}
	for attempt := 0; ; attempt++ {
		result, err := g.command(ctx, project, "git", "worktree", "remove", "--force", worktree)
		if err != nil {
			return fmt.Errorf("worktree: remove %q: %w", worktree, err)
		}
		if result.ExitCode == 0 {
			break
		}
		failure := commandFailure("git worktree remove", result)
		if !indexLockCollision.Match(combinedOutput(result)) || attempt >= g.retryCount() {
			return failure
		}
		if err := g.sleep(ctx, time.Second); err != nil {
			return fmt.Errorf("worktree: wait to retry remove: %w", err)
		}
	}
	if _, err := g.required(ctx, project, "git", "worktree", "prune"); err != nil {
		return fmt.Errorf("worktree: prune administrative entries: %w", err)
	}
	return nil
}

// removeSharedLinks deletes every root-level link inside worktree whose target
// lives inside project. Junctions created by provisioning qualify; so does any
// link an agent made pointing back into the primary checkout. os.Remove on a
// junction removes the junction itself, never the target. A worktree that is
// already gone has no links to remove. Detection goes through os.Readlink
// rather than ModeSymlink because Windows junctions are mount-point reparse
// points: Go resolves their target with Readlink but does not report them as
// symlinks in Lstat.
func removeSharedLinks(worktree, project string) error {
	entries, err := os.ReadDir(worktree)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(worktree, entry.Name())
		target, err := os.Readlink(path)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(worktree, target)
		}
		inside, err := pathWithin(project, target)
		if err != nil {
			return err
		}
		if !inside {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove shared link %q: %w", path, err)
		}
	}
	return nil
}

// pathWithin reports whether target is path itself or lives under it, after
// cleaning both. It is a pure path computation: the strings are compared
// case-insensitively because these repos live on case-insensitive Windows
// volumes.
func pathWithin(path, target string) (bool, error) {
	cleanPath := filepath.Clean(path)
	cleanTarget := filepath.Clean(target)
	rel, err := filepath.Rel(cleanPath, cleanTarget)
	if err != nil {
		return false, fmt.Errorf("relate %q to %q: %w", cleanTarget, cleanPath, err)
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

// ensureExcluded registers pattern in the clone's info/exclude, the per-clone
// ignore file shared by every worktree of the repository. It is how .worktrees
// and materialized config stay invisible to git status without editing a
// project's tracked .gitignore.
func (g RunnerGit) ensureExcluded(ctx context.Context, dir, pattern string) error {
	commonDir, err := g.gitCommonDir(ctx, dir)
	if err != nil {
		return err
	}
	infoDir := filepath.Join(commonDir, "info")
	excludePath := filepath.Join(infoDir, "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("worktree: read %q: %w", excludePath, err)
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return fmt.Errorf("worktree: create %q: %w", infoDir, err)
	}
	entry := pattern + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		entry = "\n" + entry
	}
	file, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("worktree: open %q: %w", excludePath, err)
	}
	defer file.Close()
	if _, err := file.WriteString(entry); err != nil {
		return fmt.Errorf("worktree: exclude %q in %q: %w", pattern, excludePath, err)
	}
	return nil
}

// gitCommonDir resolves the repository's shared administrative directory,
// which is where info/exclude lives for the primary checkout and every
// worktree alike.
func (g RunnerGit) gitCommonDir(ctx context.Context, dir string) (string, error) {
	result, err := g.required(ctx, dir, "git", "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("worktree: resolve Git common directory for %q: %w", dir, err)
	}
	commonDir := strings.TrimSpace(string(result.Stdout))
	if commonDir == "" {
		return "", fmt.Errorf("worktree: git rev-parse --git-common-dir returned an empty path for %q", dir)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}
	return filepath.Clean(commonDir), nil
}

func (g RunnerGit) defaultBranch(ctx context.Context, dir string) (string, error) {
	result, err := g.command(ctx, dir, "git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("worktree: read origin/HEAD: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("worktree: origin/HEAD is unavailable: %w", commandFailure("git symbolic-ref --quiet --short refs/remotes/origin/HEAD", result))
	}
	ref := strings.TrimSpace(string(result.Stdout))
	branch, ok := strings.CutPrefix(ref, "origin/")
	if !ok || branch == "" || strings.ContainsAny(branch, "\r\n") {
		return "", fmt.Errorf("worktree: origin/HEAD reference %q is malformed", ref)
	}
	return branch, nil
}

func (g RunnerGit) required(ctx context.Context, dir, name string, args ...string) (execx.Result, error) {
	result, err := g.command(ctx, dir, name, args...)
	if err != nil {
		return execx.Result{}, err
	}
	if result.ExitCode != 0 {
		return execx.Result{}, commandFailure(name+" "+strings.Join(args, " "), result)
	}
	return result, nil
}

func (g RunnerGit) command(ctx context.Context, dir, name string, args ...string) (execx.Result, error) {
	if g.Commands == nil {
		return execx.Result{}, errors.New("worktree: command runner is required")
	}
	return g.Commands.Run(ctx, execx.Request{Dir: dir, Name: name, Args: args})
}

func (g RunnerGit) retryCount() int {
	if g.Retries > 0 {
		return g.Retries
	}
	if g.Retries < 0 {
		return 0
	}
	return defaultReturnRetries
}

func (g RunnerGit) sleep(ctx context.Context, duration time.Duration) error {
	if g.Sleep != nil {
		return g.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func commandFailure(command string, result execx.Result) error {
	output := strings.TrimSpace(string(combinedOutput(result)))
	if output == "" {
		return fmt.Errorf("worktree: %s exited with code %d", command, result.ExitCode)
	}
	return fmt.Errorf("worktree: %s exited with code %d: %s", command, result.ExitCode, output)
}

func combinedOutput(result execx.Result) []byte {
	if len(result.Stdout) == 0 {
		return result.Stderr
	}
	if len(result.Stderr) == 0 {
		return result.Stdout
	}
	return append(append([]byte{}, result.Stdout...), append([]byte{'\n'}, result.Stderr...)...)
}
