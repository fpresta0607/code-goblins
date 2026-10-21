package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// lockName serializes every start and release of this home's stacks, so two
// tasks asking at once share one stack instead of starting two.
const lockName = ".services.lock"

// sayEvery is how often a start waiting for memory says again why it waits.
const sayEvery = time.Minute

// Memory is the machine's available physical memory and commit, in bytes.
type Memory struct {
	Available       uint64
	CommitAvailable uint64
}

// Service starts, shares and stops the projects' local services for one
// CFO home.
type Service struct {
	StateDir string
	DataDir  string
	Docker   Docker
	// Commands runs a manifest's check.
	Commands execx.Runner
	// Memory reads the machine's memory. It is nil for a caller that only
	// stops stacks, such as the janitor, which then measures nothing.
	Memory func() (Memory, error)
	// Floor is the memory and commit the fleet keeps free.
	Floor uint64
	// IsLive reports whether a task has a live record.
	IsLive func(task string) bool
	Now    func() time.Time
	Sleep  func(time.Duration)
	// Poll is how often a start waiting for memory reads it again.
	Poll time.Duration
	// Out takes the lines a start says while it waits.
	Out io.Writer
	// Turn takes the machine's one turn for heavy runs, the line cfo gate
	// turn stands in, for the start who names, and returns what gives it
	// back.
	Turn func(ctx context.Context, who string) (release func(), err error)
	// FloorEvery is how often a start reads memory while its services
	// start, and ReliefEvery how often it hands the engine's file cache back.
	FloorEvery  time.Duration
	ReliefEvery time.Duration
}

// Up starts the project's stack for task, or adds task to its holders when
// it is up. A start first takes the machine's one turn for heavy runs, which
// it holds until it has started, so no test suite or other build starts
// beside its build. While the start would leave memory or commit under the
// floor it says why and waits, for up to wait, and then gives up saying why.
func (s Service) Up(ctx context.Context, checkout, task string, wait time.Duration) (string, error) {
	manifest, err := LoadManifest(s.DataDir, checkout)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("services: %s declares no local services: write %s", auth.ProjectName(checkout), ManifestPath(s.DataDir, checkout))
	}
	if err != nil {
		return "", err
	}
	if !s.IsLive(task) {
		return "", fmt.Errorf("services: %s is not a live task, so it cannot hold a stack", task)
	}
	if line, isJoined, err := s.tryJoin(ctx, checkout, manifest, task); err != nil || isJoined {
		return line, err
	}
	release, err := s.Turn(ctx, fmt.Sprintf("cfo services up %s for %s", auth.ProjectName(checkout), task))
	if err != nil {
		return "", fmt.Errorf("services: take the machine's turn for heavy runs: %w", err)
	}
	defer release()
	began := s.Now()
	var said time.Time
	for {
		line, short, err := s.tryUp(ctx, checkout, manifest, task)
		if err != nil || short == "" {
			return line, err
		}
		waited := s.Now().Sub(began)
		if waited >= wait {
			if wait == 0 {
				return "", errors.New("services: " + short)
			}
			return "", fmt.Errorf("services: gave up after %s waiting for memory: %s", wait, short)
		}
		if said.IsZero() || s.Now().Sub(said) >= sayEvery {
			fmt.Fprintf(s.Out, "cfo services: waiting for memory (%s so far): %s\n", waited.Round(time.Second), short)
			said = s.Now()
		}
		s.Sleep(s.Poll)
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
}

// tryJoin joins the stack when every service the check needs runs, and says
// whether it did.
func (s Service) tryJoin(ctx context.Context, checkout string, manifest Manifest, task string) (line string, isJoined bool, err error) {
	release, err := s.lock(ctx, true)
	if err != nil {
		return "", false, err
	}
	defer func() { err = errors.Join(err, release()) }()
	record, err := ReadRecord(s.StateDir)
	if err != nil {
		return "", false, err
	}
	return s.join(ctx, &record, checkout, manifest, task)
}

