// Package disk reads how much space the drive holding a folder has left, with
// one system call and no process, so a hook, the digest and the supervisor's
// one-minute meter can all afford it.
package disk

import (
	"errors"
	"path/filepath"
	"strings"
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

// Folder is a folder the fleet writes to, whether it is the Dev Drive root
// config\dev-drive.json names, and whether it may not be made yet, as a gate
// home before the first gate run, so its error defers to a readable folder on
// the same drive.
type Folder struct {
	Path       string
	IsDevDrive bool
	IsOptional bool
}

// Drive is one drive the fleet writes to, as the board's disk meter shows it:
// its reading, whether it holds the Dev Drive, and why it could not be read.
type Drive struct {
	Reading
	IsDevDrive bool   `json:"dev_drive,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ReadDrives reads the drive holding each of folders, skipping an empty path,
// and returns one Drive for each distinct drive, in the order the folders
// first name it. A drive with a folder that fails to read carries the error
// and no free space, never room, unless that folder is optional and another
// folder on the drive reads: a folder not made yet says nothing about the
// drive under it, but a home or Dev Drive folder that cannot be read stops
// every start, so its drive shows why.
func ReadDrives(folders ...Folder) []Drive {
	var drives []Drive
	index := map[string]int{}
	isFailed := map[string]bool{}
	for _, folder := range folders {
		if folder.Path == "" {
			continue
		}
		key := strings.ToUpper(filepath.VolumeName(folder.Path))
		if key == "" {
			key = folder.Path
		}
		at, isSeen := index[key]
		if !isSeen {
			at = len(drives)
			index[key] = at
			drives = append(drives, Drive{Reading: Reading{Drive: filepath.VolumeName(folder.Path)}})
		}
		drive := &drives[at]
		drive.IsDevDrive = drive.IsDevDrive || folder.IsDevDrive
		if isFailed[key] || (folder.IsOptional && isSeen && drive.Error == "") {
			continue
		}
		reading, err := Read(folder.Path)
		if err != nil {
			drive.Reading, drive.Error = Reading{Drive: drive.Drive}, err.Error()
			isFailed[key] = !folder.IsOptional
			continue
		}
		drive.Reading, drive.Error = reading, ""
	}
	return drives
}

// GB is bytes as gigabytes, for messages.
func GB(bytes uint64) float64 {
	return float64(bytes) / (1 << 30)
}
