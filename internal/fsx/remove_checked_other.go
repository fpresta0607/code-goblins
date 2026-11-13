//go:build !windows

package fsx

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
)

// RemoveFileWhen claims a file against every other claim, checks its
// contents, and removes the file when check approves and its name is still
// that file's. A missing file or a declined check returns false without
// removing anything.
//
// The claim is the kernel's lock on the open file (flock), which only
// another claim waits on: here an open file keeps nobody from removing its
// name or putting another file there. So a file that took the name while the
// claim was waited for is claimed in its place, and one that took it during
// the check is left alone. Two removals of one file never both pass their
// check and remove: the second would remove whatever took the name after the
// first, as a new holder's record takes a dead holder's.
func RemoveFileWhen(ctx context.Context, path string, check func([]byte) (bool, error)) (isRemoved bool, err error) {
	for {
		file, err := os.Open(path)
		opens.Add(1)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		isRemoved, isNamed, err := removeClaimed(ctx, file, path, check)
		if isNamed || err != nil {
			return isRemoved, err
		}
	}
}

// removeClaimed is RemoveFileWhen of file, opened at path. isNamed is false
// when another file had the name by the time file was claimed, so nothing
// was checked.
func removeClaimed(ctx context.Context, file *os.File, path string, check func([]byte) (bool, error)) (isRemoved, isNamed bool, err error) {
	// Closing the file gives the claim up.
	defer func() { err = errors.Join(err, file.Close()) }()
	err = retryTransientContext(ctx, func() error {
		return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	})
	if err != nil {
		return false, true, &os.PathError{Op: "claim removal", Path: path, Err: err}
	}
	if !isNameOf(path, file) {
		return false, false, nil
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return false, true, err
	}
	isApproved, err := check(data)
	if err != nil || !isApproved {
		return false, true, err
	}
	if err := ctx.Err(); err != nil {
		return false, true, err
	}
	if !isNameOf(path, file) {
		return false, true, nil
	}
	if err := os.Remove(path); err != nil {
		return false, true, err
	}
	return true, true, nil
}

// isNameOf reports whether path names the file that file has open.
func isNameOf(path string, file *os.File) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	named, err := os.Stat(path)
	return err == nil && os.SameFile(opened, named)
}
