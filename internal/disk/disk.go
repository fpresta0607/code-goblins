// Package disk reads how much space the drive holding a folder has left, with
// one system call and no process, so a hook, the digest and the supervisor's
// one-minute meter can all afford it.
package disk

import (
	"errors"
	"path/filepath"
)

// Reading is the drive holding a folder: its name, the bytes free to this
// user and its size.
type Reading struct {
	Drive string `json:"drive"`
	Free  uint64 `json:"free"`
	Total uint64 `json:"total"`
}

// spaceOf reads a drive's free space and size; a test stands in for it.
var spaceOf = space

// Read reads the drive holding path.
func Read(path string) (Reading, error) {
	free, total, err := spaceOf(path)
	if err != nil {
		return Reading{}, err
	}
	return Reading{Drive: filepath.VolumeName(path), Free: free, Total: total}, nil
}

// ReadLeast reads the drive holding each of paths, skipping an empty one, and
// returns the one with the least free space: a start lands on both the home's
// drive and the Dev Drive its heavy folders moved to, and either one filling
// stops it. A drive that cannot be read is an error, never skipped, so a
// detached Dev Drive never reads as room to start.
func ReadLeast(paths ...string) (Reading, error) {
	var least Reading
	found := false
	for _, path := range paths {
		if path == "" {
			continue
		}
		reading, err := Read(path)
		if err != nil {
			return Reading{}, err
		}
		if !found || reading.Free < least.Free {
			least, found = reading, true
		}
	}
	if !found {
		return Reading{}, errors.New("disk: no folder to read")
	}
	return least, nil
}

// GB is bytes as gigabytes, for messages.
func GB(bytes uint64) float64 {
	return float64(bytes) / (1 << 30)
}
