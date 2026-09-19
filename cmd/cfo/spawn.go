package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

// quotaTimeout bounds the quota-axi call a spawn makes before picking a
// lane: a slow or hung quota-axi is no evidence, not a stalled dispatch.
const quotaTimeout = 20 * time.Second

func runSpawn(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo spawn: task ID is required")
		return 2
	}
	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "cfo spawn: unknown flag %q\n", args[0])
		return 2
	}

	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project checkout")
	brief := fs.String("brief", "", "absolute brief file")
	harnessName := fs.String("harness", "", "claude, codex, pi, or kimi; omitted, the lane table in data/routing.json picks it")
	mode := fs.String("mode", "no-mistakes", "no-mistakes, direct-PR, or local-only")
	model := fs.String("model", "", "harness model")
	effort := fs.String("effort", "", "harness effort")
	class := fs.String("class", "ordinary", "ordinary, high-risk, or mechanical pipeline policy")
	yolo := fs.Bool("yolo", false, "allow the selected delivery posture")
	auto := fs.Bool("auto", false, "route from the lane table; the default without --harness, kept as an alias")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cfo spawn: unexpected arguments")
		return 2
	}
	if *project == "" || *brief == "" {
		fmt.Fprintln(stderr, "cfo spawn: --project and --brief are required")
		return 2
	}
	if *harnessName != "" && !validSpawnHarness(*harnessName) {
		fmt.Fprintln(stderr, "cfo spawn: --harness must be claude, codex, pi, or kimi")
		return 2
	}
	if !validSpawnMode(*mode) {
		fmt.Fprintln(stderr, "cfo spawn: --mode must be no-mistakes, direct-PR, or local-only")
		return 2
	}
	if !pipeline.ValidClass(*class) {
		fmt.Fprintln(stderr, "cfo spawn: --class must be ordinary, high-risk, or mechanical")
		return 2
	}
	if runtime.resolveHome == nil || runtime.spawn == nil {
		fmt.Fprintln(stderr, "cfo spawn: command runtime is incomplete")
		return 1
	}
	checkout, err := runtime.resolveProject(*project)
	if err != nil {
		fmt.Fprintf(stderr, "cfo spawn: %v\n", err)
		return 1
	}
	*project = checkout
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	briefText, err := os.ReadFile(*brief)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := routing.Classify(string(briefText))
	routed := *harnessName == ""
	var inputs routeInputs
	if routed || *auto {
		if inputs, err = loadRouteInputs(h.Data, *project); err != nil {
			fmt.Fprintf(stderr, "cfo spawn: %v\n", err)
			return 1
		}
	}
	report, skipped := readQuota(runtime)

	taskClass, routeLane, repairRounds := *class, "explicit", 0
	var route string
	if routed {
		if len(inputs.table.Lanes) == 0 {
			fmt.Fprintf(stderr, "cfo spawn: no execution lanes: define lanes in %s (cfo doctor prints the table) or pass --harness\n", filepath.Join(h.Data, routing.FileName))
			return 1
		}
		var overrides []string
		if *model != "" {
			overrides = append(overrides, "--model "+*model)
		}
		if *effort != "" {
			overrides = append(overrides, "--effort "+*effort)
		}
		for name, lane := range inputs.table.Lanes {
			if *model != "" {
				lane.Model = *model
			}
			if *effort != "" {
				lane.Effort = *effort
			}
			inputs.table.Lanes[name] = lane
		}
		choice, err := routing.Choose(assessment, inputs.table, usableLane(report, skipped))
		if err != nil {
			fmt.Fprintf(stderr, "cfo spawn: %v\n", err)
			return 1
		}
		if !validSpawnHarness(choice.Harness) {
			fmt.Fprintf(stderr, "cfo spawn: lane %q names harness %q, which is not claude, codex, pi, or kimi\n", choice.Name, choice.Harness)
			return 1
		}
		*harnessName, *model, *effort = choice.Harness, choice.Model, choice.Effort
		routeLane = choice.Name
		route = routeLine(choice, assessment, inputs.table.Source, skipped)
		if len(overrides) > 0 {
			route += " overrides=" + strings.Join(overrides, " ")
		}
	} else {
		route = fmt.Sprintf("routed lane=explicit class=%s risk=%s source=--harness flag", assessment.Class, assessment.Risk)
		if skipped == "" {
			if headroom := report.Headroom(*harnessName, *model); headroom.Exhausted {
				route += " quota=" + headroom.String() + "; explicit --harness wins"
			}
		}
	}
	if routed || *auto {
		taskClass = string(assessment.Class)
		if b, ok := inputs.manifest.Budgets[taskClass]; ok {
			repairRounds = b.MaxRepairRounds
		} else if b, ok := inputs.manifest.Budgets["builder"]; ok {
			repairRounds = b.MaxRepairRounds
		}
	}
	var writeCapsule func(string) (string, error)
	if (routed || *auto) && inputs.manifestPath != "" {
		m := inputs.manifest
		writeCapsule = func(taskTmp string) (string, error) {
			if err := os.MkdirAll(taskTmp, 0o700); err != nil {
				return "", err
			}
			runtimePath := filepath.Join(taskTmp, "runtime-capsule.md")
			if err := os.WriteFile(runtimePath, []byte(m.Capsule()), 0o600); err != nil {
				return "", err
			}
			capsule := map[string]any{
				"objective_file":    *brief,
				"project_manifest":  inputs.manifestPath,
				"runtime_capsule":   runtimePath,
				"task_class":        taskClass,
				"route_lane":        routeLane,
				"budget":            map[string]any{"max_repair_rounds": repairRounds},
				"hygiene":           map[string]any{"superseded_work_must_be_deleted": m.Hygiene.SupersedeClean},
				"expected_evidence": filepath.Join(taskTmp, "evidence.json"),
			}
			b, _ := json.MarshalIndent(capsule, "", "  ")
			taskCapsule := filepath.Join(taskTmp, "task-capsule.json")
			if err := os.WriteFile(taskCapsule, append(b, '\n'), 0o600); err != nil {
				return "", err
			}
			augmented := filepath.Join(taskTmp, "brief.md")
			extra := fmt.Sprintf("\n\n## CFO durable task capsule\nRead %s and %s before work. Write machine-readable production evidence to %s. Rejected unshipped implementation work is disposable: delete superseded files, tests, routes and flags unless explicitly required.\n", taskCapsule, runtimePath, filepath.Join(taskTmp, "evidence.json"))
			if err := os.WriteFile(augmented, append(briefText, []byte(extra)...), 0o600); err != nil {
				return "", err
			}
			return augmented, nil
		}
	}
	result, err := runtime.spawn(context.Background(), h, spawn.Request{
		ID:        args[0],
		Project:   *project,
		BriefPath: *brief,
		Kind:      "ship",
		Mode:      *mode,
		Yolo:      *yolo,
		Harness:   harness.Kind(*harnessName),
		Model:     *model,
		Effort:    *effort,
		Session:   herdrSession(),
		Class:     *class,
		Capsule:   writeCapsule,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, result.Output)
	fmt.Fprintln(stdout, route)
	if runtime.speedHint != nil {
		if hint := runtime.speedHint(context.Background(), *harnessName); hint != "" {
			fmt.Fprintln(stdout, hint)
		}
	}
	return 0
}

