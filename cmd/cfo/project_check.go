package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/projectcheck"
)

// runProjectCheck assesses what the home knows about one project and prints
// a line for everything it could prove, each with its evidence, then one
// verdict line. It reads the checkout and the project's files in the home
// and changes neither. With --draft it writes the record it can vouch for
// to a file outside the home's projects, for a person to place. It exits 0
// when no line of the areas asked for is worse than low, and 1 otherwise.
func runProjectCheck(h home.Home, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := flag.NewFlagSet("project check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the report as JSON")
	draft := flags.String("draft", "", "write the record this run can vouch for to this file, which must not exist and must not be under the home's projects")
	var areas []string
	flags.Func("area", "assess only this area ("+strings.Join(projectcheck.Areas, ", ")+"); repeat for more than one", func(value string) error {
		if !slices.Contains(projectcheck.Areas, value) {
			return fmt.Errorf("%q is no area; the areas are %s", value, strings.Join(projectcheck.Areas, ", "))
		}
		areas = append(areas, value)
		return nil
	})
	positional, err := parseAuthArgs(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "cfo project check: one project is required")
		return 2
	}
	checkout, err := runtime.resolveProject(positional[0])
	if err != nil {
		// A project the home holds files for and this machine has no checkout
		// of is assessed as far as those files go.
		missing, held := checkoutOfHomeProject(h, positional[0], runtime)
		if !held {
			fmt.Fprintf(stderr, "cfo project check: %v\n", err)
			return 1
		}
		checkout = missing
	}
	report, err := projectcheck.Check(context.Background(), projectcheck.Options{
		DataDir:    h.Data,
		Checkout:   checkout,
		PolicyFile: filepath.Join(h.Root, "config", "pipeline.json"),
		Runner:     execx.OSRunner{},
		LookPath:   exec.LookPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "cfo project check: %v\n", err)
		return 1
	}
	if *draft != "" {
		if err := writeProjectDraft(h, *draft, report); err != nil {
			fmt.Fprintf(stderr, "cfo project check: %v\n", err)
			return 1
		}
	}
	if len(areas) > 0 {
		report = report.Only(areas)
	} else {
		areas = projectcheck.Areas
	}
	if *asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprint(stdout, report.Text())
		fmt.Fprintln(stdout, report.Verdict(areas))
		if *draft != "" {
			fmt.Fprintln(stdout, "draft: "+*draft)
			fmt.Fprintln(stdout, "draft tier: "+report.DraftTier)
		}
	}
	for _, area := range areas {
		if !report.Passed(area) {
			return 1
		}
	}
	return 0
}

// checkoutOfHomeProject returns where the checkout of a project named by a
// bare name would be, under the projects root, when the home holds a folder
// for that project. It is the path the check then reports as missing.
func checkoutOfHomeProject(h home.Home, name string, runtime commandRuntime) (string, bool) {
	if strings.ContainsAny(name, `/\`) || filepath.VolumeName(name) != "" {
		return "", false
	}
	if info, err := os.Stat(filepath.Join(h.Data, "projects", name)); err != nil || !info.IsDir() {
		return "", false
	}
	root, err := runtime.projectsRoot()
	if err != nil || strings.TrimSpace(root) == "" {
		return "", false
	}
	return filepath.Join(root, name), true
}

// writeProjectDraft writes a report's drafted record to a new file. A record
// under the home's projects steers routing and verification for live spawns,
// so a draft never goes there: whoever reads it places it. A draft with no
// verification command is not written at all, since placed as it is it
// would fail the record area of the check that drafted it.
func writeProjectDraft(h home.Home, file string, report projectcheck.Report) error {
	target, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	projects := filepath.Join(h.Data, "projects")
	if inside, err := filepath.Rel(projects, target); err == nil && inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator)) && !filepath.IsAbs(inside) {
		return fmt.Errorf("--draft never writes under %s, where a record steers live spawns: write the draft elsewhere and place it yourself", projects)
	}
	if !report.DraftVerifies() {
		return fmt.Errorf("no draft is written, since %s", report.DraftTier)
	}
	data, err := json.MarshalIndent(report.Draft, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := fsx.CreateNew(target, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("--draft %s is already there, and a draft is never written over a file", target)
	}
	if err != nil {
		return err
	}
	if _, err := out.Write(append(data, '\n')); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
