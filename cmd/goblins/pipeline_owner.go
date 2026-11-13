package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// correctionCommand is how the goblin that owns a task submits its correction
// of a handed-back step: a published CI failure goes back through the native
// handback, and a Test or Lint failure to the run's validation family.
func correctionCommand(id string, run pipeline.Handback) string {
	if run.Step == "ci" {
		return fmt.Sprintf("release it to this worktree with goblins pipeline handback %s --run %s, commit the correction on the published head, and submit it with goblins pipeline handback %s --run %s --resume --receipt <receipt>", id, run.RunID, id, run.RunID)
	}
	return fmt.Sprintf("commit one correction of exactly those findings, then submit it with goblins pipeline validate-authored %s --run %s --findings %s", id, run.RunID, strings.Join(run.FindingIDs(), ","))
}

// reportHandback tells the goblin whose run parked at a Test, Lint or CI
// failure its policy hands back what to fix and how to submit it, and records
// in the task's status that it is fixing them, not waiting on the CEO. For a
// Test or Lint failure it also puts the run's head into the branch, so the
// correction carries every commit the run validated.
func reportHandback(ctx context.Context, h home.Home, reader pipeline.Reader, meta state.TaskMeta, selection pipeline.Selection, worktree, branch string, out io.Writer) error {
	run, isHandedBack, err := reader.HandedBack(ctx, meta.Project, branch, selection.Policy)
	if err != nil {
		return fmt.Errorf("pipeline: the gate stopped, and whether it handed a failure back cannot be read: %w", err)
	}
	if !isHandedBack {
		return nil
	}
	ids := strings.Join(run.FindingIDs(), ",")
	fmt.Fprintf(out, "pipeline handback: run %s's %s step failed, and under policy version %d its gate repairs none of it, so its findings %s are this task's to fix\n", run.RunID, run.Step, selection.Policy.Version, ids)
	for _, finding := range run.Findings {
		fmt.Fprintf(out, "pipeline handback: %s (%s): %s; log %s\n", finding.ID, finding.Severity, finding.Description, run.Log)
	}
	fmt.Fprintf(out, "pipeline handback: read the log whole with no-mistakes axi logs --run %s --step %s\n", run.RunID, run.Step)
	if run.Step != "ci" {
		switch ref, err := reader.TakeRunHead(ctx, worktree, run); {
		case err != nil:
			fmt.Fprintf(out, "pipeline handback: %v\n", err)
		case ref != "":
			fmt.Fprintf(out, "pipeline handback: the branch is now at the run's head %s, which %s keeps\n", run.Head, ref)
		}
	}
	fmt.Fprintf(out, "pipeline handback: %s; a correction of anything else is the CEO's to admit\n", correctionCommand(meta.ID, run))
	if run.Step == "test" && meta.ImplementModel != "" {
		if err := escalateImplement(h, meta.ID, run.RunID, out); err != nil {
			fmt.Fprintf(out, "pipeline handback: the implement role's escalation failed: %v\n", err)
		}
	}
	return state.AppendStatus(h.State, meta.ID, fmt.Sprintf("working: fixing run %s's %s findings %s, which its gate handed back", run.RunID, run.Step, ids))
}

// escalateImplement moves task id's implement worker up one tier of its
// harness, because run's Test step failed and a failed verification escalates
// the role whose work it checked. The move is recorded on the task with the
// run that caused it, so reading the same run again moves it no further, and a
// worker that reaches its lead's tier is retired. The goblin implements the
// correction and every later slice itself, since its session's worker keeps
// its old model until a relaunch gives it the new one.
func escalateImplement(h home.Home, id, runID string, out io.Writer) error {
	policy, err := routing.Load(h.Data)
	if err != nil {
		return err
	}
	table, err := policy.LaneTable()
	if err != nil {
		return err
	}
	var from, to, lead routing.ExecutionLane
	if err := updateTaskRecord(h.State, id, func(meta *state.TaskMeta) error {
		if meta.ImplementModel == "" || meta.EscalatedRun == runID {
			return nil
		}
		lead = routing.ExecutionLane{Name: meta.RouteLane, Harness: meta.Harness, Model: meta.Model, Effort: meta.Effort}
		from = routing.ExecutionLane{Name: meta.ImplementLane, Harness: meta.Harness, Model: meta.ImplementModel, Effort: meta.ImplementEffort}
		to = table.EscalateRole(from, lead)
		if to == from {
			return nil
		}
		meta.EscalatedRun = runID
		if to == lead {
			meta.ImplementLane, meta.ImplementModel, meta.ImplementEffort = "", "", ""
		} else {
			meta.ImplementLane, meta.ImplementModel, meta.ImplementEffort = to.Name, to.Model, to.Effort
		}
		return nil
	}); err != nil {
		return err
	}
	if to == from {
		return nil
	}
	target := fmt.Sprintf("%s %s %s", to.Name, to.Model, to.Effort)
	if to == lead {
		target = "the lead " + target
	}
	if err := state.AppendStatus(h.State, id, fmt.Sprintf("role-escalated: implement %s %s %s -> %s after run %s's failed test step", from.Name, from.Model, from.Effort, target, runID)); err != nil {
		return err
	}
	if to == lead {
		fmt.Fprintf(out, "pipeline handback: a failed test step moves the implement role up one tier, to this task's lead %s %s %s, so its worker is retired: implement this correction and every later slice yourself\n", to.Name, to.Model, to.Effort)
		return nil
	}
	fmt.Fprintf(out, "pipeline handback: a failed test step moves the implement role up one tier, to %s %s %s, which the task's next relaunch gives its worker; until then implement this correction and every later slice yourself, since this session's worker stays on %s\n", to.Name, to.Model, to.Effort, from.Model)
	return nil
}

