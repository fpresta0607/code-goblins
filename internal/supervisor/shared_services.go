package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// The services every goblin shares, by the name a goblin reads them under and
// reports a wait on with goblins notify --waiting-on. A wait on the heavy line is
// reported as a wait on memory, the word goblins already use for it. No goblin
// waits on the Scrawl server, which the next review starts.
const (
	SharedGateDaemon = "gate-daemon"
	SharedHeavyLine  = "heavy-line"
	SharedDocker     = "docker"
	SharedScrawl     = "scrawl"
)

// sharedWaitTargets is the --waiting-on target that names each service.
var sharedWaitTargets = map[string]string{SharedGateDaemon: "gate-daemon", SharedHeavyLine: "memory", SharedDocker: "docker"}

// What a goblin does about a shared service now.
const (
	SharedGo   = "go"
	SharedWait = "wait"
	SharedAsk  = "ask"
)

const sharedServicesSchema = "ceo-shared-services.v1"

// dockerStartLimit is how long a start goblins services up made of the engine may
// take before an engine that still does not answer reads stopped.
const dockerStartLimit = 5 * time.Minute

// sharedProbeTimeout bounds each read of a shared service, so a wedged engine
// or a stalled daemon holds up no other fleet wake.
const sharedProbeTimeout = 10 * time.Second

// scrawlSpinShare is the share of one processor core at which the Scrawl
// server reads spinning: an idle server with a few long polls uses a few
// percent, and on 2026-11-13 a spinning one used 112.
const scrawlSpinShare = 0.5

// scrawlSpinWindow is the shortest reading a spin is judged by, so the
// processor a server's start takes is not read as a spin.
const scrawlSpinWindow = 30 * time.Second

// SharedServices is the state of every service the fleet shares, as the
// supervisor last checked it.
type SharedServices struct {
	Schema    string          `json:"schema"`
	CheckedAt time.Time       `json:"checked_at"`
	Services  []SharedService `json:"services"`
}

// SharedService is one shared service's state, in plain words and in
// fields that are the same for every service, so a goblin on any harness
// reads the same record.
type SharedService struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	IsUsable bool   `json:"is_usable"`
	// Detail says what the state rests on, Next what a goblin does now (go,
	// wait or ask) and NextDetail how, and RetryAt, when set, is the next
	// check that can change the answer.
	Detail     string    `json:"detail"`
	Next       string    `json:"next"`
	NextDetail string    `json:"next_detail"`
	RetryAt    time.Time `json:"retry_at,omitzero"`
	CheckedAt  time.Time `json:"checked_at"`
	// Holders are who holds the service (the heavy line's turns, Docker's
	// stacks), Queued who waits in the service's own line, and Waiting the
	// goblins whose latest report is a wait on it.
	Holders []string `json:"holders"`
	Queued  []string `json:"queued"`
	Waiting []string `json:"waiting"`
	// PID and Runs are the gate daemon's process and how many runs it
	// serves, -1 when its database could not count them; AvailableBytes and FloorBytes the memory the heavy line's next
	// run is read against.
	PID            int    `json:"pid,omitempty"`
	Runs           int    `json:"runs,omitempty"`
	AvailableBytes uint64 `json:"available_bytes,omitempty"`
	FloorBytes     uint64 `json:"floor_bytes,omitempty"`
	// CorePercent is the share of one processor core the Scrawl server used
	// since the last check, in percent.
	CorePercent int `json:"core_percent,omitempty"`
}

// ScrawlServer is the Scrawl server's process and the processor time it has
// used since it started, as read at ReadAt.
type ScrawlServer struct {
	PID    int           `json:"pid"`
	Start  time.Time     `json:"start"`
	CPU    time.Duration `json:"cpu"`
	ReadAt time.Time     `json:"read_at"`
}

// SharedLine is the machine's heavy line as the shared check reads it.
type SharedLine struct {
	Capacity   int
	Holding    []verify.Standing
	Waiting    []verify.Standing
	FloorBytes uint64
	Memory     Memory
}

// SharedProbes read the services the fleet shares. Each only reads, and none
// starts, stops or restarts anything.
type SharedProbes struct {
	// Daemon reads the gate daemon by its pid file and process.
	Daemon func(ctx context.Context) (pipeline.Daemon, error)
	// DaemonAnswers asks the daemon for its health once, as a gate start
	// does before its request.
	DaemonAnswers func(ctx context.Context) bool
	Line          func() (SharedLine, error)
	// Docker reports whether the engine answers.
	Docker func(ctx context.Context) (bool, error)
	// Scrawl reads the Scrawl server's process, and whether one runs.
	Scrawl func() (ScrawlServer, bool, error)
}

