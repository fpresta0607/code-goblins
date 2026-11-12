package supervisor

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// ceoHarnessLaunch is how long after it starts what the CEO's harness starts
// still belongs to launching it, its MCP servers the common case
// (fleettree.HarnessLaunch). A test shortens it to tell an idle terminal from
// one holding work.
var ceoHarnessLaunch = fleettree.HarnessLaunch

// ceoJobActivity names the work the CEO's terminal would lose if it closed,
// and names nothing while it holds none. The terminal's job is what closing
// it ends, and the harness itself is in that job: a Codex or pi CEO's program
// is cmd running an npm .cmd shim, which starts the node launcher that starts
// the harness. Nothing in a process's name or tree tells that chain from a
// command the harness started, so each member is placed against the harness
// the terminal's program runs (fleettree.HarnessCustody), and only work, or a
// process nothing can place, is named. A machine service is spared by the
// close itself, and what runs beside it in the job is spared no such way.
func ceoJobActivity(record host.Record) (string, error) {
	members, err := proc.CustodyJobProcesses(record.HostPID)
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(members, func(member proc.Entry) bool {
		return member.PID == record.ChildPID && member.Start.Equal(record.ChildStart)
	}) {
		return "", fmt.Errorf("the CEO's terminal job cannot be verified")
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
	custody, placed := fleettree.HarnessCustody(record.ChildPID, pids, processes, ceoHarnessLaunch)
	if !placed {
		return "", fmt.Errorf("the CEO's terminal program (pid %d) is not running, so what its terminal holds cannot be verified", record.ChildPID)
	}
	return ceoJobBlocking(members, services, custody, record.HostPID, proc.Arguments)
}

func ceoJobBlocking(members []proc.Entry, services map[int]proc.Service, custody map[int]fleettree.Custody, hostPID int, arguments func(int) ([]string, error)) (string, error) {
	for _, member := range members {
		if services[member.PID] != proc.NoService || custody[member.PID] == fleettree.CustodyHarness {
			continue
		}
		if member.ParentPID == hostPID {
			args, err := arguments(member.PID)
			if err == nil && len(args) == 1 && args[0] == "ceo-conpty-waker" {
				continue
			}
		}
		name := filepath.Base(member.ExeBase)
		if name == "." || name == "" {
			name = "process"
		}
		return fmt.Sprintf("Waiting for %s (pid %d) in the CEO's terminal to finish", name, member.PID), nil
	}
	return "", nil
}
