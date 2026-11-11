package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// sharedMachine stands in for the machine's shared services: the gate
// daemon's process and whether it answers its health check, the heavy line,
// and whether the Docker engine answers.
type sharedMachine struct {
	daemon         pipeline.Daemon
	isAnswering    bool
	line           SharedLine
	isDockerUp     bool
	healthAskCount int
}

func (m *sharedMachine) probes() *SharedProbes {
	return &SharedProbes{
		Daemon: func(context.Context) (pipeline.Daemon, error) { return m.daemon, nil },
		DaemonAnswers: func(context.Context) bool {
			m.healthAskCount++
			return m.isAnswering
		},
		Line:   func() (SharedLine, error) { return m.line, nil },
		Docker: func(context.Context) (bool, error) { return m.isDockerUp, nil },
	}
}

// busyDaemonMachine is a live gate daemon serving three runs that misses its
// short health reply, with a free heavy line and a running engine.
func busyDaemonMachine() *sharedMachine {
	return &sharedMachine{
		daemon:     pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: 3},
		line:       SharedLine{Capacity: 1, FloorBytes: 5 * gigabyte, Memory: Memory{Available: 10 * gigabyte, CommitAvailable: 12 * gigabyte}},
		isDockerUp: true,
	}
}

func sharedService(t *testing.T) (*Service, home.Home, *sharedMachine) {
	t.Helper()
	s, h := fleetService(t)
	machine := busyDaemonMachine()
	s.Options.Shared = machine.probes()
	return s, h, machine
}

// checkShared runs one fleet check a minute after the last and returns the
// shared wakes raised by it and the record it kept.
func checkShared(t *testing.T, s *Service, h home.Home, now *time.Time) ([]wake.Record, SharedServices) {
	t.Helper()
	before := len(fleetWakeRecords(t, h, "shared"))
	*now = now.Add(time.Minute)
	if err := s.checkFleet(context.Background(), *now); err != nil {
		t.Fatal(err)
	}
	record, err := ReadSharedServices(h.State)
	if err != nil {
		t.Fatal(err)
	}
	return fleetWakeRecords(t, h, "shared")[before:], record
}

func sharedByName(t *testing.T, record SharedServices, name string) SharedService {
	t.Helper()
	for _, service := range record.Services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("shared services %+v carry no %s", record.Services, name)
	return SharedService{}
}

// sharedWaiter writes a live goblin whose latest report is a wait on target.
func sharedWaiter(t *testing.T, h home.Home, id, target string) {
	t.Helper()
	liveGoblin(t, h, id, `C:\dev\code-goblins`)
	if err := state.AppendStatus(h.State, id, "waiting on "+target+": its gate start found the daemon busy"); err != nil {
		t.Fatal(err)
	}
}

// Issue 2365: on 2026-11-11 the gate daemon, pid 276, was alive and serving
// three runs while it missed its 250ms health reply, and every goblin that
// met the miss read it as a stopped daemon. The shared check reads the
// process first, so a live daemon that misses the reply is busy, never
// stopped, with how many runs it serves.
func TestABusyDaemonThatMissesItsHealthReplyIsBusyNotStopped(t *testing.T) {
	for _, c := range []struct {
		name        string
		daemon      pipeline.Daemon
		isAnswering bool
		wantState   string
		wantNext    string
		isUsable    bool
	}{
		{"alive and missing its reply", pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: 3}, false, "busy", SharedWait, false},
		{"alive and answering", pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: 3}, true, "ready", SharedGo, true},
		{"no process", pipeline.Daemon{State: pipeline.DaemonStopped}, false, "stopped", SharedAsk, false},
		{"an unreadable process", pipeline.Daemon{State: pipeline.DaemonUnknown, PID: 276}, false, "unknown", SharedAsk, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, h, machine := sharedService(t)
			machine.daemon, machine.isAnswering = c.daemon, c.isAnswering
			now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

			// Act
			_, record := checkShared(t, s, h, &now)

			// Assert
			daemon := sharedByName(t, record, SharedGateDaemon)
			if daemon.State != c.wantState || daemon.Next != c.wantNext || daemon.IsUsable != c.isUsable || !daemon.CheckedAt.Equal(now) {
				t.Fatalf("gate daemon = %+v, want %s, next %s, usable %t, checked at %s", daemon, c.wantState, c.wantNext, c.isUsable, now)
			}
			if !c.isUsable && !strings.Contains(daemon.NextDetail, "never start or restart it") {
				t.Fatalf("gate daemon next = %q, want it to say never start or restart it", daemon.NextDetail)
			}
			if c.wantState == "busy" && (daemon.PID != 276 || daemon.Runs != 3 || !daemon.RetryAt.Equal(now.Add(time.Minute)) || !strings.Contains(daemon.Detail, "busy, not stopped")) {
				t.Fatalf("busy gate daemon = %+v, want pid 276 serving 3 runs, rechecked at %s", daemon, now.Add(time.Minute))
			}
		})
	}
}