func sharedServicesPath(stateDir string) string {
	return filepath.Join(stateDir, "shared-services.json")
}

// ReadSharedServices reads the shared services as the supervisor last
// checked them.
func ReadSharedServices(stateDir string) (SharedServices, error) {
	data, err := fsx.ReadFile(sharedServicesPath(stateDir))
	if err != nil {
		return SharedServices{}, err
	}
	var record SharedServices
	if err := json.Unmarshal(data, &record); err != nil || record.Schema != sharedServicesSchema {
		return SharedServices{}, fmt.Errorf("shared services: %s is unreadable", sharedServicesPath(stateDir))
	}
	return record, nil
}

// checkSharedServices reads each shared service, keeps the result where goblins
// shared and the digest read it, and wakes the CEO once for the goblins
// waiting on a service that is down and once when it is usable again.
func (s *Service) checkSharedServices(ctx context.Context, w *fleetWakes, now time.Time) error {
	probes := s.Options.Shared
	if probes == nil {
		return nil
	}
	h := s.Store.Home
	record := SharedServices{Schema: sharedServicesSchema, CheckedAt: now, Services: []SharedService{
		readGateDaemon(ctx, probes, now),
		readHeavyLine(probes, now),
		readDocker(ctx, probes, h.State, now),
		readScrawl(probes, w, now),
	}}
	var err error
	for index := range record.Services {
		service := &record.Services[index]
		target, isWaitedOn := sharedWaitTargets[service.Name]
		if !isWaitedOn {
			err = errors.Join(err, s.wakeScrawlSpin(w, *service))
			continue
		}
		service.Waiting = sharedWaiting(h, target)
		if service.Name != SharedHeavyLine {
			// A wait on the heavy line is a wait on memory, which the
			// memory_ready wake already tells.
			err = errors.Join(err, s.wakeSharedWaiters(w, *service))
		}
	}
	data, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		return errors.Join(err, marshalErr)
	}
	return errors.Join(err, fsx.AtomicWriteFile(sharedServicesPath(h.State), data))
}

// readGateDaemon reads the gate daemon by its process first: a daemon that is
// alive and serving runs misses a short health reply on a loaded machine, and
// that is busy, never stopped.
func readGateDaemon(ctx context.Context, probes *SharedProbes, now time.Time) SharedService {
	service := SharedService{Name: SharedGateDaemon, CheckedAt: now, Holders: []string{}, Queued: []string{}}
	never := "It serves every goblin, so never start or restart it yourself."
	read, cancelRead := context.WithTimeout(ctx, sharedProbeTimeout)
	daemon, err := probes.Daemon(read)
	cancelRead()
	if daemon.State != pipeline.DaemonStopped {
		service.PID, service.Runs = daemon.PID, daemon.LiveRuns
	}
	isAnswering := false
	if daemon.State == pipeline.DaemonAlive {
		ask, cancelAsk := context.WithTimeout(ctx, sharedProbeTimeout)
		isAnswering = probes.DaemonAnswers(ask)
		cancelAsk()
	}
	switch {
	case daemon.State == pipeline.DaemonStopped:
		service.State, service.Detail = "stopped", "no gate daemon process runs"
		service.Next, service.NextDetail = SharedAsk, "Ask the CEO. "+never
	case daemon.State != pipeline.DaemonAlive:
		service.State, service.Detail = "unknown", "its pid file or process cannot be read"
		if err != nil {
			service.Detail += ": " + err.Error()
		}
		service.Next, service.NextDetail = SharedAsk, "Ask the CEO. "+never
	case isAnswering:
		service.State, service.IsUsable = "ready", true
		service.Detail = fmt.Sprintf("pid %d is alive, serves %s and answered its health check", daemon.PID, daemon.RunCount())
		service.Next, service.NextDetail = SharedGo, "Start or answer your gate."
	default:
		service.State = "busy"
		service.Detail = fmt.Sprintf("pid %d is alive and serves %s, and missed its short health reply, so it is busy, not stopped", daemon.PID, daemon.RunCount())
		service.Next, service.RetryAt = SharedWait, now.Add(fleetWatchEvery)
		service.NextDetail = "goblins pipeline run and respond wait their turn by themselves; report the wait with goblins notify <task> --waiting-on gate-daemon \"<why>\" and do not poll: you are told when it answers again. " + never
	}
	return service
}

