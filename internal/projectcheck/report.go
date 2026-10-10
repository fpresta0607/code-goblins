// Package projectcheck assesses what a CFO home knows about one project and
// says whether it is still true: the project's record, its verification gate,
// its configs and its connectors. Every line of its report carries the
// evidence it rests on, and nothing here writes into a project or starts one
// of its commands: it reads files, asks git, and looks programs up.
package projectcheck

import (
	"fmt"
	"slices"
	"strings"
)

// Severity says how bad a line of the report is.
type Severity string

const (
	// OK is a line that proves something right.
	OK Severity = "ok"
	// Low is worth tidying and harms nothing as it stands.
	Low Severity = "low"
	// Medium misleads or wastes a run.
	Medium Severity = "medium"
	// High breaks a command or leaves a project unsteered.
	High Severity = "high"
	// Critical can spend money or reach production.
	Critical Severity = "critical"
)

// The areas a report covers.
const (
	AreaRecord     = "record"
	AreaConfigs    = "configs"
	AreaConnectors = "connectors"
)

// Areas are the areas a report covers, in the order it prints them.
var Areas = []string{AreaRecord, AreaConfigs, AreaConnectors}

// Finding is one line of a report.
type Finding struct {
	// Area is the part of the project's knowledge the line is about.
	Area string `json:"area"`
	// Check names the line's check, the same name whatever it found.
	Check string `json:"check"`
	// Severity is how bad it is.
	Severity Severity `json:"severity"`
	// Says is what was found, in one sentence.
	Says string `json:"says"`
	// Evidence is what the line rests on: a file, a line of it, a command
	// and what it answered. It never holds a credential's value.
	Evidence string `json:"evidence"`
	// Fix is what puts it right, empty for a line that proves something.
	Fix string `json:"fix,omitempty"`
}

// Text is the finding as one line of the report.
func (f Finding) Text() string {
	line := fmt.Sprintf("%s %s/%s: %s | evidence: %s", f.Severity, f.Area, f.Check, f.Says, f.Evidence)
	if f.Fix != "" {
		line += " | fix: " + f.Fix
	}
	return line
}

// Report is what one assessment of one project found.
type Report struct {
	// Project is the checkout's folder name, which the home keys the
	// project's files by.
	Project string `json:"project"`
	// Checkout is the project's checkout.
	Checkout string `json:"checkout"`
	// Lines are the findings, in area order.
	Lines []Finding `json:"lines"`
}

// Passed reports whether an area has no line worse than low.
func (r Report) Passed(area string) bool {
	for _, line := range r.Lines {
		if line.Area == area && (line.Severity == Medium || line.Severity == High || line.Severity == Critical) {
			return false
		}
	}
	return true
}

// Only returns the report with the lines of the named areas alone.
func (r Report) Only(areas []string) Report {
	only := Report{Project: r.Project, Checkout: r.Checkout}
	for _, line := range r.Lines {
		if slices.Contains(areas, line.Area) {
			only.Lines = append(only.Lines, line)
		}
	}
	return only
}

// Verdict is the report's last line: whether each of the named areas passed,
// and for one that did not, how many lines of each severity failed it.
func (r Report) Verdict(areas []string) string {
	var verdicts []string
	for _, area := range areas {
		if r.Passed(area) {
			verdicts = append(verdicts, area+" passed")
			continue
		}
		var counts []string
		for _, severity := range []Severity{Critical, High, Medium} {
			count := 0
			for _, line := range r.Lines {
				if line.Area == area && line.Severity == severity {
					count++
				}
			}
			if count > 0 {
				counts = append(counts, fmt.Sprintf("%d %s", count, severity))
			}
		}
		verdicts = append(verdicts, area+" failed ("+strings.Join(counts, ", ")+")")
	}
	return "project " + r.Project + ": " + strings.Join(verdicts, ", ")
}

// Text is the report as its lines, one finding each.
func (r Report) Text() string {
	var b strings.Builder
	for _, line := range r.Lines {
		b.WriteString(line.Text())
		b.WriteByte('\n')
	}
	return b.String()
}