// join joins the stack when every service the check needs runs, as it is:
// compose up is not run, since it would recreate a container whose
// configuration changed under someone else's stack. The caller holds the
// services lock.
func (s Service) join(ctx context.Context, record *Record, checkout string, manifest Manifest, task string) (string, bool, error) {
	if record.Stacks == nil {
		record.Stacks = map[string]Stack{}
	}
	project := auth.ProjectName(checkout)
	stack := record.Stacks[project]
	running, err := s.runningServices(ctx, Compose{Dir: checkout, File: manifest.Compose, EnvFile: manifest.EnvFile})
	if err != nil || !containsAll(running, manifest.Services) {
		return "", false, err
	}
	holders := stack.HolderIDs()
	stack.Project, stack.Checkout, stack.Compose, stack.EnvFile, stack.Services = project, checkout, manifest.Compose, manifest.EnvFile, manifest.Services
	if stack.Since.IsZero() {
		stack.Since = s.Now().UTC()
	}
	if !stack.Holds(task) {
		stack.Holders = append(stack.Holders, Hold{Task: task, Since: s.Now().UTC()})
	}
	record.Stacks[project] = stack
	if err := WriteRecord(s.StateDir, *record); err != nil {
		return "", false, err
	}
	if len(holders) > 0 {
		return fmt.Sprintf("%s's services are up: %s shared the stack %s holds", project, task, strings.Join(holders, ", ")), true, nil
	}
	if !stack.Owned && len(stack.Started) == 0 {
		return fmt.Sprintf("%s's services were already running, so %s shares them and no release stops them", project, task), true, nil
	}
	return fmt.Sprintf("%s's services are up: %s holds them", project, task), true, nil
}