// readHeavyLine reads who holds the machine's turns for heavy runs, who
// waits, and the memory the next run is read against, with the reason the
// line's head is not started.
func readHeavyLine(probes *SharedProbes, now time.Time) SharedService {
	service := SharedService{Name: SharedHeavyLine, CheckedAt: now, Holders: []string{}, Queued: []string{}}
	line, err := probes.Line()
	if err != nil {
		service.State, service.Detail, service.Next, service.NextDetail = "unknown", "the heavy line cannot be read: "+err.Error(), SharedAsk, "Ask the CEO."
		return service
	}
	for _, run := range line.Holding {
		service.Holders = append(service.Holders, run.Who)
	}
	for _, run := range line.Waiting {
		service.Queued = append(service.Queued, run.Who)
	}
	free := min(line.Memory.Available, line.Memory.CommitAvailable)
	service.AvailableBytes, service.FloorBytes = free, line.FloorBytes
	waits := "goblins gate turn waits its turn by itself and starts your run when it may; report the wait with goblins notify <task> --waiting-on memory \"<why>\" and do not poll."
	switch {
	case len(line.Holding) >= line.Capacity:
		service.State = "full"
		service.Detail = fmt.Sprintf("all %d turns are held, by %s, and %d runs wait", line.Capacity, strings.Join(service.Holders, ", "), len(line.Waiting))
		service.Next, service.NextDetail, service.RetryAt = SharedWait, waits, now.Add(fleetWatchEvery)
	case free < line.FloorBytes:
		service.State = "short"
		service.Detail = fmt.Sprintf("%.1f GB of memory and commit is free, under the %.1f GB the next run needs, so %d waiting runs are not started", gigabytes(free), gigabytes(line.FloorBytes), len(line.Waiting))
		service.Next, service.NextDetail, service.RetryAt = SharedWait, waits, now.Add(fleetWatchEvery)
	default:
		service.State, service.IsUsable = "free", true
		service.Detail = fmt.Sprintf("%d of %d turns are held and %.1f GB is free against the %.1f GB the next run needs", len(line.Holding), line.Capacity, gigabytes(free), gigabytes(line.FloorBytes))
		service.Next, service.NextDetail = SharedGo, "Start your heavy run with goblins gate turn."
	}
	return service
}

// readDocker reads whether the engine answers and which tasks hold its
// stacks. An engine goblins services up started that has not answered within its
// start limit is stopped, not starting.
func readDocker(ctx context.Context, probes *SharedProbes, stateDir string, now time.Time) SharedService {
	service := SharedService{Name: SharedDocker, CheckedAt: now, Holders: []string{}, Queued: []string{}}
	record, recordErr := services.ReadRecord(stateDir)
	for _, project := range slices.Sorted(maps.Keys(record.Stacks)) {
		for _, hold := range record.Stacks[project].Holders {
			if !slices.Contains(service.Holders, hold.Task) {
				service.Holders = append(service.Holders, hold.Task)
			}
		}
	}
	held := ""
	if len(service.Holders) > 0 {
		held = "; its stacks are held by " + strings.Join(service.Holders, ", ")
	}
	probe, cancel := context.WithTimeout(ctx, sharedProbeTimeout)
	running, err := probes.Docker(probe)
	cancel()
	switch {
	case err != nil:
		service.State, service.Detail = "unknown", "the engine cannot be asked: "+err.Error()
		service.Next, service.NextDetail = SharedAsk, "Ask the CEO; never start or stop the engine yourself."
	case running:
		service.State, service.IsUsable = "running", true
		service.Detail = "the engine answers" + held
		service.Next, service.NextDetail = SharedGo, "Use it through goblins services up when your brief names services."
	case recordErr == nil && record.Engine.StartedByCEO && now.Sub(record.Engine.Since) < dockerStartLimit:
		service.State = "starting"
		service.Detail = fmt.Sprintf("goblins services up started the engine %s ago and it does not answer yet%s", now.Sub(record.Engine.Since).Round(time.Second), held)
		service.Next, service.RetryAt = SharedWait, record.Engine.Since.Add(dockerStartLimit)
		service.NextDetail = "Report the wait with goblins notify <task> --waiting-on docker \"<why>\" and do not poll: you are told when it answers."
	default:
		service.State = "stopped"
		service.Detail = "the engine does not answer" + held
		service.Next, service.NextDetail = SharedAsk, "If your brief names services, goblins services up starts the engine for them; otherwise ask the CEO. Never stop it yourself."
	}
	return service
}

