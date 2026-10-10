package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/projectcheck"
)

// runProjectCheck assesses what the home knows about one project and prints
// a line for everything it could prove, each with its evidence, then one
// verdict line. It reads the checkout and the project's files in the home
// and changes neither. It exits 0 when no line of the areas asked for is
// worse than low, and 1 otherwise.
func runProjectCheck(h home.Home, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := flag.NewFlagSet("project check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the report as JSON")
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
		fmt.Fprintf(stderr, "cfo project check: %v\n", err)
		return 1
	}
	report, err := projectcheck.Check(context.Background(), projectcheck.Options{
		DataDir:  h.Data,
		Checkout: checkout,
		Runner:   execx.OSRunner{},
		LookPath: exec.LookPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "cfo project check: %v\n", err)
		return 1
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
	}
	for _, area := range areas {
		if !report.Passed(area) {
			return 1
		}
	}
	return 0
}
