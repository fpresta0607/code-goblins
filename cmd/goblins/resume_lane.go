package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// resumeLane is the lane a paused task resumes on when nothing chose one for
// it while it was paused (issue #2164). Its recorded harness and model resume
// when they can still run: the harness's own catalog offers the model, and
// the quota reading finds its allowance neither used up nor at the home's
// weekly floor. Otherwise the lane table chooses, for the task's brief, a
// lane that can run, and rerouted is the status line that says so. A task
// whose harness was chosen by hand, or a table with no usable lane, refuses
// the resume with why. A catalog or quota that cannot be read is no evidence,
// so it never stops a resume.
func resumeLane(h home.Home, runtime commandRuntime, meta state.TaskMeta, briefPath string) (lane routing.ExecutionLane, rerouted string, err error) {
	recorded := routing.ExecutionLane{Name: meta.RouteLane, Harness: meta.Harness, Model: meta.Model, Effort: meta.Effort}
	if meta.Harness == "" {
		return recorded, "", nil
	}
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		return recorded, "", err
	}
	report, skipped := readQuota(runtime)
	now := quotaClock(runtime)
	usable := func(startFloor float64) routing.Usable {
		allowance := usableLane(report, skipped, settings, now, startFloor)
		return func(lane routing.ExecutionLane) (bool, string) {
			if isOffered, note := offersModel(runtime, lane); !isOffered {
				return false, note
			}
			if allowance == nil {
				return true, ""
			}
			return allowance(lane)
		}
	}
	// The start floor keeps allowance back for new work, so it does not
	// hold back the resume of work already started.
	isUsable, why := usable(0)(recorded)
	if isUsable {
		return recorded, "", nil
	}
	cannot := fmt.Sprintf("%s %s can no longer run (%s)", meta.Harness, meta.Model, why)
	if meta.RouteLane == "explicit" {
		return recorded, "", fmt.Errorf("%s, and its harness was chosen by hand; give it another with goblins switch %s", cannot, meta.ID)
	}
	inputs, err := supervisor.LoadRouteInputs(h.Data, meta.Project)
	if err != nil {
		return recorded, "", fmt.Errorf("%s, and the lane table cannot be read: %w", cannot, err)
	}
	if len(inputs.Table.Lanes) == 0 {
		return recorded, "", fmt.Errorf("%s, and no lane table names another", cannot)
	}
	brief, err := fsx.ReadFile(briefPath)
	if err != nil {
		return recorded, "", fmt.Errorf("%s, and its brief cannot be routed: %w", cannot, err)
	}
	choice, err := routing.Choose(routing.Classify(string(brief)), inputs.Table, usable(inputs.Table.StartFloorPercent))
	if err != nil {
		return recorded, "", fmt.Errorf("%s, and %w", cannot, err)
	}
	return choice.ExecutionLane, fmt.Sprintf("resume-rerouted: %s, so it resumes on %s in lane %s", cannot, strings.Join(strings.Fields(choice.Harness+" "+choice.Model+" "+choice.Effort), " "), choice.Name), nil
}

// offersModel says whether lane's harness still offers its model, by the
// catalog the harness keeps for its signed-in account. The harness's default
// model, and a catalog that is empty or cannot be read, are no evidence
// against it.
func offersModel(runtime commandRuntime, lane routing.ExecutionLane) (bool, string) {
	if runtime.harnessModels == nil || lane.Model == "" || lane.Model == "default" {
		return true, ""
	}
	models, err := runtime.harnessModels(harness.Kind(lane.Harness))
	if err != nil || len(models) == 0 {
		return true, ""
	}
	if slices.ContainsFunc(models, func(model supervisor.EngineModel) bool { return routing.SameModel(lane.Harness, lane.Model, model.ID) }) {
		return true, ""
	}
	return false, fmt.Sprintf("%s's model catalog does not offer %s", lane.Harness, lane.Model)
}
