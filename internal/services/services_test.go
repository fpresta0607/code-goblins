package services

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const gigabyte = uint64(1) << 30

// fakeDocker is one engine running one project's compose stack. Starting
// the engine costs engineCost of the machine's memory and each service
// serviceCost, given back as they stop, so a test reads the stack's cost
// the way the service measures it.
type fakeDocker struct {
	machine     *fakeMachine
	engine      bool
	running     map[string]bool
	dependsOn   map[string][]string
	foreign     []string
	engineCost  uint64
	serviceCost uint64
	upErr       error
	// upHook runs as compose up begins, such as a build taking memory.
	upHook func()
	calls  []string
}

func newFakeDocker(machine *fakeMachine) *fakeDocker {
	return &fakeDocker{
		machine:     machine,
		running:     map[string]bool{},
		dependsOn:   map[string][]string{"backend": {"redis", "qdrant"}, "worker-light": {"redis", "qdrant"}},
		engineCost:  2 * gigabyte,
		serviceCost: gigabyte / 4,
	}
}

func (f *fakeDocker) EngineRunning(context.Context) (bool, error) { return f.engine, nil }

func (f *fakeDocker) StartEngine(context.Context) error {
	f.calls = append(f.calls, "start-engine")
	f.engine = true
	f.machine.take(f.engineCost)
	return nil
}

func (f *fakeDocker) StopEngine(context.Context) error {
	f.calls = append(f.calls, "stop-engine")
	if !f.engine {
		return errors.New("the engine is not running")
	}
	f.engine = false
	f.machine.give(f.engineCost)
	return nil
}

func (f *fakeDocker) RunningContainers(context.Context) ([]string, error) {
	if !f.engine {
		return nil, errors.New("the engine is not running")
	}
	return append(f.names(), f.foreign...), nil
}

func (f *fakeDocker) Running(_ context.Context, compose Compose) ([]string, error) {
	if !f.engine {
		return nil, errors.New("the engine is not running")
	}
	if compose.Dir == "" || compose.File == "" {
		return nil, errors.New("no compose stack named")
	}
	return f.names(), nil
}

func (f *fakeDocker) Up(_ context.Context, _ Compose, services []string) error {
	f.calls = append(f.calls, "up "+strings.Join(services, ","))
	if !f.engine {
		return errors.New("the engine is not running")
	}
	if f.upHook != nil {
		f.upHook()
	}
	if f.upErr != nil {
		f.start("redis")
		return f.upErr
	}
	for _, service := range services {
		for _, dependency := range f.dependsOn[service] {
			f.start(dependency)
		}
		f.start(service)
	}
	return nil
}

func (f *fakeDocker) Down(_ context.Context, _ Compose, services []string) error {
	f.calls = append(f.calls, "down "+strings.Join(services, ","))
	if !f.engine {
		return errors.New("the engine is not running")
	}
	if len(services) == 0 {
		services = f.names()
	}
	for _, service := range services {
		if f.running[service] {
			delete(f.running, service)
			f.machine.give(f.serviceCost)
		}
	}
	return nil
}

func (f *fakeDocker) start(service string) {
	if !f.running[service] {
		f.running[service] = true
		f.machine.take(f.serviceCost)
	}
}

