//go:build windows

package uninstall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// boardStopWait is how long the board has to stop when asked, as goblins
// stop gives it, before it is ended.
const boardStopWait = 30 * time.Second

// running are the programs of this home that run now, to be stopped first.
func (m Machine) running(ctx context.Context) ([]Item, error) {
	read := m.Processes
	if read == nil {
		read = readRunning
	}
	rest, err := read(ctx)
	if err != nil {
		return nil, fmt.Errorf("uninstall: read what runs: %w", err)
	}
	owners, _ := janitor.Owners(ctx, m.Home, nil)
	terminals := make([]Terminal, 0, len(owners))
	for _, owner := range owners {
		terminals = append(terminals, Terminal{ID: owner.ID, HostPID: owner.HostPID, Marks: owner.Marks, Directories: owner.Directories})
	}
	folders := []string{m.Home.Root, filepath.Join(m.LocalAppData, "cfo"), filepath.Join(m.LocalAppData, "CodeGoblinsWindow")}
	if m.Home.DevDrive != "" {
		folders = append(folders, m.Home.DevDrive)
	}
	var items []Item
	for _, program := range owned(rest, terminals, folders, os.Getpid()) {
		items = append(items, Item{Action: Stop, Group: GroupRunning, What: program.what, Where: "pid " + strconv.Itoa(program.PID), do: m.stopper(program)})
	}
	return items, nil
}

// stopper ends one program by its pid and creation time, so it never reaches
// a program that took the pid after it ended. The board is asked to stop
// first, as goblins stop asks it, and a terminal is closed through its host,
// which ends what runs in it.
func (m Machine) stopper(program ownedProgram) func(context.Context) error {
	identity := lifecycle.Identity{PID: program.PID, Started: program.Started}
	end := func(ctx context.Context) error {
		_, err := lifecycle.Terminate(ctx, identity)
		return err
	}
	switch {
	case program.kind == kindBoard:
		return func(ctx context.Context) error {
			if err := supervisor.RequestStop(m.Home.State, program.PID); err == nil && ended(identity, boardStopWait) {
				return nil
			}
			return end(ctx)
		}
	case program.terminal != "":
		return func(ctx context.Context) error {
			if record, err := host.ReadRecord(m.Home.State, program.terminal); err == nil && record.HostPID == program.PID {
				if host.Close(m.Home.State, record, 5*time.Second) == nil && ended(identity, 10*time.Second) {
					return nil
				}
			}
			return end(ctx)
		}
	}
	return end
}

// ended waits up to within for the process identity names to end, and
// reports whether it did.
func ended(identity lifecycle.Identity, within time.Duration) bool {
	for deadline := time.Now().Add(within); ; time.Sleep(200 * time.Millisecond) {
		started, known := proc.StartTime(identity.PID)
		if !known || !started.Equal(identity.Started) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// readRunning reads every program running on this machine, with the mark of
// the terminal each was started in and its full path.
func readRunning(ctx context.Context) ([]Running, error) {
	processes, err := lifecycle.ReadAllProcesses(ctx, true)
	if err != nil {
		return nil, err
	}
	running := make([]Running, 0, len(processes))
	for _, process := range processes {
		image := ""
		if identity, err := proc.Identify(process.PID, process.Started); err == nil {
			image = identity.Image
		}
		running = append(running, Running{Process: process, Image: image})
	}
	return running, nil
}
