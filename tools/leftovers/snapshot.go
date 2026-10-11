package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The kinds of thing a snapshot holds. Each thing is named by one identity
// within its kind, such as a path for a file, and carries a detail that says
// what it holds, such as a junction's target or a small file's hash, so a
// thing that is there before and after but changed is told apart.
const (
	kindFile          = "file"
	kindRegistryKey   = "registry key"
	kindRegistryValue = "registry value"
	kindPathEntry     = "path entry"
	kindTask          = "scheduled task"
	kindCredential    = "credential"
	kindProcess       = "process"
	kindVolume        = "volume"
	kindVirtualDisk   = "virtual disk"
)

// Snapshot is what a Windows user profile and the machine around it hold
// that an install could change, as a set of things by kind.
type Snapshot struct {
	Taken  time.Time                    `json:"taken"`
	Things map[string]map[string]string `json:"things"`
	// Unread names what could not be read, each with why, so a snapshot
	// that saw less than it should says so rather than comparing equal.
	Unread []string `json:"unread,omitempty"`
}

func newSnapshot(taken time.Time) Snapshot {
	return Snapshot{Taken: taken, Things: map[string]map[string]string{}}
}

// add records one thing.
func (s Snapshot) add(kind, identity, detail string) {
	things, ok := s.Things[kind]
	if !ok {
		things = map[string]string{}
		s.Things[kind] = things
	}
	things[identity] = detail
}

// count is how many things the snapshot holds of kind.
func (s Snapshot) count(kind string) int {
	return len(s.Things[kind])
}

func writeSnapshot(path string, snapshot Snapshot) error {
	data, err := json.MarshalIndent(snapshot, "", " ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

func readSnapshot(path string) (Snapshot, error) {
	data, err := fsx.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("read the snapshot %s: %w", path, err)
	}
	if snapshot.Things == nil {
		return Snapshot{}, fmt.Errorf("the snapshot %s holds nothing", path)
	}
	return snapshot, nil
}

// profileFolder is a folder of the current user's profile the snapshot
// reads, by the variable that names it.
func profileFolder(variable string) (string, error) {
	folder := os.Getenv(variable)
	if folder == "" {
		return "", fmt.Errorf("%s is not set", variable)
	}
	return folder, nil
}
