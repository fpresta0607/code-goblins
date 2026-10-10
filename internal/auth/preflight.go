package auth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// SpawnPreflight is the spawn-time half of the auth contract: it adopts
// credentials the machine already holds, probes the services a task carries,
// and returns the environment that task's terminal should inherit.
//
// It deliberately does not run login commands or drive a browser. Those are
// what `cfo auth <project> --fix` is for: a dispatch should not silently open
// an OAuth window or re-authenticate a CLI as a side effect.
type SpawnPreflight struct {
	DataDir string
	// Caches is the home's shared package-cache folder, Home.Caches(), every
	// goblin builds against: in the home, or on the Dev Drive its heavy
	// folders moved to. It is separate from DataDir because the caches are a
	// property of the machine, not of any project's manifest.
	Caches string
	Runner execx.Runner
}

// Result is what a spawn needs from the preflight: what to inject, what to
// print, and whether a blocking service means this goblin must not start.
type Result struct {
	// Env is the credentials the terminal inherits: the variables of the
	// services in Grant.Services that are usable, and nothing else the store
	// holds.
	Env map[string]string
	// Grant is the task's need resolved against the manifest: the services it
	// carries, the ones withheld from it, and any it named that the manifest
	// does not declare.
	Grant Grant
	// Undeclared are the names stored in the project's scope that no service
	// declares, which reach no terminal.
	Undeclared []string
	// WithheldNames are the variables the terminal must not hold from
	// anywhere: every name the manifest reads for a service the task does
	// not carry, and every undeclared stored name. A terminal starts from
	// the user's own environment, which may set one of them, so the launch
	// leaves them out of it.
	WithheldNames []string
	// Caches is the shared package-cache redirects the pane inherits. They
	// are kept apart from Env because they are not secrets: they travel in
	// the launch environment rather than through the restricted file the
	// credentials use, and they apply to a project that declares nothing.
	Caches map[string]string
	// Warning is the one-line summary printed after the spawn line.
	Warning string
	// Refusal is the multi-line reason a spawn must stop, with the exact
	// command that fixes each blocking service. Empty when nothing blocks.
	Refusal string
}

// Preflight satisfies spawn.AuthPreflight. need names the services whose
// credentials the task's terminal carries. The manifest's other services are
// neither injected nor probed, so a task is never held up by, and no
// credential is used for, a service it does not carry.
func (p SpawnPreflight) Preflight(ctx context.Context, project string, need Need) (Result, error) {
	// The caches belong to the machine, so they are prepared before anything
	// is read about the project: a project that declares no credentials still
	// builds against the shared store rather than downloading its own copy.
	caches := CacheEnv(p.Caches)
	manifest, err := LoadManifest(p.DataDir, project)
	declared := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{}, err
	}
	store, err := OpenStore()
	if err != nil {
		return Result{}, err
	}
	scope := ProjectName(project)
	grant := manifest.Grant(need)
	if !declared {
		// Most projects declare nothing. That is not a fault, and a spawn
		// must not be held up by it. A project with no manifest has no
		// service to name, so what the operator stored in its scope reaches
		// no terminal: it is named, never injected.
		undeclared, err := UndeclaredNames(store, scope, Manifest{})
		if err != nil {
			return Result{}, err
		}
		return Result{Env: map[string]string{}, Grant: grant, Undeclared: undeclared, WithheldNames: undeclared, Caches: caches}, nil
	}
	// Adopting is safe to do unattended: it reads only files and tools
	// already on this machine, and writes only into this project's own scope.
	// The one value it replaces is one this project's own gitignored .env has
	// changed since the last scan read it, which is the Overlord rotating a
	// credential. It keeps the whole manifest's store current, whatever this
	// task carries.
	adopted, unscanned, err := Discover(ctx, store, p.Runner, manifest, project)
	if err != nil {
		return Result{}, err
	}
	// Migration runs after adoption, so a project's own .env is what answers
	// a name both could: the file is the current truth, and a value stored
	// before namespacing is only ever the older one.
	migrated, unreadable, err := Migrate(store, p.DataDir, project, manifest)
	if err != nil {
		return Result{}, err
	}
	adopted = append(adopted, migrated...)
	// The services checked are the ones whose verdict can change what this
	// task carries: its own, and any other that declares one of their names.
	report, err := Checker{Store: store, Runner: p.Runner, Project: scope}.Check(ctx, manifest.sharing(grant.Services))
	if err != nil {
		return Result{}, err
	}
	env, err := InjectEnv(store, scope, manifest.Only(grant.Services), report)
	if err != nil {
		return Result{}, err
	}
	undeclared, err := UndeclaredNames(store, scope, manifest)
	if err != nil {
		return Result{}, err
	}
	warning := WarningLine(project, report)
	if line := AdoptionLine(adopted); line != "" {
		warning += "; " + line
	}
	// A migration that declined has to say so on the same line an adoption
	// reports on, or the operator sees a credential that will not resolve and
	// no way from that symptom to the manifest that caused it.
	if line := MigrationPausedLine(unreadable); line != "" {
		warning += "; " + line
	}
	// A .env the scan declined to read is reported for the same reason:
	// otherwise a credential that never rotates looks exactly like one that
	// had nothing to rotate. Each cause is named separately because each
	// sends the operator somewhere else: git to investigate, a worktree to
	// return, or a file whose inspection failure is what needs explaining.
	if line := IgnoreScanFailedLine(unscanned.IgnoreUnknown); line != "" {
		warning += "; " + line
	}
	if line := WorktreeSharedLine(unscanned.WorktreeShared); line != "" {
		warning += "; " + line
	}
	if line := LinkCheckFailedLine(unscanned.LinkCheckFailed); line != "" {
		warning += "; " + line
	}
	withheldNames := append(manifest.withheldNames(grant.Services), undeclared...)
	slices.Sort(withheldNames)
	return Result{
		Env:           env,
		Grant:         grant,
		Undeclared:    undeclared,
		WithheldNames: withheldNames,
		Caches:        caches,
		Warning:       warning,
		Refusal:       RefusalLines(project, report),
	}, nil
}

