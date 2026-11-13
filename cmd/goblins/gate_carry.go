package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// gateCarryLog is where the supervisor's own carry of the gate's home writes
// what it says, in the home's state.
const gateCarryLog = "dev-drive-move.log"

// carryGateHomeFromTheBoard is how the supervisor serving h carries the
// gate's home into a Dev Drive folder by itself: this build's goblins
// dev-drive move for the home, hidden, as a process of its own, since a move
// that carried the home ends by restarting the supervisor that started it.
// It returns once the move ended: nil when the home is there, an error that
// is gatehome.ErrGateBusy when gate runs stayed in flight all the while it
// waited, and otherwise the move's own last words.
func carryGateHomeFromTheBoard(h home.Home, start func(executable, dir, logPath string, args ...string) (*exec.Cmd, error)) func(context.Context, string) error {
	return func(_ context.Context, folder string) error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		logPath := filepath.Join(h.State, gateCarryLog)
		var before int64
		if info, err := os.Stat(logPath); err == nil {
			before = info.Size()
		}
		command, err := start(executable, h.Root, logPath, "dev-drive", "move", "--to", folder)
		if err != nil {
			return err
		}
		err = command.Wait()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return nil
		case !errors.As(err, &exit):
			return err
		case exit.ExitCode() == exitGateBusy:
			return gatehome.ErrGateBusy
		}
		return errors.New(lastSaid(logPath, before, exit.ExitCode()))
	}
}

// lastSaid is the last line a move wrote to its log past offset, without the
// command's own name, or its exit code when it wrote none.
func lastSaid(logPath string, offset int64, exitCode int) string {
	said := fmt.Sprintf("goblins dev-drive move exited %d and said nothing", exitCode)
	log, err := os.Open(logPath)
	if err != nil {
		return said
	}
	defer log.Close()
	if _, err := log.Seek(offset, io.SeekStart); err != nil {
		return said
	}
	written, err := io.ReadAll(log)
	if err != nil {
		return said
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(written), "\r\n", "\n")), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return strings.TrimPrefix(last, "goblins dev-drive move: ")
	}
	return said
}
