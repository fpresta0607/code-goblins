package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/evidence"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/hygiene"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supersede"
	verifyrun "github.com/fpresta0607/code-goblins/internal/verify"
	"io"
	"os"
	"path/filepath"
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
	name := args[1]
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
	case "check":
		_, e := projectcfg.Load(p)
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 1
		}
		fmt.Fprintf(stdout, "project %s: valid\n", name)
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
	lanes := map[string]routing.ExecutionLane{}
	def, rescue := "", ""
	if *project != "" {
		name := filepath.Base(filepath.Clean(*project))
		m, e := projectcfg.Load(projectcfg.Path(h.Data, name))
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 1
		}
		def, rescue = m.Routing.DefaultLane, m.Routing.EscalateTo
		for n, l := range m.Routing.Lanes {
			lanes[n] = routing.ExecutionLane{Name: n, Harness: l.Harness, Model: l.Model, Effort: l.Effort}
		}
	}
	x := routing.ChooseExecution(a, lanes, def, rescue)
	b, _ := json.MarshalIndent(map[string]any{"task_class": a.Class, "risk": a.Risk, "lane": x}, "", "  ")
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
	r := verifyrun.Runner{Commands: execx.OSRunner{}}
	res, e := r.Run(context.Background(), cmds, *tier, meta.ID, "", meta.Worktree, time.Duration(seconds)*time.Second)
	p := filepath.Join(meta.TaskTmp, "verification-"+*tier+".json")
	_ = verifyrun.Save(p, res)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	fmt.Fprintln(stdout, p)
	return 0
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
	r := verifyrun.Runner{Commands: execx.OSRunner{}}
	res, e := r.Run(context.Background(), cmds, "security", meta.ID, "", meta.Worktree, 10*time.Minute)
	p := filepath.Join(meta.TaskTmp, "security.json")
	_ = verifyrun.Save(p, res)
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
	b, e := os.ReadFile(p)
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
		if e := runtime.sendText(context.Background(), h, m.ID, instruction); e != nil {
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
