package fsx

import (
	"io"
	"os"
	"sync/atomic"
)

// opens counts the files this process opened through Open.
var opens atomic.Int64

// Opens is how many files this process has opened for reading so far. A test
// holds a reader to a budget of them: on a loaded machine an open costs tens
// of milliseconds, and a reader that opens hundreds of files each time is
// slow whatever else it does.
func Opens() int64 { return opens.Load() }

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
	opens.Add(1)
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

// CreateNew creates path for writing with perm only if it is missing: an
// existing file is an error satisfying errors.Is(err, os.ErrExist). A create
// that meets another process's brief hold on the name, as a file deleted
// while that process still has it open is held until it lets go, waits it
// out.
func CreateNew(path string, perm os.FileMode) (*os.File, error) {
	var file *os.File
	err := retryTransient(func() (err error) {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		return err
	})
	return file, err
}
