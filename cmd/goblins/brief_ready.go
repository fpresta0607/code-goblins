package main

import (
	"bytes"
	"cmp"
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
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func runBriefReady(args []string, stdout, stderr io.Writer) (code int) {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: goblins brief ready <id>")
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
		fmt.Fprintf(stderr, "goblins brief ready: %v\n", err)
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
			fmt.Fprintf(stderr, "goblins brief ready: refuse to finalize a task with existing execution state: %s\n", path)
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
			fmt.Fprintln(stderr, "goblins brief ready: refuse to finalize an archived task")
			return 1
		}
	}
	queued, err := fleet.ReadQueuedTask(h, id)
	if err != nil {
		fmt.Fprintf(stderr, "goblins brief ready: %v\n", err)
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
		fmt.Fprintf(stderr, "goblins brief ready: %v\n", err)
		return 1
	}
	if !bytes.Equal(original, finalized) {
		current, err := fsx.ReadFile(path)
		if err != nil || !bytes.Equal(original, current) {
			fmt.Fprintln(stderr, "goblins brief ready: brief changed while finalizing; review it and retry")
			return 1
		}
		if err := fsx.AtomicWriteFile(path, finalized); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	// goblins brief writes the mode its flags select on the mode: line, which
	// spawn reads as a request, and the assessment is often recorded after it
	// (issue 1952). So readiness names the mode the posture and the finished
	// assessment select when the line asks another, for the line to be kept
	// or changed on purpose. Spawn reads a missing manifest the same way.
	manifest, _, err := projectcfg.LoadForCheckout(h.Data, queued.Row.Repo)
	if err != nil {
		manifest = projectcfg.Manifest{}
	}
	assessed := projectcfg.AssessDelivery(manifest, string(finalized))
	line := fleet.BriefDelivery(finalized)["mode"]
	authority, exceptionErr := projectcfg.ApprovedDeliveryException(h.Data, id, line, manifest)
	isApproved := authority != "" && projectcfg.SelectDelivery(manifest, string(finalized), "", authority).Mode == line
	if line != assessed.Mode && !isApproved {
		outcome := "or keep " + line + " on purpose"
		if line != "no-mistakes" {
			outcome = "since spawn starts no-mistakes without a delivery exception, and its automatic start waits until the registered primary CEO approves one with goblins spawn --delivery-exception"
		}
		if exceptionErr != nil {
			outcome += " (" + exceptionErr.Error() + ")"
		}
		fmt.Fprintf(stderr, "goblins brief ready: the brief's mode: line says %s, while the project's delivery posture (%s) and the brief's Delivery assessment select %s: %s. Change the line to %s, %s.\n", line, cmp.Or(assessed.Posture, "unset"), assessed.Mode, assessed.Reason, assessed.Mode, outcome)
	}
	fmt.Fprintln(stdout, path)
	return 0
}
