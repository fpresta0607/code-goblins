// Package fsx holds the Windows-safe file primitives every state package
// builds on: atomic replace-on-rename writes and CRLF-tolerant line reads.
package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AtomicWriteFile replaces path with data by writing a temp file in the same
// directory and renaming it over path. The rename goes through os.Root,
// which on Windows renames with POSIX semantics, so it replaces a file that
// a reader holds open with FILE_SHARE_DELETE, as Open opens it. A reader
// that does not share deletion (Go's os.Open, PowerShell's Get-Content, an
// antivirus or indexer scan) still blocks it until it lets go, so the rename
// waits that out within transientBudget.
func AtomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".cfo-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmpName)
		return err
	}
	root, err := os.OpenRoot(dir)
	if err == nil {
		err = retryTransient(func() error { return root.Rename(filepath.Base(tmpName), filepath.Base(path)) })
		root.Close()
	}
	if err != nil {
		os.Remove(tmpName)
		var renameErr *os.LinkError
		if errors.As(err, &renameErr) {
			renameErr.Old, renameErr.New = tmpName, path
		}
		return err
	}
	return nil
}

// transientBudget is how long a state file operation waits out another
// process holding the file before it reports the failure.
var transientBudget = 5 * time.Second

// retryTransient runs op until it succeeds, fails for any reason other than
// another process holding the file, or would outlast transientBudget,
// waiting 10 ms after the first attempt and twice as long after each next
// one, up to half a second.
func retryTransient(op func() error) error {
	deadline := time.Now().Add(transientBudget)
	wait := 10 * time.Millisecond
	for {
		err := op()
		if err == nil || !heldByAnother(err) || time.Now().Add(wait).After(deadline) {
			return err
		}
		time.Sleep(wait)
		wait = min(2*wait, 500*time.Millisecond)
	}
}

// ReadLines returns the file's lines, treating CRLF and LF endings equally.
// A missing file returns an error satisfying errors.Is(err, os.ErrNotExist).
func ReadLines(path string) ([]string, error) {
	data, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}
