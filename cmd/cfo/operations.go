package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/evidence"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/hygiene"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supersede"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	verifyrun "github.com/fpresta0607/code-goblins/internal/verify"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func runProject(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "cfo project: show, check, or init plus <project> required")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if args[0] == "check" {
		return runProjectCheck(h, args[1:], stdout, stderr, runtime)
	}
	// A project is a name or a path here as everywhere, and its record is
	// filed under its checkout's folder name.
	name, err := credentialScope(runtime, args[1])
	if err != nil {
		fmt.Fprintf(stderr, "cfo project: %v\n", err)
		return 1
	}
	p := projectcfg.Path(h.Data, name)
	switch args[0] {
	case "show":
		m, e := projectcfg.Load(p)
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 1
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	case "init":
		if _, e := os.Stat(p); e == nil {
			fmt.Fprintln(stderr, "project manifest already exists")
			return 1
		}
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			fmt.Fprintln(stderr, e)
			return 1
		}
		m := projectcfg.Manifest{Project: name}
		b, _ := json.MarshalIndent(m, "", "  ")
		if e := os.WriteFile(p, append(b, '\n'), 0644); e != nil {
			fmt.Fprintln(stderr, e)
			return 1
		}
		fmt.Fprintln(stdout, p)
		return 0
	default:
		fmt.Fprintln(stderr, "cfo project: unknown subcommand")
		return 2
	}
}