// readScrawl reads the share of one processor core the Scrawl server used
// since the last check, or since it started when the last check read no
// server or another one, and keeps this reading for the next check.
func readScrawl(probes *SharedProbes, w *fleetWakes, now time.Time) SharedService {
	service := SharedService{Name: SharedScrawl, CheckedAt: now, Holders: []string{}, Queued: []string{}, Waiting: []string{}}
	never := "It serves every goblin's review page, so never end or restart it yourself."
	server, isRunning, err := probes.Scrawl()
	last := w.Scrawl
	w.Scrawl = ScrawlServer{}
	switch {
	case err != nil:
		service.State, service.Detail = "unknown", "the Scrawl server's process cannot be read: "+err.Error()
		service.Next, service.NextDetail = SharedAsk, "Ask the CEO. "+never
		return service
	case !isRunning:
		service.State, service.Detail = "stopped", "no Scrawl server runs"
		service.Next, service.NextDetail = SharedGo, "It starts with the next review."
		return service
	}
	w.Scrawl = server
	used, over, window := server.CPU, server.ReadAt.Sub(server.Start), "since it started"
	if last.PID == server.PID && last.Start.Equal(server.Start) && last.ReadAt.Before(server.ReadAt) {
		used, over, window = server.CPU-last.CPU, server.ReadAt.Sub(last.ReadAt), "since the last check"
	}
	share := 0.0
	if over > 0 {
		share = used.Seconds() / over.Seconds()
	}
	service.PID, service.CorePercent, service.IsUsable, service.Next = server.PID, int(math.Round(share*100)), true, SharedGo
	service.Detail = fmt.Sprintf("pid %d used %d percent of one processor core in the %s %s", server.PID, service.CorePercent, over.Round(time.Second), window)
	if over >= scrawlSpinWindow && share >= scrawlSpinShare {
		service.State = "spinning"
		service.Detail += fmt.Sprintf(", at or over the %.0f percent a spinning server reads", scrawlSpinShare*100)
		service.NextDetail = "It still serves reviews; whether to restart it is the CEO's. " + never
		return service
	}
	service.State, service.NextDetail = "running", "Use it for reviews."
	return service
}

// wakeScrawlSpin wakes the CEO once when the Scrawl server reads spinning,
// and again for a later spin once a reading finds it quiet or stopped.
func (s *Service) wakeScrawlSpin(w *fleetWakes, service SharedService) error {
	switch service.State {
	case "running", "stopped":
		w.ScrawlSpinning = false
	case "spinning":
		if w.ScrawlSpinning {
			return nil
		}
		detail := fmt.Sprintf("shared_busy: the Scrawl server is spinning: %s. It still serves reviews and each check measures it again; whether to restart it is the CEO's to decide, never a goblin's.", service.Detail)
		if err := raiseFleetWake(s.Store.Home.State, "shared", service.Name, detail); err != nil {
			return err
		}
		w.ScrawlSpinning = true
	}
	return nil
}

// sharedWaiting names the live goblins that wait on target now, in name
// order.
func sharedWaiting(h home.Home, target string) []string {
	waiting := []string{}
	for _, meta := range liveTasks(h.State) {
		if waitsOn(h.State, meta, target) {
			waiting = append(waiting, meta.ID)
		}
	}
	return waiting
}

