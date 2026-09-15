package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/retention"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
)

func runRetention(args []string, out, errs io.Writer, runtime commandRuntime) int {
	if len(args) < 1 || len(args) > 2 || (len(args) == 2 && args[1] != "--apply" && args[1] != "--dry-run") {
		fmt.Fprintln(errs, "cfo retention <id> [--dry-run|--apply]")
		return 2
	}
	if err := state.ValidTaskID(args[0]); err != nil {
		fmt.Fprintln(errs, "retention: invalid task id")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(errs, "retention: primary home required")
		return 1
	}
	meta, err := state.ReadTaskMeta(h.State, args[0])
	if errors.Is(err, os.ErrNotExist) {
		var receipt taskcontext.Retirement
		receipt, err = taskcontext.ReadRetirement(h, args[0])
		meta = receipt.Meta
	}
	if err != nil {
		fmt.Fprintln(errs, "retention: task metadata unavailable; retained context is preserved")
		return 1
	}
	commands := execx.OSRunner{}
	client := &herdr.Client{Commands: commands, Session: meta.HerdrSession}
	service := retention.Service{Home: h, Commands: commands, Prober: monitor.NewHerdrProber(client), Processes: reap.CIMProcesses{Commands: commands}}
	entries, err := service.Run(context.Background(), args[0], len(args) == 2 && args[1] == "--apply")
	if err == nil {
		err = json.NewEncoder(out).Encode(entries)
	}
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	return 0
}