func (f *fakeDocker) names() []string {
	var names []string
	for name := range f.running {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type fakeMachine struct {
	available, commit uint64
}

func (m *fakeMachine) read() (Memory, error) {
	return Memory{Available: m.available, CommitAvailable: m.commit}, nil
}

func (m *fakeMachine) take(bytes uint64) { m.available -= bytes; m.commit -= bytes }
func (m *fakeMachine) give(bytes uint64) { m.available += bytes; m.commit += bytes }

type fakeCheck struct {
	exit  int
	out   string
	calls []execx.Request
}

func (c *fakeCheck) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	c.calls = append(c.calls, request)
	return execx.Result{ExitCode: c.exit, Stdout: []byte(c.out)}, nil
}

type harness struct {
	service  Service
	docker   *fakeDocker
	machine  *fakeMachine
	check    *fakeCheck
	live     map[string]bool
	out      *bytes.Buffer
	checkout string
	clock    *time.Time
}

const testManifest = `{
  "project": "PrecisionDocs-AI",
  "compose": "docker-compose.dev.yml",
  "env_file": ".env.docker.local",
  "services": ["backend", "worker-light"],
  "check": ["powershell", "-NoProfile", "-File", "guard.ps1"],
  "memory_estimate_gb": 3
}`

func newHarness(t *testing.T) *harness {
	t.Helper()
	dataDir := t.TempDir()
	writeManifest(t, dataDir, "PrecisionDocs-AI", testManifest)
	machine := &fakeMachine{available: 12 * gigabyte, commit: 16 * gigabyte}
	docker := newFakeDocker(machine)
	check := &fakeCheck{}
	live := map[string]bool{"task-a": true, "task-b": true}
	out := &bytes.Buffer{}
	clock := time.Date(2026, 10, 8, 14, 0, 0, 0, time.UTC)
	h := &harness{docker: docker, machine: machine, check: check, live: live, out: out, checkout: `C:\dev\PrecisionDocs-AI`, clock: &clock}
	h.service = Service{
		StateDir: t.TempDir(),
		DataDir:  dataDir,
		Docker:   docker,
		Commands: check,
		Memory:   machine.read,
		Floor:    4 * gigabyte,
		IsLive:   func(task string) bool { return live[task] },
		Now:      func() time.Time { return *h.clock },
		Sleep:    func(d time.Duration) { *h.clock = h.clock.Add(d) },
		Poll:     15 * time.Second,
		Out:      out,
	}
	return h
}

func (h *harness) record(t *testing.T) Record {
	t.Helper()
	record, err := ReadRecord(h.service.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (h *harness) up(t *testing.T, task string) string {
	t.Helper()
	line, err := h.service.Up(context.Background(), h.checkout, task, 0)
	if err != nil {
		t.Fatalf("Up(%s): %v", task, err)
	}
	return line
}

func (h *harness) down(t *testing.T, task string) string {
	t.Helper()
	line, err := h.service.Down(context.Background(), h.checkout, task)
	if err != nil {
		t.Fatalf("Down(%s): %v", task, err)
	}
	return line
}

func TestUpStartsTheEngineAndTheStackForTheFirstTask(t *testing.T) {
	// Arrange
	h := newHarness(t)

	// Act
	line := h.up(t, "task-a")

	// Assert
	if want := []string{"start-engine", "up backend,worker-light"}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want %v", h.docker.calls, want)
	}
	if len(h.check.calls) != 1 || h.check.calls[0].Dir != h.checkout || h.check.calls[0].Name != "powershell" {
		t.Errorf("check calls = %+v, want the manifest's check run once in the checkout", h.check.calls)
	}
	record := h.record(t)
	stack := record.Stacks["PrecisionDocs-AI"]
	if !record.Engine.StartedByCFO {
		t.Error("the record does not say cfo started the engine")
	}
	if !stack.Owned || !slices.Equal(stack.HolderIDs(), []string{"task-a"}) {
		t.Errorf("stack = %+v, want it owned and held by task-a", stack)
	}
	// The engine (2 GB) and four services (a quarter each) came up.
	if stack.Cost.Bytes != 3*gigabyte {
		t.Errorf("measured cost = %d, want the 3 GB the start took", stack.Cost.Bytes)
	}
	if !strings.Contains(line, "started") || !strings.Contains(line, "task-a") {
		t.Errorf("line = %q, want it to say the stack started for task-a", line)
	}
}

func TestASecondTaskSharesTheStackWithoutStartingIt(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.up(t, "task-a")
	h.docker.calls = nil
	// Memory is now too short for a start, which a share does not need.
	h.machine.available, h.machine.commit = 5*gigabyte, 5*gigabyte

	// Act
	line := h.up(t, "task-b")

	// Assert
	if len(h.docker.calls) != 0 {
		t.Fatalf("docker calls = %v, want none for a stack already up", h.docker.calls)
	}
	if len(h.check.calls) != 1 {
		t.Errorf("check ran %d times, want only for the start", len(h.check.calls))
	}
	stack := h.record(t).Stacks["PrecisionDocs-AI"]
	if !slices.Equal(stack.HolderIDs(), []string{"task-a", "task-b"}) {
		t.Errorf("holders = %v, want task-a and task-b", stack.HolderIDs())
	}
	if !strings.Contains(line, "task-a") || !strings.Contains(line, "shared") {
		t.Errorf("line = %q, want it to say it shares task-a's stack", line)
	}
}

func TestUpTwiceForOneTaskHoldsTheStackOnce(t *testing.T) {
	h := newHarness(t)
	h.up(t, "task-a")

	h.up(t, "task-a")

	if holders := h.record(t).Stacks["PrecisionDocs-AI"].HolderIDs(); !slices.Equal(holders, []string{"task-a"}) {
		t.Fatalf("holders = %v, want task-a once", holders)
	}
}

func TestReleaseKeepsTheStackWhileAnotherTaskHoldsIt(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.up(t, "task-a")
	h.up(t, "task-b")
	h.docker.calls = nil

	// Act
	line := h.down(t, "task-a")

	// Assert
	if len(h.docker.calls) != 0 {
		t.Fatalf("docker calls = %v, want the stack left up for task-b", h.docker.calls)
	}
	if holders := h.record(t).Stacks["PrecisionDocs-AI"].HolderIDs(); !slices.Equal(holders, []string{"task-b"}) {
		t.Errorf("holders = %v, want task-b", holders)
	}
	if !strings.Contains(line, "task-b") {
		t.Errorf("line = %q, want it to name task-b, which still holds it", line)
	}
}

func TestTheLastReleaseStopsTheStackAndTheEngineCFOStarted(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.up(t, "task-a")
	h.up(t, "task-b")
	h.down(t, "task-a")
	h.docker.calls = nil

	// Act
	line := h.down(t, "task-b")

	// Assert
	if want := []string{"down ", "stop-engine"}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want %v", h.docker.calls, want)
	}
	record := h.record(t)
	stack := record.Stacks["PrecisionDocs-AI"]
	if stack.IsUp() || stack.Owned || record.Engine.StartedByCFO {
		t.Errorf("record = %+v, want the stack down and the engine no longer cfo's", record)
	}
	// The measured cost is kept for the next start.
	if stack.Cost.Bytes != 3*gigabyte {
		t.Errorf("cost = %d, want the measured 3 GB kept", stack.Cost.Bytes)
	}
	if !strings.Contains(line, "stopped") || !strings.Contains(line, "engine") {
		t.Errorf("line = %q, want it to say the stack and the engine stopped", line)
	}
}

func TestTheLastReleaseLeavesAnEngineCFODidNotStart(t *testing.T) {
	h := newHarness(t)
	h.docker.engine = true
	h.up(t, "task-a")
	h.docker.calls = nil

	h.down(t, "task-a")

	if want := []string{"down "}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want only the stack stopped", h.docker.calls)
	}
	if !h.docker.engine {
		t.Error("the engine cfo did not start was stopped")
	}
}

