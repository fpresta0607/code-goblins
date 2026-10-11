package uninstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblinWorktree is one goblin worktree of the home: its folder, its task,
// and the person's checkout it belongs to.
type goblinWorktree struct {
	Path     string
	ID       string
	Checkout string
}

// worktrees are every goblin worktree under the home's worktree folders, on
// the Dev Drive too, by the .git file that ties each to its checkout.
func (m Machine) worktrees() ([]goblinWorktree, error) {
	var found []goblinWorktree
	for _, root := range m.Home.WorktreeRoots() {
		projects, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("uninstall: list the worktrees in %s: %w", root, err)
		}
		for _, project := range projects {
			if !project.IsDir() {
				continue
			}
			tasks, err := os.ReadDir(filepath.Join(root, project.Name()))
			if err != nil {
				return nil, fmt.Errorf("uninstall: list the worktrees in %s: %w", filepath.Join(root, project.Name()), err)
			}
			for _, task := range tasks {
				path := filepath.Join(root, project.Name(), task.Name())
				if checkout, ok := checkoutOf(path); ok {
					found = append(found, goblinWorktree{Path: path, ID: task.Name(), Checkout: checkout})
				}
			}
		}
	}
	return found, nil
}

// checkoutOf reads a worktree's .git file, gitdir: <checkout>\.git\worktrees\<name>,
// for the checkout it belongs to.
func checkoutOf(worktree string) (string, bool) {
	data, err := fsx.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return "", false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	gitdir = filepath.Clean(strings.TrimSpace(gitdir))
	worktrees := filepath.Dir(gitdir)
	if !strings.EqualFold(filepath.Base(worktrees), "worktrees") {
		return "", false
	}
	common := filepath.Dir(worktrees)
	if strings.EqualFold(filepath.Base(common), ".git") {
		return filepath.Dir(common), true
	}
	return common, true
}