// A daemon with no process is not asked for its health, so a stopped
// daemon's reading adds no wait of its own.
func TestAStoppedDaemonIsNotAskedForItsHealth(t *testing.T) {
	// Arrange
	s, h, machine := sharedService(t)
	machine.daemon = pipeline.Daemon{State: pipeline.DaemonStopped}
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	checkShared(t, s, h, &now)

	// Assert
	if machine.healthAskCount != 0 {
		t.Fatalf("a stopped daemon was asked for its health %d times, want none", machine.healthAskCount)
	}
}

// Three goblins waiting on the same busy daemon wake the CEO once, however
// many checks the outage lasts, and once more when it answers again, naming
// all three; a waiting goblin is told once, not at every later check.
func TestThreeGoblinsWaitingOnOneServiceWakeOnceAndAreToldOnce(t *testing.T) {
	// Arrange
	s, h, machine := sharedService(t)
	for _, id := range []string{"huey", "wanda", "fern"} {
		sharedWaiter(t, h, id, "gate-daemon")
	}
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act: three checks while it is busy.
	var busyWakes []wake.Record
	for range 3 {
		woke, record := checkShared(t, s, h, &now)
		busyWakes = append(busyWakes, woke...)
		if waiting := sharedByName(t, record, SharedGateDaemon).Waiting; strings.Join(waiting, ",") != "fern,huey,wanda" {
			t.Fatalf("gate daemon waiting = %v, want fern, huey and wanda", waiting)
		}
	}

	// Assert
	if len(busyWakes) != 1 || !strings.HasPrefix(busyWakes[0].Detail, "shared_busy:") || !strings.Contains(busyWakes[0].Detail, "3 goblins wait on it, fern, huey, wanda") || !strings.Contains(busyWakes[0].Detail, "nothing restarts it") {
		t.Fatalf("shared wakes while busy = %+v, want one naming the three waiting goblins", busyWakes)
	}

	// Act: the daemon answers again, and the goblins have not yet reported
	// anything new.
	machine.isAnswering = true
	var readyWakes []wake.Record
	for range 3 {
		woke, _ := checkShared(t, s, h, &now)
		readyWakes = append(readyWakes, woke...)
	}

	// Assert
	if len(readyWakes) != 1 || !strings.HasPrefix(readyWakes[0].Detail, "shared_ready:") || !strings.Contains(readyWakes[0].Detail, "tell fern, huey, wanda, once") {
		t.Fatalf("shared wakes once usable = %+v, want one telling fern, huey and wanda", readyWakes)
	}
}

// A goblin that starts waiting after the others were told is told on its
// own, and a new outage tells every waiter again once it ends.
func TestALaterWaiterAndALaterOutageAreToldAgain(t *testing.T) {
	// Arrange
	s, h, machine := sharedService(t)
	sharedWaiter(t, h, "huey", "gate-daemon")
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	checkShared(t, s, h, &now)
	machine.isAnswering = true
	checkShared(t, s, h, &now)

	// Act
	sharedWaiter(t, h, "wanda", "gate-daemon")
	lateWaiter, _ := checkShared(t, s, h, &now)
	machine.isAnswering = false
	secondOutage, _ := checkShared(t, s, h, &now)
	machine.isAnswering = true
	secondReturn, _ := checkShared(t, s, h, &now)

	// Assert
	if len(lateWaiter) != 1 || !strings.Contains(lateWaiter[0].Detail, "tell wanda, once") || strings.Contains(lateWaiter[0].Detail, "huey") {
		t.Fatalf("wakes for a later waiter = %+v, want one telling wanda alone", lateWaiter)
	}
	if len(secondOutage) != 1 || !strings.HasPrefix(secondOutage[0].Detail, "shared_busy:") {
		t.Fatalf("wakes for a second outage = %+v, want one shared_busy", secondOutage)
	}
	if len(secondReturn) != 1 || !strings.Contains(secondReturn[0].Detail, "tell huey, wanda, once") {
		t.Fatalf("wakes once usable again = %+v, want one telling huey and wanda", secondReturn)
	}
}

// Nobody waiting on a busy service wakes nobody: the record still says busy
// for a goblin that reads it.
func TestABusyServiceNobodyWaitsOnWakesNobody(t *testing.T) {
	// Arrange
	s, h, _ := sharedService(t)
	sharedWaiter(t, h, "huey", "memory")
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	woke, record := checkShared(t, s, h, &now)

	// Assert
	if len(woke) != 0 || sharedByName(t, record, SharedGateDaemon).State != "busy" {
		t.Fatalf("wakes=%+v gate daemon=%+v, want no wake and a busy daemon", woke, sharedByName(t, record, SharedGateDaemon))
	}
}

