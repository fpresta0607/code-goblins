package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// These run real git, because the whole check is a reading of what git prints
// and a scripted runner would only agree with the test that scripted it.
func noticeRepo(t *testing.T, gitignore string) string {
	t.Helper()
	project := t.TempDir()
	if out, err := exec.Command("git", "-C", project, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// What Acquire registers before any worktree exists, so every case here
	// is one where git itself already ignores the directory.
	exclude := filepath.Join(project, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte(".worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if gitignore != "" {
		if err := os.WriteFile(filepath.Join(project, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return project
}

func TestGitignoreNoticeNamesTheFileAndTheLineToAdd(t *testing.T) {
	for label, gitignore := range map[string]string{
		"no .gitignore":        "",
		"unrelated .gitignore": "node_modules/\n*.log\n",
	} {
		project := noticeRepo(t, gitignore)
		notice := Service{Commands: execx.OSRunner{}}.GitignoreNotice(context.Background(), project)
		for _, want := range []string{filepath.Join(project, ".gitignore"), "`.worktrees/`"} {
			if !strings.Contains(notice, want) {
				t.Errorf("%s: notice %q does not name %q", label, notice, want)
			}
		}
		if strings.Contains(notice, "\n") {
			t.Errorf("%s: the notice is more than one line: %q", label, notice)
		}
		if _, err := os.Stat(filepath.Join(project, ".gitignore")); gitignore == "" && err == nil {
			t.Errorf("%s: the notice created a .gitignore in the operator's repository", label)
		}
	}
}

func TestGitignoreNoticeIsSilentWhenTheProjectCoversIt(t *testing.T) {
	for _, gitignore := range []string{".worktrees/\n", "/.worktrees\n", "node_modules/\r\n.worktrees/\r\n", ".*\n"} {
		project := noticeRepo(t, gitignore)
		if notice := (Service{Commands: execx.OSRunner{}}).GitignoreNotice(context.Background(), project); notice != "" {
			t.Errorf(".gitignore %q: unexpected notice %q", gitignore, notice)
		}
	}
}

func TestGitignoreNoticeIsOnlyForTheFirstWorktree(t *testing.T) {
	project := noticeRepo(t, "")
	if err := os.MkdirAll(filepath.Join(project, ".worktrees", "gb-earlier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if notice := (Service{Commands: execx.OSRunner{}}).GitignoreNotice(context.Background(), project); notice != "" {
		t.Errorf("a checkout that already holds a worktree was warned again: %q", notice)
	}
}

func TestGitignoreNoticeSaysNothingAboutADirectoryGitCannotAnswerFor(t *testing.T) {
	if notice := (Service{Commands: execx.OSRunner{}}).GitignoreNotice(context.Background(), t.TempDir()); notice != "" {
		t.Errorf("a directory that is not a repository was warned: %q", notice)
	}
}
