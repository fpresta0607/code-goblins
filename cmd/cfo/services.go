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
)

const servicesUsage = `usage: cfo services up <project> --task <id> [--wait <duration>]
       cfo services down <project> --task <id>

up starts the local services the project declares in
data/projects/<project>/services.json for the task's full-stack check, or
shares them with the tasks that already hold them up. It starts the Docker
engine first when the engine is not running, and starts anything only while
free memory and commit stay above the 4 GB floor with the stack's measured
cost added. Until they would, it says why and waits, for up to --wait (30m by
default, 0 refuses at once).

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
// command, the machine's memory beside the fleet's floor, and the home's
// task records.
func defaultProjectServices(h home.Home, progress io.Writer) services.Service {
	commands := execx.OSRunner{}
	return services.Service{
		StateDir: h.State,
		DataDir:  h.Data,
		Docker:   services.CLI{Commands: commands},
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
	}
}