// tryUp joins the stack when its services run, as another start may have
// started them while this one waited for its turn, or starts it when memory
// allows, and otherwise returns why memory does not.
func (s Service) tryUp(ctx context.Context, checkout string, manifest Manifest, task string) (line, short string, err error) {
	release, err := s.lock(ctx, true)
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, release()) }()
	record, err := ReadRecord(s.StateDir)
	if err != nil {
		return "", "", err
	}
	if line, isJoined, err := s.join(ctx, &record, checkout, manifest, task); err != nil || isJoined {
		return line, "", err
	}
	project := auth.ProjectName(checkout)
	stack := record.Stacks[project]
	compose := Compose{Dir: checkout, File: manifest.Compose, EnvFile: manifest.EnvFile}

	cost, basis := stack.cost(manifest)
	before, err := s.Memory()
	if err != nil {
		return "", "", fmt.Errorf("services: read memory: %w", err)
	}
	if short := shortfall(project, before, s.Floor, cost, basis); short != "" {
		return "", short, nil
	}
	if err := s.check(ctx, checkout, manifest.Check); err != nil {
		return "", "", err
	}

	engineUp, err := s.Docker.EngineRunning(ctx)
	if err != nil {
		return "", "", err
	}
	startedEngine := false
	if !engineUp {
		// Written before the start, so a start cut short leaves the record
		// knowing whose engine it is.
		record.Engine = Engine{StartedByCFO: true, Since: s.Now().UTC()}
		if err := WriteRecord(s.StateDir, record); err != nil {
			return "", "", err
		}
		fmt.Fprintln(s.Out, "cfo services: starting the Docker engine")
		if err := s.Docker.StartEngine(ctx); err != nil {
			if running, readErr := s.Docker.EngineRunning(ctx); readErr == nil && !running {
				record.Engine = Engine{}
			}
			return "", "", errors.Join(fmt.Errorf("services: start the Docker engine: %w", err), WriteRecord(s.StateDir, record))
		}
		startedEngine = true
	}
	already, err := s.Docker.Running(ctx, compose)
	if err != nil {
		return "", "", err
	}
	if !stack.IsUp() {
		// A stack cfo started stays cfo's though nobody holds it, until a
		// release or the janitor stops it.
		stack.Owned = stack.Owned || len(already) == 0
		stack.Since = s.Now().UTC()
	}
	stack.Project, stack.Checkout, stack.Compose, stack.EnvFile, stack.Services = project, checkout, manifest.Compose, manifest.EnvFile, manifest.Services
	if !stack.Owned {
		stack.Started = union(stack.Started, missing(manifest.Services, already))
	}
	if !stack.Holds(task) {
		stack.Holders = append(stack.Holders, Hold{Task: task, Since: s.Now().UTC()})
	}
	stack.MemoryBeforeStart = before.Available
	// Written before the start too, so the janitor can stop what a start
	// cut short left running once its task is gone, and the stop can
	// measure what it took.
	record.Stacks[project] = stack
	if err := WriteRecord(s.StateDir, record); err != nil {
		return "", "", err
	}
	fmt.Fprintf(s.Out, "cfo services: starting %s's %s and what they depend on, which builds any image they lack first and can take many minutes\n", project, strings.Join(manifest.Services, ", "))
	if isEnded, err := s.upHoldingTheFloor(ctx, project, compose, manifest.Services); err != nil {
		upErr := fmt.Errorf("services: start %s's services: %w", project, err)
		// A start cfo ended itself failed for cfo's reason, not a service's,
		// and gives its memory back at once.
		if !isEnded {
			saved, saveErr := s.saveFailures(ctx, compose, project, task, upErr)
			if saved != "" {
				upErr = fmt.Errorf("%w; %s", upErr, saved)
			}
			if saveErr != nil {
				upErr = fmt.Errorf("%w; %w", upErr, saveErr)
			}
		}
		if !stack.Owned {
			// A start that failed part way may have started dependencies
			// the stack it came beside did not have.
			if running, readErr := s.Docker.Running(ctx, compose); readErr == nil {
				stack.Started = union(stack.Started, missing(running, already))
			}
		}
		stack.Holders = slices.DeleteFunc(stack.Holders, func(hold Hold) bool { return hold.Task == task })
		if stack.IsUp() {
			// Its other holders keep it up, so no stop follows to measure
			// from the reading.
			stack.MemoryBeforeStart = 0
		}
		record.Stacks[project] = stack
		if !stack.IsUp() {
			_, stopErr := s.stop(ctx, &record, project)
			upErr = errors.Join(upErr, stopErr)
		}
		return "", "", errors.Join(upErr, WriteRecord(s.StateDir, record))
	}
	if !stack.Owned {
		running, err := s.Docker.Running(ctx, compose)
		if err != nil {
			return "", "", err
		}
		stack.Started = union(stack.Started, missing(running, already))
	}
	if stack.Owned || len(stack.Started) > 0 || startedEngine {
		after, err := s.Memory()
		if err != nil {
			return "", "", fmt.Errorf("services: read memory: %w", err)
		}
		// A start that also started the engine measures the whole cost. One
		// that found the engine running measures less than a start from
		// nothing takes, so it only ever raises the cost.
		if took := drop(before.Available, after.Available); startedEngine || took > stack.Cost.Bytes {
			stack.Cost = Cost{Bytes: took, MeasuredAt: s.Now().UTC()}
		}
	}
	stack.MemoryBeforeStart = 0
	record.Stacks[project] = stack
	if err := WriteRecord(s.StateDir, record); err != nil {
		return "", "", err
	}
	what := "started " + project + "'s services"
	if !stack.Owned {
		what = "started " + strings.Join(stack.Started, ", ") + " beside " + project + "'s services already running"
	}
	if startedEngine {
		what += " and the Docker engine they run on"
	}
	return fmt.Sprintf("%s for %s, which took %s of memory", what, task, gigabytes(stack.Cost.Bytes)), "", nil
}

