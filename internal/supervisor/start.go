package supervisor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const (
	// memoryFloor is the free memory the fleet keeps: nothing starts under it.
	memoryFloor = 3 << 30
	// memoryNext is the free memory at which the CFO starts the next queued
	// task: the floor and about 1 GB for the new session.
	memoryNext = 4 << 30
)

// The fleet's defaults for a goblin whose backlog row and brief name none.
const (
	defaultHarness = "claude"
	defaultModel   = "claude-opus-5-5"
	defaultEffort  = "xhigh"
)

var (
	spawnHarnesses = []string{"claude", "codex", "pi", "kimi"}
	spawnModes     = []string{"no-mistakes", "direct-PR", "local-only"}
	spawnValue     = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	briefSetting   = regexp.MustCompile(`(?i)^\s*(harness|model|effort|mode)\s*:\s*(\S+)\s*$`)
)

// Dispatch is what a queued task's Start reads and runs on this machine:
// its memory, and cfo spawn itself. Without it the board starts no goblin.
type Dispatch struct {
	// Memory reads the machine's available and total physical memory, in
	// bytes.
	Memory func() (available, total uint64, err error)
	// Spawn runs cfo with args and returns what it printed.
	Spawn func(ctx context.Context, args []string) (string, error)
}

// Memory is the machine's available memory beside the fleet's floor, under
// which nothing starts, and the mark at which the CFO starts the next task.
type Memory struct {
	Available uint64 `json:"available"`
	Total     uint64 `json:"total"`
	Floor     uint64 `json:"floor"`
	Next      uint64 `json:"next"`
}

// startPlan is the cfo spawn a Start runs.
type startPlan struct {
	id, project, brief, harness, model, effort, mode string
}

func (p startPlan) args() []string {
	args := []string{"spawn", p.id, "--project", p.project, "--brief", p.brief, "--harness", p.harness}
	for _, flag := range [][2]string{{"--model", p.model}, {"--effort", p.effort}, {"--mode", p.mode}} {
		if flag[1] != "" {
			args = append(args, flag[0], flag[1])
		}
	}
	return args
}

