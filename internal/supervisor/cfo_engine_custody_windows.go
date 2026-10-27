package supervisor

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

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
	return cfoJobBlocking(members, services, record.ChildPID, record.HostPID, proc.Arguments)
}

func cfoJobBlocking(members []proc.Entry, services map[int]proc.Service, cfoPID, hostPID int, arguments func(int) ([]string, error)) (string, error) {
	for _, member := range members {
		if member.PID == cfoPID || services[member.PID] != proc.NoService {
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
