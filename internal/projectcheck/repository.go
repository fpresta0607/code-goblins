package projectcheck

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// repository reads a checkout through git. What the project tracks is read
// at its default branch as the checkout last fetched it, never from the
// folder: a checkout that lags its remote, or sits on another branch, would
// answer for a repository that is no longer the one a goblin's worktree is
// cut from. What only the folder holds, such as an env file, is read there.
type repository struct {
	dir    string
	runner execx.Runner
	// ref is the branch tracked files are read at, commit its commit as
	// lines name it and full the same commit in full.
	ref, commit, full string
	// remote and branch are the two halves of ref when it is a remote's
	// default branch, both empty when the folder's own commit is read.
	remote, branch string
	// fetched says when ref was last fetched, as a clause for a line's
	// evidence, empty when there is no remote to fetch from.
	fetched string
	// tracked are the paths ref holds, with forward slashes.
	tracked map[string]bool
	// index are the paths the checkout's own index tracks, which a folder
	// that lags or sits on another branch can differ in.
	index map[string]bool
}

// openRepository reads which branch is the checkout's default, when it was
// last fetched as of now, and what it tracks. A folder inside another
// repository is no checkout: git would answer for the repository around it.
func openRepository(ctx context.Context, runner execx.Runner, dir string, now time.Time) (*repository, error) {
	r := &repository{dir: dir, runner: runner, ref: "HEAD", tracked: map[string]bool{}, index: map[string]bool{}}
	if out, code, err := r.git(ctx, "rev-parse", "--show-prefix"); err != nil || code != 0 || strings.TrimSpace(out) != "" {
		return nil, fmt.Errorf("projectcheck: %s is not a git checkout", dir)
	}
	if out, code, err := r.git(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && code == 0 && strings.TrimSpace(out) != "" {
		r.ref = strings.TrimSpace(out)
		r.remote, r.branch, _ = strings.Cut(r.ref, "/")
	}
	out, code, err := r.git(ctx, "rev-parse", "--short", r.ref)
	if err != nil || code != 0 {
		return nil, fmt.Errorf("projectcheck: %s is not a git checkout with a commit at %s", dir, r.ref)
	}
	r.commit = strings.TrimSpace(out)
	if out, code, err = r.git(ctx, "rev-parse", r.ref); err != nil || code != 0 {
		return nil, fmt.Errorf("projectcheck: git could not name the commit of %s at %s", dir, r.ref)
	}
	r.full = strings.TrimSpace(out)
	if r.remote != "" {
		r.fetched = r.lastFetched(ctx, now)
	}
	out, code, err = r.git(ctx, "-c", "core.quotePath=false", "ls-tree", "-r", "-z", "--name-only", r.ref)
	if err != nil || code != 0 {
		return nil, fmt.Errorf("projectcheck: git could not list the files of %s at %s", dir, r.ref)
	}
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			r.tracked[name] = true
		}
	}
	out, code, err = r.git(ctx, "-c", "core.quotePath=false", "ls-files", "-z")
	if err != nil || code != 0 {
		return nil, fmt.Errorf("projectcheck: git could not list the index of %s", dir)
	}
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			r.index[name] = true
		}
	}
	return r, nil
}

// at names where tracked files were read, for a line's evidence.
func (r *repository) at() string { return r.ref + " " + r.commit }

// asRead names where tracked files were read and how old that reading is,
// for a line that says what it read.
func (r *repository) asRead() string { return r.at() + r.fetched }

// lastFetched says when the default branch was last fetched: the time of
// the last fetch that named it, which git keeps as FETCH_HEAD, or else the
// last time the branch moved in this checkout, by its reflog.
func (r *repository) lastFetched(ctx context.Context, now time.Time) string {
	var when time.Time
	if out, code, err := r.git(ctx, "rev-parse", "--git-path", "FETCH_HEAD"); err == nil && code == 0 {
		file := strings.TrimSpace(out)
		if !filepath.IsAbs(file) {
			file = filepath.Join(r.dir, file)
		}
		data, err := fsx.ReadFile(file)
		info, statErr := os.Stat(file)
		if err == nil && statErr == nil && strings.Contains(string(data), "branch '"+r.branch+"' of") {
			when = info.ModTime()
		}
	}
	if when.IsZero() {
		if out, code, err := r.git(ctx, "reflog", "show", "-1", "--date=iso-strict", "--format=%gd", "refs/remotes/"+r.ref); err == nil && code == 0 {
			if _, stamp, found := strings.Cut(strings.TrimSuffix(strings.TrimSpace(out), "}"), "@{"); found {
				when, _ = time.Parse(time.RFC3339, stamp)
			}
		}
	}
	if when.IsZero() {
		return ", fetched at a time git did not record"
	}
	age := max(now.Sub(when), 0)
	ago := count(int(age.Hours()/24), "day")
	switch {
	case age < time.Hour:
		ago = count(int(age.Minutes()), "minute")
	case age < 48*time.Hour:
		ago = count(int(age.Hours()), "hour")
	}
	return ", last fetched " + when.Local().Format("2006-01-02 15:04") + ", " + ago + " before this run"
}

