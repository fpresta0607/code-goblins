package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// correctionCommand is how the goblin that owns a task submits its correction
// of a handed-back step: a published CI failure goes back through the native
// handback, and a Test or Lint failure to the run's validation family.
func correctionCommand(id string, run pipeline.Handback) string {
	if run.Step == "ci" {
		return fmt.Sprintf("release it to this worktree with cfo pipeline handback %s --run %s, commit the correction on the published head, and submit it with cfo pipeline handback %s --run %s --resume --receipt <receipt>", id, run.RunID, id, run.RunID)
	}
	return fmt.Sprintf("commit one correction of exactly those findings, then submit it with cfo pipeline validate-authored %s --run %s --findings %s", id, run.RunID, strings.Join(run.FindingIDs(), ","))
}

// reportHandback tells the goblin whose run parked at a Test, Lint or CI
// failure its policy hands back what to fix and how to submit it, and records
// in the task's status that it is fixing them, not waiting on the CFO. For a
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
	fmt.Fprintf(out, "pipeline handback: %s; a correction of anything else is the CFO's to admit\n", correctionCommand(meta.ID, run))
	return state.AppendStatus(h.State, meta.ID, fmt.Sprintf("working: fixing run %s's %s findings %s, which its gate handed back", run.RunID, run.Step, ids))
}

// checkOwnerCIResume holds a resume of a CI handback that is not the CFO's to
// the owner path a Test or Lint correction takes: it comes from the task's
// own goblin, and its one commit on the published head stays in the scope of
// the CI failure. The CFO's own resume is not held to it.
func checkOwnerCIResume(ctx context.Context, h home.Home, reader pipeline.Reader, meta state.TaskMeta, selection pipeline.Selection, worktree, branch string) error {
	cfoResume := fmt.Sprintf("the CFO resumes any other with cfo pipeline handback %s --run <run> --resume --receipt <receipt>", meta.ID)
	if err := ownGoblin(h.State, meta.ID); err != nil {
		return fmt.Errorf("pipeline: under policy version %d a CI correction is resumed by task %s's own goblin (%v), and %s", selection.Policy.Version, meta.ID, err, cfoResume)
	}
	run, isHandedBack, err := reader.HandedBack(ctx, meta.Project, branch, selection.Policy)
	if err != nil {
		return err
	}
	if !isHandedBack || run.Step != "ci" {
		return fmt.Errorf("pipeline: %s's latest run holds no CI failure its gate handed back, so %s", branch, cfoResume)
	}
	trusted, err := reader.TrustedHead(ctx, meta.Project)
	if err != nil {
		return err
	}
	if err := reader.CheckCorrection(ctx, worktree, trusted, run); err != nil {
		return fmt.Errorf("%w; %s", err, cfoResume)
	}
	return nil
}

// ownerCorrection submits the task's own goblin's one-commit correction of
// the findings its gate handed back to the run's validation family, with the
// run's launch and the task's frozen policy, so no allowance is renewed and no
// CFO admission is needed. Any correction outside that is refused before the
// run is stopped, naming the CFO's admission.
func ownerCorrection(ctx context.Context, h home.Home, reader pipeline.Reader, meta state.TaskMeta, selection pipeline.Selection, worktree, branch, runID, findings string, out io.Writer) error {
	cfoAdmission := fmt.Sprintf("the CFO admits any other correction with cfo pipeline validate-authored %s --admission-file <file>", meta.ID)
	if selection.Policy.Version < 7 {
		return fmt.Errorf("pipeline: task %s's policy version %d hands no failure back to its goblin, so %s", meta.ID, selection.Policy.Version, cfoAdmission)
	}
	run, isHandedBack, err := reader.HandedBack(ctx, meta.Project, branch, selection.Policy)
	if err != nil {
		return err
	}
	if !isHandedBack || run.RunID != runID {
		return fmt.Errorf("pipeline: run %s is parked at no Test or Lint failure its gate handed back, so %s", runID, cfoAdmission)
	}
	if run.Step == "ci" {
		return fmt.Errorf("pipeline: run %s's CI failure goes back through the native handback: %s", run.RunID, correctionCommand(meta.ID, run))
	}
	declared, open := strings.Split(findings, ","), run.FindingIDs()
	slices.Sort(declared)
	slices.Sort(open)
	if !slices.Equal(declared, open) {
		return fmt.Errorf("pipeline: --findings must name exactly run %s's open %s findings, %s, and %s", run.RunID, run.Step, strings.Join(open, ","), cfoAdmission)
	}
	trusted, err := reader.TrustedHead(ctx, meta.Project)
	if err != nil {
		return err
	}
	if err := reader.CheckCorrection(ctx, worktree, trusted, run); err != nil {
		return fmt.Errorf("%w; %s", err, cfoAdmission)
	}
	file, err := reader.OwnerAdmission(ctx, worktree, trusted, meta.TaskTmp, run, selection, nativeEnv(reader.Root))
	if err != nil {
		return err
	}
	if err := state.AppendStatus(h.State, meta.ID, fmt.Sprintf("pipeline-owner-correction: run=%s step=%s findings=%s by=%s", run.RunID, run.Step, strings.Join(open, ","), meta.ID)); err != nil {
		return err
	}
	return pipelineAdmission(ctx, h, reader, meta, selection, worktree, branch, "validate-authored", file, out)
}
