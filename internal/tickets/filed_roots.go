package tickets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// filedRootsFile is the file in a task's temporary directory that names, one
// address a line, every root issue the task opened with cfo issue file.
const filedRootsFile = "filed-roots"

// RecordFiledRoot notes, in taskTmp, the temporary directory of the task that
// opened it, the address of a root issue. A goblin that files an issue may be
// taking it up itself as a follow-up it was given, so while the task runs the
// issue is its own. Each address is one appended line, so two filings at once
// both stay.
func RecordFiledRoot(taskTmp, url string) error {
	file, err := fsx.OpenAppend(filepath.Join(taskTmp, filedRootsFile), 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(url + "\n")
	return errors.Join(err, file.Close())
}

// FiledRoots are the addresses of the root issues the task whose temporary
// directory is taskTmp opened, none when it opened none.
func FiledRoots(taskTmp string) ([]string, error) {
	data, err := fsx.ReadFile(filepath.Join(taskTmp, filedRootsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}
