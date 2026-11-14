package supervisor

import (
	"context"
	"errors"
	"slices"
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
// whether the Docker engine answers, and the Scrawl server's process.
type sharedMachine struct {
	daemon          pipeline.Daemon
	isAnswering     bool
	line            SharedLine
	isDockerUp      bool
	healthAskCount  int
	scrawl          ScrawlServer
	isScrawlRunning bool
	scrawlErr       error
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
		Scrawl: func() (ScrawlServer, bool, error) { return m.scrawl, m.isScrawlRunning, m.scrawlErr },
	}
}

// busyDaemonMachine is a live gate daemon serving three runs that misses its
// short health reply, with a free heavy line, a running engine and a quiet
// Scrawl server.
func busyDaemonMachine() *sharedMachine {
	return &sharedMachine{
		daemon:          pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: 3},
		line:            SharedLine{Capacity: 1, FloorBytes: 5 * gigabyte, Memory: Memory{Available: 10 * gigabyte, CommitAvailable: 12 * gigabyte}},
		isDockerUp:      true,
		scrawl:          ScrawlServer{PID: 4120, Start: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC), CPU: time.Minute},
		isScrawlRunning: true,
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
		wantPID     int
		wantRuns    int
	}{
		{"alive and missing its reply", pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: 3}, false, "busy", SharedWait, false, 276, 3},
		{"alive, missing its reply, with an unreadable database", pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: -1}, false, "busy", SharedWait, false, 276, -1},
		{"alive and answering", pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: 3}, true, "ready", SharedGo, true, 276, 3},
		{"no process", pipeline.Daemon{State: pipeline.DaemonStopped}, false, "stopped", SharedAsk, false, 0, 0},
		{"a pid file naming a gone process", pipeline.Daemon{State: pipeline.DaemonStopped, PID: 276, LiveRuns: -1}, false, "stopped", SharedAsk, false, 0, 0},
		{"an unreadable process", pipeline.Daemon{State: pipeline.DaemonUnknown, PID: 276}, false, "unknown", SharedAsk, false, 276, 0},
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
			if daemon.PID != c.wantPID || daemon.Runs != c.wantRuns {
				t.Fatalf("gate daemon = %+v, want pid %d serving %d runs", daemon, c.wantPID, c.wantRuns)
			}
			if c.wantState == "busy" && (!daemon.RetryAt.Equal(now.Add(time.Minute)) || !strings.Contains(daemon.Detail, "busy, not stopped")) {
				t.Fatalf("busy gate daemon = %+v, want it busy, not stopped, rechecked at %s", daemon, now.Add(time.Minute))
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
	checkTwice := func() []wake.Record {
		first, _ := checkShared(t, s, h, &now)
		second, _ := checkShared(t, s, h, &now)
		return append(first, second...)
	}
	checkTwice()
	machine.isAnswering = true
	checkTwice()

	// Act
	sharedWaiter(t, h, "wanda", "gate-daemon")
	lateWaiter, _ := checkShared(t, s, h, &now)
	machine.isAnswering = false
	secondOutage := checkTwice()
	machine.isAnswering = true
	secondReturn := checkTwice()

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

// Issue 2365's loaded machine: a live daemon misses its health reply at one
// check and answers at the next. A service is taken for down or usable only
// once two checks in a row agree, so a waiter on a reply missed now and then
// wakes nobody at each flip, and two misses in a row still wake the CEO once.
func TestAHealthReplyMissedNowAndThenWakesNobodyUntilTwoChecksAgree(t *testing.T) {
	// Arrange
	s, h, machine := sharedService(t)
	sharedWaiter(t, h, "huey", "gate-daemon")
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	var flapping []wake.Record
	for check := range 10 {
		machine.isAnswering = check%2 == 1
		woke, _ := checkShared(t, s, h, &now)
		flapping = append(flapping, woke...)
	}
	machine.isAnswering = false
	var steady []wake.Record
	for range 2 {
		woke, _ := checkShared(t, s, h, &now)
		steady = append(steady, woke...)
	}

	// Assert
	if len(flapping) != 0 {
		t.Fatalf("shared wakes while the reply flapped = %+v, want none", flapping)
	}
	if len(steady) != 1 || !strings.HasPrefix(steady[0].Detail, "shared_busy:") {
		t.Fatalf("shared wakes after two misses in a row = %+v, want one shared_busy", steady)
	}
}

// A crash-looping daemon is down at every check, read busy at one and stopped
// at the next. Two checks in a row that find it down wake the CEO once, though
// their next steps differ, and it is taken for the CEO's to decide only once
// two checks in a row find it stopped.
func TestAnOutageReadBusyAndStoppedByTurnsWakesTheCEOOnce(t *testing.T) {
	// Arrange
	s, h, machine := sharedService(t)
	sharedWaiter(t, h, "huey", "gate-daemon")
	busy := machine.daemon
	stopped := pipeline.Daemon{State: pipeline.DaemonStopped}
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	var alternating []wake.Record
	for check := range 6 {
		machine.daemon = busy
		if check%2 == 1 {
			machine.daemon = stopped
		}
		woke, _ := checkShared(t, s, h, &now)
		alternating = append(alternating, woke...)
	}
	machine.daemon = stopped
	var held []wake.Record
	for range 2 {
		woke, _ := checkShared(t, s, h, &now)
		held = append(held, woke...)
	}

	// Assert
	if len(alternating) != 1 || !strings.HasPrefix(alternating[0].Detail, "shared_busy:") || !strings.Contains(alternating[0].Detail, "huey") {
		t.Fatalf("shared wakes while it was read busy and stopped by turns = %+v, want one shared_busy naming huey", alternating)
	}
	if len(held) != 1 || !strings.Contains(held[0].Detail, "it no longer comes back by itself, so whether to start it is now the CEO's to decide") {
		t.Fatalf("shared wakes once it stayed stopped = %+v, want one telling the CEO it is now its to decide", held)
	}
}

// An outage the CEO was told comes back by itself and then stops coming back,
// a busy daemon whose process dies or an engine whose start ran past its
// limit, wakes the CEO once more, as it is now the CEO's to decide; coming back
// to busy or starting wakes nobody, as the CEO already holds it.
func TestAnOutageThatStopsComingBackByItselfWakesTheCEOOnceMore(t *testing.T) {
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		target  string
		arrange func(t *testing.T, h home.Home, machine *sharedMachine)
		worsen  func(t *testing.T, h home.Home, machine *sharedMachine)
		recover func(t *testing.T, h home.Home, machine *sharedMachine)
	}{
		{
			name:    "a busy daemon whose process dies",
			target:  "gate-daemon",
			arrange: func(*testing.T, home.Home, *sharedMachine) {},
			worsen: func(_ *testing.T, _ home.Home, machine *sharedMachine) {
				machine.daemon = pipeline.Daemon{State: pipeline.DaemonStopped}
			},
			recover: func(_ *testing.T, _ home.Home, machine *sharedMachine) {
				machine.daemon = pipeline.Daemon{State: pipeline.DaemonAlive, PID: 277, LiveRuns: 1}
			},
		},
		{
			name:   "an engine whose start runs past its limit",
			target: "docker",
			arrange: func(t *testing.T, h home.Home, machine *sharedMachine) {
				machine.isDockerUp = false
				if err := services.WriteRecord(h.State, services.Record{Engine: services.Engine{StartedByCEO: true, Since: now}}); err != nil {
					t.Fatal(err)
				}
			},
			worsen: func(t *testing.T, h home.Home, _ *sharedMachine) {
				if err := services.WriteRecord(h.State, services.Record{Engine: services.Engine{StartedByCEO: true, Since: now.Add(-time.Hour)}}); err != nil {
					t.Fatal(err)
				}
			},
			recover: func(t *testing.T, h home.Home, _ *sharedMachine) {
				if err := services.WriteRecord(h.State, services.Record{Engine: services.Engine{StartedByCEO: true, Since: now.Add(6 * time.Minute)}}); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, h, machine := sharedService(t)
			c.arrange(t, h, machine)
			sharedWaiter(t, h, "fern", c.target)
			checkTime := now
			checkTwice := func() []wake.Record {
				first, _ := checkShared(t, s, h, &checkTime)
				second, _ := checkShared(t, s, h, &checkTime)
				return append(first, second...)
			}
			waiting := checkTwice()

			// Act
			c.worsen(t, h, machine)
			worse := append(checkTwice(), checkTwice()...)
			c.recover(t, h, machine)
			again := checkTwice()

			// Assert
			if len(waiting) != 1 || !strings.Contains(waiting[0].Detail, "comes back by itself, so nothing restarts it") {
				t.Fatalf("wakes while it comes back by itself = %+v, want one saying nothing restarts it", waiting)
			}
			if len(worse) != 1 || !strings.HasPrefix(worse[0].Detail, "shared_busy:") || !strings.Contains(worse[0].Detail, " is stopped: ") || !strings.Contains(worse[0].Detail, "it no longer comes back by itself, so whether to start it is now the CEO's to decide, never a goblin's") || !strings.Contains(worse[0].Detail, "fern") {
				t.Fatalf("wakes once it stopped = %+v, want one more telling the CEO it is now its to decide", worse)
			}
			if len(again) != 0 {
				t.Fatalf("wakes once it came back by itself again = %+v, want none", again)
			}
		})
	}
}