// AdoptionLine reports what this preflight registered, by name and origin, so
// a credential the Overlord rotated is visibly picked up at dispatch rather
// than silently. A refresh is named separately from a first adoption because
// they mean different things: one filled an empty slot, the other replaced a
// value every goblin dispatched until now was carrying.
//
// A stored value a .env disagreed with on its first reading is named too: the
// file was passed over, and an edit that did not take must not look like one
// that did.
//
// Origins are file paths and tool names. No value appears here, and none ever
// may: this line goes to the spawn output, which `cfo peek` reads back and
// which lives in a pane's scrollback.
func AdoptionLine(adopted []Adopted) string {
	var refreshed, added, kept []string
	for _, item := range adopted {
		switch {
		case item.Kept:
			kept = append(kept, item.Name+" over "+item.Origin)
		case item.Refreshed:
			refreshed = append(refreshed, item.Name+" from "+item.Origin)
		default:
			added = append(added, item.Name+" from "+item.Origin)
		}
	}
	var parts []string
	if len(refreshed) > 0 {
		parts = append(parts, fmt.Sprintf("refreshed %d (%s)", len(refreshed), strings.Join(refreshed, ", ")))
	}
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("adopted %d (%s)", len(added), strings.Join(added, ", ")))
	}
	if len(kept) > 0 {
		parts = append(parts, fmt.Sprintf("kept %d stored over a .env value read for the first time (%s), edit that line to rotate", len(kept), strings.Join(kept, ", ")))
	}
	return strings.Join(parts, "; ")
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
	// A task that carries no service has none to count, and "0/0 green"
	// would read as a check that ran and found nothing wrong.
	if len(report.Statuses) == 0 {
		return "auth: no service's credentials for " + ProjectName(project)
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
	commands := make([]string, 0, len(blocking))
	seen := map[string]bool{}
	for _, status := range blocking {
		names = append(names, fmt.Sprintf("%s (%s)", status.Service, status.State))
		for _, command := range remedies(project, status) {
			if seen[command] {
				continue
			}
			seen[command] = true
			commands = append(commands, "`"+command+"`")
		}
	}
	return fmt.Sprintf("%s; BLOCKING: %s - run %s", line, strings.Join(names, ", "), strings.Join(commands, ", "))
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
		for _, command := range remedies(project, status) {
			fmt.Fprintf(&b, "\n    %s", command)
		}
	}
	return b.String()
}
