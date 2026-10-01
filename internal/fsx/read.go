package fsx

import (
	"io"
	"os"
)

// Open is os.Open for every file a fleet program reads. Another fleet
// process may replace the file with AtomicWriteFile while it is open, so on
// Windows it shares the file for deletion too, which lets that rename go
// through, and the reader keeps reading the file it opened. An open that
// meets another process's brief hold on the file waits it out.
func Open(path string) (*os.File, error) {
	var file *os.File
	err := retryTransient(func() (err error) {
		file, err = open(path)
		return err
	})
	return file, err
}

// ReadFile is os.ReadFile for every file a fleet program reads, opened as
// Open opens it.
func ReadFile(path string) ([]byte, error) {
	file, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// OpenAppend opens path to append to it, creating it with perm if it is
// missing, for every file a fleet program appends to. An open that meets
// another process's brief hold on the file waits it out.
func OpenAppend(path string, perm os.FileMode) (*os.File, error) {
	var file *os.File
	err := retryTransient(func() (err error) {
		file, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
		return err
	})
	return file, err
}
