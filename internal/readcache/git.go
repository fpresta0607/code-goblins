package readcache

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

var (
	fullCommit = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	// refName is a branch or ref named in full or short, never a commit,
	// which a name all of hex digits could be read as.
	refName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._/-]*$`)
	hexName = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// StatusArgs is the git status the cache answers: --ignored=matching names
// each folder git ignores, which the next read's walk of the worktree
// passes over, and -z keeps every name as git has it.
func StatusArgs() []string {
	return []string{"--porcelain", "--untracked-files=normal", "--ignored=matching", "-z"}
}

// gitRead plans one of the git reads the board repeats, each with the files
// git reads to answer it: HEAD for the branch checked out, HEAD and its
// branch's ref for the commit, every ref for a ref listing or a name git
// looks up among them, the reflog, the worktrees' records, and the worktree
// itself for its status. Every one depends on the configuration files too.
// What compares commits named in full depends on nothing else, since a
// commit never changes. Any other git command is no read the cache knows.
func gitRead(req execx.Request) (read, bool) {
	if hasGitVariables(req.Env) {
		return read{}, false
	}
	args := req.Args
	for len(args) > 0 && (args[0] == "--no-pager" || args[0] == "-c" && len(args) > 1) {
		if args[0] == "-c" {
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return read{}, false
	}
	command, rest := args[0], args[1:]
	flags, names := splitFlags(rest)
	r := read{key: "git\x00" + req.Dir + "\x00" + strings.Join(req.Args, "\x00"), answers: []int{0}, isSilent: true}
	var stamp func(s *stamper, repo repository, previous *execx.Result)
	isAnswerOne := false
	switch {
	case command == "rev-parse" && len(names) == 1 && onlyFlags(flags, "--verify", "--quiet", "-q"):
		switch {
		case names[0] == "HEAD":
			stamp = commit
		case isRefName(names[0]):
			stamp = everyRef
		}
		isAnswerOne = slices.Contains(flags, "--verify") && (slices.Contains(flags, "--quiet") || slices.Contains(flags, "-q"))
	case command == "for-each-ref" && len(rest) > 0 && !slices.ContainsFunc(rest, func(arg string) bool {
		return !strings.HasPrefix(arg, "--format=") && !strings.HasPrefix(arg, "refs/")
	}):
		stamp = everyRef
	case command == "branch" && slices.Equal(rest, []string{"--show-current"}):
		stamp = branch
	case command == "symbolic-ref" && len(names) == 1 && onlyFlags(flags, "--quiet", "-q", "--short"):
		// A short name is the shortest no other ref makes ambiguous.
		stamp = everyRef
		if names[0] == "HEAD" && !slices.Contains(flags, "--short") {
			stamp = branch
		}
		isAnswerOne = true
	case command == "config" && len(rest) == 2 && rest[0] == "--get":
		stamp = nothing
		isAnswerOne = true
	case command == "reflog" && len(rest) == 1 && strings.HasPrefix(rest[0], "--format="):
		stamp = reflog
	case command == "worktree" && slices.Equal(rest, []string{"list", "--porcelain"}):
		stamp = func(s *stamper, repo repository, _ *execx.Result) { s.worktrees(repo) }
	case command == "merge-base" && len(rest) == 3 && rest[0] == "--is-ancestor" && fullCommit.MatchString(rest[1]):
		switch {
		case fullCommit.MatchString(rest[2]):
			stamp = nothing
		case rest[2] == "HEAD":
			stamp = commit
		}
		isAnswerOne = true
	case command == "merge-base" && len(rest) == 2 && fullCommit.MatchString(rest[0]) && fullCommit.MatchString(rest[1]):
		stamp = nothing
	case command == "merge-base" && len(rest) == 2 && rest[0] == "HEAD" && isRefName(rest[1]):
		stamp = everyRef
	case command == "diff" && len(rest) == 5 && slices.Equal(rest[:3], []string{"--name-only", "--no-renames", "-z"}) && fullCommit.MatchString(rest[3]) && fullCommit.MatchString(rest[4]):
		stamp = nothing
	case command == "status" && slices.Equal(rest, StatusArgs()):
		stamp = status
		// A status may run the repository's file system monitor hook, which
		// would open a console window of its own without one.
		r.isSilent = false
	}
	if stamp == nil {
		return read{}, false
	}
	if isAnswerOne {
		r.answers = append(r.answers, 1)
	}
	r.stamp = func(s *stamper, config []string, previous *execx.Result) {
		repo, ok := openRepository(s, req.Dir)
		if !ok {
			s.unsettle()
			return
		}
		if s.config(repo, config) {
			// A status reads an excludes file the configuration names,
			// which is not read here.
			if command == "status" {
				s.unsettle()
			}
		}
		stamp(s, repo, previous)
	}
	return r, true
}

func nothing(*stamper, repository, *execx.Result) {}

func branch(s *stamper, repo repository, _ *execx.Result) {
	s.content(filepath.Join(repo.gitDir, "HEAD"))
}

func commit(s *stamper, repo repository, _ *execx.Result) { s.head(repo) }

func everyRef(s *stamper, repo repository, _ *execx.Result) { s.refs(repo) }

func reflog(s *stamper, repo repository, _ *execx.Result) {
	s.head(repo)
	s.file(filepath.Join(repo.gitDir, "logs", "HEAD"))
}

// status adds what git status reads: HEAD and its branch, the index, the
// ignore rules outside the worktree, and every entry of the worktree but
// those the last status named ignored, whose content no status shows. A
// first status, which knows nothing of what git ignores, is never kept: an
// ignored folder such as node_modules can hold a hundred thousand files. A
// worktree with submodules is never kept either, since a submodule's state
// lies in the repository's own folder.
func status(s *stamper, repo repository, previous *execx.Result) {
	if previous == nil {
		s.unsettle()
		return
	}
	if _, err := os.Lstat(filepath.Join(repo.worktree, ".gitmodules")); !errors.Is(err, fs.ErrNotExist) {
		s.unsettle()
		return
	}
	s.head(repo)
	s.file(filepath.Join(repo.gitDir, "index"))
	s.content(filepath.Join(repo.commonDir, "info", "exclude"))
	for _, name := range globalExcludes() {
		s.content(name)
	}
	ignored := ignoredPaths(previous.Stdout)
	s.tree(repo.worktree, func(relative string) bool {
		return strings.EqualFold(path.Base(relative), ".git") || ignored[strings.ToLower(relative)]
	}, maxEntries)
}

// ignoredPaths are the paths a status with -z and --ignored=matching names
// ignored, folded to lower case as Windows compares names, a folder without
// its trailing slash.
func ignoredPaths(porcelain []byte) map[string]bool {
	ignored := map[string]bool{}
	entries := strings.Split(string(porcelain), "\x00")
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		code, name := entry[:2], entry[3:]
		if code == "!!" {
			ignored[strings.ToLower(strings.TrimSuffix(name, "/"))] = true
		}
		// A rename or a copy names its source in the entry after it.
		if strings.ContainsAny(code, "RC") {
			index++
		}
	}
	return ignored
}

// globalExcludes are where git looks for the user's ignore rules when the
// configuration names no file of its own.
func globalExcludes() []string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return []string{filepath.Join(xdg, "git", "ignore")}
	}
	var files []string
	for _, home := range []string{os.Getenv("HOME"), os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH"), os.Getenv("USERPROFILE")} {
		if home != "" {
			files = append(files, filepath.Join(home, ".config", "git", "ignore"))
		}
	}
	return files
}

func isRefName(name string) bool {
	return refName.MatchString(name) && !hexName.MatchString(name) && !strings.Contains(name, "..")
}

// splitFlags splits args into the flags and the names.
func splitFlags(args []string) (flags, names []string) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		} else {
			names = append(names, arg)
		}
	}
	return flags, names
}

func onlyFlags(flags []string, allowed ...string) bool {
	return !slices.ContainsFunc(flags, func(flag string) bool { return !slices.Contains(allowed, flag) })
}

// readVariables begin the names of the variables that change where git
// reads a repository or its configuration, or which objects it sees.
var readVariables = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_CONFIG", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS", "GIT_SHALLOW_FILE", "GIT_GRAFT_FILE"}

// hasGitVariables says whether env, or this process's own environment when
// env is nil, sets a variable that changes what git reads, such as GIT_DIR.
// One that only changes how git talks, such as GIT_TERMINAL_PROMPT or
// GIT_EDITOR, changes no answer.
func hasGitVariables(env []string) bool {
	if env == nil {
		env = os.Environ()
	}
	return slices.ContainsFunc(env, func(variable string) bool {
		name, _, _ := strings.Cut(variable, "=")
		return slices.ContainsFunc(readVariables, func(prefix string) bool { return strings.HasPrefix(strings.ToUpper(name), prefix) })
	})
}