// A goblin waiting on a stopped Docker engine wakes the CEO once, told that
// starting it is the CEO's to decide, never a goblin's, and is told once the
// engine answers again.
func TestAGoblinWaitingOnAStoppedEngineIsTheCEOsToDecideAndIsToldWhenItAnswers(t *testing.T) {
	// Arrange
	s, h, machine := sharedService(t)
	machine.isDockerUp = false
	sharedWaiter(t, h, "fern", "docker")
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	var stoppedWakes []wake.Record
	for range 3 {
		woke, record := checkShared(t, s, h, &now)
		stoppedWakes = append(stoppedWakes, woke...)
		if docker := sharedByName(t, record, SharedDocker); docker.State != "stopped" || strings.Join(docker.Waiting, ",") != "fern" {
			t.Fatalf("docker = %+v, want stopped with fern waiting", docker)
		}
	}
	machine.isDockerUp = true
	var runningWakes []wake.Record
	for range 3 {
		woke, _ := checkShared(t, s, h, &now)
		runningWakes = append(runningWakes, woke...)
	}

	// Assert
	if len(stoppedWakes) != 1 || !strings.HasPrefix(stoppedWakes[0].Detail, "shared_busy: the Docker engine is stopped") || !strings.Contains(stoppedWakes[0].Detail, "1 goblin waits on it, fern") || !strings.Contains(stoppedWakes[0].Detail, "the CEO's to decide, never a goblin's") || strings.Contains(stoppedWakes[0].Detail, "nothing restarts it") {
		t.Fatalf("shared wakes while the engine is stopped = %+v, want one naming fern that leaves the engine to the CEO", stoppedWakes)
	}
	if len(runningWakes) != 1 || !strings.HasPrefix(runningWakes[0].Detail, "shared_ready: the Docker engine is usable again") || !strings.Contains(runningWakes[0].Detail, "tell fern, once") {
		t.Fatalf("shared wakes once the engine answers = %+v, want one telling fern", runningWakes)
	}
}

