// Package disk reads how much space the drive holding a folder has left, with
// one system call and no process, so a hook, the digest and the supervisor's
// one-minute meter can all afford it.
package disk

import "path/filepath"

// Reading is the drive holding a folder: its name, the bytes free to this
// user and its size.
type Reading struct {
	Drive string `json:"drive"`
	Free  uint64 `json:"free"`
	Total uint64 `json:"total"`
}

// Read reads the drive holding path.
func Read(path string) (Reading, error) {
	free, total, err := space(path)
	if err != nil {
		return Reading{}, err
	}
	return Reading{Drive: filepath.VolumeName(path), Free: free, Total: total}, nil
}

// GB is bytes as gigabytes, for messages.
func GB(bytes uint64) float64 {
	return float64(bytes) / (1 << 30)
}
