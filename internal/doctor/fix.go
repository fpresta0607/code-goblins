package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// npmTimeout bounds one npm install, which downloads a harness's platform
// build of 100 MB or more.
const npmTimeout = 15 * time.Minute

// Fixer installs a harness's newest version as its own installer does, once
// that version has proved it starts the way the fleet starts goblins, and only
// while nothing runs from the install it replaces: npm cannot replace a
// program that runs, and a goblin, a gate agent or a session of the
// Overlord's on it would lose its harness mid-turn.
type Fixer struct {
	// Commands runs npm.
	Commands execx.Runner
	// RunningFrom lists the processes running from folder, by pid.
	RunningFrom func(folder string) ([]int, error)
	// Prove starts executable, the harness name's staged build, and returns
	// nil once the fleet's launch automation brings it to its composer.
	Prove func(ctx context.Context, name, executable string) error
	// Probe asks the harness name on PATH for its version, as doctor does.
	Probe func(ctx context.Context, name string) HarnessProbe
	// Stage is the folder each new version is staged and proved in.
	Stage string
}

// Fixed is what one harness's fix came to: Outcome in a few words, and
// whether it failed. A fix that waits for a harness to stop running has not
// failed.
type Fixed struct {
	Name     string
	Version  string
	Outcome  string
	IsFailed bool
}

// Fix brings the harness version names up to its newest. A harness installed
// from npm is staged apart with npm's --prefix, proved, and then installed
// globally and probed again; a harness with an installer of its own, Claude
// Code, updates itself on its own channel, so only its command is named.
func (f Fixer) Fix(ctx context.Context, releases Releases, version HarnessVersion) Fixed {
	fixed := Fixed{Name: version.Name, Version: version.Newest}
	failed := func(outcome string) Fixed {
		fixed.Outcome, fixed.IsFailed = outcome, true
		return fixed
	}
	release := releases[version.Name]
	if release.Package == "" {
		fixed.Outcome = "Claude Code updates itself on its own channel and the board's Update restarts each session onto it; " + version.Update + " installs " + version.Newest + " now"
		return fixed
	}
	root, err := f.npm(ctx, "root", "-g")
	if err != nil {
		return failed("npm's global folder could not be read: " + err.Error())
	}
	folder := filepath.Join(strings.TrimSpace(root), filepath.FromSlash(release.Package))
	running, err := f.RunningFrom(folder)
	if err != nil {
		return failed("what runs from " + folder + " could not be read: " + err.Error())
	}
	if len(running) > 0 {
		pids := make([]string, len(running))
		for index, pid := range running {
			pids[index] = strconv.Itoa(pid)
		}
		fixed.Outcome = fmt.Sprintf("waits: %d processes run from %s (pid %s); run it again once they end", len(running), folder, strings.Join(pids, ", "))
		return fixed
	}
	stage := filepath.Join(f.Stage, version.Name+"-"+version.Newest)
	if err := os.RemoveAll(stage); err != nil {
		return failed("the stage " + stage + " could not be cleared: " + err.Error())
	}
	defer os.RemoveAll(stage)
	target := release.Package + "@" + version.Newest
	if _, err := f.npm(ctx, "install", "--prefix", stage, "--no-audit", "--no-fund", target); err != nil {
		return failed(version.Newest + " could not be staged: " + err.Error())
	}
	executable := filepath.Join(stage, "node_modules", ".bin", version.Name+".cmd")
	if _, err := os.Stat(executable); err != nil {
		return failed(version.Newest + " was staged without its program: " + err.Error())
	}
	if err := f.Prove(ctx, version.Name, executable); err != nil {
		return failed(version.Installed + " is kept: " + version.Newest + " did not reach its composer: " + err.Error())
	}
	if _, err := f.npm(ctx, "install", "-g", "--no-audit", "--no-fund", target); err != nil {
		return failed(version.Update + " " + err.Error())
	}
	if probe := f.Probe(ctx, version.Name); versionIn(probe.Detail) != version.Newest {
		return failed("installed, but " + version.Name + " --version answers " + probe.Detail)
	}
	fixed.Outcome = version.Newest + " installed after it reached its composer in a terminal of its own"
	return fixed
}

// npm runs npm with args and returns what it printed, or why it failed.
func (f Fixer) npm(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, npmTimeout)
	defer cancel()
	result, err := f.Commands.Run(ctx, execx.Request{Name: "npm.cmd", Args: args, KillTree: true})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		output := strings.TrimSpace(string(result.Stderr))
		if lines := strings.Split(output, "\n"); len(lines) > 5 {
			output = strings.Join(lines[len(lines)-5:], "\n")
		}
		return "", fmt.Errorf("failed (exit %d): %s", result.ExitCode, output)
	}
	return string(result.Stdout), nil
}

// RunningFrom lists, by pid, the processes running from folder: one whose
// program is in it, such as Codex's own codex.exe under its npm package, and
// one with an argument naming a file in it, as node runs pi's script. A
// process whose program or arguments cannot be read, such as a system one,
// is not this user's harness and is passed over.
func RunningFrom(folder string) ([]int, error) {
	processes, err := proc.Processes()
	if err != nil {
		return nil, err
	}
	inside := func(path string) bool {
		path = strings.ToLower(filepath.Clean(strings.Trim(path, `"`)))
		prefix := strings.ToLower(filepath.Clean(folder)) + `\`
		return strings.HasPrefix(path, prefix)
	}
	var running []int
	for _, process := range processes {
		start, isKnown := proc.StartTime(process.PID)
		if !isKnown {
			continue
		}
		identity, err := proc.Identify(process.PID, start)
		if err != nil {
			continue
		}
		isRunning := inside(identity.Image)
		for _, argument := range identity.Arguments {
			isRunning = isRunning || inside(argument)
		}
		if isRunning {
			running = append(running, process.PID)
		}
	}
	return running, nil
}