// Each read of a shared service runs under its own short deadline, never the
// supervisor's long-lived context, so a wedged docker info or a stalled
// daemon holds up no other fleet wake; a read its deadline ended is unknown
// and the pass still keeps its record.
func TestEveryReadOfASharedServiceHasItsOwnDeadline(t *testing.T) {
	// Arrange
	s, h, _ := sharedService(t)
	var deadlines []time.Time
	bounded := func(ctx context.Context) {
		if deadline, ok := ctx.Deadline(); ok {
			deadlines = append(deadlines, deadline)
		}
	}
	s.Options.Shared.Daemon = func(ctx context.Context) (pipeline.Daemon, error) {
		bounded(ctx)
		return pipeline.Daemon{State: pipeline.DaemonAlive, PID: 276, LiveRuns: 3}, nil
	}
	s.Options.Shared.DaemonAnswers = func(ctx context.Context) bool {
		bounded(ctx)
		return false
	}
	s.Options.Shared.Docker = func(ctx context.Context) (bool, error) {
		bounded(ctx)
		return false, context.DeadlineExceeded
	}
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	_, record := checkShared(t, s, h, &now)
	finished := time.Now()

	// Assert
	// stall-accepted: it compares the deadline the code set with the moment the check ended, so a stall only makes it looser; the test waits on nothing.
	if len(deadlines) != 3 || slices.ContainsFunc(deadlines, func(deadline time.Time) bool { return deadline.After(finished.Add(sharedProbeTimeout)) }) {
		t.Fatalf("the shared reads' deadlines = %v, want each of the three reads bounded by %s", deadlines, sharedProbeTimeout)
	}
	if docker := sharedByName(t, record, SharedDocker); docker.State != "unknown" || docker.Next != SharedAsk {
		t.Fatalf("docker = %+v, want unknown once its read ran out of time", docker)
	}
	if daemon := sharedByName(t, record, SharedGateDaemon); daemon.State != "busy" {
		t.Fatalf("gate daemon = %+v, want busy", daemon)
	}
}