func TestTheLastReleaseLeavesTheEngineWhileContainersCFODidNotStartRun(t *testing.T) {
	h := newHarness(t)
	h.up(t, "task-a")
	h.docker.foreign = []string{"overlord-postgres"}
	h.docker.calls = nil

	line := h.down(t, "task-a")

	if want := []string{"down "}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want the engine left running", h.docker.calls)
	}
	if !strings.Contains(line, "overlord-postgres") {
		t.Errorf("line = %q, want it to name the container that keeps the engine running", line)
	}
	if !h.record(t).Engine.StartedByCFO {
		t.Error("the record forgot cfo started the engine, so no later release can stop it")
	}
}

func TestUpRefusesWhenMemoryWouldFallUnderTheFloorAndSaysWhy(t *testing.T) {
	// Arrange: 6 GB free, the 3 GB estimate added leaves 3 GB, under the
	// 4 GB floor.
	h := newHarness(t)
	h.machine.available, h.machine.commit = 6*gigabyte, 16*gigabyte

	// Act
	_, err := h.service.Up(context.Background(), h.checkout, "task-a", 0)

	// Assert
	if err == nil {
		t.Fatal("Up started a stack that leaves memory under the floor")
	}
	for _, want := range []string{"3.0 GB", "estimated", "6.0 GB of memory", "4 GB floor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to say %q", err, want)
		}
	}
	if len(h.docker.calls) != 0 || len(h.check.calls) != 0 {
		t.Errorf("docker calls = %v check calls = %d, want nothing started or checked", h.docker.calls, len(h.check.calls))
	}
	if h.record(t).Stacks["PrecisionDocs-AI"].IsUp() {
		t.Error("a refused start recorded a hold")
	}
}

