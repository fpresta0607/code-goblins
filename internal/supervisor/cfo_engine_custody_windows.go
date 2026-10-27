package supervisor

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func cfoJobActivity(record host.Record) (string, error) {
	members, err := proc.JobProcesses(record.HostPID)
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
	return cfoJobBlocking(members, services, record.ChildPID, proc.Arguments)
}

func cfoJobBlocking(members []proc.Entry, services map[int]proc.Service, cfoPID int, arguments func(int) ([]string, error)) (string, error) {
	for _, member := range members {
		if member.PID == cfoPID || services[member.PID] != proc.NoService {
			continue
		}
		switch strings.ToLower(filepath.Base(member.ExeBase)) {
		case "no-mistakes.exe":
			return "Waiting for the CFO's gate command to finish", nil
		case "cfo.exe", "goblins.exe":
			args, err := arguments(member.PID)
			if err != nil {
				return "", fmt.Errorf("cannot inspect a process in the CFO's terminal job")
			}
			if len(args) < 2 {
				return "", fmt.Errorf("cannot identify a process in the CFO's terminal job")
			}
			switch strings.ToLower(args[1]) {
			case "pipeline", "gate", "verify":
				return "Waiting for the CFO's gate command to finish", nil
			case "host":
				return "Waiting for a worker contained in the CFO's terminal to finish", nil
			}
		}
	}
	return "", nil
}
