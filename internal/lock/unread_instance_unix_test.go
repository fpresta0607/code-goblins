//go:build linux || darwin

package lock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// An owner whose instance could not be read would leave a record that names
// no instance, which every reader that can read the owner's instance takes
// for another program: the next acquirer would remove a live holder's
// record. So no record is written for such an owner, and the lock stays free.
func TestNoRecordIsWrittenForAnOwnerWhoseInstanceCannotBeRead(t *testing.T) {
	acquirers := map[string]func(dir, name string) (*Info, error){
		"named owner": func(dir, name string) (*Info, error) {
			return AcquireNamedOwner(dir, name, os.Getpid(), "watch")
		},
		"exclusive": AcquireExclusiveNamed,
	}
	for kind, acquire := range acquirers {
		t.Run(kind, func(t *testing.T) {
			// Arrange
			dir, name := t.TempDir(), ".watch.lock"
			read := processInstance
			processInstance = func(int) (string, processStatus) { return "", statusUnknown }
			t.Cleanup(func() { processInstance = read })

			// Act
			info, err := acquire(dir, name)

			// Assert
			if err == nil {
				t.Errorf("the lock was taken as %+v by an owner whose instance could not be read, want a refusal", info)
			}
			if _, statErr := os.Stat(filepath.Join(dir, name)); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("the lock's record is there after the refusal (%v), want none written", statErr)
			}
		})
	}
}