// runRoute is the dry run of a spawn's routing: it classifies the brief text
// given on the command line, loads the same lane table a spawn would, applies
// the same quota check, and prints what would run without spawning anything.
func runRoute(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	fs := flag.NewFlagSet("route", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project name or checkout")
	attempts := fs.Int("attempts", 0, "prior failures")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	brief := strings.Join(fs.Args(), " ")
	if brief == "" {
		fmt.Fprintln(stderr, "cfo route: brief is required")
		return 2
	}
	h, e := runtime.resolveHome()
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	a := routing.Classify(brief)
	a.Attempts = *attempts
	// Resolved the way a spawn resolves it, or the dry run would read a
	// different manifest than the dispatch it previews.
	checkout, e := runtime.resolveProject(*project)
	if e != nil {
		fmt.Fprintf(stderr, "cfo route: %v\n", e)
		return 1
	}
	inputs, e := supervisor.LoadRouteInputs(h.Data, checkout)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	report, skipped := readQuota(runtime)
	settings, e := fleetconfig.Read(h.Root)
	if e != nil {
		fmt.Fprintf(stderr, "cfo route: %v\n", e)
		return 1
	}
	choice, e := routing.Choose(a, inputs.Table, usableLane(report, skipped, settings, quotaClock(runtime)))
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	out := map[string]any{"task_class": a.Class, "risk": a.Risk, "lane": choice.ExecutionLane, "wanted": choice.Wanted, "source": inputs.Table.Source}
	if skipped != "" {
		out["quota"] = "check skipped (" + skipped + ")"
	} else {
		out["quota"] = choice.Notes
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(stdout, string(b))
	return 0
}

func taskManifest(runtime commandRuntime, id string) (state.TaskMeta, projectcfg.Manifest, error) {
	h, e := runtime.resolveHome()
	if e != nil {
		return state.TaskMeta{}, projectcfg.Manifest{}, e
	}
	m, e := state.ReadTaskMeta(h.State, id)
	if e != nil {
		return m, projectcfg.Manifest{}, e
	}
	pm, e := projectcfg.Load(projectcfg.Path(h.Data, filepath.Base(filepath.Clean(m.Project))))
	return m, pm, e
}

func runVerify(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 1 {
		return 2
	}
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tier := fs.String("tier", "fast", "fast, full, or deep")
	if e := fs.Parse(args[1:]); e != nil {
		return 2
	}
	meta, m, e := taskManifest(runtime, args[0])
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	var cmds []projectcfg.Command
	seconds := m.Verification.MaxFastSeconds
	switch *tier {
	case "fast":
		cmds = m.Verification.Fast
	case "full":
		cmds = m.Verification.Full
		seconds = m.Verification.MaxFullSeconds
	case "deep":
		cmds = m.Verification.Deep
		seconds = m.Verification.MaxFullSeconds
	default:
		return 2
	}
	if seconds <= 0 {
		seconds = 90
	}
	p := filepath.Join(meta.TaskTmp, "verification-"+*tier+".json")
	// A tier whose record declares what its checks read besides git's files
	// takes a pass it already has on the same source and inputs again,
	// instead of running.
	var key verifyrun.ReuseKey
	isKeyed := false
	if reuse := m.Verification.Reuse; reuse != nil && slices.Contains(reuse.Tiers, *tier) {
		// A tier ended while its key is read leaves no earlier pass standing.
		running := verifyrun.Result{Scope: *tier, Task: meta.ID, Start: time.Now(), ExitCode: -1, Status: "running"}
		if err := verifyrun.Save(p, []verifyrun.Result{running}); err != nil {
			fmt.Fprintf(stderr, "cfo verify: what the tier does could not be saved to %s: %v\n", p, err)
			return 1
		}
		named := verifyrun.ReuseKey{Project: m.Project, Scope: *tier, LimitSeconds: seconds, Executor: version}
		for _, command := range cmds {
			named.Commands = append(named.Commands, []string(command))
		}
		read, err := verifyrun.ReadReuseKey(context.Background(), execx.OSRunner{}, meta.Worktree, named, *reuse)
		if err != nil {
			fmt.Fprintf(stderr, "cfo verify: the %s tier runs, as no pass of it can be taken again: %v\n", *tier, err)
		} else if pass, err := verifyrun.FindPass(read, time.Now(), reuse.MaxAge(), meta.ID); err == nil {
			if err := verifyrun.Save(p, pass.Results); err != nil {
				fmt.Fprintf(stderr, "cfo verify: what the tier took could not be saved to %s: %v\n", p, err)
				return 1
			}
			fmt.Fprintf(stderr, "cfo verify: the %s tier passed on this source at %s, so it takes that pass again: %s\n", *tier, pass.Kept.Format(time.RFC3339), pass.Results[0].Reused)
			fmt.Fprintln(stdout, p)
			return 0
		} else if !errors.Is(err, verifyrun.ErrNoPass) {
			fmt.Fprintf(stderr, "cfo verify: the %s tier runs, as its pass could not be read: %v\n", *tier, err)
		} else {
			key, isKeyed = read, true
		}
	}
	// A keyed tier reads its key again once its first command holds its turn,
	// since the worktree can change while it waits, and takes a pass kept for
	// that key by then, as one a run ahead of it in the line kept, instead of
	// running. Only the first command asks: a later command's read would key
	// the pass by what the commands before it may have changed.
	var ready func(context.Context) error
	var taken verifyrun.Pass
	if isKeyed {
		isAsked := false
		ready = func(ctx context.Context) error {
			if isAsked {
				return nil
			}
			isAsked = true
			atTurn, err := verifyrun.ReadReuseKey(ctx, execx.OSRunner{}, meta.Worktree, key, *m.Verification.Reuse)
			if err != nil {
				isKeyed = false
				fmt.Fprintf(stderr, "cfo verify: the %s tier's pass is not kept to be taken again, as its key could not be read at its turn: %v\n", *tier, err)
				return nil
			}
			key = atTurn
			pass, err := verifyrun.FindPass(atTurn, time.Now(), m.Verification.Reuse.MaxAge(), meta.ID)
			if errors.Is(err, verifyrun.ErrNoPass) {
				return nil
			}
			if err != nil {
				isKeyed = false
				fmt.Fprintf(stderr, "cfo verify: the %s tier runs and keeps no pass, as its pass could not be read at its turn: %v\n", *tier, err)
				return nil
			}
			taken = pass
			return errPassTaken
		}
	}
	res, e := runMeasuredTier("cfo verify", cmds, *tier, meta, time.Duration(seconds)*time.Second, p, stderr, runtime, ready)
	if len(taken.Results) > 0 && e == nil {
		if err := verifyrun.Save(p, taken.Results); err != nil {
			fmt.Fprintf(stderr, "cfo verify: what the tier took could not be saved to %s: %v\n", p, err)
			return 1
		}
		fmt.Fprintf(stderr, "cfo verify: the %s tier found at its turn a pass kept at %s for what it holds then, so it takes that pass instead of running: %s\n", *tier, taken.Kept.Format(time.RFC3339), taken.Results[0].Reused)
		fmt.Fprintln(stdout, p)
		return 0
	}
	// A pass is kept for its key only if its key read the same once the tier
	// had run.
	if e == nil && isKeyed {
		after, err := verifyrun.ReadReuseKey(context.Background(), execx.OSRunner{}, meta.Worktree, key, *m.Verification.Reuse)
		if err == nil && after.ID() != key.ID() {
			err = errors.New("what it depends on read otherwise once it had run")
		}
		if err == nil {
			err = verifyrun.KeepPass(key, res, time.Now())
		}
		if err != nil {
			fmt.Fprintf(stderr, "cfo verify: the %s tier's pass is not kept to be taken again: %v\n", *tier, err)
		}
		if err := verifyrun.PrunePasses(time.Now()); err != nil {
			fmt.Fprintf(stderr, "cfo verify: old passes could not all be removed: %v\n", err)
		}
	}
	if e != nil {
		// A check ends with the line that names what failed, so the tier
		// says that line, and the file that holds everything the check said.
		if len(res) > 0 {
			if said := strings.TrimSpace(res[len(res)-1].Output); said != "" {
				lines := strings.Split(said, "\n")
				fmt.Fprintln(stderr, strings.TrimSpace(lines[len(lines)-1]))
			}
		}
		fmt.Fprintln(stderr, e)
		fmt.Fprintln(stderr, p)
		return 1
	}
	fmt.Fprintln(stdout, p)
	return 0
}

// errPassTaken is why a keyed tier's first command never started: the tier
// found a pass kept for the key it read at its turn.
var errPassTaken = errors.New("cfo verify: the tier found a pass kept for the key it read at its turn, so it takes that pass instead")

// runMeasuredTier runs a tier's commands as measured executions in the
// machine's one line for heavy runs, each limited from when it starts, and
// saves what became of each to path. Before it waits it saves that the tier
// is running, so a tier ended in its wait never leaves an earlier pass
// standing, and a tier that ran nothing saves why with status not_run. A
// result that cannot be saved fails the tier, since nothing else would show
// what it did. ready, when set, is asked before each command starts whether
// the tier still needs it (verify.Executor.Ready). A tier it ends with
// errPassTaken, whose first command's record says it never started and
// whose cleanup is confirmed, saves nothing and returns no result and no
// error, so that its caller saves the pass it took. Anything else that went
// wrong on the way fails the tier as it would otherwise.
func runMeasuredTier(name string, cmds []projectcfg.Command, scope string, meta state.TaskMeta, limit time.Duration, path string, stderr io.Writer, runtime commandRuntime, ready func(context.Context) error) ([]verifyrun.Result, error) {
	running := verifyrun.Result{Scope: scope, Task: meta.ID, Start: time.Now(), ExitCode: -1, Status: "running"}
	if err := verifyrun.Save(path, []verifyrun.Result{running}); err != nil {
		return nil, fmt.Errorf("%s: what the tier does could not be saved to %s: %w", name, path, err)
	}
	notRun := func(failure error) []verifyrun.Result {
		return []verifyrun.Result{{Scope: scope, Task: meta.ID, Start: running.Start, ExitCode: -1, Status: "not_run", Error: failure.Error()}}
	}
	executor, err := measuredExecutor(name, fmt.Sprintf("%s %s of task %s", name, scope, taskLabel(meta.ID, runtime)), stderr, runtime.availableMemory)
	if err != nil {
		failure := fmt.Errorf("%s: this run takes no turn: %w", name, err)
		return nil, errors.Join(failure, verifyrun.Save(path, notRun(failure)))
	}
	executor.Source = func(ctx context.Context, dir string) (verifyrun.Source, error) {
		return verifyrun.ReadSource(ctx, execx.OSRunner{}, dir)
	}
	executor.Ready = ready
	res, runError := verifyrun.Runner{Commands: executor}.Run(context.Background(), cmds, scope, meta.ID, "", meta.Worktree, limit)
	if len(res) == 0 && runError != nil {
		res = notRun(runError)
	}
	// A command whose cleanup was not confirmed keeps the machine's line
	// until it is, and this process is its only owner.
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cleanupError := executor.RetryCleanup(cleanup)
	if errors.Is(runError, errPassTaken) && cleanupError == nil && len(res) == 1 {
		if record, err := verifyrun.LoadExecution(res[0].Execution); err == nil && record.Status == "not_started" {
			return nil, nil
		}
	}
	failure := errors.Join(runError, cleanupError)
	for index, result := range res {
		if result.Execution == "" {
			continue
		}
		// A record that cannot be read here still holds its leftovers.
		record, err := verifyrun.LoadExecution(result.Execution)
		if err != nil {
			continue
		}
		res[index].Leftovers, res[index].Spared, res[index].Source = record.Measurement.Leftovers, record.Measurement.Spared, record.Source
		if _, err := record.Source.Tested(); err != nil && record.Status != "not_started" {
			fmt.Fprintf(stderr, "%s: %s tested no one source: %v\n", name, strings.Join(result.Command, " "), err)
		}
		if len(record.Measurement.Leftovers) > 0 {
			fmt.Fprintf(stderr, "%s: %s left %s running after it exited, which the run ended\n", name, strings.Join(result.Command, " "), processNames(record.Measurement.Leftovers))
		}
		if len(record.Measurement.Spared) > 0 {
			fmt.Fprintf(stderr, "%s: %s started %s, which serves the whole machine, so the run left it running\n", name, strings.Join(result.Command, " "), processNames(record.Measurement.Spared))
		}
	}
	if err := verifyrun.Save(path, res); err != nil {
		failure = errors.Join(failure, fmt.Errorf("%s: what the tier did could not be saved to %s: %w", name, path, err))
	}
	// The store keeps the newest records within its budget. One it cannot
	// remove now is tried again after the next tier, so the tier keeps its
	// own result.
	if err := verifyrun.PruneExecutions(time.Now()); err != nil {
		fmt.Fprintf(stderr, "%s: old execution records could not all be removed: %v\n", name, err)
	}
	return res, failure
}

// processNames names processes by image name and pid.
func processNames(processes []verifyrun.Leftover) string {
	named := make([]string, len(processes))
	for index, process := range processes {
		named[index] = fmt.Sprintf("%s pid %d", process.Name, process.PID)
	}
	return strings.Join(named, ", ")
}

func runSecurity(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 1 {
		return 2
	}
	fs := flag.NewFlagSet("security", flag.ContinueOnError)
	fs.SetOutput(stderr)
	deep := fs.Bool("deep", false, "run deep security commands")
	if e := fs.Parse(args[1:]); e != nil {
		return 2
	}
	meta, m, e := taskManifest(runtime, args[0])
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	if m.Security.Mode == "" || m.Security.Mode == "off" {
		fmt.Fprintln(stdout, "security off")
		return 0
	}
	cmds := append([]projectcfg.Command(nil), m.Security.Fast...)
	needsDeep := *deep || m.Security.Mode == "always" || m.Security.Mode == "scheduled"
	if m.Security.Mode == "risk" && !needsDeep {
		diff, diffErr := execx.OSRunner{}.Run(context.Background(), execx.Request{
			Name: "git", Args: []string{"diff", "--name-only", "HEAD~1", "HEAD"}, Dir: meta.Worktree,
		})
		if diffErr != nil || diff.ExitCode != 0 {
			// Fail closed for risk classification: if the changed scope cannot be
			// established cheaply, run the stronger configured scan.
			needsDeep = true
		} else {
			needsDeep = m.Security.NeedsDeep(strings.Fields(string(diff.Stdout)))
		}
	}
	if needsDeep {
		cmds = append(cmds, m.Security.Deep...)
	}
	p := filepath.Join(meta.TaskTmp, "security.json")
	if len(cmds) == 0 && !needsDeep {
		// The mode asked for no scan of this change.
		if err := verifyrun.Save(p, []verifyrun.Result{}); err != nil {
			fmt.Fprintf(stderr, "cfo security: what the tier did could not be saved to %s: %v\n", p, err)
			return 1
		}
		fmt.Fprintln(stdout, p)
		return 0
	}
	_, e = runMeasuredTier("cfo security", cmds, "security", meta, 10*time.Minute, p, stderr, runtime, nil)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	fmt.Fprintln(stdout, p)
	return 0
}

func runDeploy(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 1 {
		return 2
	}
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	target := fs.String("target", "", "target name")
	if e := fs.Parse(args[1:]); e != nil {
		return 2
	}
	meta, m, e := taskManifest(runtime, args[0])
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	res, e := projectcfg.RunDeployment(context.Background(), execx.OSRunner{}, m.Deployment, meta.Worktree, *target)
	p := filepath.Join(meta.TaskTmp, "deployment.json")
	_ = verifyrun.Save(p, res)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	fmt.Fprintln(stdout, p)
	return 0
}

func runEvidence(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 1 {
		return 2
	}
	h, e := runtime.resolveHome()
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	m, e := state.ReadTaskMeta(h.State, args[0])
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	p := filepath.Join(m.TaskTmp, "evidence.json")
	b, e := fsx.ReadFile(p)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	fmt.Fprint(stdout, string(b))
	return 0
}

func runSupersede(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 1 {
		return 2
	}
	fs := flag.NewFlagSet("supersede", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reason := fs.String("reason", "", "reason")
	if e := fs.Parse(args[1:]); e != nil {
		return 2
	}
	h, e := runtime.resolveHome()
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	m, e := state.ReadTaskMeta(h.State, args[0])
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	p, instruction, e := supersede.Record(m.TaskTmp, m.ID, *reason)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	_ = state.AppendStatus(h.State, m.ID, "superseded: "+*reason)
	if runtime.sendText != nil {
		e := runtime.sendText(context.Background(), h, m.ID, instruction)
		if errors.Is(e, fleet.ErrQueuedForToolCall) {
			fmt.Fprintf(stdout, "steer queued for %s: it is in a turn and takes the steer at its next tool call; do not send it again\n", m.ID)
		} else if e != nil {
			fmt.Fprintf(stderr, "recorded supersede but steer failed: %v\n", e)
			return 1
		}
	}
	fmt.Fprintln(stdout, p)
	return 0
}

func runHygiene(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 1 {
		return 2
	}
	meta, _, e := taskManifest(runtime, args[0])
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	x, e := hygiene.Tests(meta.Worktree)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	b, _ := json.MarshalIndent(x, "", "  ")
	fmt.Fprintln(stdout, string(b))
	return 0
}

var _ = evidence.Artifact{}
