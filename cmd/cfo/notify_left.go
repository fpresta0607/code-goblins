package main

import (
	"errors"
	"os"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// defaultLeftRunning reads what task id's goblin started that still runs,
// placed as the supervisor's start watch places it, beside the report that
// asks (leftBeside). A task whose terminal does not run here, or that has
// no record, started nothing that is read.
func defaultLeftRunning(h home.Home, id string) ([]fleettree.Placed, error) {
	record, err := host.ReadRecord(h.State, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !host.Running(record) {
		return nil, nil
	}
	meta, err := state.ReadTaskMeta(h.State, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	reader := &fleettree.Reader{Owner: lifecycle.OwnerOf(h)}
	placed, err := reader.Placed([]fleettree.Goblin{{Meta: meta, HarnessPID: record.ChildPID, HarnessStarted: record.ChildStart}})
	if err != nil {
		return nil, err
	}
	return leftBeside(placed, os.Getpid()), nil
}

// leftBeside are the processes among placed that are left running beside
// the report process self makes: all but self, the processes it runs
// under, which end with its report, and a machine service, which serves
// every goblin.
func leftBeside(placed []fleettree.Placed, self int) []fleettree.Placed {
	byPID := make(map[int]fleettree.Placed, len(placed))
	for _, process := range placed {
		byPID[process.PID] = process
	}
	reporting := map[int]bool{}
	for pid := self; !reporting[pid]; {
		process, isPlaced := byPID[pid]
		if !isPlaced {
			break
		}
		reporting[pid] = true
		pid = process.ParentPID
	}
	return slices.DeleteFunc(slices.Clone(placed), func(process fleettree.Placed) bool {
		return reporting[process.PID] || process.Service
	})
}
