package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

const servicesUsage = `usage: cfo services up <project> --task <id> [--wait <duration>]
       cfo services down <project> --task <id>

up starts the local services the project declares in
data/projects/<project>/services.json for the task's full-stack check, or
shares them with the tasks that already hold them up. A start first takes the
machine's one turn for heavy runs, the line cfo gate turn stands in, and
waits for it with no limit of its own. Then it starts anything only while
free memory and commit stay above the 4 GB floor with the stack's measured
cost added. Until they would, it says why and waits, for up to --wait (30m by
default, 0 refuses at once).

It starts the Docker engine first when the engine is not running. Docker
Desktop opens its dashboard window as it starts while its setting "Open the
Docker Dashboard when Docker Desktop starts" is on, so cfo closes that one
window, found by the process that start started, before it shows, and
Docker Desktop runs on in the tray. cfo changes no Docker Desktop setting.

While compose up builds and starts the stack, cfo reads memory and commit
every 2 seconds and hands the engine's file cache back every 10 seconds. The
first reading under the 4 GB floor ends compose up with everything it
started, and nothing else, and the start then stops what it started.

A start whose compose up fails first saves, for each container that ran and
stopped, keeps restarting or runs without passing its health check, its
state from docker inspect with its health check's last output and the end of
its docker logs, to services-<project>-<time>.log in the task's folder, and
its failure names that file. Then it stops what it started.

down releases the task's hold. The last release stops what cfo started, and
the engine when cfo started it. cfo runtime shows each stack, who holds it and
what it costs.
`

// servicesWait is how long cfo services up waits for memory by default.
const servicesWait = 30 * time.Minute

// runServices starts or releases a project's local services for a task.
func runServices(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || (args[0] != "up" && args[0] != "down") {
		fmt.Fprint(stderr, servicesUsage)
		return 2
	}
	verb := args[0]
	flags := flag.NewFlagSet("cfo services "+verb, flag.ContinueOnError)
	flags.SetOutput(stderr)
	task := flags.String("task", "", "the task that holds the services")
	wait := servicesWait
	if verb == "up" {
		flags.DurationVar(&wait, "wait", servicesWait, "how long to wait for memory before giving up, 0 to refuse at once")
	}
	var positional []string
	rest := args[1:]
	for len(rest) > 0 {
		if err := flags.Parse(rest); err != nil {
			return 2
		}
		rest = flags.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if len(positional) != 1 || *task == "" || wait < 0 {
		fmt.Fprint(stderr, servicesUsage)
		return 2
	}
	if err := state.ValidTaskID(*task); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if runtime.resolveHome == nil || runtime.projectServices == nil {
		fmt.Fprintln(stderr, "cfo services: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "cfo services: not a primary home")
		return 1
	}
	checkout, err := runtime.resolveProject(positional[0])
	if err != nil {
		fmt.Fprintln(stderr, "cfo services:", err)
		return 1
	}
	if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "cfo services: %s is not a project checkout on this machine\n", checkout)
		return 1
	}
	service := runtime.projectServices(h, stdout)
	ctx := context.Background()
	var line string
	if verb == "up" {
		line, err = service.Up(ctx, checkout, *task, wait)
	} else {
		line, err = service.Down(ctx, checkout, *task)
	}
	if err != nil {
		fmt.Fprintln(stderr, "cfo "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "cfo services: "+line)
	return 0
}

// defaultProjectServices is the production services of a home: the docker
// command and this machine's desktop, the machine's memory beside the
// fleet's floor, the machine's one line for heavy runs, and the home's task
// records. A start reads memory every 2 seconds while it builds and hands
// the engine's file cache back every 10, as cg-services-pd-build's watch did
// on 2026-10-11.
func defaultProjectServices(h home.Home, progress io.Writer) services.Service {
	commands := execx.OSRunner{}
	return services.Service{
		StateDir: h.State,
		DataDir:  h.Data,
		Docker:   services.CLI{Commands: commands, Desktop: services.WindowsDesktop, Out: progress},
		Commands: commands,
		Memory: func() (services.Memory, error) {
			memory, err := supervisor.MachineMemory()
			return services.Memory{Available: memory.Available, CommitAvailable: memory.CommitAvailable}, err
		},
		Floor:  supervisor.MemoryFloor,
		IsLive: services.LiveIn(h.State),
		Now:    time.Now,
		Sleep:  time.Sleep,
		Poll:   15 * time.Second,
		Out:    progress,
		Turn: func(ctx context.Context, who string) (func(), error) {
			turn, err := takeGateTurn(ctx, "cfo services", progress, progress, supervisor.MachineMemory, who, 0, verify.NoLimit)
			return func() { _ = turn.Release() }, err
		},
		FloorEvery:  2 * time.Second,
		ReliefEvery: 10 * time.Second,
	}
}