// startTask serves POST /api/tasks/start: the Overlord's Start on a queued
// task, which dispatches it through cfo spawn as the CFO would.
func (h *HTTP) startTask(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Task string `json:"task"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if state.ValidTaskID(input.Task) != nil {
		apiError(w, http.StatusBadRequest, "Invalid task ID")
		return
	}
	if err := h.Service.startTask(input.Task); err != nil {
		var refusal StartRefusal
		if !errors.As(err, &refusal) {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respond(w, http.StatusConflict, struct {
			Error   string `json:"error"`
			Passing bool   `json:"passing"`
		}{bounded(refusal.Reason, 1500), refusal.Passing})
		return
	}
	h.Service.notify()
	// Every snapshot from this revision on shows this start, not a failure
	// of the last one.
	h.Service.mu.Lock()
	revision := h.Service.revision
	h.Service.mu.Unlock()
	respond(w, http.StatusAccepted, struct {
		Starting bool   `json:"starting"`
		Revision uint64 `json:"revision"`
	}{true, revision})
}

// startTask checks that id can start now and starts cfo spawn for it; one
// task starts at a time, and the board shows it starting until spawn ends.
func (s *Service) startTask(id string) error {
	dispatch := s.Options.Dispatch
	if dispatch == nil {
		return StartRefusal{Reason: "This board cannot start goblins"}
	}
	s.starts.Lock()
	defer s.starts.Unlock()
	if s.startErrors == nil {
		s.startErrors = map[string]string{}
	}
	if s.starting != "" {
		return StartRefusal{Reason: s.starting + " is starting; start another once it is up", Passing: true}
	}
	plan, err := planStart(s.Store.Home, id)
	if err != nil {
		return err
	}
	available, _, err := dispatch.Memory()
	if err != nil {
		return StartRefusal{Reason: "Free memory cannot be read, so nothing starts: " + err.Error()}
	}
	if available < memoryFloor {
		// Rounded down, so memory just under the floor never reads as 3.0 GB.
		return StartRefusal{Reason: fmt.Sprintf("Only %.1f GB of memory is free, under the fleet's 3 GB floor; start it once memory frees", math.Floor(float64(available)/(1<<30)*10)/10), Passing: true}
	}
	s.starting = id
	delete(s.startErrors, id)
	go s.runStart(dispatch, plan)
	return nil
}

// runStart runs cfo spawn to its end, which confirms the goblin works before
// it returns, so it is never cut short: a killed spawn strands its task.
// Either way the CFO is told through the wake queue, as a notify tells it,
// before the card stops showing the start.
func (s *Service) runStart(dispatch *Dispatch, plan startPlan) {
	output, err := dispatch.Spawn(context.Background(), plan.args())
	failure := ""
	if err != nil {
		failure = spawnFailure(output, err)
	}
	h := s.Store.Home
	detail := "start failed: " + failure
	if err == nil {
		// Started now, it goes to the top of In progress, under the lock an
		// order the Overlord saves meanwhile takes.
		s.ordering.Lock()
		order, readErr := fleet.ReadAttention(h)
		if readErr == nil {
			readErr = fleet.WriteAttention(h, append([]string{plan.id}, slices.DeleteFunc(order, func(id string) bool { return id == plan.id })...))
		}
		s.ordering.Unlock()
		if readErr != nil {
			s.publish(readErr)
		}
		detail = "started: the Overlord started this from the board, at the top of In progress; its row is still in backlog.md's Queued section"
	}
	if _, err := wake.Append(h.State, "notify", plan.id, detail); err != nil {
		s.publish(err)
	} else if _, err := wake.PublishEpisode(h.State); err != nil {
		s.publish(err)
	}
	s.starts.Lock()
	s.starting = ""
	if failure != "" {
		s.startErrors[plan.id] = failure
	}
	s.starts.Unlock()
	s.notify()
}

// spawnFailure is why cfo spawn failed: the last line it printed, which is
// its refusal, or the error when it printed nothing.
func spawnFailure(output string, err error) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return bounded(last, 500)
	}
	return bounded(err.Error(), 500)
}

// planStart is the cfo spawn a Start of id runs, or why it cannot: id must be
// queued work with a brief and a project, and not already running. Harness,
// model, effort and mode come from the backlog row, then the brief, then the
// fleet's defaults.
func planStart(h home.Home, id string) (startPlan, error) {
	if _, err := os.Stat(filepath.Join(h.State, id+".meta")); err == nil {
		return startPlan{}, StartRefusal{Reason: id + " already runs; open it from In progress"}
	}
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		return startPlan{}, err
	}
	listed := func(row fleet.BacklogRow) bool { return row.Structured && row.ID == id }
	var row fleet.BacklogRow
	if at := slices.IndexFunc(backlog.Queued, listed); at >= 0 {
		row = backlog.Queued[at]
	}
	brief := filepath.Join(h.Data, id, "brief.md")
	_, briefErr := os.Stat(brief)
	switch {
	case slices.ContainsFunc(backlog.Parked, listed) || !row.Structured && briefErr != nil:
		return startPlan{}, StartRefusal{Reason: id + " is not queued"}
	case briefErr != nil:
		return startPlan{}, StartRefusal{Reason: id + " has no brief at data\\" + id + "\\brief.md yet; the CFO writes one before it can start"}
	case !row.Structured && !slices.ContainsFunc(queuedBriefs(h), func(task Task) bool { return task.ID == id }):
		return startPlan{}, StartRefusal{Reason: id + " is not queued"}
	}
	plan := startPlan{id: id, brief: brief, project: briefProject(brief)}
	if plan.project == "" {
		plan.project = row.Repo
	}
	if plan.project == "" {
		return startPlan{}, StartRefusal{Reason: "The brief for " + id + " names no project"}
	}
	named := briefSettings(brief)
	pick := func(fromRow, key, fallback string) string {
		switch {
		case fromRow != "":
			return fromRow
		case named[key] != "":
			return named[key]
		}
		return fallback
	}
	plan.harness = pick(row.Harness, "harness", defaultHarness)
	fallbackModel, fallbackEffort := "", ""
	if plan.harness == defaultHarness {
		fallbackModel, fallbackEffort = defaultModel, defaultEffort
	}
	plan.model = pick(row.Model, "model", fallbackModel)
	plan.effort = pick(row.Effort, "effort", fallbackEffort)
	plan.mode = pick(row.Mode, "mode", "")
	switch {
	case !slices.Contains(spawnHarnesses, plan.harness):
		return startPlan{}, StartRefusal{Reason: "The backlog row or brief names harness " + plan.harness + ", which cfo spawn does not run"}
	case plan.mode != "" && !slices.Contains(spawnModes, plan.mode):
		return startPlan{}, StartRefusal{Reason: "The backlog row or brief names mode " + plan.mode + ", which cfo spawn does not run"}
	case plan.model != "" && !spawnValue.MatchString(plan.model), plan.effort != "" && !spawnValue.MatchString(plan.effort):
		return startPlan{}, StartRefusal{Reason: "The backlog row or brief names a model or effort cfo spawn cannot take"}
	}
	return plan, nil
}

// briefSettings are the harness, model, effort and mode a brief names on
// lines of their own, such as "mode: direct-PR" under its Delivery heading.
func briefSettings(path string) map[string]string {
	settings := map[string]string{}
	lines, err := fsx.ReadLines(path)
	if err != nil {
		return settings
	}
	for _, line := range lines {
		if match := briefSetting.FindStringSubmatch(line); match != nil {
			key := strings.ToLower(match[1])
			if settings[key] == "" {
				settings[key] = match[2]
			}
		}
	}
	return settings
}
