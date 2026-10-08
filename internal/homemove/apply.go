package homemove

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Result is what applying a plan did and what reading it back found.
type Result struct {
	// Files and Bytes are what the new home holds of the moved folders,
	// read back; CacheFiles and CacheBytes its caches folder.
	Files      int
	Bytes      int64
	CacheFiles int
	CacheBytes int64
	// AfterDigest is the digest over the SHA-256 of every file the move
	// kept as it was, read back from the new home.
	AfterDigest string
	// Differ are the files the new home does not hold as the plan says.
	Differ []string
}

// Apply makes the plan: each moved folder is renamed into the new home whole
// when nothing in it is tracked and both homes are on one drive, and moved
// or copied file by file otherwise, each copy verified against the plan's
// hash before its source goes; the task records are rewritten; and the new
// home is read back. A step that fails puts back the folders it already
// renamed and stops; a file already moved one at a time is named in the
// error.
func (p Plan) Apply() (Result, error) {
	if err := os.MkdirAll(p.To, 0o755); err != nil {
		return Result{}, err
	}
	tracked := map[string]bool{}
	byFolder := map[string][]File{}
	for _, file := range p.Files {
		folder, _, _ := strings.Cut(file.Rel, "/")
		byFolder[folder] = append(byFolder[folder], file)
		if file.Copy {
			tracked[folder] = true
		}
	}
	var renamed []string
	putBack := func(cause error) error {
		for i := len(renamed) - 1; i >= 0; i-- {
			if err := os.Rename(filepath.Join(p.To, renamed[i]), filepath.Join(p.From, renamed[i])); err != nil {
				cause = errors.Join(cause, fmt.Errorf("homemove: put %s back: %w", renamed[i], err))
			}
		}
		return cause
	}
	for _, folder := range append(append([]string{}, movedFolders...), cachesFolder) {
		source := filepath.Join(p.From, folder)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if !tracked[folder] && sameDrive(p.From, p.To) {
			if err := os.Rename(source, filepath.Join(p.To, folder)); err == nil {
				renamed = append(renamed, folder)
				continue
			}
		}
		if folder == cachesFolder {
			if err := moveTree(source, filepath.Join(p.To, folder)); err != nil {
				return Result{}, putBack(fmt.Errorf("homemove: move %s: %w", folder, err))
			}
			continue
		}
		for _, file := range byFolder[folder] {
			if err := p.moveFile(file); err != nil {
				return Result{}, putBack(err)
			}
		}
		removeEmptyFolders(source)
	}
	for _, rewrite := range p.Rewrites {
		target := filepath.Join(p.To, filepath.FromSlash(rewrite.Rel))
		current, err := fsx.ReadFile(target)
		if err != nil || hashOf(current) != rewrite.Before {
			return Result{}, fmt.Errorf("homemove: %s is not what the plan read, so it was not rewritten; the folders are moved, and nothing else changed", target)
		}
		if err := fsx.AtomicWriteFile(target, rewrite.Content); err != nil {
			return Result{}, err
		}
	}
	return p.readBack()
}

// moveFile moves one file into the new home, or copies it when the checkout
// tracks it: by rename on one drive, and otherwise by a copy checked against
// the plan's hash before the source goes.
func (p Plan) moveFile(file File) error {
	from := filepath.Join(p.From, filepath.FromSlash(file.Rel))
	to := filepath.Join(p.To, filepath.FromSlash(file.Rel))
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if !file.Copy && sameDrive(p.From, p.To) && os.Rename(from, to) == nil {
		return nil
	}
	if err := copyChecked(from, to, file.Hash); err != nil {
		return fmt.Errorf("homemove: copy %s: %w", file.Rel, err)
	}
	if file.Copy {
		return nil
	}
	return os.Remove(from)
}

func copyChecked(from, to, want string) error {
	in, err := fsx.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_, got, _, err := read(to)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the copy hashes to %s, not %s", got, want)
	}
	return nil
}

// moveTree moves a folder to another drive: every file copied, then the
// source removed.
func moveTree(from, to string) error {
	err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		_, hash, _, err := read(path)
		if err != nil {
			return err
		}
		return copyChecked(path, target, hash)
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(from)
}

// readBack hashes every planned file in the new home and counts its caches.
func (p Plan) readBack() (Result, error) {
	rewritten := map[string]string{}
	for _, rewrite := range p.Rewrites {
		rewritten[rewrite.Rel] = rewrite.After
	}
	var result Result
	var kept []string
	for _, file := range p.Files {
		_, hash, size, err := read(filepath.Join(p.To, filepath.FromSlash(file.Rel)))
		want := file.Hash
		if after, ok := rewritten[file.Rel]; ok {
			want = after
		}
		if err != nil || hash != want {
			result.Differ = append(result.Differ, file.Rel)
			continue
		}
		result.Files++
		result.Bytes += size
		if _, ok := rewritten[file.Rel]; !ok {
			kept = append(kept, hash)
		}
	}
	files, size, err := count(filepath.Join(p.To, cachesFolder))
	if err != nil {
		return result, err
	}
	result.CacheFiles, result.CacheBytes = files, size
	result.AfterDigest = digest(kept)
	sort.Strings(result.Differ)
	if len(result.Differ) > 0 {
		return result, fmt.Errorf("homemove: %d files in the new home are not what the plan moved, starting with %s", len(result.Differ), result.Differ[0])
	}
	if files != p.CacheFiles || size != p.CacheBytes {
		return result, fmt.Errorf("homemove: the caches hold %d files and %d bytes after the move, not the %d and %d planned", files, size, p.CacheFiles, p.CacheBytes)
	}
	return result, nil
}

// removeEmptyFolders removes every folder under root, root included, that a
// move left empty, deepest first; a folder that still holds anything stays.
func removeEmptyFolders(root string) {
	var folders []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			folders = append(folders, path)
		}
		return nil
	})
	for i := len(folders) - 1; i >= 0; i-- {
		_ = os.Remove(folders[i])
	}
}

func sameDrive(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}