// git runs git in dir and returns what it printed, or an error that says
// what failed.
func (m Machine) git(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := m.Commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("git %s exited with code %d: %s", strings.Join(args, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return string(result.Stdout), nil
}

// unlanded is a worktree's work on no remote: its files changed since its
// last commit and its commits no remote, tag or other branch holds.
type unlanded struct {
	branch  string
	changed []string
	commits int
	// notOwn are the arguments that pick the commits, after HEAD.
	notOwn []string
}

func (u unlanded) none() bool { return len(u.changed) == 0 && u.commits == 0 }

// describe says what the work is, such as 1 commit not pushed, 2 changed
// files.
func (u unlanded) describe() string {
	var parts []string
	if u.commits > 0 {
		parts = append(parts, plural(u.commits, "commit")+" not pushed")
	}
	if len(u.changed) > 0 {
		parts = append(parts, plural(len(u.changed), "changed file"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// unlandedIn reads the work of worktree w that is on no remote.
func (m Machine) unlandedIn(ctx context.Context, w goblinWorktree) (unlanded, error) {
	var u unlanded
	branch, err := m.Commands.Run(ctx, execx.Request{Dir: w.Path, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil {
		return u, err
	}
	if branch.ExitCode == 0 {
		u.branch = strings.TrimSpace(string(branch.Stdout))
	}
	status, err := m.git(ctx, w.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return u, err
	}
	fields := strings.Split(status, "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		u.changed = append(u.changed, filepath.FromSlash(entry[3:]))
		// A rename or copy names its source after it.
		if entry[0] == 'R' || entry[0] == 'C' {
			i++
		}
	}
	u.notOwn = []string{"--not", "--remotes", "--tags"}
	if u.branch != "" {
		u.notOwn = append(u.notOwn, "--exclude=refs/heads/"+u.branch)
	}
	u.notOwn = append(u.notOwn, "--branches")
	count, err := m.git(ctx, w.Path, append([]string{"rev-list", "--count", "HEAD"}, u.notOwn...)...)
	if err != nil {
		return u, err
	}
	if u.commits, err = strconv.Atoi(strings.TrimSpace(count)); err != nil {
		return u, fmt.Errorf("git rev-list --count printed %q", strings.TrimSpace(count))
	}
	return u, nil
}

// work is each goblin's work that has not landed, which the uninstall keeps
// in the kept folder unless the person chose to remove it, and the work the
// home's archive tags hold.
func (m Machine) work(ctx context.Context, kept string, choices Choices) ([]Item, error) {
	if choices.RemoveWork {
		return nil, nil
	}
	worktrees, err := m.worktrees()
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, w := range worktrees {
		u, err := m.unlandedIn(ctx, w)
		if err != nil {
			return nil, fmt.Errorf("uninstall: read what %s has not landed: %w", w.Path, err)
		}
		if u.none() {
			continue
		}
		into := filepath.Join(kept, "work", filepath.Base(w.Checkout), w.ID)
		items = append(items, Item{Action: Keep, Group: GroupWork, What: w.ID + "'s work, " + u.describe(), Where: w.Path, do: func(ctx context.Context) error {
			return m.keepWork(ctx, w, u, into)
		}})
	}
	tags, err := state.ArchiveTags(m.Home.State)
	if err != nil {
		return nil, err
	}
	for _, tag := range tags {
		commits, err := m.git(ctx, tag.Checkout, "rev-list", "--count", "refs/tags/"+tag.Tag, "--not", "--remotes", "--branches")
		if err != nil {
			// A tag that is gone, or a checkout that is, holds nothing to
			// keep any more.
			continue
		}
		if n, _ := strconv.Atoi(strings.TrimSpace(commits)); n > 0 {
			into := filepath.Join(kept, "work", filepath.Base(tag.Checkout), strings.ReplaceAll(tag.Tag, "/", "-")+".bundle")
			items = append(items, Item{Action: Keep, Group: GroupWork, What: "an archived goblin branch, " + tag.Tag + ", " + plural(n, "commit") + " not pushed", Where: tag.Checkout, do: func(ctx context.Context) error {
				return m.bundle(ctx, tag.Checkout, into, "refs/tags/"+tag.Tag, "--not", "--remotes", "--branches")
			}})
		}
	}
	return items, nil
}

// keepWork copies a worktree's unlanded work into the folder into: each
// changed file as it is, the changes to its tracked files as a patch, its
// commits as a git bundle, and a note on how to bring them back. Each is
// read back before the worktree may go.
func (m Machine) keepWork(ctx context.Context, w goblinWorktree, u unlanded, into string) error {
	if err := os.MkdirAll(into, 0o755); err != nil {
		return err
	}
	for _, changed := range u.changed {
		source := filepath.Join(w.Path, changed)
		info, err := os.Lstat(source)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			continue
		}
		if err := copyFile(source, filepath.Join(into, "files", changed)); err != nil {
			return err
		}
	}
	if len(u.changed) > 0 {
		patch, err := m.git(ctx, w.Path, "diff", "--binary", "HEAD")
		if err != nil {
			return err
		}
		if patch != "" {
			if err := writeChecked(filepath.Join(into, "changes.patch"), []byte(patch)); err != nil {
				return err
			}
		}
	}
	name := u.branch
	if name == "" {
		name = "goblin/" + w.ID
	}
	if u.commits > 0 {
		if err := m.bundle(ctx, w.Path, filepath.Join(into, "commits.bundle"), append([]string{"HEAD"}, u.notOwn...)...); err != nil {
			return err
		}
	}
	return writeChecked(filepath.Join(into, "README.txt"), []byte(restoreNote(w, u, name, time.Now())))
}

// restoreNote says where the work was and how to bring it back.
func restoreNote(w goblinWorktree, u unlanded, branch string, at time.Time) string {
	note := fmt.Sprintf("Work the goblin %s had not landed, which the Code Goblins uninstall kept on %s.\r\n", w.ID, at.Format("2 January 2006"))
	note += fmt.Sprintf("It was %s, in %s, a worktree of your checkout %s.\r\n\r\n", u.describe(), w.Path, w.Checkout)
	if u.commits > 0 {
		note += "To bring its commits back as the branch " + branch + ":\r\n"
		note += fmt.Sprintf("  git -C \"%s\" fetch \"%s\" HEAD:%s\r\n\r\n", w.Checkout, "commits.bundle", branch)
	}
	if len(u.changed) > 0 {
		note += "The files it had changed and not committed are in the files folder, as they were.\r\n"
		note += "changes.patch holds the same changes to its tracked files, for git apply.\r\n"
	}
	return note
}

// bundle writes the commits args pick in checkout into a git bundle at path,
// and reads it back with git bundle verify.
func (m Machine) bundle(ctx context.Context, checkout, path string, args ...string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := m.git(ctx, checkout, append([]string{"bundle", "create", path}, args...)...); err != nil {
		return err
	}
	if _, err := m.git(ctx, checkout, "bundle", "verify", "--quiet", path); err != nil {
		return fmt.Errorf("the bundle %s did not verify: %w", path, err)
	}
	return nil
}

// fleetData are the person's own fleet data in the home, its data and config
// folders, which the uninstall keeps unless the person chose to remove them.
func (m Machine) fleetData(kept string, choices Choices) ([]Item, error) {
	if choices.RemoveWork || m.Checkout {
		return nil, nil
	}
	var items []Item
	for _, folder := range []struct{ what, name string }{
		{"your fleet data: briefs, backlog, memory and reports", "data"},
		{"your fleet's settings", "config"},
	} {
		source := filepath.Join(m.Home.Root, folder.name)
		entries, err := os.ReadDir(source)
		if errors.Is(err, fs.ErrNotExist) || err == nil && len(entries) == 0 {
			continue
		}
		if err != nil {
			return nil, err
		}
		into := filepath.Join(kept, "fleet", folder.name)
		items = append(items, Item{Action: Keep, Group: GroupFleetData, What: folder.what, Where: source, Bytes: sizeOf(source), do: func(context.Context) error {
			return copyTree(source, into)
		}})
	}
	return items, nil
}

// copyTree copies every file under source to the same place under target,
// each read back, and links as nothing.
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(target, relative), 0o755)
		}
		return copyFile(path, filepath.Join(target, relative))
	})
}

// copyFile copies source to target and reads the copy back.
func copyFile(source, target string) error {
	data, err := fsx.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return writeChecked(target, data)
}

// writeChecked writes data to path, never over a file there, and reads it
// back.
func writeChecked(path string, data []byte) error {
	file, err := fsx.CreateNew(path, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	written, err := fsx.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(written, data) {
		return fmt.Errorf("%s did not read back as written", path)
	}
	return nil
}