// The machine's heavy line is read from the admission store real runs keep:
// a store nobody has set to two turns has one turn and its flat 4 GiB floor.
func TestTheMachinesHeavyLineIsReadFromItsAdmissionStore(t *testing.T) {
	// Arrange
	t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())

	// Act
	line, err := MachineSharedLine()

	// Assert
	if err != nil || line.Capacity != 1 || line.FloorBytes != 4<<30 || len(line.Holding) != 0 || len(line.Waiting) != 0 || line.Memory.Available == 0 {
		t.Fatalf("MachineSharedLine = %+v, %v; want one free turn, a 4 GiB floor and the machine's memory", line, err)
	}
}

// A light run beside the slots holds none of the heavy line's turns: with one
// heavy run and one light run holding, a line of two reads the heavy run as
// its only holder and one of its two turns free.
func TestTheHeavyLineReadsOnlyTheSlotsHoldersAsItsHolders(t *testing.T) {
	// Arrange
	heavyLine(t, 2, 0)
	store, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	heavy := verify.Admission{Dir: store, Available: func() (uint64, error) { return 64 * gigabyte, nil }, Who: "a heavy run", Estimate: gigabyte, Budget: time.Hour, Limit: time.Minute, Poll: 10 * time.Millisecond}
	slot, err := heavy.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer slot.Release()
	light := heavy
	light.IsLight, light.Who = true, "a light run"
	beside, err := light.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer beside.Release()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	line, err := MachineSharedLine()
	if err != nil {
		t.Fatal(err)
	}
	line.Memory = Memory{Available: 16 * gigabyte, CommitAvailable: 16 * gigabyte}
	service := readHeavyLine(&SharedProbes{Line: func() (SharedLine, error) { return line, nil }}, now)

	// Assert
	if !slices.Equal(service.Holders, []string{"a heavy run"}) || service.State != "free" || !strings.HasPrefix(service.Detail, "1 of 2 turns are held") {
		t.Fatalf("heavy line = %+v, want the heavy run its only holder and one of two turns free", service)
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

// A goblin the CEO stopped, or one being changed, failed or paused for
// something else, waits on no shared service, whatever its last report said,
// so it is not counted and wakes nobody; a lifecycle record of an older
// generation says nothing about the goblin now, so its report still counts.
func TestAGoblinItsLifecycleHoldsIsNoSharedWaiter(t *testing.T) {
	pausedAt := time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name      string
		record    state.Lifecycle
		isWaiting bool
	}{
		{"stopped", state.Lifecycle{ID: "huey", Generation: "s1", Operation: "stop-1", Action: "stop", Phase: "stopped", Updated: pausedAt}, false},
		{"stopping", state.Lifecycle{ID: "huey", Generation: "s1", Operation: "stop-1", Action: "stop", Phase: "stopping", Updated: pausedAt}, false},
		{"failed", state.Lifecycle{ID: "huey", Generation: "s1", Operation: "stop-1", Action: "stop", Phase: "failed", Updated: pausedAt}, false},
		{"paused by the Overlord", state.Lifecycle{ID: "huey", Generation: "s1", Operation: "pause-1", Action: "pause", Phase: "paused", Pause: &state.PauseCondition{Reason: "overlord", At: pausedAt}, Updated: pausedAt}, false},
		{"stopped in an older generation", state.Lifecycle{ID: "huey", Generation: "s0", Operation: "stop-1", Action: "stop", Phase: "stopped", Updated: pausedAt}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, h, _ := sharedService(t)
			sharedWaiter(t, h, "huey", "gate-daemon")
			if err := state.WriteLifecycle(h.State, c.record); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

			// Act
			first, _ := checkShared(t, s, h, &now)
			second, record := checkShared(t, s, h, &now)

			// Assert
			woke := append(first, second...)
			waiting := sharedByName(t, record, SharedGateDaemon).Waiting
			if c.isWaiting && (len(woke) != 1 || !slices.Equal(waiting, []string{"huey"})) {
				t.Fatalf("wakes=%+v waiting=%v, want one shared_busy for huey", woke, waiting)
			}
			if !c.isWaiting && (len(woke) != 0 || len(waiting) != 0) {
				t.Fatalf("wakes=%+v waiting=%v, want no wake and nobody waiting", woke, waiting)
			}
		})
	}
}

