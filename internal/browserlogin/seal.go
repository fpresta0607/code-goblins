package browserlogin

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// sealsDir holds one file for each task whose browser a key fetch reads in
// now. While it lasts, the task's goblin's browser commands are refused
// (cfo hook goblin-seal), so nothing the page shows reaches its transcript.
const sealsDir = "browser-seals"

// seal is a seal file's shape: when it lifts by itself, which bounds a seal
// a supervisor that stopped mid-fetch left behind.
type seal struct {
	Until time.Time `json:"until"`
}

func sealPath(stateDir, task string) (string, error) {
	if err := state.ValidTaskID(task); err != nil {
		return "", err
	}
	return filepath.Join(stateDir, sealsDir, task+".json"), nil
}

// Seal seals the browser task holds until until.
func Seal(stateDir, task string, until time.Time) error {
	path, err := sealPath(stateDir, task)
	if err != nil {
		return err
	}
	data, err := json.Marshal(seal{Until: until})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

// Unseal lifts task's seal; a task with none is unsealed already.
func Unseal(stateDir, task string) error {
	path, err := sealPath(stateDir, task)
	if err != nil {
		return err
	}
	if err := fsx.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Sealed reports whether task's browser is sealed at now. A seal file that
// cannot be read or understood counts as a seal, so a damaged one never
// lets a browser command through mid-fetch.
func Sealed(stateDir, task string, now time.Time) (bool, error) {
	path, err := sealPath(stateDir, task)
	if err != nil {
		return false, err
	}
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	var found seal
	if err != nil || json.Unmarshal(data, &found) != nil {
		return true, errors.New("the browser seal cannot be read")
	}
	return now.Before(found.Until), nil
}

// ClearSeals lifts every seal, as a supervisor that starts has no fetch
// running.
func ClearSeals(stateDir string) error {
	return fsx.RemoveAll(filepath.Join(stateDir, sealsDir))
}
