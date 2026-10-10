package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/gatetest"
)

const table = `{"frontend":{"paths":["frontend/**"],"packages":["./internal/boardweb"]},"browser":{"paths":["frontend/**"]},"go":[{"shard":"conpty","packages":"./internal/conpty"},{"shard":"rest","except":"./internal/conpty"}]}`

// The workflow starts the go jobs unless the plan's go output is the empty
// list, written exactly [], and the frontend and browser jobs when theirs is
// exactly true: a plan that left the go jobs out and wrote null, or wrote
// nothing, would start them or fail the run.
func TestThePlanWritesTheOutputsTheWorkflowReads(t *testing.T) {
	jobTable, err := parseTable(table)
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		jobs gatetest.Jobs
		want string
	}{
		"every job":                  {gatetest.Jobs{Everything: true, Why: "a workflow changed", Frontend: true, Browser: true, Go: jobTable.Go}, `go=[{"shard":"conpty","packages":"./internal/conpty"},{"shard":"rest","except":"./internal/conpty"}]` + "\nfrontend=true\nbrowser=true\n"},
		"the board alone":            {gatetest.Jobs{Why: "the change reaches 0 of the module's packages", Frontend: true, Browser: true}, "go=[]\nfrontend=true\nbrowser=true\n"},
		"one go job":                 {gatetest.Jobs{Why: "the change reaches 1 of the module's packages", Go: jobTable.Go[1:]}, `go=[{"shard":"rest","except":"./internal/conpty"}]` + "\nfrontend=false\nbrowser=false\n"},
		"nothing, as for a document": {gatetest.Jobs{Why: "the change reaches 0 of the module's packages"}, "go=[]\nfrontend=false\nbrowser=false\n"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var outputs, reasons bytes.Buffer

			// Act
			err := write(test.jobs, gatetest.Plan{Base: "1111111111111111"}, jobTable, &outputs, &reasons)

			// Assert
			if err != nil || outputs.String() != test.want {
				t.Errorf("the plan wrote %q (%v), want %q", outputs.String(), err, test.want)
			}
			for _, job := range []string{"go (conpty)", "go (rest)", "frontend", "browser"} {
				if !strings.Contains(reasons.String(), "ci plan: "+job+": ") {
					t.Errorf("the plan does not say whether %s runs:\n%s", job, &reasons)
				}
			}
			if !strings.Contains(reasons.String(), test.jobs.Why) {
				t.Errorf("the plan does not say why (%s):\n%s", test.jobs.Why, &reasons)
			}
		})
	}
}

// A table the plan cannot read in full is refused: a rule read wrong would
// leave jobs out.
func TestThePlanRefusesATableItCannotReadInFull(t *testing.T) {
	for name, source := range map[string]string{
		"no table":                         ``,
		"not JSON":                         `jobs`,
		"a field this build does not know": strings.Replace(table, `"browser":`, `"lint":{"paths":["**"]},"browser":`, 1),
		"no go job":                        strings.Replace(table, `"go":[{"shard":"conpty","packages":"./internal/conpty"},{"shard":"rest","except":"./internal/conpty"}]`, `"go":[]`, 1),
		"no path for the browser jobs":     strings.Replace(table, `"browser":{"paths":["frontend/**"]}`, `"browser":{"paths":[]}`, 1),
		"no path for the frontend job":     strings.Replace(table, `"frontend":{"paths":["frontend/**"],`, `"frontend":{"paths":[],`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			_, err := parseTable(source)

			// Assert
			if err == nil {
				t.Errorf("the plan read %q as a table of jobs, want it refused", source)
			}
		})
	}
}