// checkOwnerCIResume holds a resume of a CI handback that is not the CEO's to
// the owner path a Test or Lint correction takes: it comes from the task's
// own goblin, and its one commit on the published head stays in the scope of
// the CI failure. The CEO's own resume is not held to it.
func checkOwnerCIResume(ctx context.Context, h home.Home, reader pipeline.Reader, meta state.TaskMeta, selection pipeline.Selection, worktree, branch string) error {
	ceoResume := fmt.Sprintf("the CEO resumes any other with goblins pipeline handback %s --run <run> --resume --receipt <receipt>", meta.ID)
	if err := ownGoblin(h.State, meta.ID); err != nil {
		return fmt.Errorf("pipeline: under policy version %d a CI correction is resumed by task %s's own goblin (%v), and %s", selection.Policy.Version, meta.ID, err, ceoResume)
	}
	run, isHandedBack, err := reader.HandedBack(ctx, meta.Project, branch, selection.Policy)
	if err != nil {
		return err
	}
	if !isHandedBack || run.Step != "ci" {
		return fmt.Errorf("pipeline: %s's latest run holds no CI failure its gate handed back, so %s", branch, ceoResume)
	}
	trusted, err := reader.TrustedHead(ctx, meta.Project)
	if err != nil {
		return err
	}
	if err := reader.CheckCorrection(ctx, worktree, trusted, run); err != nil {
		return fmt.Errorf("%w; %s", err, ceoResume)
	}
	return nil
}

// ownerCorrection submits the task's own goblin's one-commit correction of
// the findings its gate handed back to the run's validation family, with the
// run's launch and the task's frozen policy, so no allowance is renewed and no
// CEO admission is needed. Any correction outside that is refused before the
// run is stopped, naming the CEO's admission.
func ownerCorrection(ctx context.Context, h home.Home, reader pipeline.Reader, meta state.TaskMeta, selection pipeline.Selection, worktree, branch, runID, findings string, out io.Writer) error {
	ceoAdmission := fmt.Sprintf("the CEO admits any other correction with goblins pipeline validate-authored %s --admission-file <file>", meta.ID)
	if selection.Policy.Version < 7 {
		return fmt.Errorf("pipeline: task %s's policy version %d hands no failure back to its goblin, so %s", meta.ID, selection.Policy.Version, ceoAdmission)
	}
	run, isHandedBack, err := reader.HandedBack(ctx, meta.Project, branch, selection.Policy)
	if err != nil {
		return err
	}
	if !isHandedBack || run.RunID != runID {
		return fmt.Errorf("pipeline: run %s is parked at no Test or Lint failure its gate handed back, so %s", runID, ceoAdmission)
	}
	if run.Step == "ci" {
		return fmt.Errorf("pipeline: run %s's CI failure goes back through the native handback: %s", run.RunID, correctionCommand(meta.ID, run))
	}
	declared, open := strings.Split(findings, ","), run.FindingIDs()
	slices.Sort(declared)
	slices.Sort(open)
	if !slices.Equal(declared, open) {
		return fmt.Errorf("pipeline: --findings must name exactly run %s's open %s findings, %s, and %s", run.RunID, run.Step, strings.Join(open, ","), ceoAdmission)
	}
	trusted, err := reader.TrustedHead(ctx, meta.Project)
	if err != nil {
		return err
	}
	if err := reader.CheckCorrection(ctx, worktree, trusted, run); err != nil {
		return fmt.Errorf("%w; %s", err, ceoAdmission)
	}
	file, err := reader.OwnerAdmission(ctx, worktree, trusted, meta.TaskTmp, run, selection, pipeline.NativeEnv(reader.Root))
	if err != nil {
		return err
	}
	if err := state.AppendStatus(h.State, meta.ID, fmt.Sprintf("pipeline-owner-correction: run=%s step=%s findings=%s by=%s", run.RunID, run.Step, strings.Join(open, ","), meta.ID)); err != nil {
		return err
	}
	return pipelineAdmission(ctx, h, reader, meta, selection, worktree, branch, "validate-authored", file, out)
}
