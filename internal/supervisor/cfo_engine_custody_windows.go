package supervisor

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// cfoHarnessLaunch is how long after it starts what the CFO's harness starts
// still belongs to launching it, its MCP servers the common case
// (fleettree.HarnessLaunch). A test shortens it to tell an idle terminal from
// one holding work.
var cfoHarnessLaunch = fleettree.HarnessLaunch

// cfoJobActivity names the work the CFO's terminal would lose if it closed,
// and names nothing while it holds none. The terminal's job is what closing
// it ends, and the harness itself is in that job: a Codex or pi CFO's program
// is cmd running an npm .cmd shim, which starts the node launcher that starts
// the harness. Nothing in a process's name or tree tells that chain from a
// command the harness started, so each member is placed against the harness
// the terminal's program runs (fleettree.HarnessCustody), and only work, or a
// process nothing can place, is named. A machine service is spared by the
// close itself, and what runs beside it in the job is spared no such way.
func cfoJobActivity(record host.Record) (string, error) {
	members, err := proc.CustodyJobProcesses(record.HostPID)
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(members, func(member proc.Entry) bool {
		return member.PID == record.ChildPID && member.Start.Equal(record.ChildStart)
	}) {
		return "", fmt.Errorf("the CFO's terminal job cannot be verified")
	}
	services, err := proc.RunningServicesAmong(members)
	if err != nil {
		return "", err
	}
	processes, err := fleettree.Processes()
	if err != nil {
		return "", err
	}
	pids := make([]int, 0, len(members))
	for _, member := range members {
		pids = append(pids, member.PID)
	}
	custody, placed := fleettree.HarnessCustody(record.ChildPID, pids, processes, cfoHarnessLaunch)
	if !placed {
		return "", fmt.Errorf("the CFO's terminal program (pid %d) is not running, so what its terminal holds cannot be verified", record.ChildPID)
	}
	return cfoJobBlocking(members, services, custody, record.HostPID, proc.Arguments)
}

func cfoJobBlocking(members []proc.Entry, services map[int]proc.Service, custody map[int]fleettree.Custody, hostPID int, arguments func(int) ([]string, error)) (string, error) {
	for _, member := range members {
		if services[member.PID] != proc.NoService || custody[member.PID] == fleettree.CustodyHarness {
			continue
		}
		if member.ParentPID == hostPID {
			args, err := arguments(member.PID)
			if err == nil && len(args) == 1 && args[0] == "cfo-conpty-waker" {
				continue
			}
		}
		name := filepath.Base(member.ExeBase)
		if name == "." || name == "" {
			name = "process"
		}
		return fmt.Sprintf("Waiting for %s (pid %d) in the CFO's terminal to finish", name, member.PID), nil
	}
	return "", nil
}