// saveFailures saves what each container of a failed start that did not come
// up says of itself, its state with its health check's output and the end of
// its log, to a file in the task's folder, and says where, or why it could
// not. It runs before the stop that follows, which takes the containers and
// their logs with it, as it did twice on 2026-10-20 after a backend came up
// unhealthy. It writes nothing while the task's cleanup holds its folder, as
// CleanupLockName asks of every writer into a live task's folder.
func (s Service) saveFailures(ctx context.Context, compose Compose, project, task string, startErr error) (string, error) {
	const unsaved = "what its failed services said could not be saved"
	failures, err := s.Docker.Failed(ctx, compose)
	if err != nil {
		return "", fmt.Errorf("%s: %w", unsaved, err)
	}
	if len(failures) == 0 {
		return "", nil
	}
	now := s.Now().UTC()
	var text strings.Builder
	fmt.Fprintf(&text, "%s's services did not come up for %s at %s: %v\n", project, task, now.Format(time.RFC3339), startErr)
	names := make([]string, len(failures))
	for index, failure := range failures {
		names[index] = failure.Service
		fmt.Fprintf(&text, "\n== %s, container %s\n\n-- its state, with its health check's last output (docker inspect):\n%s\n\n-- the end of its log (docker logs):\n%s\n", failure.Service, failure.Container, failure.State, failure.Log)
	}
	path := filepath.Join(s.StateDir, "tasktmp", task, "services-"+project+"-"+now.Format("20060102T150405Z")+".log")
	cleanup := state.CleanupLockName(task)
	if _, err := lock.AcquireExclusiveNamed(s.StateDir, cleanup); err != nil {
		if errors.Is(err, lock.ErrHeld) {
			return "", fmt.Errorf("%s, as the task's cleanup holds its folder: %w", unsaved, err)
		}
		return "", fmt.Errorf("%s: %w", unsaved, err)
	}
	writeErr := fsx.AtomicWriteFile(path, []byte(text.String()))
	releaseErr := lock.ReleaseExclusiveNamed(s.StateDir, cleanup)
	if writeErr != nil {
		return "", fmt.Errorf("%s: %w", unsaved, errors.Join(writeErr, releaseErr))
	}
	saved := "the state and log of " + strings.Join(names, ", ") + " are saved in " + path
	if releaseErr != nil {
		return saved, fmt.Errorf("the task's cleanup lock could not be released: %w", releaseErr)
	}
	return saved, nil
}

// upHoldingTheFloor runs compose up, which builds any image the stack lacks,
// and holds the floor while it runs, as cg-services-pd-build's watch beside
// it did on 2026-10-11. Every FloorEvery it reads memory and commit, and the
// first reading under the floor ends compose up with everything it started,
// and so its build and nothing else. Every ReliefEvery, one at a time, it
// hands the engine's file cache back (Docker.Relieve). The build the watch
// guarded never fell under 9.21 GB free, where the two before it, without
// it, reached the 4 GB floor. It says whether it ended compose up itself.
func (s Service) upHoldingTheFloor(ctx context.Context, project string, compose Compose, services []string) (isEnded bool, err error) {
	building, endBuild := context.WithCancel(ctx)
	defer endBuild()
	built := make(chan error, 1)
	go func() { built <- s.Docker.Up(building, compose, services) }()
	relieving, endRelief := context.WithCancel(ctx)
	defer endRelief()
	relieved := make(chan error, 1)
	isRelieving, hasReliefFailed := false, false
	// A relief still running when the build ends is ended and waited for, so
	// nothing it started outlives the start.
	defer func() {
		if isRelieving {
			endRelief()
			<-relieved
		}
	}()
	floor := time.NewTicker(s.FloorEvery)
	defer floor.Stop()
	relief := time.NewTicker(s.ReliefEvery)
	defer relief.Stop()
	for {
		select {
		case err := <-built:
			return false, err
		case <-floor.C:
			memory, err := s.Memory()
			if err != nil {
				endBuild()
				<-built
				return true, fmt.Errorf("cfo could not read memory while it built, so it ended the build: %w", err)
			}
			if memory.Available >= s.Floor && memory.CommitAvailable >= s.Floor {
				continue
			}
			endBuild()
			<-built
			fmt.Fprintf(s.Out, "cfo services: %s of memory and %s of commit are free, under the %.0f GB floor, so cfo ended its start of %s's services and everything that start ran\n", gigabytes(memory.Available), gigabytes(memory.CommitAvailable), float64(s.Floor)/(1<<30), project)
			return true, fmt.Errorf("the %.0f GB floor ended it, with %s of memory and %s of commit free", float64(s.Floor)/(1<<30), gigabytes(memory.Available), gigabytes(memory.CommitAvailable))
		case <-relief.C:
			if !isRelieving {
				isRelieving = true
				go func() { relieved <- s.Docker.Relieve(relieving) }()
			}
		case err := <-relieved:
			isRelieving = false
			if err != nil && !hasReliefFailed {
				hasReliefFailed = true
				fmt.Fprintf(s.Out, "cfo services: the cache relief failed, and the floor watch goes on without it: %v\n", err)
			}
		}
	}
}

