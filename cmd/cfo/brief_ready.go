package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func runBriefReady(args []string, stdout, stderr io.Writer) (code int) {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: cfo brief ready <id>")
		return 2
	}
	id := args[0]
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	queueLock := ".queued-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(h.State, queueLock); err != nil {
		fmt.Fprintf(stderr, "cfo brief ready: %v\n", err)
		return 1
	}
	defer func() {
		if err := lock.ReleaseExclusiveNamed(h.State, queueLock); err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
		}
	}()
	for _, path := range []string{state.TaskMetaPath(h.State, id), state.StatusPath(h.State, id), filepath.Join(h.State, "outcomes", id+".json")} {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(stderr, "cfo brief ready: refuse to finalize a task with existing execution state: %s\n", path)
			return 1
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	archived, err := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, entry := range archived {
		if strings.HasPrefix(entry.Name(), id+".") {
			fmt.Fprintln(stderr, "cfo brief ready: refuse to finalize an archived task")
			return 1
		}
	}
	if _, err := fleet.ReadQueuedTask(h, id); err != nil {
		fmt.Fprintf(stderr, "cfo brief ready: %v\n", err)
		return 1
	}
	path := filepath.Join(h.Data, id, "brief.md")
	original, err := fsx.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	finalized, err := fleet.FinalizeBrief(original)
	if err != nil {
		fmt.Fprintf(stderr, "cfo brief ready: %v\n", err)
		return 1
	}
	if !bytes.Equal(original, finalized) {
		current, err := fsx.ReadFile(path)
		if err != nil || !bytes.Equal(original, current) {
			fmt.Fprintln(stderr, "cfo brief ready: brief changed while finalizing; review it and retry")
			return 1
		}
		if err := fsx.AtomicWriteFile(path, finalized); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	fmt.Fprintln(stdout, path)
	return 0
}
