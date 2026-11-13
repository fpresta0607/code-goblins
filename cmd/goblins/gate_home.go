package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/installbase"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
)

// gateHomeTries and gateHomeEvery bound how long goblins dev-drive move waits for
// the gate runs in flight to finish before the gate's home moves: half an
// hour, asked every half minute. A move that gives up leaves everything as it
// was, and the board carries the gate's home itself once no gate run is in
// flight.
const (
	gateHomeTries = 60
	gateHomeEvery = 30 * time.Second
)

// gateDaemon is no-mistakes' daemon of one gate home, each command run with
// that home as NM_HOME and without no-mistakes' own update check, which runs
// a program of its own in the background.
type gateDaemon struct {
	commands execx.Runner
}

// Stop stops root's daemon with daemon stop, which no-mistakes refuses while
// a gate run is in flight. daemon uninstall is no stop here: off macOS it
// changes nothing and exits 0, so a move that asked it never waited (#2525).
func (d gateDaemon) Stop(ctx context.Context, root string) error {
	return d.run(ctx, root, "daemon", "stop")
}

// Start starts root's daemon. no-mistakes refuses to start one that runs
// already, which is the daemon started all the same.
func (d gateDaemon) Start(ctx context.Context, root string) error {
	err := d.run(ctx, root, "daemon", "start")
	if err == nil {
		return nil
	}
	if daemon, readErr := d.reader(root).Daemon(ctx); readErr == nil && daemon.State == pipeline.DaemonAlive {
		return nil
	}
	return err
}

func (d gateDaemon) Hold(ctx context.Context, root string) (func() error, error) {
	return d.reader(root).Idle(ctx)
}

func (d gateDaemon) Forget(ctx context.Context, root string) error {
	return forgetGateDaemonTask(ctx, d.commands, root)
}

func (d gateDaemon) reader(root string) pipeline.Reader {
	return pipeline.Reader{Root: root, Commands: gatedb.Runner{Next: d.commands}}
}

func (d gateDaemon) run(ctx context.Context, root string, args ...string) error {
	result, err := d.commands.Run(ctx, execx.Request{Name: "no-mistakes", Args: args, Env: append(pipeline.NativeEnv(root), "NO_MISTAKES_NO_UPDATE_CHECK=1")})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		said := strings.TrimSpace(string(result.Stderr))
		if said == "" {
			said = strings.TrimSpace(string(result.Stdout))
		}
		return fmt.Errorf("no-mistakes %s exited %d: %s", strings.Join(args, " "), result.ExitCode, said)
	}
	return nil
}

// gateHomeLine says where the gate's home is against the Dev Drive folder
// devDrive, empty without one, for goblins dev-drive and goblins doctor.
func gateHomeLine(runtime commandRuntime, devDrive string) string {
	root, err := runtime.gateHome()
	if err != nil {
		return "gate home: unreadable (" + err.Error() + ")"
	}
	return "gate home: " + gatehome.Read(root, devDrive).Line()
}

// placeGateHome puts the gate's home on the Dev Drive the home at root has its
// heavy folders on, for goblins install, which runs before no-mistakes is
// installed: on a machine whose gate has no home yet it is named there from
// the start, and one elsewhere moves there unless a gate run is in flight,
// which it does not wait for. A home without a Dev Drive changes nothing.
func placeGateHome(root string, stdout io.Writer, runtime commandRuntime) {
	config, err := home.ReadDevDriveConfig(root)
	if err != nil || config.Root == "" {
		return
	}
	if _, err := carryGateHomeOnto(config.Root, stdout, runtime, 1, 0); err != nil {
		fmt.Fprintf(stdout, "%s The gate's home did not move to the Dev Drive (%v); the board's Dev Drive row says what it waits on.\n", notePrefix, err)
	}
}

// carryGateHome moves the gate's home at old into the Dev Drive folder
// devDrive, asking the daemon to stop and be held off up to tries times,
// every apart, and saying on out each time it waits. It says whether the
// gate's home moved or was placed there.
func carryGateHome(ctx context.Context, old, devDrive string, tries int, every time.Duration, out io.Writer) (string, bool, error) {
	return carryGateHomeWith(ctx, execx.OSRunner{}, old, devDrive, tries, every, out)
}

// carryGateHomeWith is carryGateHome running every program through commands.
func carryGateHomeWith(ctx context.Context, commands execx.Runner, old, devDrive string, tries int, every time.Duration, out io.Writer) (string, bool, error) {
	mover := gatehome.Mover{Daemon: gateDaemon{commands: commands}, Env: installbase.NewEnvStore(commands), Git: commands, Tries: tries, Every: every, Now: time.Now,
		Waiting: func(err error) {
			fmt.Fprintf(out, "gate home: waiting for the gate runs in flight to finish before it moves (%v)\n", err)
		}}
	return mover.Move(ctx, old, devDrive)
}