// The heavy line reads full when every turn is held, short when memory is
// under the next run's floor, and free otherwise, naming who holds and who
// waits; a wait on it is a wait on memory, which memory_ready already tells,
// so it raises no shared wake.
func TestTheHeavyLineSaysWhyItsHeadIsNotStarted(t *testing.T) {
	holding := []verify.Standing{{Who: "huey"}}
	waiting := []verify.Standing{{Who: "wanda"}, {Who: "fern"}}
	for _, c := range []struct {
		name      string
		line      SharedLine
		wantState string
		wantNext  string
	}{
		{"every turn held", SharedLine{Capacity: 1, Holding: holding, Waiting: waiting, FloorBytes: 5 * gigabyte, Memory: Memory{Available: 10 * gigabyte, CommitAvailable: 10 * gigabyte}}, "full", SharedWait},
		{"commit under the floor", SharedLine{Capacity: 2, Holding: holding, Waiting: waiting, FloorBytes: 5 * gigabyte, Memory: Memory{Available: 10 * gigabyte, CommitAvailable: 4 * gigabyte}}, "short", SharedWait},
		{"a turn and memory free", SharedLine{Capacity: 2, Holding: holding, Waiting: waiting, FloorBytes: 5 * gigabyte, Memory: Memory{Available: 10 * gigabyte, CommitAvailable: 10 * gigabyte}}, "free", SharedGo},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, h, machine := sharedService(t)
			machine.line = c.line
			sharedWaiter(t, h, "wanda", "memory")
			now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

			// Act
			woke, record := checkShared(t, s, h, &now)

			// Assert
			line := sharedByName(t, record, SharedHeavyLine)
			if line.State != c.wantState || line.Next != c.wantNext || strings.Join(line.Holders, ",") != "huey" || strings.Join(line.Queued, ",") != "wanda,fern" || strings.Join(line.Waiting, ",") != "wanda" || line.FloorBytes != 5*gigabyte {
				t.Fatalf("heavy line = %+v, want %s, next %s, held by huey, wanda and fern in line, wanda waiting", line, c.wantState, c.wantNext)
			}
			if len(woke) != 0 {
				t.Fatalf("shared wakes = %+v, want none for a wait on memory", woke)
			}
		})
	}
}

// The Docker engine reads running when it answers, starting for five
// minutes after cfo services up started it, and stopped after, naming the
// tasks whose stacks hold it.
func TestTheDockerEngineSaysWhetherItIsStartingOrStopped(t *testing.T) {
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name      string
		isUp      bool
		engine    services.Engine
		wantState string
		wantNext  string
		wantRetry time.Time
	}{
		{"answering", true, services.Engine{}, "running", SharedGo, time.Time{}},
		{"started by cfo a minute ago", false, services.Engine{StartedByCFO: true, Since: now}, "starting", SharedWait, now.Add(dockerStartLimit)},
		{"started by cfo past its limit", false, services.Engine{StartedByCFO: true, Since: now.Add(-dockerStartLimit)}, "stopped", SharedAsk, time.Time{}},
		{"never started by cfo", false, services.Engine{}, "stopped", SharedAsk, time.Time{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, h, machine := sharedService(t)
			machine.isDockerUp = c.isUp
			record := services.Record{Engine: c.engine, Stacks: map[string]services.Stack{`C:\dev\pocketpiggies`: {Holders: []services.Hold{{Task: "fern"}}}}}
			if err := services.WriteRecord(h.State, record); err != nil {
				t.Fatal(err)
			}
			checkTime := now

			// Act
			_, shared := checkShared(t, s, h, &checkTime)

			// Assert
			docker := sharedByName(t, shared, SharedDocker)
			if docker.State != c.wantState || docker.Next != c.wantNext || !docker.RetryAt.Equal(c.wantRetry) || strings.Join(docker.Holders, ",") != "fern" {
				t.Fatalf("docker = %+v, want %s, next %s, retry at %s, held by fern", docker, c.wantState, c.wantNext, c.wantRetry)
			}
		})
	}
}

// A probe that cannot read its service says unknown and to ask the CEO, and
// the other services are still read.
func TestAnUnreadableServiceIsUnknownAndTheOthersAreStillRead(t *testing.T) {
	// Arrange
	s, h, _ := sharedService(t)
	s.Options.Shared.Docker = func(context.Context) (bool, error) { return false, errors.New("docker info: access denied") }
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	_, record := checkShared(t, s, h, &now)

	// Assert
	docker := sharedByName(t, record, SharedDocker)
	if docker.State != "unknown" || docker.Next != SharedAsk || !strings.Contains(docker.Detail, "access denied") {
		t.Fatalf("docker = %+v, want unknown with its error and ask", docker)
	}
	if sharedByName(t, record, SharedGateDaemon).State != "busy" || sharedByName(t, record, SharedHeavyLine).State != "free" {
		t.Fatalf("shared services = %+v, want the gate daemon and heavy line still read", record.Services)
	}
}
