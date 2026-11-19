package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// unmeasuredStartCost is what a start of a harness is taken to cost until
	// one has been measured: about what a new goblin session took.
	unmeasuredStartCost = 1 << 30
	// launchSettle is how long after its terminal's host started a goblin is
	// still counted as taking the memory of its start.
	launchSettle = 5 * time.Minute
	// startCostsKept is how many measured start costs of one harness are kept.
	startCostsKept = 5
	// startCostsSchema names the format of state/start-costs.json.
	startCostsSchema = "ceo-start-costs.v1"
)

// StartCost is one measured start of a goblin: the private working set its
// process tree held when it settled, in bytes, when that was read and the
// task it was.
type StartCost struct {
	Bytes uint64    `json:"bytes"`
	At    time.Time `json:"at"`
	Task  string    `json:"task"`
}

// startCosts is state/start-costs.json: the latest measured start costs of
// each harness, oldest first.
type startCosts struct {
	Schema    string                 `json:"schema"`
	Harnesses map[string][]StartCost `json:"harnesses,omitempty"`
}

// startCostsPath is the file that keeps the measured start costs.
func startCostsPath(stateDir string) string {
	return filepath.Join(stateDir, "start-costs.json")
}

// readStartCosts reads the measured start costs of stateDir. A home that
// measured none has none, and a file that cannot be read is an error, since
// costs it hides would admit starts the machine has no room for.
func readStartCosts(stateDir string) (map[string][]StartCost, error) {
	data, err := fsx.ReadFile(startCostsPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("start costs: %w", err)
	}
	var costs startCosts
	if err := json.Unmarshal(data, &costs); err != nil || costs.Schema != startCostsSchema {
		return nil, fmt.Errorf("start costs: %s is unreadable", startCostsPath(stateDir))
	}
	return costs.Harnesses, nil
}

// writeStartCost adds sample to the measured start costs of harness in
// stateDir and keeps the latest startCostsKept of them. Its caller is the
// only writer of the file.
func writeStartCost(stateDir, harness string, sample StartCost) error {
	kept, err := readStartCosts(stateDir)
	if err != nil {
		return err
	}
	if kept == nil {
		kept = map[string][]StartCost{}
	}
	samples := append(kept[harness], sample)
	kept[harness] = samples[max(0, len(samples)-startCostsKept):]
	data, err := json.Marshal(startCosts{Schema: startCostsSchema, Harnesses: kept})
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(startCostsPath(stateDir), data)
}

// PendingStart is a goblin whose start is still settling: its harness's start
// cost less what its process tree holds already, in bytes.
type PendingStart struct {
	Task      string
	Harness   string
	Remaining uint64
}

// ReservedStart is a start of a project's services that waits for memory or
// is starting, and the memory it was admitted with.
type ReservedStart struct {
	Task    string
	Project string
	Cost    uint64
}

// Room is what a start of a goblin needs of the machine's free memory beyond
// the floor, at one reading: the floor from the fleet's settings, what a start
// of each harness measured to cost, the starts still taking their memory and
// the services starts that were admitted with theirs.
type Room struct {
	Floor    uint64
	Costs    map[string][]StartCost
	Pending  []PendingStart
	Reserved []ReservedStart
}

// Cost is what a start of harness takes: the largest of its last
// startCostsKept measured costs, or unmeasuredStartCost while none is measured.
func (r Room) Cost(harness string) uint64 {
	samples := r.Costs[harness]
	if len(samples) == 0 {
		return unmeasuredStartCost
	}
	var largest uint64
	for _, sample := range samples[max(0, len(samples)-startCostsKept):] {
		largest = max(largest, sample.Bytes)
	}
	return largest
}

// pending is the memory the starts under way have yet to take.
func (r Room) pending() uint64 {
	var total uint64
	for _, start := range r.Pending {
		total += start.Remaining
	}
	return total
}

// reserved is the memory the services starts under way were admitted with.
func (r Room) reserved() uint64 {
	var total uint64
	for _, start := range r.Reserved {
		total += start.Cost
	}
	return total
}

// Need is the free memory and commit a start of harness needs: the floor,
// what the starts under way and the services starts hold, and its own cost.
func (r Room) Need(harness string) uint64 {
	return r.Floor + r.pending() + r.reserved() + r.Cost(harness)
}

// costliest is the harness whose start costs the most, the first of the
// spawn harnesses on a tie.
func (r Room) costliest() string {
	costliest := spawnHarnesses[0]
	for _, harness := range spawnHarnesses[1:] {
		if r.Cost(harness) > r.Cost(costliest) {
			costliest = harness
		}
	}
	return costliest
}

// startHarness is the harness to price a start by: harness, or the costliest
// while the start's harness is not chosen yet and goblins spawn routes it.
func (r Room) startHarness(harness string) string {
	if harness == "" {
		return r.costliest()
	}
	return harness
}

