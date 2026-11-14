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

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// gateCarryLog is where the supervisor's own carry of the gate's home writes
// what it says, in the home's state.
const gateCarryLog = "dev-drive-move.log"

// boardDevDrive is the Dev Drive setting of the board serving h on this
// machine: it reads the machine and where the gate's home is as runtime
// does, counts the gate runs in flight in the database of the gate home it
// is asked about, read in process, none in a home with no database yet
// (hasGateDatabase), and carries the gate's home with this
// build's goblins dev-drive move, started through start.
func boardDevDrive(h home.Home, runtime commandRuntime, start func(executable, dir, logPath string, args ...string) (*exec.Cmd, error)) *supervisor.DevDrive {
	return &supervisor.DevDrive{Read: runtime.readDevDrive, GateHome: runtime.gateHome, Boot: devdrive.BootTime,
		StartTask:   func(ctx context.Context) error { return devdrive.StartTask(ctx, execx.OSRunner{}) },
		TaskCurrent: func(ctx context.Context) (bool, error) { return devdrive.TaskCurrent(ctx, execx.OSRunner{}) },
		GateRuns: func(ctx context.Context, root string) (int, error) {
			if !hasGateDatabase(root) {
				return 0, nil
			}
			return pipeline.Reader{Root: root, Commands: gatedb.Runner{Next: execx.OSRunner{}}}.InFlight(ctx)
		},
		CarryGateHome: carryGateHomeFromTheBoard(h, start)}
}

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

// moveSays starts each line goblins dev-drive move says of its own failure.
const moveSays = "goblins dev-drive move: "

// lastSaid is what a move last said in its log past offset, as one line: all
// it wrote from the last line it began in its own name, without that name,
// since what it says of one failure can run over several lines; its last
// line when it named itself in none; or its exit code when it wrote nothing.
func lastSaid(logPath string, offset int64, exitCode int) string {
	said := fmt.Sprintf("goblins dev-drive move exited %d and said nothing", exitCode)
	log, err := fsx.Open(logPath)
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
	from := len(lines) - 1
	for index, line := range lines {
		if strings.HasPrefix(line, moveSays) {
			from = index
		}
	}
	var last []string
	for _, line := range lines[from:] {
		if line = strings.TrimSpace(line); line != "" {
			last = append(last, line)
		}
	}
	if len(last) == 0 {
		return said
	}
	return strings.TrimPrefix(strings.Join(last, "; "), moveSays)
}
