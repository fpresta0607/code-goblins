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

// Preflight satisfies spawn.AuthPreflight.
func (p SpawnPreflight) Preflight(ctx context.Context, project string) (map[string]string, string, error) {
	manifest, err := LoadManifest(p.DataDir, project)
	if errors.Is(err, fs.ErrNotExist) {
		// Most projects declare nothing. That is not a fault, and a spawn
		// must not be held up by it.
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	store, err := OpenStore()
	if err != nil {
		return nil, "", err
	}
	// Adopting is safe to do unattended: it only fills credentials that are
	// absent, from files and tools already on this machine.
	if _, err := Discover(ctx, store, p.Runner, manifest, project); err != nil {
		return nil, "", err
	}
	report, err := Checker{Store: store, Runner: p.Runner}.Check(ctx, manifest)
	if err != nil {
		return nil, "", err
	}
	env, err := InjectEnv(store, manifest, report)
	if err != nil {
		return nil, "", err
	}
	return env, WarningLine(project, report), nil
}

// WarningLine summarizes a preflight in one line for the spawn output, so a
// red service reaches the CFO at dispatch rather than the goblin mid-task. A
// clean preflight still reports what was injected, because silence would be
// indistinguishable from "no manifest".
func WarningLine(project string, report Report) string {
	green := 0
	for _, status := range report.Statuses {
		if status.Green() {
			green++
		}
	}
	blocking := report.Blocking()
	if len(blocking) == 0 {
		return fmt.Sprintf("auth: %d/%d services green for %s", green, len(report.Statuses), ProjectName(project))
	}
	names := make([]string, 0, len(blocking))
	for _, status := range blocking {
		names = append(names, fmt.Sprintf("%s (%s)", status.Service, status.State))
	}
	return fmt.Sprintf("auth: %d/%d services green for %s; BLOCKING: %s - run `cfo auth %s --fix`",
		green, len(report.Statuses), ProjectName(project), strings.Join(names, ", "), project)
}
