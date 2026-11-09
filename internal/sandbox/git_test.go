package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepository is a repository of its own in a temporary folder, with a
// file to commit, whose git reads no configuration of this machine's.
func gitRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, nil, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runGit runs git in dir with env added to an environment that reads no
// global or system configuration, and fails the test when git fails.
func runGit(t *testing.T, dir string, env map[string]string, args ...string) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", append([]string{"-c", "user.name=goblin", "-c", "user.email=goblin@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1")
	for name, value := range env {
		command.Env = append(command.Env, name+"="+value)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

// hostGit is the environment a sandbox task's host git runs with, its hooks
// read from an empty folder of its own.
func hostGit(t *testing.T) map[string]string {
	t.Helper()
	return HostGitEnvironment(t.TempDir())
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Each escape is planted as the container would plant it, through files of
// the repository or of a repository it made inside it, and is proven to run
// without the host environment before it is proven not to run with it.
func TestHostGitRunsNothingTheRepositoryPlanted(t *testing.T) {
	for _, escape := range []struct {
		name  string
		plant func(t *testing.T, dir, marker string)
		run   []string
	}{
		{"a hook", func(t *testing.T, dir, marker string) {
			hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
			if err := os.WriteFile(hook, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, nil, "add", "a.txt")
		}, []string{"commit", "-q", "-m", "x"}},
		{"an fsmonitor", func(t *testing.T, dir, marker string) {
			runGit(t, dir, nil, "add", "a.txt")
			runGit(t, dir, nil, "commit", "-q", "-m", "x")
			runGit(t, dir, nil, "config", "core.fsmonitor", "echo ran > '"+filepath.ToSlash(marker)+"'; false")
		}, []string{"status", "--porcelain"}},
		{"a filter its .gitattributes names", func(t *testing.T, dir, marker string) {
			runGit(t, dir, nil, "config", "filter.planted.clean", "echo ran > '"+filepath.ToSlash(marker)+"'; cat")
			if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=planted\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, []string{"add", "a.txt"}},
		{"a filter a submodule's own repository names", func(t *testing.T, dir, marker string) {
			sub := filepath.Join(dir, "sub")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, sub, nil, "init", "-q")
			if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("a\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, sub, nil, "add", "a.txt")
			runGit(t, sub, nil, "commit", "-q", "-m", "x")
			runGit(t, dir, nil, "add", "a.txt", "sub")
			runGit(t, dir, nil, "commit", "-q", "-m", "x")
			runGit(t, sub, nil, "config", "filter.planted.clean", "echo ran > '"+filepath.ToSlash(marker)+"'; cat")
			if err := os.MkdirAll(filepath.Join(sub, ".git", "info"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, ".git", "info", "attributes"), []byte("* filter=planted\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("b\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, []string{"status", "--porcelain"}},
	} {
		t.Run(escape.name, func(t *testing.T) {
			// Arrange: the premise, that the planted escape runs at all.
			plain := gitRepository(t)
			plainMarker := filepath.Join(t.TempDir(), "ran")
			escape.plant(t, plain, plainMarker)
			runGit(t, plain, nil, escape.run...)
			if !exists(plainMarker) {
				t.Fatalf("the planted %s did not run under plain git, so this test proves nothing", escape.name)
			}
			guarded := gitRepository(t)
			marker := filepath.Join(t.TempDir(), "ran")
			escape.plant(t, guarded, marker)

			// Act
			runGit(t, guarded, hostGit(t), escape.run...)

			// Assert
			if exists(marker) {
				t.Fatalf("the planted %s ran under a sandbox task's host git", escape.name)
			}
		})
	}
}

// worktreeWithLink is a folder with a .git link as git writes it for a
// worktree, and the link's text.
func worktreeWithLink(t *testing.T) (string, string) {
	t.Helper()
	worktree := t.TempDir()
	link := "gitdir: " + filepath.ToSlash(filepath.Join(t.TempDir(), "repo", ".git", "worktrees", "task"))
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(link+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return worktree, link
}

func TestCheckGitLinkRefusesARepositoryTheContainerMade(t *testing.T) {
	for _, escape := range []struct {
		name    string
		replace func(t *testing.T, worktree string) (cwd string)
		want    string
	}{
		{"a .git link that names another git folder", func(t *testing.T, worktree string) string {
			if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /work/evil\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return worktree
		}, "the link its spawn recorded"},
		{"a .git folder in place of the link", func(t *testing.T, worktree string) string {
			if err := os.Remove(filepath.Join(worktree, ".git")); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(worktree, ".git", "hooks"), 0o755); err != nil {
				t.Fatal(err)
			}
			return worktree
		}, "is a folder"},
		{"a .git of its own under the folder the command runs in", func(t *testing.T, worktree string) string {
			nested := filepath.Join(worktree, "src", "pkg")
			if err := os.MkdirAll(filepath.Join(worktree, "src", ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			return nested
		}, "holds a .git of its own"},
		{"a command outside the worktree", func(t *testing.T, worktree string) string {
			return t.TempDir()
		}, "outside its worktree"},
	} {
		t.Run(escape.name, func(t *testing.T) {
			// Arrange
			worktree, link := worktreeWithLink(t)
			cwd := escape.replace(t, worktree)

			// Act
			err := CheckGitLink(worktree, cwd, link)

			// Assert
			if err == nil || !strings.Contains(err.Error(), escape.want) {
				t.Fatalf("CheckGitLink = %v, want a refusal saying %q", err, escape.want)
			}
		})
	}
}

func TestCheckGitLinkLetsTheRecordedLinkBe(t *testing.T) {
	// Arrange
	worktree, link := worktreeWithLink(t)
	sub := filepath.Join(worktree, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	errs := []error{CheckGitLink(worktree, worktree, link), CheckGitLink(worktree, sub, link), CheckGitLink(worktree, "", link)}

	// Assert
	for _, err := range errs {
		if err != nil {
			t.Fatalf("CheckGitLink = %v, want the recorded link let through", err)
		}
	}
}
