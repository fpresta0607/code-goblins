package uninstall

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// allWorktrees are the home's goblin worktrees and those an older build kept
// in a checkout's own .worktrees folder.
func (m Machine) allWorktrees() ([]goblinWorktree, []string, error) {
	found, err := m.worktrees()
	if err != nil {
		return nil, nil, err
	}
	checkouts, err := m.knownCheckouts(found)
	if err != nil {
		return nil, nil, err
	}
	for _, checkout := range checkouts {
		legacy := filepath.Join(checkout, home.LegacyWorktreesDir)
		entries, err := os.ReadDir(legacy)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(legacy, entry.Name())
			if _, ok := checkoutOf(path); ok && strings.HasPrefix(entry.Name(), home.LegacyWorktreePrefix) {
				found = append(found, goblinWorktree{Path: path, ID: strings.TrimPrefix(entry.Name(), home.LegacyWorktreePrefix), Checkout: checkout})
			}
		}
	}
	return found, checkouts, nil
}

// knownCheckouts are the person's checkouts Code Goblins worked in: those of
// its worktrees and archive tags, those its tasks name, and those directly
// under the projects root.
func (m Machine) knownCheckouts(worktrees []goblinWorktree) ([]string, error) {
	var checkouts []string
	add := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if !slices.ContainsFunc(checkouts, func(known string) bool { return strings.EqualFold(known, path) }) {
			if isThere, _ := there(filepath.Join(path, ".git")); isThere {
				checkouts = append(checkouts, path)
			}
		}
	}
	for _, w := range worktrees {
		add(w.Checkout)
	}
	tags, err := state.ArchiveTags(m.Home.State)
	if err != nil {
		return nil, err
	}
	for _, tag := range tags {
		add(tag.Checkout)
	}
	if scan, err := state.ScanIDs(m.Home.State); err == nil {
		for _, id := range scan.MetaIDs {
			if meta, err := state.ReadTaskMeta(m.Home.State, id); err == nil {
				add(meta.Project)
			}
		}
	}
	if m.Install.Env != nil {
		if root, set, err := m.Install.Env.Get(install.ProjectsRootVariable); err == nil && set {
			if entries, err := os.ReadDir(root); err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						add(filepath.Join(root, entry.Name()))
					}
				}
			}
		}
	}
	return checkouts, nil
}

// checkouts are what Code Goblins left in the person's checkouts: each
// goblin worktree, with the branch the goblin made in it, the archive tags
// cleanup made, the ignore lines it marked, and the records of worktrees
// that are gone. The work each held was kept before, unless the person chose
// to remove it.
func (m Machine) checkouts(ctx context.Context, choices Choices) ([]Item, error) {
	worktrees, checkouts, err := m.allWorktrees()
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, w := range worktrees {
		u, err := m.unlandedIn(ctx, w)
		if err != nil {
			return nil, err
		}
		what := w.ID + "'s worktree"
		if choices.RemoveWork && !u.none() {
			what += ", with its work not landed, " + u.describe()
		}
		branch := ""
		if u.branch != "" && m.goblinMade(w, u.branch) {
			branch = u.branch
			what += ", and its branch " + branch
		}
		items = append(items, Item{Action: Remove, Group: GroupCheckouts, What: what, Where: w.Path, Bytes: sizeOf(w.Path), do: func(ctx context.Context) error {
			return m.removeWorktree(ctx, w, branch)
		}})
	}
	tags, err := state.ArchiveTags(m.Home.State)
	if err != nil {
		return nil, err
	}
	for _, tag := range tags {
		if _, err := m.git(ctx, tag.Checkout, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag.Tag); err != nil {
			continue
		}
		items = append(items, Item{Action: Remove, Group: GroupCheckouts, What: "the archive tag " + tag.Tag, Where: tag.Checkout, do: func(ctx context.Context) error {
			_, err := m.git(ctx, tag.Checkout, "tag", "--delete", tag.Tag)
			return err
		}})
	}
	for _, checkout := range checkouts {
		exclude := filepath.Join(checkout, ".git", "info", "exclude")
		if lines := markedLines(exclude); lines > 0 {
			items = append(items, Item{Action: Remove, Group: GroupCheckouts, What: plural(lines, "ignore line") + " it added", Where: exclude, do: func(context.Context) error {
				return unmark(exclude)
			}})
		}
		if gone, err := m.goneWorktrees(ctx, checkout); err == nil && gone > 0 {
			items = append(items, Item{Action: Remove, Group: GroupCheckouts, What: plural(gone, "record") + " of goblin worktrees that are gone", Where: checkout, do: func(ctx context.Context) error {
				_, err := m.git(ctx, checkout, "worktree", "prune")
				return err
			}})
		}
		legacy := filepath.Join(checkout, home.LegacyWorktreesDir)
		if entries, err := os.ReadDir(legacy); err == nil && len(entries) == 0 {
			items = append(items, removal(GroupCheckouts, "the empty folder an older build kept worktrees in", legacy))
		}
	}
	return items, nil
}

