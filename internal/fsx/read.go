package fsx

import (
	"io"
	"os"
)

// Open is os.Open for every file a fleet program reads.
func Open(path string) (*os.File, error) {
	return os.Open(path)
}

// ReadFile is os.ReadFile for every file a fleet program reads.
func ReadFile(path string) ([]byte, error) {
	file, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}
