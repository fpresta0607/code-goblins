package conpty

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// endJobFor bounds how long endJob keeps ending a job's processes one by one
// while a machine service is spared.
const endJobFor = 5 * time.Second

// endJob ends every process in job, as TerminateJobObject does, except a
// machine service started in the terminal and what runs under it
// (proc.Service): Docker Desktop or the no-mistakes daemon a goblin started
// serves the whole machine, and closing the goblin's terminal must not end
// it. With one there, the job stops ending its processes when its last handle
// closes, so the service outlives the console, and the rest are ended one by
// one until none is left.
func endJob(job windows.Handle) error {
	spared, rest, err := jobMembers(job)
	if err != nil || len(spared) == 0 {
		return errors.Join(err, windows.TerminateJobObject(job, 1))
	}
	if err := proc.KeepJobOnClose(job); err != nil {
		return err
	}
	for deadline := time.Now().Add(endJobFor); ; time.Sleep(50 * time.Millisecond) {
		for _, pid := range rest {
			proc.EndJobMember(job, pid)
		}
		if _, rest, err = jobMembers(job); err != nil {
			return err
		}
		if len(rest) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("conpty: the terminal's processes %v outlived it beside the machine services it keeps running", rest)
		}
	}
}

// jobMembers splits the processes in job into the machine services, with
// what runs under them, and the rest.
func jobMembers(job windows.Handle) (spared, rest []int, err error) {
	members, err := proc.JobMembers(job)
	if err != nil {
		return nil, nil, err
	}
	services, err := proc.RunningServices()
	if err != nil {
		return nil, nil, err
	}
	for _, pid := range members {
		if services[pid] != proc.NoService {
			spared = append(spared, pid)
		} else {
			rest = append(rest, pid)
		}
	}
	slices.Sort(rest)
	return spared, rest, nil
}