// removeWorktree removes a goblin worktree from its checkout, one git still
// knows or one it has forgotten, and the branch the goblin made in it.
func (m Machine) removeWorktree(ctx context.Context, w goblinWorktree, branch string) error {
	if err := (worktree.RunnerGit{Commands: m.Commands}).Remove(ctx, w.Checkout, w.Path); err != nil {
		return err
	}
	if branch == "" {
		return nil
	}
	_, err := m.git(ctx, w.Checkout, "branch", "--delete", "--force", branch)
	return err
}

// goblinMade reports whether the goblin made branch in its worktree: the
// branch's history in the checkout begins after the worktree was made. A
// branch of the person's own that a goblin worked on began before, and stays.
func (m Machine) goblinMade(w goblinWorktree, branch string) bool {
	log, err := fsx.ReadFile(filepath.Join(w.Checkout, ".git", "logs", "refs", "heads", filepath.FromSlash(branch)))
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(log), "\n")
	// A reflog line is: old new name <email> <unix time> <zone>\t<message>
	fields := strings.Fields(strings.SplitN(first, "\t", 2)[0])
	if len(fields) < 2 {
		return false
	}
	seconds, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return false
	}
	made, ok := worktreeMade(w)
	return ok && !time.Unix(seconds, 0).Before(made.Truncate(time.Second))
}

// worktreeMade is when the worktree was made: when git made its record in
// the checkout.
func worktreeMade(w goblinWorktree) (time.Time, bool) {
	data, err := fsx.ReadFile(filepath.Join(w.Path, ".git"))
	if err != nil {
		return time.Time{}, false
	}
	gitdir := filepath.Clean(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:")))
	return createdAt(filepath.Join(gitdir, "commondir"))
}

// goneWorktrees counts the worktrees a checkout records whose folders are
// gone and were the home's.
func (m Machine) goneWorktrees(ctx context.Context, checkout string) (int, error) {
	listed, err := m.git(ctx, checkout, "worktree", "list", "--porcelain")
	if err != nil {
		return 0, err
	}
	gone := 0
	for _, line := range strings.Split(listed, "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		path = filepath.FromSlash(path)
		inHome := slices.ContainsFunc(m.Home.WorktreeRoots(), func(root string) bool { return within(path, root) })
		if isThere, _ := there(path); inHome && !isThere {
			gone++
		}
	}
	return gone, nil
}

// markedLines counts the lines of an exclude file Code Goblins added, each
// under its marker.
func markedLines(path string) int {
	data, err := fsx.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(strings.ReplaceAll(string(data), "\r\n", "\n"), worktree.ExcludeMarker+"\n")
}

// unmark removes each marker Code Goblins wrote in an exclude file and the
// line under it, and leaves every other line as it is.
func unmark(path string) error {
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if lines[i] == worktree.ExcludeMarker {
			i++
			continue
		}
		kept = append(kept, lines[i])
	}
	return fsx.AtomicWriteFile(path, []byte(strings.Join(kept, "\n")))
}