func TestUpRefusesWhenCommitWouldFallUnderTheFloor(t *testing.T) {
	h := newHarness(t)
	h.machine.available, h.machine.commit = 16*gigabyte, 6*gigabyte

	_, err := h.service.Up(context.Background(), h.checkout, "task-a", 0)

	if err == nil || !strings.Contains(err.Error(), "6.0 GB of commit") {
		t.Fatalf("err = %v, want a refusal naming the short commit", err)
	}
}

func TestUpWaitsForMemoryAndStartsOnceItIsFree(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.machine.available, h.machine.commit = 6*gigabyte, 6*gigabyte
	reads := 0
	h.service.Memory = func() (Memory, error) {
		reads++
		if reads == 4 {
			h.machine.give(4 * gigabyte)
		}
		return h.machine.read()
	}

	// Act
	line, err := h.service.Up(context.Background(), h.checkout, "task-a", 10*time.Minute)

	// Assert
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !strings.Contains(h.out.String(), "waiting for memory") || !strings.Contains(h.out.String(), "4 GB floor") {
		t.Errorf("out = %q, want it to say why it waits", h.out.String())
	}
	if strings.Count(h.out.String(), "waiting for memory") != 1 {
		t.Errorf("out = %q, want the reason said once in the first minute", h.out.String())
	}
	if !strings.Contains(line, "started") {
		t.Errorf("line = %q, want the stack started", line)
	}
}

func TestUpGivesUpAfterItsWaitAndSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.machine.available, h.machine.commit = 6*gigabyte, 6*gigabyte

	_, err := h.service.Up(context.Background(), h.checkout, "task-a", 2*time.Minute)

	if err == nil || !strings.Contains(err.Error(), "gave up after 2m0s") || !strings.Contains(err.Error(), "4 GB floor") {
		t.Fatalf("err = %v, want it to give up and say why", err)
	}
	if len(h.docker.calls) != 0 {
		t.Errorf("docker calls = %v, want nothing started", h.docker.calls)
	}
}

func TestUpAddsTheMeasuredCostOnceItHasOne(t *testing.T) {
	// Arrange: the last run measured 6 GB, so 9 GB free leaves 3 GB, where
	// the 3 GB estimate would have left 6.
	h := newHarness(t)
	measured := time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)
	if err := WriteRecord(h.service.StateDir, Record{Stacks: map[string]Stack{"PrecisionDocs-AI": {Project: "PrecisionDocs-AI", Cost: Cost{Bytes: 6 * gigabyte, MeasuredAt: measured}}}}); err != nil {
		t.Fatal(err)
	}
	h.machine.available, h.machine.commit = 9*gigabyte, 16*gigabyte

	// Act
	_, err := h.service.Up(context.Background(), h.checkout, "task-a", 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "6.0 GB") || !strings.Contains(err.Error(), "measured 2026-10-07") {
		t.Fatalf("err = %v, want a refusal on the measured 6 GB", err)
	}
}