// remoteHead asks the remote where its default branch is, which reads the
// remote and writes nothing to the repository. Answered is false when the
// remote gave no commit within the time allowed. Git is told not to ask for
// a sign-in, so a remote that wants one is a remote that did not answer.
func (r *repository) remoteHead(ctx context.Context) (head string, answered bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := r.runner.Run(ctx, execx.Request{
		Dir: r.dir, Name: "git", Args: []string{"ls-remote", r.remote, "refs/heads/" + r.branch},
		Env: append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"), KillTree: true,
	})
	fields := strings.Fields(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || len(fields) != 2 || fields[1] != "refs/heads/"+r.branch {
		return "", false
	}
	return fields[0], true
}

func (r *repository) git(ctx context.Context, args ...string) (string, int, error) {
	result, err := r.runner.Run(ctx, execx.Request{Dir: r.dir, Name: "git", Args: args})
	return string(result.Stdout), result.ExitCode, err
}

// read returns a tracked file as the default branch has it.
func (r *repository) read(ctx context.Context, name string) ([]byte, bool) {
	if !r.tracked[name] {
		return nil, false
	}
	result, err := r.runner.Run(ctx, execx.Request{Dir: r.dir, Name: "git", Args: []string{"show", r.ref + ":" + name}})
	if err != nil || result.ExitCode != 0 {
		return nil, false
	}
	return result.Stdout, true
}

// has reports whether the repository tracks name or the folder holds it.
func (r *repository) has(name string) bool {
	if r.tracked[path.Clean(filepath.ToSlash(name))] {
		return true
	}
	_, err := os.Stat(filepath.Join(r.dir, filepath.FromSlash(name)))
	return err == nil
}

// tracks reports whether the default branch holds name: as a file, or with
// folder as a folder that has a file under it.
func (r *repository) tracks(name string, folder bool) bool {
	if !folder {
		return r.tracked[name]
	}
	return under(r.tracked, name)
}

// indexTracks reports whether the checkout's own index tracks name or a file
// under it, which makes the folder's copy one of the branch the folder is on.
func (r *repository) indexTracks(name string) bool {
	return r.index[name] || under(r.index, name)
}

// under reports whether a path of paths lies under folder.
func under(paths map[string]bool, folder string) bool {
	prefix := folder + "/"
	for name := range paths {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// everHeld reports whether a commit the default branch can reach ever held
// name. Known is false where that cannot be told: a shallow clone has only
// the end of its history.
func (r *repository) everHeld(ctx context.Context, name string) (held, known bool) {
	if out, code, err := r.git(ctx, "rev-parse", "--is-shallow-repository"); err != nil || code != 0 || strings.TrimSpace(out) != "false" {
		return false, false
	}
	out, code, err := r.git(ctx, "log", "-1", "--format=%h", r.ref, "--", name)
	if err != nil || code != 0 {
		return false, false
	}
	return strings.TrimSpace(out) != "", true
}

// ignored returns which of the folder's paths git ignores. A path git
// tracks is never ignored, whatever an ignore file says.
func (r *repository) ignored(ctx context.Context, names []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(names) == 0 {
		return ignored, nil
	}
	// Exit 1 is git saying none of them is ignored.
	out, code, err := r.git(ctx, append([]string{"-c", "core.quotePath=false", "check-ignore", "--"}, names...)...)
	if err != nil || (code != 0 && code != 1) {
		return nil, fmt.Errorf("projectcheck: git check-ignore did not answer in %s", r.dir)
	}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimRight(line, "\r")
		// git quotes a name it had to escape, in the syntax strconv reads.
		if unquoted, err := strconv.Unquote(name); err == nil {
			name = unquoted
		}
		if name != "" {
			ignored[name] = true
		}
	}
	return ignored, nil
}

// heavyFolders are never walked: what they hold is installed or built, not
// the project's own.
var heavyFolders = map[string]bool{"node_modules": true, "venv": true, "dist": true, "build": true, "target": true, "__pycache__": true}

// envFiles returns the env files of the project, with forward slashes: those
// the default branch tracks anywhere, and those the folder holds at its root
// or up to two folders down, where a project keeps the env file of an app
// inside it. Folders whose name starts with a dot are not walked, nor is a
// clone or a worktree of another repository kept inside the checkout.
func (r *repository) envFiles() []string {
	seen := map[string]bool{}
	for name := range r.tracked {
		if isEnvFile(path.Base(name)) {
			seen[name] = true
		}
	}
	root := filepath.Clean(r.dir)
	_ = filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || file == root {
			return nil
		}
		relative := filepath.ToSlash(strings.TrimPrefix(file, root+string(filepath.Separator)))
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || heavyFolders[entry.Name()] || strings.Count(relative, "/") >= 2 {
				return filepath.SkipDir
			}
			// A folder with a .git of its own is another repository.
			if _, err := os.Lstat(filepath.Join(file, ".git")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if isEnvFile(entry.Name()) {
			seen[relative] = true
		}
		return nil
	})
	return sortedKeys(seen)
}

// envTemplate matches the name of an env template a repository keeps
// without the leading dot, with another joint, or with its words the other
// way round: env.template, .env-example, example.env.
var envTemplate = regexp.MustCompile(`^(\.?env[._-](example|sample|template|dist|defaults)|(example|sample|template)[._-]env)$`)

// isEnvFile reports whether a file name is an env file's: .env, .env.<kind>,
// or a template of one under another spelling.
func isEnvFile(base string) bool {
	return base == ".env" || strings.HasPrefix(base, ".env.") || envTemplate.MatchString(strings.ToLower(base))
}

// isExample reports whether an env file is a template a repository commits
// on purpose, with names and no values.
func isExample(base string) bool {
	for _, suffix := range []string{".example", ".sample", ".template", ".dist", ".defaults"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return envTemplate.MatchString(strings.ToLower(base))
}