// Shortfall says why the free memory or commit in memory, the lesser of the
// two, is under what a start of harness needs, naming the floor, the start's
// cost and what the starts under way hold, or is empty when the start fits.
func (r Room) Shortfall(memory Memory, harness string) string {
	need := r.Need(harness)
	if min(memory.Available, memory.CommitAvailable) >= need {
		return ""
	}
	var free string
	switch {
	case memory.Available < need && memory.CommitAvailable < need:
		free = fmt.Sprintf("Only %.1f GB of memory and %.1f GB of commit (RAM plus page file) are free", gigabytes(memory.Available), gigabytes(memory.CommitAvailable))
	case memory.Available < need:
		free = fmt.Sprintf("Only %.1f GB of memory is free", gigabytes(memory.Available))
	default:
		free = fmt.Sprintf("Only %.1f GB of commit (RAM plus page file) is free", gigabytes(memory.CommitAvailable))
	}
	parts := []string{
		fmt.Sprintf("floor %.1f", gigabytes(r.Floor)),
		fmt.Sprintf("start %.1f", gigabytes(r.Cost(harness))),
	}
	if held := r.pending(); held > 0 {
		parts = append(parts, fmt.Sprintf("goblins starting %.1f", gigabytes(held)))
	}
	if held := r.reserved(); held > 0 {
		parts = append(parts, fmt.Sprintf("services starting %.1f", gigabytes(held)))
	}
	return fmt.Sprintf("%s, and a %s start needs %.1f GB: %s", free, harness, gigabytes(need), strings.Join(parts, ", "))
}

// MachineMemoryFloor reads the free memory the fleet keeps from the home's
// settings. Settings that cannot be read are an error, never a default.
func MachineMemoryFloor(h home.Home) (uint64, error) {
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		return 0, fmt.Errorf("the memory floor cannot be read: %w", err)
	}
	return fleetconfig.Bytes(settings.MemoryFloorGB), nil
}

// ReadRoom reads the room at now: the floor from the home's settings, the
// measured start costs, each native goblin whose terminal host runs and
// started within launchSettle of now with what its harness's start still has
// to take beyond what its process tree holds, and each live services
// reservation. Its pending starts count their full cost while processes is nil
// or fails. A settings file or a record that cannot be read is an error, never
// a default, since a default could admit a start the machine has no room for.
func ReadRoom(h home.Home, processes func() ([]fleettree.Process, error), now time.Time) (Room, error) {
	floor, err := MachineMemoryFloor(h)
	if err != nil {
		return Room{}, err
	}
	costs, err := readStartCosts(h.State)
	if err != nil {
		return Room{}, err
	}
	room := Room{Floor: floor, Costs: costs}
	var listed []fleettree.Process
	var isListed, isUnlisted bool
	for _, meta := range liveTasks(h.State) {
		if meta.Backend != "native" {
			continue
		}
		record, err := host.ReadRecord(h.State, meta.ID)
		if err != nil || now.Sub(record.Started) >= launchSettle || !host.Running(record) {
			continue
		}
		if !isListed && !isUnlisted && processes != nil {
			if listed, err = processes(); err == nil {
				isListed = true
			} else {
				isUnlisted = true
			}
		}
		remaining := room.Cost(meta.Harness)
		if isListed {
			remaining -= min(remaining, treeMemory(listed, record.HostPID))
		}
		room.Pending = append(room.Pending, PendingStart{Task: meta.ID, Harness: meta.Harness, Remaining: remaining})
	}
	held, err := services.ReadRecord(h.State)
	if err != nil {
		return Room{}, err
	}
	for project, admitting := range held.Admitting {
		if admitting.IsLive() {
			room.Reserved = append(room.Reserved, ReservedStart{Task: admitting.Task, Project: project, Cost: admitting.Cost})
		}
	}
	slices.SortFunc(room.Reserved, func(left, right ReservedStart) int { return strings.Compare(left.Project, right.Project) })
	return room, nil
}

// treeMemory is the private working set, in bytes, of the process pid and all
// its descendants in processes.
func treeMemory(processes []fleettree.Process, pid int) uint64 {
	children := map[int][]fleettree.Process{}
	var total uint64
	for _, process := range processes {
		children[process.ParentPID] = append(children[process.ParentPID], process)
		if process.PID == pid {
			total += process.Memory
		}
	}
	seen := map[int]bool{pid: true}
	for queue := []int{pid}; len(queue) > 0; queue = queue[1:] {
		for _, child := range children[queue[0]] {
			if !seen[child.PID] {
				seen[child.PID] = true
				total += child.Memory
				queue = append(queue, child.PID)
			}
		}
	}
	return total
}

// room reads the room as of now with the processes the dispatch lists.
func (s *Service) room(now time.Time) (Room, error) {
	var processes func() ([]fleettree.Process, error)
	if s.Options.Dispatch != nil {
		processes = s.Options.Dispatch.Processes
	}
	return ReadRoom(s.Store.Home, processes, now)
}

// ResumeEngine is the harness and model a paused goblin resumes on: the engine
// it was given while paused, as goblins resume does, else the one it paused on.
func ResumeEngine(stateDir string, meta state.TaskMeta) (harness, model string, err error) {
	choice, err := state.ReadEngineChoice(stateDir, meta.ID)
	if err == nil && choice.Generation == meta.SpawnGen {
		return choice.Harness, choice.Model, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("resume settings of %s: %w", meta.ID, err)
	}
	return meta.Harness, meta.Model, nil
}
