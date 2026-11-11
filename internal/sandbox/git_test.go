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
	if output, err := gitCommand(t, dir, env, args...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

// gitCommand is git with args in dir, as runGit runs it.
func gitCommand(t *testing.T, dir string, env map[string]string, args ...string) *exec.Cmd {
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
	return command
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
		{"a filter a submodule's own repository names", plantSubmodule, []string{"status", "--porcelain"}},
		{"a filter a submodule's own repository names, which .gitmodules never ignores", func(t *testing.T, dir, marker string) {
			plantSubmodule(t, dir, marker)
			if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte("[submodule \"sub\"]\n\tpath = sub\n\turl = ./sub\n\tignore = none\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, []string{"status", "--porcelain"}},
		{"a filter a submodule's own repository names, which a checkout brings in after the check", plantSubmoduleBranch, []string{"checkout", "planted"}},
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

			// Act: as a sandbox task's host runs it, not at all when its
			// submodule check refuses the repository.
			if submoduleRefusal(guarded, filepath.Join(guarded, ".git")) == nil {
				runGit(t, guarded, hostGit(t), escape.run...)
			}

			// Assert
			if exists(marker) {
				t.Fatalf("the planted %s ran under a sandbox task's host git", escape.name)
			}
		})
	}
}

// plantSubmodule makes sub in dir a repository of its own, committed to
// dir as a submodule, whose own configuration and attributes run a filter
// that writes marker on its changed file.
func plantSubmodule(t *testing.T, dir, marker string) {
	t.Helper()
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
}

// plantSubmoduleBranch makes sub in dir a repository of its own, as
// plantSubmodule does, but commits it as a submodule only on the branch
// planted, leaving dir on its first branch, whose index names no submodule:
// a checkout of planted brings the submodule in after any check of the
// index before it.
func plantSubmoduleBranch(t *testing.T, dir, marker string) {
	t.Helper()
	runGit(t, dir, nil, "add", "a.txt")
	runGit(t, dir, nil, "commit", "-q", "-m", "x")
	runGit(t, dir, nil, "switch", "-q", "-c", "planted")
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
	runGit(t, dir, nil, "add", "sub")
	runGit(t, dir, nil, "commit", "-q", "-m", "x")
	runGit(t, dir, nil, "switch", "-q", "-")
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
}

// A folder the container made a bare repository of is one plain git takes
// for a repository from a folder in it, reading its configuration, and one
// a sandbox task's host git does not.
func TestHostGitTakesNoFolderItFindsForABareRepository(t *testing.T) {
	// Arrange: the premise, that plain git takes it for one.
	worktree := gitRepository(t)
	bare := filepath.Join(worktree, "vendor", "x")
	runGit(t, worktree, nil, "init", "-q", "--bare", bare)
	runGit(t, bare, nil, "rev-parse", "--git-dir")

	// Act
	output, err := gitCommand(t, bare, hostGit(t), "rev-parse", "--git-dir").CombinedOutput()

	// Assert
	if err == nil {
		t.Fatalf("host git took %s for a repository: %s", bare, output)
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

// linkedWorktree is a worktree git added to a repository of its own, as a
// task's spawn adds one, and its .git link.
func linkedWorktree(t *testing.T) (string, string) {
	t.Helper()
	repository := gitRepository(t)
	runGit(t, repository, nil, "add", "a.txt")
	runGit(t, repository, nil, "commit", "-q", "-m", "x")
	worktree := filepath.Join(t.TempDir(), "task")
	runGit(t, repository, nil, "worktree", "add", "-q", worktree)
	link, err := ReadGitLink(worktree)
	if err != nil {
		t.Fatal(err)
	}
	return worktree, link
}

// A worktree whose index git wrote with a gitlink, in each index version git
// writes, or that holds a .gitmodules, is refused, naming the submodule; one
// with neither is let through.
func TestCheckGitLinkRefusesASubmoduleOfTheWorktree(t *testing.T) {
	const commit = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	for _, test := range []struct {
		name  string
		plant func(t *testing.T, worktree string)
		want  string
	}{
		{"a .gitmodules", func(t *testing.T, worktree string) {
			if err := os.WriteFile(filepath.Join(worktree, ".gitmodules"), []byte("[submodule \"sub\"]\n\tpath = sub\n\tignore = none\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, ".gitmodules"},
		{"a gitlink in an index of version 2", func(t *testing.T, worktree string) {
			runGit(t, worktree, nil, "update-index", "--add", "--cacheinfo", "160000,"+commit+",vendor/x")
		}, "names vendor/x as a submodule"},
		{"a gitlink in an index of version 3", func(t *testing.T, worktree string) {
			if err := os.WriteFile(filepath.Join(worktree, "new.txt"), []byte("n\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, worktree, nil, "add", "-N", "new.txt")
			runGit(t, worktree, nil, "update-index", "--add", "--cacheinfo", "160000,"+commit+",vendor/x")
		}, "names vendor/x as a submodule"},
		{"a gitlink in an index of version 4", func(t *testing.T, worktree string) {
			runGit(t, worktree, nil, "update-index", "--index-version", "4")
			runGit(t, worktree, nil, "update-index", "--add", "--cacheinfo", "100644,"+commit+",vendor/a.txt")
			runGit(t, worktree, nil, "update-index", "--add", "--cacheinfo", "160000,"+commit+",vendor/x")
		}, "names vendor/x as a submodule"},
		{"files alone in an index of version 4", func(t *testing.T, worktree string) {
			runGit(t, worktree, nil, "update-index", "--index-version", "4")
			runGit(t, worktree, nil, "update-index", "--add", "--cacheinfo", "100644,"+commit+",vendor/a.txt")
		}, ""},
		{"files alone", func(t *testing.T, worktree string) {}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktree, link := linkedWorktree(t)
			test.plant(t, worktree)

			// Act
			err := CheckGitLink(worktree, worktree, link)

			// Assert
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "cannot use submodules")) {
				t.Fatalf("CheckGitLink = %v, want %q", err, test.want)
			}
		})
	}
}

// A .git the container made anywhere in the worktree, a folder or a link
// file, in any case, is refused naming it and how to remove it; the
// worktree's own link and a name that only starts with .git are let be.
func TestNestedGitRefusalFindsARepositoryAnywhereInTheWorktree(t *testing.T) {
	for _, test := range []struct {
		name, plant string
		isFile      bool
		want        string
	}{
		{"a repository deep in the packages", "node_modules/pkg/.git", false, "goblins sandbox run -- rm -rf node_modules/pkg/.git"},
		{"a submodule's link file", "vendor/x/.git", true, "goblins sandbox run -- rm -rf vendor/x/.git"},
		{"a repository in another case", "src/.GIT", false, "goblins sandbox run -- rm -rf src/.GIT"},
		{"a folder whose name starts with .git", ".github/workflows", false, ""},
		{"the worktree's own link alone", "", false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktree, _ := worktreeWithLink(t)
			if test.plant != "" {
				path := filepath.Join(worktree, filepath.FromSlash(test.plant))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if test.isFile {
					if err := os.WriteFile(path, []byte("gitdir: /work/evil\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				} else if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			err := NestedGitRefusal(worktree)

			// Assert
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), filepath.Join(worktree, filepath.FromSlash(test.plant)))) {
				t.Fatalf("NestedGitRefusal = %v, want %q", err, test.want)
			}
		})
	}
}
