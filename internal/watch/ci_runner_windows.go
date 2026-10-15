package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// newCIRunnerWatch is the watch on the desktop CI runner that answers on
// pipe, which starts this build's turns and logs under stateDir. A test
// binary watches only a pipe its test names, never the machine's runner, so
// no test can start a job's turn on the machine.
func newCIRunnerWatch(stateDir, pipe string) (*ciRunnerWatch, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if name := strings.ToLower(filepath.Base(executable)); pipe == cirunner.PipeName && (strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")) {
		return nil, errors.New("a test watches only a desktop CI runner of its own")
	}
	dir := ciRunnerFolder(stateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &ciRunnerWatch{
		ask: func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error {
			return cirunner.Ask(ctx, pipe, request, reply)
		},
		commands:   execx.OSRunner{},
		started:    proc.StartTime,
		executable: executable,
		logs:       dir,
		now:        time.Now,
		say:        appendLine(filepath.Join(dir, "watch.log"), time.Now),
	}, nil
}
