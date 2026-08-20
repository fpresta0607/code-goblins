package auth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// SpawnPreflight is the spawn-time half of the auth contract: it adopts
// credentials the machine already holds, probes the project's services, and
// returns the environment a goblin's pane should inherit.
//
// It deliberately does not run login commands or drive a browser. Those are
// what `cfo auth <project> --fix` is for: a dispatch should not silently open
// an OAuth window or re-authenticate a CLI as a side effect.
type SpawnPreflight struct {
	DataDir string
	Runner  execx.Runner
}

// Result is what a spawn needs from the preflight: what to inject, what to
// print, and whether a blocking service means this goblin must not start.
type Result struct {
	// Env is the credentials the pane inherits.
	Env map[string]string
	// Warning is the one-line summary printed after the spawn line.
	Warning string
	// Refusal is the multi-line reason a spawn must stop, with the exact
	// command that fixes each blocking service. Empty when nothing blocks.
	Refusal string
}

// Preflight satisfies spawn.AuthPreflight.
func (p SpawnPreflight) Preflight(ctx context.Context, project string) (Result, error) {
	manifest, err := LoadManifest(p.DataDir, project)
	if errors.Is(err, fs.ErrNotExist) {
		// Most projects declare nothing. That is not a fault, and a spawn
		// must not be held up by it.
		return Result{}, nil
	}
	if err != nil {
		return Result{}, err
	}
	store, err := OpenStore()
	if err != nil {
		return Result{}, err
	}
	scope := ProjectName(project)
	// Adopting is safe to do unattended: it only fills credentials that are
	// absent, from files and tools already on this machine, into this
	// project's own scope.
	if _, err := Discover(ctx, store, p.Runner, manifest, project); err != nil {
		return Result{}, err
	}
	report, err := Checker{Store: store, Runner: p.Runner, Project: scope}.Check(ctx, manifest)
	if err != nil {
		return Result{}, err
	}
	env, err := InjectEnv(store, scope, manifest, report)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Env:     env,
		Warning: WarningLine(project, report),
		Refusal: RefusalLines(project, report),
	}, nil
}

// WarningLine summarizes a preflight in one line for the spawn output, so a
// red service reaches the CFO at dispatch rather than the goblin mid-task. A
// clean preflight still reports what was injected, because silence would be
// indistinguishable from "no manifest".
func WarningLine(project string, report Report) string {
	green := 0
	unverified := 0
	for _, status := range report.Statuses {
		switch status.State {
		case StateGreen:
			green++
		case StateUnverified:
			unverified++
		}
	}
	line := fmt.Sprintf("auth: %d/%d services green for %s", green, len(report.Statuses), ProjectName(project))
	if unverified > 0 {
		// A credential nothing could confirm is injected, so say it was not
		// confirmed rather than letting the green count imply it was.
		line += fmt.Sprintf(", %d unverified", unverified)
	}
	blocking := report.Blocking()
	if len(blocking) == 0 {
		return line
	}
	names := make([]string, 0, len(blocking))
	for _, status := range blocking {
		names = append(names, fmt.Sprintf("%s (%s)", status.Service, status.State))
	}
	return fmt.Sprintf("%s; BLOCKING: %s - run `cfo auth %s --fix`", line, strings.Join(names, ", "), project)
}

// RefusalLines is what a spawn prints instead of dispatching: every blocking
// service, the fact that was actually established about it, and the exact
// command that fixes it. It is empty when nothing blocks.
func RefusalLines(project string, report Report) string {
	blocking := report.Blocking()
	if len(blocking) == 0 {
		return ""
	}
	scope := ProjectName(project)
	var b strings.Builder
	fmt.Fprintf(&b, "%d blocking service(s) for %s; fix these or pass --yolo to dispatch anyway", len(blocking), scope)
	for _, status := range blocking {
		fmt.Fprintf(&b, "\n  %s (%s): %s", status.Service, status.State, status.Detail)
		for _, name := range status.Missing {
			fmt.Fprintf(&b, "\n    %s", StoreCommand(scope, name))
		}
		if len(status.Missing) == 0 {
			fmt.Fprintf(&b, "\n    cfo auth %s --fix", project)
		}
	}
	return b.String()
}
