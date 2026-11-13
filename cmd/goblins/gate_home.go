package main

import (
	"context"
	"errors"
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

// gateHomeMoveLock is the file in the home's state that one move of the
// gate's home holds for its whole run, whichever gate home it reads, so a
// move the board or a terminal starts while another runs waits on it.
const gateHomeMoveLock = "gate-home-move.lock"

// gateDaemon is no-mistakes' daemon of one gate home, each command run with
// that home as NM_HOME and without no-mistakes' own update check, which runs
// a program of its own in the background.
type gateDaemon struct {
	commands execx.Runner
}

func (d gateDaemon) InFlight(ctx context.Context, root string) error {
	return gateRefusal(d.reader(root).AtRest(ctx))
}

// Stop stops root's daemon with daemon stop, which no-mistakes refuses while
// a gate run is in flight: a daemon stop that ran and exited nonzero is that
// refusal, and one that could not run is not. daemon uninstall is no stop
// here: off macOS it changes nothing and exits 0, so a move that asked it
// never waited (#2525).
func (d gateDaemon) Stop(ctx context.Context, root string) error {
	isRefused, err := d.run(ctx, root, "daemon", "stop")
	if isRefused {
		return gatehome.Refusal{Err: err}
	}
	return err
}

// Start starts root's daemon. no-mistakes refuses to start one that runs
// already, which is the daemon started all the same.
func (d gateDaemon) Start(ctx context.Context, root string) error {
	_, err := d.run(ctx, root, "daemon", "start")
	if err == nil {
		return nil
	}
	if daemon, readErr := d.reader(root).Daemon(ctx); readErr == nil && daemon.State == pipeline.DaemonAlive {
		return nil
	}
	return err
}

func (d gateDaemon) Hold(ctx context.Context, root string) (func() error, error) {
	release, err := d.reader(root).Idle(ctx)
	return release, gateRefusal(err)
}

// gateRefusal is err as a move takes it: a gate that is busy, with a run in
// flight or a daemon holding its lock, is the gate's own no, which the move
// waits on, and a gate that could not be read is not.
func gateRefusal(err error) error {
	if errors.Is(err, pipeline.ErrBusy) {
		return gatehome.Refusal{Err: err}
	}
	return err
}

func (d gateDaemon) Forget(ctx context.Context, root string) error {
	return forgetGateDaemonTask(ctx, d.commands, root)
}

func (d gateDaemon) reader(root string) pipeline.Reader {
	return pipeline.Reader{Root: root, Commands: gatedb.Runner{Next: d.commands}}
}

// run runs no-mistakes for root's home, and says whether its error is
// no-mistakes' own refusal, a command that ran and exited nonzero, and no
// command that could not run.
func (d gateDaemon) run(ctx context.Context, root string, args ...string) (bool, error) {
	result, err := d.commands.Run(ctx, execx.Request{Name: "no-mistakes", Args: args, Env: append(pipeline.NativeEnv(root), "NO_MISTAKES_NO_UPDATE_CHECK=1")})
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		said := strings.TrimSpace(string(result.Stderr))
		if said == "" {
			said = strings.TrimSpace(string(result.Stdout))
		}
		return true, fmt.Errorf("no-mistakes %s exited %d: %s", strings.Join(args, " "), result.ExitCode, said)
	}
	return false, nil
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

// placeGateHome puts the gate's home on the Dev Drive the home h has its
// heavy folders on, for goblins install, which runs before no-mistakes is
// installed: on a machine whose gate has no home yet it is named there from
// the start, and one elsewhere moves there unless a gate run is in flight,
// which it does not wait for. A home without a Dev Drive changes nothing.
func placeGateHome(h home.Home, stdout io.Writer, runtime commandRuntime) {
	config, err := home.ReadDevDriveConfig(h.Root)
	if err != nil || config.Root == "" {
		return
	}
	if _, err := carryGateHomeOnto(config.Root, h.State, stdout, runtime, 1, 0); err != nil {
		fmt.Fprintf(stdout, "%s The gate's home did not move to the Dev Drive (%v); the board's Dev Drive row says what it waits on.\n", notePrefix, err)
	}
}

// carryGateHome moves the gate's home at old into the Dev Drive folder
// devDrive, holding the move's lock file lock and asking the daemon to stop
// and be held off up to tries times each, every apart, and saying on out each
// time it waits. It says whether the gate's home moved or was placed there.
func carryGateHome(ctx context.Context, old, devDrive, lock string, tries int, every time.Duration, out io.Writer) (string, bool, error) {
	return carryGateHomeWith(ctx, execx.OSRunner{}, old, devDrive, lock, tries, every, out)
}

// carryGateHomeWith is carryGateHome running every program through commands.
func carryGateHomeWith(ctx context.Context, commands execx.Runner, old, devDrive, lock string, tries int, every time.Duration, out io.Writer) (string, bool, error) {
	mover := gatehome.Mover{Daemon: gateDaemon{commands: commands}, Lock: lock, Env: installbase.NewEnvStore(commands), Git: commands, Tries: tries, Every: every, Now: time.Now,
		Waiting: func(err error) {
			fmt.Fprintf(out, "gate home: waiting for the gate to rest before it moves (%v)\n", err)
		}}
	return mover.Move(ctx, old, devDrive)
}