// routeInputs is what a spawn routes with: the lane table and, when the
// project has a manifest, that manifest for its budgets and capsule.
type routeInputs struct {
	table        routing.Table
	manifest     projectcfg.Manifest
	manifestPath string // "" when the project has no manifest
}

// loadRouteInputs reads the fleet lane table from data/routing.json and the
// project's manifest when it has one. A manifest that defines lanes
// overrides the fleet table for that project. A project without a manifest
// routes from the fleet table alone; a manifest that exists but does not
// load is an error, never silently ignored.
func loadRouteInputs(dataDir, project string) (routeInputs, error) {
	policy, err := routing.Load(dataDir)
	if err != nil {
		return routeInputs{}, err
	}
	table, err := policy.LaneTable()
	if err != nil {
		return routeInputs{}, err
	}
	in := routeInputs{table: table}
	if project == "" {
		return in, nil
	}
	m, path, err := projectcfg.LoadForCheckout(dataDir, project)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return in, nil
	case err != nil:
		return routeInputs{}, err
	}
	in.manifest, in.manifestPath = m, path
	if len(m.Routing.Lanes) > 0 {
		in.table = routing.Table{Lanes: map[string]routing.ExecutionLane{}, DefaultLane: m.Routing.DefaultLane, EscalateTo: m.Routing.EscalateTo, Source: "project override " + path}
		for name, l := range m.Routing.Lanes {
			in.table.Lanes[name] = routing.ExecutionLane{Name: name, Harness: l.Harness, Model: l.Model, Effort: l.Effort}
		}
	}
	return in, nil
}

// readQuota runs the quota check under a bound. A runtime without a reader
// (a test's) and every failure read as a skipped check, never a blocked
// spawn.
func readQuota(runtime commandRuntime) (quota.Report, string) {
	if runtime.quota == nil {
		return quota.Report{}, "no quota reader in this runtime"
	}
	ctx, cancel := context.WithTimeout(context.Background(), quotaTimeout)
	defer cancel()
	return runtime.quota(ctx)
}

// usableLane is the quota check a routed spawn walks its lanes with: a lane
// whose provider or model scope is exhausted now is unusable; no evidence is
// usable. A skipped check means no check at all.
func usableLane(report quota.Report, skipped string) routing.Usable {
	if skipped != "" {
		return nil
	}
	return func(lane routing.ExecutionLane) (bool, string) {
		headroom := report.Headroom(lane.Harness, lane.Model)
		return !headroom.Exhausted, headroom.String()
	}
}

// routeLine is the line after "spawned" that names the lane that ran, the
// lane that was wanted when quota moved it, the class and risk the brief was
// classified as, where the table came from, and what the quota check found
// for each lane it tried or why it was skipped.
func routeLine(c routing.Choice, a routing.Assessment, source, skipped string) string {
	line := "routed lane=" + c.Name
	if c.Wanted != c.Name {
		line += " wanted=" + c.Wanted
	}
	line += fmt.Sprintf(" class=%s risk=%s source=%s", a.Class, a.Risk, source)
	switch {
	case skipped != "":
		line += " quota=check skipped (" + skipped + ")"
	case len(c.Notes) > 0:
		line += " quota=" + strings.Join(c.Notes, "; ")
	}
	return line
}

func herdrSession() string {
	if session := os.Getenv("HERDR_SESSION"); session != "" {
		return session
	}
	return "default"
}

func validSpawnHarness(name string) bool {
	switch harness.Kind(name) {
	case harness.Claude, harness.Codex, harness.Pi, harness.Kimi:
		return true
	default:
		return false
	}
}

func validSpawnMode(mode string) bool {
	switch mode {
	case "no-mistakes", "direct-PR", "local-only":
		return true
	default:
		return false
	}
}
