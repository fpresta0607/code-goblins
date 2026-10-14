// Command cg-ci-runner is the desktop CI runner's Windows service: it runs one
// GitHub Actions job at a time for one repository, in a Hyper-V VM put back
// to a clean checkpoint before every job, as the fleet asks for each through
// the machine's heavy line.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
)

const (
	// logLimit is where the service's log is moved aside.
	logLimit = 10 << 20
	// stopLimit is how long the service may take to stop: a job's cleanup,
	// and some more.
	stopLimit = 3 * time.Minute
	// startLimit is how long the service may take to put back what a stop
	// mid-job left, before it serves.
	startLimit = 5 * time.Minute
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, thisMachine()))
}

// serviceFolder holds the service's settings, the App's key, its log and
// each job's DVD.
func serviceFolder() string {
	return filepath.Join(os.Getenv("ProgramData"), "Code Goblins", "ci-runner")
}

// handler runs the service for Windows' service manager.
type handler struct {
	dir string
}

// Execute puts back what a stop mid-job left, serves the fleet until Windows
// stops the service, and then ends the job that runs, if one does, and waits
// for its cleanup.
func (h handler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending, WaitHint: uint32(startLimit.Milliseconds())}
	log, err := openLog(filepath.Join(h.dir, "ci-runner.log"), logLimit)
	if err != nil {
		return true, 1
	}
	defer log.Close()
	service, config, err := cirunner.NewService(h.dir, log.Say)
	if err != nil {
		log.Say("the service cannot start: " + err.Error())
		return true, 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starting, started := context.WithTimeout(ctx, startLimit)
	err = service.Start(starting)
	started()
	if err != nil {
		log.Say("the service cannot put back the VM and its runners: " + err.Error())
		return true, 3
	}
	served := make(chan error, 1)
	go func() { served <- cirunner.Serve(ctx, service, cirunner.PipeName, config.Allowed) }()
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	log.Say(fmt.Sprintf("the service started for %s/%s", config.Owner, config.Repo))
	for {
		select {
		case err := <-served:
			log.Say(fmt.Sprintf("the pipe stopped serving: %v", err))
			return true, 4
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending, WaitHint: uint32(stopLimit.Milliseconds())}
				cancel()
				<-served
				log.Say("the service stopped")
				return false, 0
			}
		}
	}
}
