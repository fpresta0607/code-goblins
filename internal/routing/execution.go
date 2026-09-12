package routing

import "strings"

type TaskClass string

const (
	Scout          TaskClass = "scout"
	Mechanical     TaskClass = "mechanical"
	Implementation TaskClass = "implementation"
	Architecture   TaskClass = "architecture"
	Review         TaskClass = "review"
	Debug          TaskClass = "debug"
	Migration      TaskClass = "migration"
	Deployment     TaskClass = "deployment"
	Security       TaskClass = "security"
	Rescue         TaskClass = "rescue"
)

type Assessment struct {
	Class                                          TaskClass
	Risk                                           string
	Attempts                                       int
	ExplicitHarness, ExplicitModel, ExplicitEffort string
}
type ExecutionLane struct{ Name, Harness, Model, Effort string }

func ChooseExecution(a Assessment, lanes map[string]ExecutionLane, defaultLane, rescueLane string) ExecutionLane {
	if a.ExplicitHarness != "" {
		return ExecutionLane{Name: "explicit", Harness: a.ExplicitHarness, Model: a.ExplicitModel, Effort: a.ExplicitEffort}
	}
	name := defaultLane
	if a.Class == Scout {
		if _, ok := lanes["open-scout"]; ok {
			name = "open-scout"
		}
	}
	if a.Class == Mechanical {
		if _, ok := lanes["open-builder"]; ok {
			name = "open-builder"
		}
	}
	if a.Risk == "high" || a.Class == Architecture || a.Class == Security || a.Class == Migration || a.Attempts >= 2 {
		name = rescueLane
	}
	l := lanes[name]
	if l.Name == "" {
		l.Name = name
	}
	return l
}
func Classify(brief string) Assessment {
	s := strings.ToLower(brief)
	a := Assessment{Class: Implementation, Risk: "normal"}
	switch {
	case strings.Contains(s, "security") || strings.Contains(s, "auth") || strings.Contains(s, "payment"):
		a.Class = Security
		a.Risk = "high"
	case strings.Contains(s, "migration"):
		a.Class = Migration
		a.Risk = "high"
	case strings.Contains(s, "architecture"):
		a.Class = Architecture
		a.Risk = "high"
	case strings.Contains(s, "rename") || strings.Contains(s, "mechanical"):
		a.Class = Mechanical
	case strings.Contains(s, "investigate") || strings.Contains(s, "trace") || strings.Contains(s, "scout"):
		a.Class = Scout
	}
	return a
}