// wakeSharedWaiters raises one wake for every goblin waiting on a service
// that is not usable, however many wait, and one more once it is usable,
// naming the goblins to tell, each told once. An outage that waited on the
// service to come back by itself and no longer does wakes the CEO once more,
// as it is now the CEO's to decide. A reading counts only once the next one
// agrees whether the service is usable, so a daemon that misses its health
// reply now and then wakes nobody at each miss, and an outage is the CEO's to
// decide only once two readings in a row say so, so one read busy and stopped
// by turns still wakes the CEO.
func (s *Service) wakeSharedWaiters(w *fleetWakes, service SharedService) error {
	lastNext, isRead := w.SharedNext[service.Name]
	if w.SharedNext == nil {
		w.SharedNext = map[string]string{}
	}
	w.SharedNext[service.Name] = service.Next
	if len(service.Waiting) == 0 {
		delete(w.SharedRaised, service.Name)
		delete(w.SharedTold, service.Name)
		return nil
	}
	if !isRead || (lastNext == SharedGo) != service.IsUsable {
		return nil
	}
	if !service.IsUsable {
		// A new outage tells every waiter again once it ends.
		delete(w.SharedTold, service.Name)
		next := SharedWait
		if service.Next == SharedAsk && lastNext == SharedAsk {
			next = SharedAsk
		}
		raised := w.SharedRaised[service.Name]
		if raised == SharedAsk || raised == next {
			return nil
		}
		owner := "it is shared and comes back by itself, so nothing restarts it"
		switch {
		case raised == SharedWait:
			owner = "it no longer comes back by itself, so whether to start it is now the CEO's to decide, never a goblin's"
		case next == SharedAsk:
			owner = "whether to start it is the CEO's to decide, never a goblin's"
		}
		detail := fmt.Sprintf("shared_busy: the %s is %s: %s. %d %s on it, %s; %s, and each is told once when it is usable again.", sharedName(service.Name), service.State, service.Detail, len(service.Waiting), goblinsWait(len(service.Waiting)), strings.Join(service.Waiting, ", "), owner)
		if err := raiseFleetWake(s.Store.Home.State, "shared", service.Name, detail); err != nil {
			return err
		}
		if w.SharedRaised == nil {
			w.SharedRaised = map[string]string{}
		}
		w.SharedRaised[service.Name] = next
		return nil
	}
	told := w.SharedTold[service.Name]
	var untold []string
	for _, task := range service.Waiting {
		if !slices.Contains(told, task) {
			untold = append(untold, task)
		}
	}
	delete(w.SharedRaised, service.Name)
	if len(untold) == 0 {
		return nil
	}
	detail := fmt.Sprintf("shared_ready: the %s is usable again: %s; tell %s, once, that the %s it waits on is usable.", sharedName(service.Name), service.Detail, strings.Join(untold, ", "), sharedName(service.Name))
	if err := raiseFleetWake(s.Store.Home.State, "shared", service.Name, detail); err != nil {
		return err
	}
	if w.SharedTold == nil {
		w.SharedTold = map[string][]string{}
	}
	w.SharedTold[service.Name] = slices.Clone(service.Waiting)
	return nil
}

// sharedName names a shared service in a sentence.
func sharedName(name string) string {
	switch name {
	case SharedGateDaemon:
		return "gate daemon"
	case SharedHeavyLine:
		return "heavy-run line"
	case SharedDocker:
		return "Docker engine"
	case SharedScrawl:
		return "Scrawl server"
	}
	return name
}

func goblinsWait(count int) string {
	if count == 1 {
		return "goblin waits"
	}
	return "goblins wait"
}

// MachineSharedLine reads the machine's heavy line and memory for the shared
// check. A light run beside the slots holds none of the line's turns, so only
// the slots' holders are read as its holders.
func MachineSharedLine() (SharedLine, error) {
	dir, err := verify.AdmissionDir()
	if err != nil {
		return SharedLine{}, err
	}
	capacity, _, err := verify.AdmissionLimits(dir)
	if err != nil {
		return SharedLine{}, err
	}
	holding, waiting, err := verify.Line(dir)
	if err != nil {
		return SharedLine{}, err
	}
	holding = slices.DeleteFunc(holding, func(run verify.Standing) bool { return run.IsLight })
	var estimate uint64
	if len(waiting) > 0 {
		estimate = waiting[0].Estimate
	}
	floor, err := verify.HeadFloor(dir, estimate, 0)
	if err != nil {
		return SharedLine{}, err
	}
	memory, err := MachineMemory()
	if err != nil {
		return SharedLine{}, err
	}
	return SharedLine{Capacity: capacity, Holding: holding, Waiting: waiting, FloorBytes: floor, Memory: memory}, nil
}

// MachineScrawl reads the Scrawl server's process and the processor time it
// has used. Only one server holds its port, so of two the earlier started is
// read.
func MachineScrawl() (ScrawlServer, bool, error) {
	servers, err := proc.PageServers()
	if err != nil || len(servers) == 0 {
		return ScrawlServer{}, false, err
	}
	server := slices.MinFunc(servers, func(a, b proc.ServiceProcess) int { return a.Start.Compare(b.Start) })
	used, isMeasured := proc.CPUTime(server.PID)
	if !isMeasured {
		return ScrawlServer{}, false, fmt.Errorf("the processor time of pid %d cannot be read", server.PID)
	}
	return ScrawlServer{PID: server.PID, Start: server.Start, CPU: used, ReadAt: time.Now().UTC()}, true, nil
}
