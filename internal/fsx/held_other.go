//go:build !windows

package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// heldTries is how many times OpenHeld and CommitUnheld take a file again
// when a replace put another at its name while they took it.
const heldTries = 3

// OpenHeld opens path for reading and holds it against a replace by
// CommitUnheld until the file is closed, as Windows holds a file opened
// without sharing deletion. Here a reader never blocks a rename by itself, so
// it takes the kernel's shared lock (flock) on the file, which readers share
// and CommitUnheld waits out. A file the name no longer holds once it is
// locked was replaced in between, so the name is opened again.
func OpenHeld(path string) (*os.File, error) {
	for range heldTries {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		if err := lockFile(file, syscall.LOCK_SH); err != nil {
			file.Close()
			return nil, err
		}
		if isNameOf(path, file) {
			return file, nil
		}
		file.Close()
	}
	return nil, fmt.Errorf("%s was replaced each of the %d times it was opened", path, heldTries)
}

// CommitUnheld is Commit once no reader holds the file at the path
// (OpenHeld): it takes the kernel's exclusive lock on that file, waiting
// readers out within transientBudget, and renames while it holds it. A replace
// that fails leaves nothing staged.
func (s *Staged) CommitUnheld() error {
	for range heldTries {
		live, err := os.Open(s.path)
		if errors.Is(err, fs.ErrNotExist) {
			return s.Commit()
		}
		if err != nil {
			s.Discard()
			return err
		}
		if err := lockFile(live, syscall.LOCK_EX); err != nil {
			live.Close()
			s.Discard()
			return err
		}
		if isNameOf(s.path, live) {
			err := s.Commit()
			return errors.Join(err, live.Close())
		}
		live.Close()
	}
	s.Discard()
	return fmt.Errorf("%s was replaced each of the %d times it was taken for a replace", s.path, heldTries)
}

// lockFile takes the kernel's lock on file, shared or exclusive as how says,
// waiting out another's within transientBudget.
func lockFile(file *os.File, how int) error {
	err := retryTransient(func() error {
		return syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB)
	})
	if err != nil {
		return &os.PathError{Op: "hold", Path: file.Name(), Err: err}
	}
	return nil
}
