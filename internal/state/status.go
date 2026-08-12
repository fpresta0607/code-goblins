package state

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// AppendStatus appends one raw line to state/<id>.status, creating the log on
// first use. Lines carry their own grammar; this layer adds nothing.
func AppendStatus(dir, id, line string) error {
	f, err := os.OpenFile(filepath.Join(dir, id+".status"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(line + "\n")
	return errors.Join(werr, f.Close())
}

// TailStatus returns the last n lines of state/<id>.status. A missing log
// returns (nil, nil): "no status yet" is a real fleet state, not an error.
// ponytail: whole-file read; switch to a reverse block scan if logs outgrow
// the line caps a later plan ports.
func TailStatus(dir, id string, n int) ([]string, error) {
	lines, err := fsx.ReadLines(filepath.Join(dir, id+".status"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