func TestUpRefusesATaskThatIsNotLive(t *testing.T) {
	h := newHarness(t)

	_, err := h.service.Up(context.Background(), h.checkout, "gone-task", 0)

	if err == nil || !strings.Contains(err.Error(), "gone-task is not a live task") {
		t.Fatalf("err = %v, want the dead task refused", err)
	}
	if len(h.docker.calls) != 0 {
		t.Errorf("docker calls = %v, want none", h.docker.calls)
	}
}

func TestUpRefusesAProjectThatDeclaresNoServices(t *testing.T) {
	h := newHarness(t)

	_, err := h.service.Up(context.Background(), `C:\dev\SIQstack-Website`, "task-a", 0)

	if err == nil || !strings.Contains(err.Error(), "declares no local services") || !strings.Contains(err.Error(), ManifestFileName) {
		t.Fatalf("err = %v, want it to name the missing declaration", err)
	}
}

func TestUpRefusesWhenTheProjectsCheckFails(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.check.exit, h.check.out = 1, ".env.docker.local points at production Supabase"

	// Act
	_, err := h.service.Up(context.Background(), h.checkout, "task-a", 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "points at production Supabase") {
		t.Fatalf("err = %v, want the check's own words", err)
	}
	if len(h.docker.calls) != 0 {
		t.Errorf("docker calls = %v, want nothing started", h.docker.calls)
	}
}

func TestAFailedStartStopsWhatItStarted(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.docker.upErr = errors.New("backend exited 1")

	// Act
	_, err := h.service.Up(context.Background(), h.checkout, "task-a", 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "backend exited 1") {
		t.Fatalf("err = %v, want the start's failure", err)
	}
	if want := []string{"start-engine", "up backend,worker-light", "down ", "stop-engine"}; !slices.Equal(h.docker.calls, want) {
		t.Errorf("docker calls = %v, want %v", h.docker.calls, want)
	}
	record := h.record(t)
	if record.Stacks["PrecisionDocs-AI"].IsUp() || record.Stacks["PrecisionDocs-AI"].Owned || record.Engine.StartedByCFO {
		t.Errorf("record = %+v, want nothing left held or owned", record)
	}
}

func TestAStackAlreadyRunningIsSharedAndNeverStopped(t *testing.T) {
	// Arrange: the Overlord's own stack runs every service the check needs.
	h := newHarness(t)
	h.docker.engine = true
	for _, service := range []string{"redis", "qdrant", "backend", "worker-light"} {
		h.docker.running[service] = true
	}

	// Act
	h.up(t, "task-a")
	h.down(t, "task-a")

	// Assert
	if len(h.docker.calls) != 0 || len(h.check.calls) != 0 {
		t.Fatalf("docker calls = %v check calls = %d, want the running stack joined as it is and never stopped", h.docker.calls, len(h.check.calls))
	}
	if len(h.docker.names()) != 4 {
		t.Errorf("running = %v, want the Overlord's stack left running", h.docker.names())
	}
}

func TestBesideARunningStackOnlyWhatCFOStartedIsStopped(t *testing.T) {
	// Arrange: redis and qdrant run already, the backend and the worker do
	// not.
	h := newHarness(t)
	h.docker.engine = true
	h.docker.running["redis"], h.docker.running["qdrant"] = true, true

	// Act
	h.up(t, "task-a")
	h.down(t, "task-a")

	// Assert
	if want := []string{"up backend,worker-light", "down backend,worker-light"}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want %v", h.docker.calls, want)
	}
	if got := h.docker.names(); !slices.Equal(got, []string{"qdrant", "redis"}) {
		t.Errorf("running = %v, want redis and qdrant left as they were", got)
	}
}

