package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/measure"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// reportMeasurement records task meta's run for its done or failed report as
// runtime measures it, and says what it recorded. A measurement that fails
// is said and never fails the report.
func reportMeasurement(runtime commandRuntime, h home.Home, meta state.TaskMeta, outcome, pr string, stdout, stderr io.Writer) {
	if runtime.measureRun == nil {
		return
	}
	pr, _, _ = strings.Cut(strings.TrimSpace(pr), " ")
	record, err := runtime.measureRun(context.Background(), h, meta, outcome, pr)
	if err != nil {
		fmt.Fprintln(stderr, "goblins notify: this run's role measurements could not be recorded: "+err.Error())
		return
	}
	var roles []string
	for _, role := range record.Roles {
		if !slices.Contains(roles, role.Role) {
			roles = append(roles, role.Role)
		}
	}
	fmt.Fprintf(stdout, "Recorded this run's role measurements (%s) over %d gate runs.\n", strings.Join(roles, ", "), len(record.Runs))
	if len(record.Unmeasured) > 0 {
		fmt.Fprintf(stdout, "Unmeasured: %s.\n", strings.Join(record.Unmeasured, "; "))
	}
}

// measureRun records in the home's role measurements what each role of
// task meta's run spent, from its previous done or failed report, or its
// spawn, to this outcome's report, and returns the record. What cannot be
// read is named in the record as unmeasured, and an error means nothing was
// recorded.
func measureRun(ctx context.Context, h home.Home, meta state.TaskMeta, outcome, pr string) (measure.Record, error) {
	now := time.Now().UTC()
	records, err := measure.Read(h.State)
	if err != nil {
		return measure.Record{}, err
	}
	input := measure.Input{Task: meta.ID, Project: meta.Project, Outcome: outcome, PR: pr, To: now, Harness: strings.ToLower(meta.Harness), LeadModel: meta.Model, LeadEffort: meta.Effort, ImplementModel: meta.ImplementModel, ImplementEffort: meta.ImplementEffort}
	generationStart := func(generation string) (time.Time, bool) {
		nanoseconds, err := strconv.ParseInt(strings.TrimPrefix(generation, "s"), 10, 64)
		return time.Unix(0, nanoseconds).UTC(), err == nil
	}
	if from, isMeasured := measure.Since(records, meta.ID); isMeasured {
		input.From = from
	} else if from, isKnown := generationStart(cmp.Or(meta.FirstGen, meta.SpawnGen)); isKnown {
		input.From = from
	} else {
		input.Unmeasured = append(input.Unmeasured, "start: the task's record names no spawn generation, so all it ever spent is measured")
	}
	// Only the conversation the task holds now is read, so a relaunch in the
	// run leaves out what a conversation before it spent unless this one
	// continues it.
	if started, isKnown := generationStart(meta.SpawnGen); isKnown && started.After(input.From) {
		input.Unmeasured = append(input.Unmeasured, "lead and workers before "+started.Format(time.RFC3339)+": the task was relaunched then, and only the conversation it holds now is read")
	}
	if err := readSessionSpend(&input, h, meta); err != nil {
		input.Unmeasured = append(input.Unmeasured, "lead and workers: "+err.Error())
	}
	if err := readGateSpend(ctx, &input, meta); err != nil {
		input.Unmeasured = append(input.Unmeasured, "gate: "+err.Error())
	}
	lines, err := fsx.ReadLines(state.StatusPath(h.State, meta.ID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		input.Unmeasured = append(input.Unmeasured, "escalations: "+err.Error())
	}
	for _, line := range lines {
		if at, event := state.SplitStatus(line); at.After(input.From) && !at.After(now) && strings.HasPrefix(event, "role-escalated: ") {
			input.Escalations = append(input.Escalations, strings.TrimPrefix(event, "role-escalated: "))
		}
	}
	record := measure.Build(input)
	return record, measure.Append(h.State, record)
}

// readSessionSpend reads into input what the goblin's own conversation and
// its workers spent: the conversation the board recorded for the task, else
// the one its running harness is in.
func readSessionSpend(input *measure.Input, h home.Home, meta state.TaskMeta) error {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	session, err := taskSession(h.State, meta)
	if err != nil {
		return err
	}
	path := fleettree.HeldTranscript(userHome, input.Harness, meta.Worktree, session)
	if path == "" {
		return errors.New("no conversation of the task's goblin was found")
	}
	switch input.Harness {
	case "claude":
		if input.Lead, err = measure.ClaudeSpend(path, input.From, input.To); err != nil {
			return err
		}
		input.Workers, err = measure.ClaudeWorkers(path, input.From, input.To)
	case "codex":
		if input.Lead, err = measure.CodexSpend(path, input.From, input.To); err != nil {
			return err
		}
		input.Workers, err = measure.CodexWorkers(userHome, path, input.From, input.To)
	case "pi":
		input.Lead, err = measure.PiSpend(path, input.From, input.To)
	default:
		err = fmt.Errorf("no reader for harness %q", input.Harness)
	}
	return err
}

// readGateSpend reads into input what the gate's agents spent in the runs of
// the branch checked out in the task's worktree.
func readGateSpend(ctx context.Context, input *measure.Input, meta state.TaskMeta) error {
	commands := gatedb.Runner{Next: execx.OSRunner{}}
	_, branch, err := taskWorktree(ctx, commands, meta, "")
	if err != nil {
		return err
	}
	root, err := gatehome.Root()
	if err != nil {
		return err
	}
	input.Gate, input.Tests, err = pipeline.Reader{Root: root, Commands: commands}.GateSpend(ctx, meta.Project, branch, input.From, input.To)
	return err
}
