package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

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
	harnessName := fs.String("harness", "", "claude, codex, pi, or kimi")
	mode := fs.String("mode", "no-mistakes", "no-mistakes, direct-PR, or local-only")
	model := fs.String("model", "", "harness model")
	effort := fs.String("effort", "", "harness effort")
	class := fs.String("class", "ordinary", "ordinary, high-risk, or mechanical pipeline policy")
	yolo := fs.Bool("yolo", false, "allow the selected delivery posture")
	auto := fs.Bool("auto", false, "route harness/model/effort from project policy")
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
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	taskClass, routeLane, repairRounds := *class, "explicit", 0
	if *auto || *harnessName == "" {
		m, _, loadErr := projectcfg.LoadForCheckout(h.Data, *project)
		if loadErr != nil {
			fmt.Fprintf(stderr, "cfo spawn: auto routing needs a valid project manifest: %v\n", loadErr)
			return 1
		}
		briefBytes, readErr := os.ReadFile(*brief)
		if readErr != nil {
			fmt.Fprintln(stderr, readErr)
			return 1
		}
		a := routing.Classify(string(briefBytes))
		lanes := map[string]routing.ExecutionLane{}
		for name, l := range m.Routing.Lanes {
			lanes[name] = routing.ExecutionLane{Name: name, Harness: l.Harness, Model: l.Model, Effort: l.Effort}
		}
		if *harnessName != "" {
			a.ExplicitHarness = *harnessName
			a.ExplicitModel = *model
			a.ExplicitEffort = *effort
		}
		chosen := routing.ChooseExecution(a, lanes, m.Routing.DefaultLane, m.Routing.EscalateTo)
		if chosen.Harness == "" {
			fmt.Fprintln(stderr, "cfo spawn: routing policy selected an empty harness")
			return 1
		}
		*harnessName, *model, *effort = chosen.Harness, chosen.Model, chosen.Effort
		taskClass, routeLane = string(a.Class), chosen.Name
		if b, ok := m.Budgets[taskClass]; ok {
			repairRounds = b.MaxRepairRounds
		} else if b, ok := m.Budgets["builder"]; ok {
			repairRounds = b.MaxRepairRounds
		}
	}
	briefPath := *brief
	if *auto || routeLane != "explicit" {
		m, manifestPath, loadErr := projectcfg.LoadForCheckout(h.Data, *project)
		if loadErr != nil {
			fmt.Fprintln(stderr, loadErr)
			return 1
		}
		taskTmp := filepath.Join(h.State, "tasktmp", args[0])
		if err := os.MkdirAll(taskTmp, 0o700); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		runtimePath := filepath.Join(taskTmp, "runtime-capsule.md")
		if err := os.WriteFile(runtimePath, []byte(m.Capsule()), 0o600); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		capsule := map[string]any{
			"objective_file":    *brief,
			"project_manifest":  manifestPath,
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
			fmt.Fprintln(stderr, err)
			return 1
		}
		orig, err := os.ReadFile(*brief)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		augmented := filepath.Join(taskTmp, "brief.md")
		extra := fmt.Sprintf("\n\n## CFO durable task capsule\nRead %s and %s before work. Write machine-readable production evidence to %s. Rejected unshipped implementation work is disposable: delete superseded files, tests, routes and flags unless explicitly required.\n", taskCapsule, runtimePath, filepath.Join(taskTmp, "evidence.json"))
		if err := os.WriteFile(augmented, append(orig, []byte(extra)...), 0o600); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		briefPath = augmented
	}
	result, err := runtime.spawn(context.Background(), h, spawn.Request{
		ID:        args[0],
		Project:   *project,
		BriefPath: briefPath,
		Kind:      "ship",
		Mode:      *mode,
		Yolo:      *yolo,
		Harness:   harness.Kind(*harnessName),
		Model:     *model,
		Effort:    *effort,
		Session:   herdrSession(),
		Class:     *class,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, result.Output)
	if runtime.speedHint != nil {
		if hint := runtime.speedHint(context.Background(), *harnessName); hint != "" {
			fmt.Fprintln(stdout, hint)
		}
	}
	return 0
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