// A wait on the heavy line is a wait on memory, so a goblin paused for memory
// waits on it whatever it last reported, as memory_ready counts it, while a
// stopped goblin or one paused for something else does not.
func TestAGoblinPausedForMemoryWaitsOnTheHeavyLine(t *testing.T) {
	// Arrange
	s, h, _ := sharedService(t)
	pausedAt := time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC)
	liveGoblin(t, h, "huey", `C:\dev\code-goblins`)
	sharedWaiter(t, h, "wanda", "memory")
	sharedWaiter(t, h, "fern", "memory")
	for _, record := range []state.Lifecycle{
		{ID: "huey", Generation: "s1", Operation: "pause-1", Action: "pause", Phase: "paused", Pause: &state.PauseCondition{Reason: "memory", At: pausedAt}, Updated: pausedAt},
		{ID: "wanda", Generation: "s1", Operation: "stop-1", Action: "stop", Phase: "stopped", Updated: pausedAt},
		{ID: "fern", Generation: "s1", Operation: "pause-1", Action: "pause", Phase: "paused", Pause: &state.PauseCondition{Reason: "overlord", At: pausedAt}, Updated: pausedAt},
	} {
		if err := state.WriteLifecycle(h.State, record); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	// Act
	_, record := checkShared(t, s, h, &now)

	// Assert
	if waiting := sharedByName(t, record, SharedHeavyLine).Waiting; !slices.Equal(waiting, []string{"huey"}) {
		t.Fatalf("heavy line waiting = %v, want huey alone", waiting)
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
// minutes after goblins services up started it, and stopped after, naming the
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
		{"started by goblins a minute ago", false, services.Engine{StartedByCEO: true, Since: now}, "starting", SharedWait, now.Add(dockerStartLimit)},
		{"started by goblins past its limit", false, services.Engine{StartedByCEO: true, Since: now.Add(-dockerStartLimit)}, "stopped", SharedAsk, time.Time{}},
		{"never started by goblins", false, services.Engine{}, "stopped", SharedAsk, time.Time{}},
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

// Issue 2380: on 2026-11-13 the Scrawl server had used 10,293 s of processor
// time in four hours of life and read 112 percent of one core, and nothing
// the fleet reads said so. The shared check reads its share of one core over
// the minute since the last check, so a server that spins is seen at the next
// check and wakes the CEO once for the spin, and once more for a later one.
func TestASpinningScrawlServerIsSeenAndWakesTheCEOOnceASpin(t *testing.T) {
	// Arrange
	s, h, machine := sharedService(t)
	now := time.Date(2026, 11, 13, 13, 59, 0, 0, time.UTC)
	machine.scrawl = ScrawlServer{PID: 20764, Start: now.Add(time.Minute - 4*time.Hour), CPU: 10293 * time.Second}
	for _, c := range []struct {
		name        string
		used        time.Duration
		wantState   string
		wantPercent int
		wantWakes   int
	}{
		{"its first reading, against its four hours of life", 0, "spinning", 71, 1},
		{"a minute at 112 percent", 67200 * time.Millisecond, "spinning", 112, 0},
		{"a quiet minute", 600 * time.Millisecond, "running", 1, 0},
		{"a later spin", 54 * time.Second, "spinning", 90, 1},
	} {
		machine.scrawl.CPU += c.used

		// Act
		woke, record := checkShared(t, s, h, &now)

		// Assert
		scrawl := sharedByName(t, record, SharedScrawl)
		if scrawl.State != c.wantState || scrawl.CorePercent != c.wantPercent || scrawl.PID != 20764 || !scrawl.IsUsable || scrawl.Next != SharedGo {
			t.Fatalf("%s: scrawl = %+v, want %s at %d percent of one core, pid 20764, usable", c.name, scrawl, c.wantState, c.wantPercent)
		}
		if len(woke) != c.wantWakes {
			t.Fatalf("%s: wakes = %+v, want %d", c.name, woke, c.wantWakes)
		}
		if c.wantWakes == 1 && (woke[0].Key != SharedScrawl || !strings.Contains(woke[0].Detail, "Scrawl server is spinning") || !strings.Contains(woke[0].Detail, "the CEO's to decide")) {
			t.Fatalf("%s: wake = %+v, want the Scrawl server spinning, the CEO's to decide", c.name, woke[0])
		}
	}
}

// A Scrawl server is judged against its own life: one that restarted is not
// measured against the one before it, one only seconds old is not judged by
// the processor its start took, a stopped one starts with the next review,
// and one that cannot be read is the CEO's to ask about.
func TestTheScrawlServerIsReadAgainstItsOwnLife(t *testing.T) {
	now := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name        string
		server      ScrawlServer
		isRunning   bool
		err         error
		wantState   string
		wantNext    string
		wantPercent int
		wantDetail  string
	}{
		{"a restarted server", ScrawlServer{PID: 31000, Start: now.Add(-10 * time.Minute), CPU: 6 * time.Second}, true, nil, "running", SharedGo, 1, "since it started"},
		{"a server seconds old", ScrawlServer{PID: 31000, Start: now.Add(-5 * time.Second), CPU: 4 * time.Second}, true, nil, "running", SharedGo, 80, "since it started"},
		{"no server", ScrawlServer{}, false, nil, "stopped", SharedGo, 0, "no Scrawl server runs"},
		{"an unreadable server", ScrawlServer{}, false, errors.New("access denied"), "unknown", SharedAsk, 0, "access denied"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, h, machine := sharedService(t)
			clock := now.Add(-2 * time.Minute)
			checkShared(t, s, h, &clock)
			machine.scrawl, machine.isScrawlRunning, machine.scrawlErr = c.server, c.isRunning, c.err

			// Act
			woke, record := checkShared(t, s, h, &clock)

			// Assert
			scrawl := sharedByName(t, record, SharedScrawl)
			if scrawl.State != c.wantState || scrawl.Next != c.wantNext || scrawl.CorePercent != c.wantPercent || !strings.Contains(scrawl.Detail, c.wantDetail) {
				t.Fatalf("scrawl = %+v, want %s, next %s, %d percent, detail with %q", scrawl, c.wantState, c.wantNext, c.wantPercent, c.wantDetail)
			}
			if len(woke) != 0 {
				t.Fatalf("wakes = %+v, want none", woke)
			}
		})
	}
}