// Down releases task's hold on the project's stack, and stops what cfo
// started once no task holds it.
func (s Service) Down(ctx context.Context, checkout, task string) (line string, err error) {
	release, err := s.lock(ctx, true)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, release()) }()
	record, err := ReadRecord(s.StateDir)
	if err != nil {
		return "", err
	}
	project := auth.ProjectName(checkout)
	if !record.Stacks[project].Holds(task) {
		return fmt.Sprintf("%s holds no stack of %s, so nothing changed", task, project), nil
	}
	line, err = s.release(ctx, &record, project, task)
	return line, errors.Join(err, WriteRecord(s.StateDir, record))
}

// ReleaseTask releases every stack task holds, as a finished task's cleanup
// does.
func (s Service) ReleaseTask(ctx context.Context, task string) (lines []string, err error) {
	release, err := s.lock(ctx, true)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, release()) }()
	record, err := ReadRecord(s.StateDir)
	if err != nil {
		return nil, err
	}
	held := false
	for _, stack := range record.Sorted() {
		if !stack.Holds(task) {
			continue
		}
		held = true
		line, releaseErr := s.release(ctx, &record, stack.Project, task)
		if line != "" {
			lines = append(lines, line)
		}
		err = errors.Join(err, releaseErr)
	}
	if !held {
		return nil, nil
	}
	return lines, errors.Join(err, WriteRecord(s.StateDir, record))
}

// Sweep drops the holds of tasks that are no longer live and stops every
// stack cfo started that no live task holds, and the engine cfo started
// once nothing it started runs on it. A start or release under way is left
// to finish: the next sweep looks again.
func (s Service) Sweep(ctx context.Context) (lines []string, err error) {
	release, err := s.lock(ctx, false)
	if errors.Is(err, lock.ErrHeld) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, release()) }()
	record, err := ReadRecord(s.StateDir)
	if err != nil {
		return nil, err
	}
	changed := false
	for _, stack := range record.Sorted() {
		var gone []string
		stack.Holders = slices.DeleteFunc(stack.Holders, func(hold Hold) bool {
			if s.IsLive(hold.Task) {
				return false
			}
			gone = append(gone, hold.Task)
			return true
		})
		if len(gone) > 0 {
			record.Stacks[stack.Project] = stack
			changed = true
		}
		if stack.IsUp() || (!stack.Owned && len(stack.Started) == 0) {
			continue
		}
		line, stopErr := s.stop(ctx, &record, stack.Project)
		changed = true
		err = errors.Join(err, stopErr)
		if stopErr == nil {
			why := "no task held them"
			if len(gone) > 0 {
				why = "no live task held them (" + strings.Join(gone, ", ") + " ended)"
			}
			lines = append(lines, line+": "+why)
		}
	}
	if record.Engine.StartedByCFO && !record.holdsAnything() {
		line, stopErr := s.stopEngine(ctx, &record)
		changed = true
		err = errors.Join(err, stopErr)
		if line != "" && stopErr == nil {
			lines = append(lines, line)
		}
	}
	if !changed {
		return lines, err
	}
	return lines, errors.Join(err, WriteRecord(s.StateDir, record))
}

