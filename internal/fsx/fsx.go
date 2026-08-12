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
// directory and renaming it over path. The rename retries briefly because
// antivirus and indexer scans on Windows hold transient sharing locks.
func AtomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cfo-tmp-*")
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
	var renameErr error
	for attempt := 0; attempt < 10; attempt++ {
		if renameErr = os.Rename(tmpName, path); renameErr == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	os.Remove(tmpName)
	return renameErr
}

// ReadLines returns the file's lines, treating CRLF and LF endings equally.
// A missing file returns an error satisfying errors.Is(err, os.ErrNotExist).
func ReadLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
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
