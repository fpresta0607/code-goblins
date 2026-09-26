package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type Git struct{ Base string }
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}
type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}
type FileDiff struct {
	Path     string `json:"path"`
	Patch    string `json:"patch"`
	Code     string `json:"code"`
	Head     string `json:"head"`
	Revision string `json:"revision"`
	Binary   bool   `json:"binary"`
	// CodeOmitted says the file is over the code preview limit, so Code is
	// empty and only the patch shows it.
	CodeOmitted bool   `json:"code_omitted"`
	Fingerprint string `json:"fingerprint"`
}

// maxCodePreview bounds a file's whole-file code preview; a larger file shows
// only its patch. maxGitOutput bounds everything else a preview reads.
const (
	maxCodePreview = 256 << 10
	maxGitOutput   = 1 << 20
)

var errPreviewTooLarge = errors.New("file exceeds the preview limit")

var commitID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// limitedBuffer keeps at most limit bytes of a command's output. It is not a
// bytes.Buffer, whose ReadFrom would let io.Copy go around Write and the limit.
type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.limit {
		b.exceeded = true
		return 0, errors.New("Git output exceeds preview limit")
	}
	return b.buffer.Write(p)
}

func (Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = dir
	// Optional locks are what let git status rewrite the index, and these
	// reads run every minute inside worktrees a goblin is using.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 2 * time.Second
	// Stderr is discarded: only warnings reach it when a command succeeds,
	// and a failure is reported by its exit status.
	out := &limitedBuffer{limit: maxGitOutput}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		if out.exceeded {
			return "", fmt.Errorf("Git %s output exceeds the %d KiB preview limit", args[0], maxGitOutput>>10)
		}
		return "", fmt.Errorf("Git %s failed: %w", args[0], err)
	}
	return out.buffer.String(), nil
}

func SafeFilePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n:") {
		return false
	}
	for _, part := range strings.Split(strings.ReplaceAll(path, "\\", "/"), "/") {
		name := strings.ToLower(part)
		if name == "" || name == "." || name == ".." || name == ".git" || strings.HasPrefix(name, ".env") || name == "auth.ps1" || name == ".npmrc" || name == ".netrc" || name == ".pypirc" || strings.HasPrefix(name, "id_rsa") || strings.HasPrefix(name, "id_ed25519") || strings.Contains(name, "credential") {
			return false
		}
		switch filepath.Ext(name) {
		case ".pem", ".key", ".p12", ".pfx":
			return false
		}
	}
	return true
}

func (g Git) validateRevision(ctx context.Context, dir, revision string) error {
	if revision == "" {
		return nil
	}
	if !commitID.MatchString(revision) {
		return errors.New("revision must be a full commit ID")
	}
	_, err := g.run(ctx, dir, "merge-base", "--is-ancestor", revision, "HEAD")
	if err != nil {
		return errors.New("commit is not in this task's history")
	}
	return nil
}