// release takes task off the project's holders, and stops the stack when it
// was the last.
func (s Service) release(ctx context.Context, record *Record, project, task string) (string, error) {
	stack := record.Stacks[project]
	stack.Holders = slices.DeleteFunc(stack.Holders, func(hold Hold) bool { return hold.Task == task })
	record.Stacks[project] = stack
	if stack.IsUp() {
		return fmt.Sprintf("%s released %s's services, which %s still holds", task, project, strings.Join(stack.HolderIDs(), ", ")), nil
	}
	line, err := s.stop(ctx, record, project)
	if err != nil {
		return "", err
	}
	return task + " was the last to hold them: " + line, nil
}

// stop takes down what cfo started of a stack nobody holds, and the engine
// when cfo started it and nothing else holds or runs on it. With the engine
// stopped, as quitting Docker Desktop leaves it, nothing of the stack runs,
// so it is only marked stopped.
func (s Service) stop(ctx context.Context, record *Record, project string) (string, error) {
	stack := record.Stacks[project]
	startedFrom := stack.MemoryBeforeStart
	engineUp, err := s.Docker.EngineRunning(ctx)
	if err != nil {
		return "", err
	}
	if !engineUp {
		stack.Owned, stack.Started, stack.Since, stack.MemoryBeforeStart = false, nil, time.Time{}, 0
		record.Stacks[project] = stack
		record.Engine = Engine{}
		return project + "'s services were already stopped with the Docker engine", nil
	}
	compose := Compose{Dir: stack.Checkout, File: stack.Compose, EnvFile: stack.EnvFile}
	var before Memory
	if s.Memory != nil {
		reading, err := s.Memory()
		if err != nil {
			return "", fmt.Errorf("services: read memory: %w", err)
		}
		before = reading
	}
	line := project + "'s services were not cfo's to stop, so they keep running"
	switch {
	case stack.Owned:
		if err := s.Docker.Down(ctx, compose, nil); err != nil {
			return "", fmt.Errorf("services: stop %s's services: %w", project, err)
		}
		line = "stopped " + project + "'s services"
	case len(stack.Started) > 0:
		if err := s.Docker.Down(ctx, compose, stack.Started); err != nil {
			return "", fmt.Errorf("services: stop %s: %w", strings.Join(stack.Started, ", "), err)
		}
		line = "stopped " + strings.Join(stack.Started, ", ") + ", the services cfo started beside " + project + "'s"
	}
	stopped := stack.Owned || len(stack.Started) > 0
	stack.Owned, stack.Started, stack.Since, stack.MemoryBeforeStart = false, nil, time.Time{}, 0
	record.Stacks[project] = stack
	if record.Engine.StartedByCFO && !record.holdsAnything() {
		engineLine, err := s.stopEngine(ctx, record)
		if err != nil {
			return "", err
		}
		line += ", and " + engineLine
		stopped = true
	}
	if stopped && s.Memory != nil {
		after, err := s.Memory()
		if err != nil {
			return "", fmt.Errorf("services: read memory: %w", err)
		}
		// A start cut short never measured itself, and Docker's VM gives its
		// memory back slowly, so what it took is the drop from before it
		// began to before this stop where that is more than the rise.
		if took := max(drop(after.Available, before.Available), drop(startedFrom, before.Available)); took > stack.Cost.Bytes {
			stack.Cost = Cost{Bytes: took, MeasuredAt: s.Now().UTC()}
			record.Stacks[project] = stack
		}
	}
	return line, nil
}

