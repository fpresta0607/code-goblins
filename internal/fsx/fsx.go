// Package fsx holds the Windows-safe file primitives every state package
// builds on: atomic replace-on-rename writes and CRLF-tolerant line reads.
package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	staged, err := Stage(path, data)
	if err != nil {
		return err
	}
	return staged.Commit()
}

// Staged is a file written whole beside the place it is meant for and not
// yet in it. Staging is the slow part of a replace: it makes a new file,
// which a virus scanner reads, and on a loaded machine that took a quarter of
// a second and more. Commit only renames. A caller that replaces a file under
// a lock stages before it takes the lock and commits under it, so nobody
// waits in line for the scanner.
type Staged struct {
	temp, path string
}

// stages counts the files this process staged.
var stages atomic.Int64

// Stages is how many files this process has staged so far, each replace
// through AtomicWriteFile included. A test holds a path under a lock to none.
func Stages() int64 { return stages.Load() }

// Stage writes data to a temporary file in path's directory, for Commit to
// put in path's place.
func Stage(path string, data []byte) (*Staged, error) {
	stages.Add(1)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cfo-tmp-*")
	if err != nil {
		return nil, err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return &Staged{temp: tmp.Name(), path: path}, nil
}

// Commit puts the staged file in its place, replacing what is there, and
// waits out another process holding either file as AtomicWriteFile does. A
// commit that fails removes the staged file.
func (s *Staged) Commit() error {
	temp := s.temp
	s.temp = ""
	root, err := os.OpenRoot(filepath.Dir(s.path))
	if err == nil {
		err = retryTransient(func() error { return root.Rename(filepath.Base(temp), filepath.Base(s.path)) })
		root.Close()
	}
	if err != nil {
		os.Remove(temp)
		var renameErr *os.LinkError
		if errors.As(err, &renameErr) {
			renameErr.Old, renameErr.New = temp, s.path
		}
		return err
	}
	return nil
}

// Discard removes a staged file that was never committed, and does nothing
// to one that was.
func (s *Staged) Discard() {
	if s != nil && s.temp != "" {
		os.Remove(s.temp)
		s.temp = ""
	}
}

// Remove is os.Remove for a fleet file another process may be holding: a
// removal that meets another process's brief hold on the file waits it out
// within transientBudget. A missing file is an error, as with os.Remove.
func Remove(path string) error {
	return retryTransient(func() error { return os.Remove(path) })
}

// RemoveAll is os.RemoveAll for a folder or file another process may be
// holding for a moment, as a virus scanner holds a file it just saw and
// Windows a program that just ran: the removal waits that out within
// transientBudget. A missing path is no error, as with os.RemoveAll.
func RemoveAll(path string) error {
	return retryTransient(func() error { return os.RemoveAll(path) })
}

// transientBudget is how long a state file operation waits out another
// process holding the file before it reports the failure.
var transientBudget = 5 * time.Second

// longestPause is the longest a state file operation sleeps between two
// tries. A holder lets go at a moment nobody is told, so what a hold costs is
// its own length and the pause it ends in: with pauses of up to half a second
// a hold of 0.7 s cost 1.13 s.
const longestPause = 50 * time.Millisecond

// sleep is time.Sleep, which a test replaces to count the waits the budget
// bounds apart from the file work around them.
var sleep = time.Sleep

// retryTransient runs op until it succeeds, fails for any reason other than
// another process holding the file, or would outlast transientBudget,
// waiting 10 ms after the first attempt and twice as long after each next
// one, up to longestPause.
func retryTransient(op func() error) error {
	deadline := time.Now().Add(transientBudget)
	wait := 10 * time.Millisecond
	for {
		err := op()
		if err == nil || !heldByAnother(err) || time.Now().Add(wait).After(deadline) {
			return err
		}
		sleep(wait)
		wait = min(2*wait, longestPause)
	}
}

// WaitOut runs op, one operation on a fleet file, with the wait a replace
// and a removal have: it is tried again while another process's brief hold
// on the file refuses it, within transientBudget. It is for an operation this
// package has no function of its own for, such as a rename that must be
// written through or one of a program.
func WaitOut(op func() error) error {
	return retryTransient(op)
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
