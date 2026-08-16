package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/harness"
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
	yolo := fs.Bool("yolo", false, "allow the selected delivery posture")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cfo spawn: unexpected arguments")
		return 2
	}
	if *project == "" || *brief == "" || *harnessName == "" {
		fmt.Fprintln(stderr, "cfo spawn: --project, --brief, and --harness are required")
		return 2
	}
	if !validSpawnHarness(*harnessName) {
		fmt.Fprintln(stderr, "cfo spawn: --harness must be claude, codex, pi, or kimi")
		return 2
	}
	if !validSpawnMode(*mode) {
		fmt.Fprintln(stderr, "cfo spawn: --mode must be no-mistakes, direct-PR, or local-only")
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