func TestUpStartsAHeldStackAgainWhenItsServicesStopped(t *testing.T) {
	h := newHarness(t)
	h.up(t, "task-a")
	h.docker.running = map[string]bool{}
	h.docker.calls = nil

	h.up(t, "task-b")

	if want := []string{"up backend,worker-light"}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want the stack started again", h.docker.calls)
	}
	if holders := h.record(t).Stacks["PrecisionDocs-AI"].HolderIDs(); !slices.Equal(holders, []string{"task-a", "task-b"}) {
		t.Errorf("holders = %v, want both kept", holders)
	}
}

func TestSweepStopsAStackNoLiveTaskHolds(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.up(t, "task-a")
	h.up(t, "task-b")
	h.docker.calls = nil
	delete(h.live, "task-a")
	delete(h.live, "task-b")

	// Act
	lines, err := h.service.Sweep(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if want := []string{"down ", "stop-engine"}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want %v", h.docker.calls, want)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "PrecisionDocs-AI") || !strings.Contains(lines[0], "no live task") {
		t.Errorf("lines = %v, want one saying the stack stopped because no live task held it", lines)
	}
	if h.record(t).Stacks["PrecisionDocs-AI"].IsUp() {
		t.Error("the stopped stack still has holders")
	}
}

func TestSweepKeepsAStackALiveTaskHolds(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.up(t, "task-a")
	h.up(t, "task-b")
	h.docker.calls = nil
	delete(h.live, "task-a")

	// Act
	lines, err := h.service.Sweep(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(h.docker.calls) != 0 || len(lines) != 0 {
		t.Fatalf("docker calls = %v lines = %v, want the stack left up for task-b", h.docker.calls, lines)
	}
	if holders := h.record(t).Stacks["PrecisionDocs-AI"].HolderIDs(); !slices.Equal(holders, []string{"task-b"}) {
		t.Errorf("holders = %v, want the dead task-a dropped", holders)
	}
}

func TestSweepWithNothingHeldTouchesNothing(t *testing.T) {
	h := newHarness(t)

	lines, err := h.service.Sweep(context.Background())

	if err != nil || len(lines) != 0 || len(h.docker.calls) != 0 {
		t.Fatalf("lines = %v err = %v calls = %v, want nothing done", lines, err, h.docker.calls)
	}
}

func TestReleaseTaskReleasesEveryStackItHolds(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.up(t, "task-a")
	h.docker.calls = nil

	// Act
	lines, err := h.service.ReleaseTask(context.Background(), "task-a")

	// Assert
	if err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	if want := []string{"down ", "stop-engine"}; !slices.Equal(h.docker.calls, want) {
		t.Fatalf("docker calls = %v, want %v", h.docker.calls, want)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "PrecisionDocs-AI") {
		t.Errorf("lines = %v, want one naming the released stack", lines)
	}
}

func TestReleaseTaskWithNothingHeldSaysNothing(t *testing.T) {
	h := newHarness(t)

	lines, err := h.service.ReleaseTask(context.Background(), "task-a")

	if err != nil || len(lines) != 0 || len(h.docker.calls) != 0 {
		t.Fatalf("lines = %v err = %v calls = %v, want nothing", lines, err, h.docker.calls)
	}
}

func TestDownForATaskThatHoldsNothingChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.up(t, "task-a")
	h.docker.calls = nil

	line := h.down(t, "task-b")

	if len(h.docker.calls) != 0 || !strings.Contains(line, "holds no") {
		t.Fatalf("line = %q calls = %v, want nothing released", line, h.docker.calls)
	}
}

