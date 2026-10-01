package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func runConnectionRepair(args []string, stdout, stderr io.Writer) int {
	if len(args) != 4 {
		fmt.Fprintln(stderr, "usage: cfo connection-repair <task> <generation> <connection> <action>")
		return 2
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, "Connection home is unavailable.")
		return 1
	}
	meta, err := state.ReadTaskMeta(h.State, args[0])
	if err != nil || meta.SpawnGen != args[1] {
		fmt.Fprintln(stderr, "Task changed. Refresh connections.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := connections.NewInspector(h.Data, h.State).RepairConnection(ctx, meta, args[2], args[3]); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Sign-in completed. Rechecking the connection.")
	return 0
}