// stopEngine stops the engine cfo started, unless a container cfo did not
// start runs on it.
func (s Service) stopEngine(ctx context.Context, record *Record) (string, error) {
	running, err := s.Docker.EngineRunning(ctx)
	if err != nil {
		return "", err
	}
	if !running {
		record.Engine = Engine{}
		return "", nil
	}
	containers, err := s.Docker.RunningContainers(ctx)
	if err != nil {
		return "", err
	}
	if len(containers) > 0 {
		return "left the Docker engine cfo started running, because " + strings.Join(containers, ", ") + " runs on it and cfo did not start it", nil
	}
	if err := s.Docker.StopEngine(ctx); err != nil {
		return "", fmt.Errorf("services: stop the Docker engine: %w", err)
	}
	record.Engine = Engine{}
	return "stopped the Docker engine cfo started", nil
}

// check runs the manifest's check in the checkout.
func (s Service) check(ctx context.Context, checkout string, command []string) error {
	if len(command) == 0 {
		return nil
	}
	result, err := s.Commands.Run(ctx, execx.Request{Dir: checkout, Name: command[0], Args: command[1:]})
	if err != nil {
		return fmt.Errorf("services: run the project's check %s: %w", command[0], err)
	}
	if result.ExitCode != 0 {
		said := strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
		return fmt.Errorf("services: the project's check refused the start (exit %d): %s", result.ExitCode, said)
	}
	return nil
}

// runningServices names the stack's running services, none while the engine
// is down.
func (s Service) runningServices(ctx context.Context, compose Compose) ([]string, error) {
	engineUp, err := s.Docker.EngineRunning(ctx)
	if err != nil || !engineUp {
		return nil, err
	}
	return s.Docker.Running(ctx, compose)
}

// lock takes the home's services lock, waiting for a start or release under
// way when wait is true.
func (s Service) lock(ctx context.Context, wait bool) (func() error, error) {
	for {
		_, err := lock.AcquireExclusiveNamedFor(s.StateDir, lockName, "cfo services")
		if err == nil {
			return func() error { return lock.ReleaseExclusiveNamed(s.StateDir, lockName) }, nil
		}
		if !wait || !errors.Is(err, lock.ErrHeld) {
			return nil, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// holdsAnything reports whether any stack is held, or is cfo's and still
// running, so the engine under it must stay.
func (r Record) holdsAnything() bool {
	for _, stack := range r.Stacks {
		if stack.IsUp() || stack.Owned || len(stack.Started) > 0 {
			return true
		}
	}
	return false
}

// cost is what a start of the stack is expected to take, with what that
// expectation rests on.
func (s Stack) cost(manifest Manifest) (uint64, string) {
	if s.Cost.Bytes > 0 {
		return s.Cost.Bytes, "measured " + s.Cost.MeasuredAt.UTC().Format("2006-01-02 15:04Z")
	}
	return uint64(manifest.MemoryEstimateGB * (1 << 30)), "estimated, not measured yet"
}

// shortfall says why a start costing cost does not fit, or is empty when it
// does: both memory and commit must stay at or above the floor with it
// added.
func shortfall(project string, memory Memory, floor, cost uint64, basis string) string {
	need := floor + cost
	if memory.Available >= need && memory.CommitAvailable >= need {
		return ""
	}
	return fmt.Sprintf("%s's services cost %s (%s), and %s of memory and %s of commit (memory plus page file) are free, which must stay above the %.0f GB floor with that added",
		project, gigabytes(cost), basis, gigabytes(memory.Available), gigabytes(memory.CommitAvailable), float64(floor)/(1<<30))
}

func gigabytes(bytes uint64) string {
	return fmt.Sprintf("%.1f GB", math.Floor(float64(bytes)/(1<<30)*10)/10)
}

// drop is how far from fell to to, zero when it rose.
func drop(from, to uint64) uint64 {
	if to >= from {
		return 0
	}
	return from - to
}

func containsAll(have, want []string) bool {
	for _, name := range want {
		if !slices.Contains(have, name) {
			return false
		}
	}
	return true
}

// missing are the names in want that have does not hold.
func missing(want, have []string) []string {
	var out []string
	for _, name := range want {
		if !slices.Contains(have, name) {
			out = append(out, name)
		}
	}
	return out
}

func union(left, right []string) []string {
	out := slices.Clone(left)
	for _, name := range right {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}
