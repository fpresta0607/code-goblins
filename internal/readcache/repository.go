package readcache

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// repository is where git keeps a worktree's state: its own git folder,
// which holds its HEAD, index and reflog, and the common folder its
// repository's refs, packed refs, config and worktrees live in.
type repository struct {
	worktree, gitDir, commonDir string
}

// openRepository finds the git folders of worktree, the top of a worktree,
// from its .git folder or the folder its .git file names and the common
// folder that one names, and adds what it read to s. It refuses any other
// layout, such as a folder inside a worktree, a bare repository or one that
// keeps its refs in a reftable, whose reads run git as before.
func openRepository(s *stamper, worktree string) (repository, bool) {
	if worktree == "" || !filepath.IsAbs(worktree) {
		return repository{}, false
	}
	dotGit := filepath.Join(worktree, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return repository{}, false
	}
	repo := repository{worktree: worktree, gitDir: dotGit}
	switch {
	case info.IsDir():
		s.text(dotGit, "/")
	case info.Mode().IsRegular():
		link, isLink := strings.CutPrefix(strings.TrimSpace(s.content(dotGit)), "gitdir: ")
		if !isLink || link == "" {
			return repository{}, false
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(worktree, link)
		}
		repo.gitDir = filepath.Clean(link)
	default:
		return repository{}, false
	}
	repo.commonDir = repo.gitDir
	if common := strings.TrimSpace(s.content(filepath.Join(repo.gitDir, "commondir"))); common != "" {
		if !filepath.IsAbs(common) {
			common = filepath.Join(repo.gitDir, common)
		}
		repo.commonDir = filepath.Clean(common)
	}
	if _, err := os.Stat(filepath.Join(repo.commonDir, "reftable")); !errors.Is(err, fs.ErrNotExist) {
		return repository{}, false
	}
	return repo, true
}

// config adds every configuration file git reads for repo, its own and then
// the user's and the machine's that files names, and says whether one names
// an excludes file. A file that includes another unsettles the sum, since
// the included one is not read here.
func (s *stamper) config(repo repository, files []string) (namesExcludes bool) {
	own := []string{filepath.Join(repo.commonDir, "config")}
	if repo.gitDir != repo.commonDir {
		own = append(own, filepath.Join(repo.gitDir, "config.worktree"))
	}
	for _, name := range append(own, files...) {
		text := strings.ToLower(s.content(name))
		if strings.Contains(text, "[include") {
			s.unsettle()
		}
		namesExcludes = namesExcludes || strings.Contains(text, "excludesfile")
	}
	return namesExcludes
}

// head adds repo's HEAD and, when it names a branch, the branch's loose ref
// and the packed refs that hold it otherwise. A branch whose loose ref names
// another ref unsettles the sum.
func (s *stamper) head(repo repository) {
	target, isBranch := strings.CutPrefix(strings.TrimSpace(s.content(filepath.Join(repo.gitDir, "HEAD"))), "ref: ")
	if !isBranch {
		return
	}
	if !strings.HasPrefix(target, "refs/") || strings.Contains(target, "..") {
		s.unsettle()
		return
	}
	for _, folder := range []string{repo.gitDir, repo.commonDir} {
		if strings.HasPrefix(s.content(filepath.Join(folder, filepath.FromSlash(target))), "ref:") {
			s.unsettle()
		}
	}
	s.file(filepath.Join(repo.commonDir, "packed-refs"))
}

// refs adds HEAD and every ref of repo: the packed refs and each loose ref
// file.
func (s *stamper) refs(repo repository) {
	s.head(repo)
	s.file(filepath.Join(repo.commonDir, "packed-refs"))
	s.tree(filepath.Join(repo.commonDir, "refs"), nil, maxEntries)
}

// worktrees adds what git worktree list reads besides the refs: the main
// worktree's HEAD and each linked worktree's record, and whether the .git
// file each record names is still there, which a removed worktree's is not.
func (s *stamper) worktrees(repo repository) {
	s.refs(repo)
	s.content(filepath.Join(repo.commonDir, "HEAD"))
	folder := filepath.Join(repo.commonDir, "worktrees")
	listed, err := os.ReadDir(folder)
	if errors.Is(err, fs.ErrNotExist) {
		s.text(folder, "-")
		return
	}
	if err != nil {
		s.unsettle()
		return
	}
	for _, entry := range listed {
		record := filepath.Join(folder, entry.Name())
		s.text(entry.Name())
		link := strings.TrimSpace(s.content(filepath.Join(record, "gitdir")))
		s.content(filepath.Join(record, "HEAD"))
		s.content(filepath.Join(record, "locked"))
		if link != "" {
			s.file(link)
		}
	}
}

// gitConfigFiles are the configuration files git reads besides a
// repository's own, as this process's environment places them: the user's
// and the machine's, the machine's beside the git on PATH. A GIT_CONFIG
// variable that moves them leaves every read to git (hasGitVariables).
func gitConfigFiles() []string {
	// Git for Windows takes HOME, else HOMEDRIVE and HOMEPATH where that
	// folder exists, else USERPROFILE, so a home it may take is read.
	homes := []string{os.Getenv("HOME")}
	if homes[0] == "" {
		homes = []string{os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH"), os.Getenv("USERPROFILE")}
	}
	var files []string
	for _, home := range homes {
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		files = append(files, filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config"))
	}
	if data := os.Getenv("PROGRAMDATA"); data != "" {
		files = append(files, filepath.Join(data, "Git", "config"))
	}
	// Git for Windows reads etc\gitconfig under its own folder, which holds
	// cmd\git.exe and mingw64\bin\git.exe.
	if git, err := exec.LookPath("git"); err == nil {
		for folder, level := filepath.Dir(git), 0; level < 3; folder, level = filepath.Dir(folder), level+1 {
			files = append(files, filepath.Join(folder, "etc", "gitconfig"))
		}
	}
	return files
}
