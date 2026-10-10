package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// timesPath is where the store keeps how long a project's tests take.
func timesPath(project string) (string, error) {
	store, err := StoreDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(store, "times", folder(project)+".json"), nil
}

// Times reads the shortest time a run here has seen each of a project's
// tests pass in, in seconds, by its package's import path and its name.
// A project no run has timed has none. The times are this machine's, which
// is what a run that must fit this machine goes by.
func Times(project string) (map[string]map[string]float64, error) {
	path, err := timesPath(project)
	if err != nil {
		return nil, err
	}
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var times map[string]map[string]float64
	if err := json.Unmarshal(data, &times); err != nil {
		return nil, fmt.Errorf("verify: read the test times %s: %w", path, err)
	}
	return times, nil
}

// KeepTimes adds what a run timed to a project's record, and keeps for each
// test the shortest time it has passed in. A push leaves out the tests the
// record has as slow, and a test left out is never timed again, so one pass
// on a busy machine must not make a quick test slow. A test that grows
// slower keeps its old time and keeps running, which costs time and loses
// nothing. A record that cannot be read is replaced. Two runs that end
// together can each miss what the other timed, which the next run of those
// tests puts back.
func KeepTimes(project string, seen map[string]map[string]float64) error {
	if len(seen) == 0 {
		return nil
	}
	path, err := timesPath(project)
	if err != nil {
		return err
	}
	times, _ := Times(project)
	if times == nil {
		times = map[string]map[string]float64{}
	}
	for importPath, tests := range seen {
		if times[importPath] == nil {
			times[importPath] = map[string]float64{}
		}
		for test, seconds := range tests {
			if kept, isTimed := times[importPath][test]; !isTimed || seconds < kept {
				times[importPath][test] = seconds
			}
		}
	}
	data, err := json.Marshal(times)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}