func (g Git) Head(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", "--verify", "HEAD")
	return strings.TrimSpace(out), err
}
func (g Git) Branch(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "symbolic-ref", "--short", "HEAD")
	return strings.TrimSpace(out), err
}
func (g Git) Clean(ctx context.Context, dir string) (bool, error) {
	out, err := g.run(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	return strings.TrimSpace(out) == "", err
}

func (g Git) base(ctx context.Context, dir string) (string, error) {
	if g.Base != "" {
		if err := g.validateRevision(ctx, dir, g.Base); err != nil {
			return "", fmt.Errorf("retained task base is unavailable: %w", err)
		}
		return g.Base, nil
	}
	ref, err := g.run(ctx, dir, "symbolic-ref", "-q", "refs/remotes/origin/HEAD")
	if err == nil && strings.HasPrefix(strings.TrimSpace(ref), "refs/remotes/") {
		base, err := g.run(ctx, dir, "merge-base", "HEAD", strings.TrimSpace(ref))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(base), nil
	}
	return "", errors.New("task diff base is unavailable: establish origin/HEAD for this project before reviewing the full task diff")
}

func (g Git) Files(ctx context.Context, dir, revision string) ([]ChangedFile, error) {
	if err := g.validateRevision(ctx, dir, revision); err != nil {
		return nil, err
	}
	var out string
	var err error
	if revision != "" {
		out, err = g.run(ctx, dir, "diff-tree", "--root", "--no-commit-id", "--name-status", "--no-renames", "-r", "-z", revision)
	} else {
		base, baseErr := g.base(ctx, dir)
		if baseErr != nil {
			return nil, baseErr
		}
		out, err = g.run(ctx, dir, "diff", "--name-status", "--no-renames", "-z", base, "--")
	}
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	files := []ChangedFile{}
	if out != "" {
		if len(parts)%2 != 0 {
			return nil, errors.New("invalid Git file list")
		}
		for i := 0; i < len(parts); i += 2 {
			if SafeFilePath(parts[i+1]) {
				files = append(files, ChangedFile{Path: parts[i+1], Status: parts[i]})
			}
		}
	}
	if revision == "" {
		untracked, err := g.run(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, path := range strings.Split(untracked, "\x00") {
			if SafeFilePath(path) {
				files = append(files, ChangedFile{Path: path, Status: "?"})
			}
		}
	}
	if len(files) > 500 {
		return nil, errors.New("more than 500 changed files; narrow the task before previewing")
	}
	return files, nil
}

func (g Git) Diff(ctx context.Context, dir, revision, path string) (result FileDiff, err error) {
	defer func() {
		if err == nil {
			result.Fingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(result.Patch)))
		}
	}()
	if !SafeFilePath(path) {
		return FileDiff{}, errors.New("unsafe or credential-bearing file path")
	}
	files, err := g.Files(ctx, dir, revision)
	if err != nil {
		return FileDiff{}, err
	}
	var status string
	for _, f := range files {
		if f.Path == path {
			status = f.Status
			break
		}
	}
	if status == "" {
		return FileDiff{}, errors.New("file is not in this change set")
	}
	head, err := g.Head(ctx, dir)
	if err != nil {
		return FileDiff{}, err
	}
	d := FileDiff{Path: path, Head: head, Revision: revision}
	if revision != "" {
		d.Patch, err = g.run(ctx, dir, "show", "--format=", "--no-ext-diff", "--no-textconv", "--no-renames", revision, "--", path)
		if err == nil && status != "D" {
			// The file is sized before it is read, so one past the Git output
			// limit still shows its patch.
			var size string
			if size, err = g.run(ctx, dir, "cat-file", "-s", revision+":"+path); err == nil {
				var n int
				n, err = strconv.Atoi(strings.TrimSpace(size))
				d.CodeOmitted = err == nil && n > maxCodePreview
				if err == nil && !d.CodeOmitted {
					d.Code, err = g.run(ctx, dir, "show", revision+":"+path)
				}
			}
		}
	} else {
		if status != "D" {
			// An untracked file's patch is built from its content, so it is
			// read as far as a patch may go; any other file only as far as
			// its code preview.
			limit := maxCodePreview
			if status == "?" {
				limit = maxGitOutput
			}
			d.Code, err = readPreview(dir, path, limit)
			if status != "?" && errors.Is(err, errPreviewTooLarge) {
				d.CodeOmitted, err = true, nil
			}
		}
		if err != nil {
			return FileDiff{}, err
		}
		if status == "?" {
			var patch strings.Builder
			header := fmt.Sprintf("--- /dev/null\n+++ b/%s\n", path)
			// A zero-byte file has no line to select; a lone newline is one empty line.
			if d.Code == "" {
				patch.WriteString(header)
			} else {
				lines := strings.Split(strings.TrimSuffix(d.Code, "\n"), "\n")
				hunk := fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines))
				patch.Grow(len(header) + len(hunk) + len(d.Code) + len(lines) + 1)
				patch.WriteString(header)
				patch.WriteString(hunk)
				for _, line := range lines {
					patch.WriteByte('+')
					patch.WriteString(line)
					patch.WriteByte('\n')
				}
			}
			d.Patch = patch.String()
		} else {
			base, baseErr := g.base(ctx, dir)
			if baseErr != nil {
				return FileDiff{}, baseErr
			}
			d.Patch, err = g.run(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", base, "--", path)
		}
	}
	if err != nil {
		return FileDiff{}, err
	}
	d.Binary = strings.ContainsRune(d.Code, '\x00') || strings.Contains(d.Patch, "Binary files ")
	if d.Binary {
		d.Code, d.CodeOmitted = "", false
		d.Patch = "Binary file changed. Text preview is unavailable."
	}
	if len(d.Code) > maxCodePreview {
		d.Code, d.CodeOmitted = "", true
	}
	return d, nil
}

// readPreview reads a changed file of at most limit bytes for the preview
// through an os.Root on the canonical task root.
func readPreview(dir, path string, limit int) (string, error) {
	canonical, err := fsx.Canonical(dir)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := openRegular(root, filepath.FromSlash(path))
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", fmt.Errorf("%w of %d KiB", errPreviewTooLarge, limit>>10)
	}
	return string(data), nil
}

// openRegular opens name under root only through plain directories to a
// regular file. Lstat reports a symlink as ModeSymlink and a junction or other
// reparse point as ModeIrregular, so neither is followed, and the open itself
// goes through root, so a component swapped for a link after these checks
// still cannot resolve outside it.
func openRegular(root *os.Root, name string) (*os.File, error) {
	parts := strings.Split(name, string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return nil, err
		}
		if i < len(parts)-1 && info.Mode().Type() != fs.ModeDir || i == len(parts)-1 && !info.Mode().IsRegular() {
			return nil, errors.New("only a regular file reached through plain directories can be opened")
		}
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("only a regular file reached through plain directories can be opened")
	}
	return f, nil
}

func (g Git) History(ctx context.Context, dir string) ([]Commit, error) {
	out, err := g.run(ctx, dir, "log", "-80", "--format=%H%x00%h%x00%s%x00%an%x00%aI%x00")
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(strings.TrimSuffix(line, "\x00"), "\x00")
		if len(parts) != 5 || !commitID.MatchString(parts[0]) {
			return nil, errors.New("invalid Git history response")
		}
		commits = append(commits, Commit{SHA: parts[0], Short: parts[1], Subject: parts[2], Author: parts[3], Date: parts[4]})
	}
	return commits, nil
}