// A start says what it does before each step that can take minutes, so a
// goblin waiting on it sees progress rather than silence.
func TestUpSaysWhatItStartsBeforeEachSlowStep(t *testing.T) {
	h := newHarness(t)

	h.up(t, "task-a")

	want := "cfo services: starting the Docker engine\n" +
		"cfo services: starting PrecisionDocs-AI's backend, worker-light and what they depend on, which builds any image they lack first and can take many minutes\n"
	if got := h.out.String(); got != want {
		t.Errorf("progress = %q, want %q", got, want)
	}
}

// When Docker Desktop was quit under a held stack, nothing of it runs, so the
// last release marks it stopped instead of failing on a compose down the
// stopped engine cannot answer, which the janitor would retry every hour.
func TestTheLastReleaseAfterTheEngineWasQuitMarksTheStackStopped(t *testing.T) {
	// Arrange
	h := newHarness(t)
	h.up(t, "task-a")
	h.docker.engine = false
	h.docker.running = map[string]bool{}
	h.docker.calls = nil

	// Act
	line := h.down(t, "task-a")

	// Assert
	if len(h.docker.calls) != 0 {
		t.Fatalf("docker calls = %v, want none against a stopped engine", h.docker.calls)
	}
	record := h.record(t)
	if stack := record.Stacks["PrecisionDocs-AI"]; stack.IsUp() || stack.Owned || record.Engine.StartedByCFO {
		t.Errorf("record = %+v, want the stack and the engine no longer cfo's", record)
	}
	if want := "task-a was the last to hold them: PrecisionDocs-AI's services were already stopped with the Docker engine"; line != want {
		t.Errorf("line = %q, want %q", line, want)
	}
}

// A start ended part way through its build, as the memory floor ends one,
// never measures its own drop. The stop that follows gives back only part of
// what the start took while Docker's VM returns memory slowly, so measuring
// the stop alone wrote a cost under the estimate (PrecisionDocs, 2026-10-09:
// the build took 6.7 GB and the stop recorded 3.6 GB, lowering the next
// start's mark). The stop measures from the memory read before the start.
func TestAStopAfterAStartCutShortRecordsWhatTheStartTook(t *testing.T) {
	// Arrange: 12 GB free, the engine takes 2 GB, the build 7 GB more before
	// its process ends, and the build's memory is not given back at once.
	h := newHarness(t)
	h.docker.upHook = func() {
		h.machine.take(7 * gigabyte)
		runtime.Goexit()
	}
	cutShort := make(chan struct{})
	go func() {
		defer close(cutShort)
		_, _ = h.service.Up(context.Background(), h.checkout, "task-a", 0)
	}()
	<-cutShort
	h.docker.upHook = nil

	// Act
	h.down(t, "task-a")

	// Assert
	stack := h.record(t).Stacks["PrecisionDocs-AI"]
	if stack.Cost.Bytes != 9*gigabyte {
		t.Errorf("cost = %.1f GB, want the 9 GB the start took, not the 2 GB the stop gave back", float64(stack.Cost.Bytes)/float64(gigabyte))
	}
	if stack.MemoryBeforeStart != 0 {
		t.Errorf("memory before start = %d, want it cleared once the stop measured from it", stack.MemoryBeforeStart)
	}
}

// A start that finished measured itself, so a later stop measures only what
// it gives back, never the memory the rest of the fleet took meanwhile.
func TestAStopAfterAFinishedStartMeasuresOnlyItsOwnRise(t *testing.T) {
	// Arrange: the start measures 3 GB, then the fleet takes 5 GB more.
	h := newHarness(t)
	h.up(t, "task-a")
	h.machine.take(5 * gigabyte)

	// Act
	h.down(t, "task-a")

	// Assert
	if cost := h.record(t).Stacks["PrecisionDocs-AI"].Cost.Bytes; cost != 3*gigabyte {
		t.Errorf("cost = %.1f GB, want the 3 GB the start measured", float64(cost)/float64(gigabyte))
	}
}
