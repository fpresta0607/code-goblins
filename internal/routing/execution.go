package routing

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

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

// ExecutionLane is a lane with its name attached: what a spawn actually runs.
type ExecutionLane struct{ Name, Harness, Model, Effort, Note string }

// ChooseExecution names the lane an assessment asks for. An explicit harness
// wins outright. A lane named after the class (scout, mechanical, ...) is used
// when the table defines one; high risk, a high-risk class, or a second
// attempt escalates, and escalation wins over a class-named lane; everything
// else runs on the default lane.
func ChooseExecution(a Assessment, lanes map[string]ExecutionLane, defaultLane, escalateTo string) ExecutionLane {
	if a.ExplicitHarness != "" {
		return ExecutionLane{Name: "explicit", Harness: a.ExplicitHarness, Model: a.ExplicitModel, Effort: a.ExplicitEffort}
	}
	name := defaultLane
	if _, ok := lanes[string(a.Class)]; ok {
		name = string(a.Class)
	}
	if escalates(a) && escalateTo != "" {
		name = escalateTo
	}
	l := lanes[name]
	if l.Name == "" {
		l.Name = name
	}
	return l
}

func escalates(a Assessment) bool {
	return a.Risk == "high" || a.Class == Architecture || a.Class == Security || a.Class == Migration || a.Attempts >= 2
}

// Choice is what a spawn runs and why.
type Choice struct {
	ExecutionLane
	// Wanted is the lane the assessment asked for. It differs from Name only
	// when the usable check moved the spawn to another lane.
	Wanted string
	// Notes are the per-lane findings in the order the lanes were tried, so
	// the spawn line can say what was wanted, what ran, and why.
	Notes []string
}

// Usable reports whether a lane can run now, with a note saying what the
// evidence was either way. An empty note is not recorded.
type Usable func(ExecutionLane) (ok bool, note string)

// Choose picks the lane an assessment asks for and then, when a usable check
// is given, walks the fallback order (the wanted lane, the default, the
// escalation, then the rest by name) until a lane passes. No usable lane is
// an error naming every lane's finding, so a spawn refuses rather than
// dispatching into a wall.
func Choose(a Assessment, t Table, usable Usable) (Choice, error) {
	wanted := ChooseExecution(a, t.Lanes, t.DefaultLane, t.EscalateTo)
	c := Choice{ExecutionLane: wanted, Wanted: wanted.Name}
	if usable == nil {
		return c, nil
	}
	for _, name := range fallbackOrder(wanted.Name, t) {
		lane := t.Lanes[name]
		ok, note := usable(lane)
		if note != "" {
			c.Notes = append(c.Notes, name+": "+note)
		}
		if ok {
			c.ExecutionLane = lane
			return c, nil
		}
	}
	return c, fmt.Errorf("no usable lane: %s", strings.Join(c.Notes, "; "))
}

// fallbackOrder is the wanted lane, the default, the escalation, then every
// other lane by name: the nearest lane in capability first, so a spawn moved
// by quota lands as close as possible to what the class asked for.
func fallbackOrder(wanted string, t Table) []string {
	order := []string{wanted}
	seen := map[string]bool{wanted: true}
	add := func(name string) {
		if _, ok := t.Lanes[name]; ok && !seen[name] {
			order = append(order, name)
			seen[name] = true
		}
	}
	add(t.DefaultLane)
	add(t.EscalateTo)
	rest := make([]string, 0, len(t.Lanes))
	for name := range t.Lanes {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

// The classes a brief can ask for, as phrases matched against the ask only.
// Every pattern is a whole word or phrase: "auth" alone matched "author",
// "oauth" and the Authentication boilerplate in every generated brief, so
// every brief used to read as high-risk security work. A migration is asked
// for when the brief says to add, write, apply or run one, or to migrate
// something; one mentioned in passing ("advanced by triggers (migration
// 257)") is not. A rename, investigation, audit or diagnosis is asked for
// only when it leads a sentence or list item: "Rename X to Y" is, while "the
// rename sweep" in a bug report, "add a rename action" and "add an audit log"
// are not.
var (
	securityAsk     = regexp.MustCompile(`\b(security|vulnerabilit(y|ies)|exploit|cve-\d+|xss|csrf|sql injection|access control|stripe|billing|payment (flow|processing|link)s?)\b`)
	migrationAsk    = regexp.MustCompile(`\b(add|write|create|generate|apply|run|ship|land|author|commit)\b[^.;\n]{0,60}\bmigrations?\b|\bmigrate\b`)
	architectureAsk = regexp.MustCompile(`\b(architecture|architectural|rearchitect)\b`)
	rescueAsk       = regexp.MustCompile(`\b(rescue|salvage)\b`)
	scoutAsk        = regexp.MustCompile(`(?:^|[\n.!?:]|[-*]\s)\s*(investigate|investigation|audit|diagnose|diagnosis)\b|\b(scout|feasibility)\b`)
	debugAsk        = regexp.MustCompile(`\b(fix|fixes|bug|bugs|regression|regressions|crash|crashes|broken|failing|root[- ]cause)\b`)
	deploymentAsk   = regexp.MustCompile(`\b(deploy|deploys|deployment|redeploy)\b`)
	reviewAsk       = regexp.MustCompile(`\breview\b[^.;\n]{0,30}\b(pr|prs|pull requests?|diff|branch|changes?|code|merges?)\b`)
	mechanicalAsk   = regexp.MustCompile(`(?:^|[\n.!?:]|[-*]\s)\s*rename\b[^.,;\n]{0,50}\b(to|as|into)\b|\b(mechanical|typos?)\b|\bdocs?[- ]only\b`)

	kindLine  = regexp.MustCompile(`(?m)^kind:\s*(\S+)`)
	codeSpan  = regexp.MustCompile("`[^`\n]*`")
	pathToken = regexp.MustCompile(`\S*/\S*`)
)

// droppedSections are headings whose text is not the ask: what not to do,
// and the boilerplate every generated brief carries.
var droppedSections = []string{"constraints", "authentication", "commits", "delivery", "non-goals", "out of scope"}

// Classify reads what a brief asks for and freezes it into a class and risk.
//
// The Delivery kind decides scout outright: the CFO already classified a
// scout when briefing it, and a ship never becomes a scout because its task
// says "investigate". Otherwise only the ask is read: the sections that say
// what not to do (Constraints) and the generated boilerplate (Authentication,
// Commits, Delivery) are dropped, as are code spans and paths, so a brief
// that says "do not touch billing" or reads docs/architecture.md is not
// billing or architecture work. High-risk classes are matched first, then
// the cheaper ones, and the default is ordinary implementation.
//
// ponytail: keyword rules over the ask, not a model call. The ceiling is a
// brief whose ask names another class's subject in passing ("keep the
// security scan green"); the upgrade is a small classifier model behind the
// same Assessment, with these rules as its test set.
func Classify(brief string) Assessment {
	a := Assessment{Class: Implementation, Risk: "normal"}
	kind := deliveryKind(brief)
	if kind == "scout" {
		a.Class = Scout
		return a
	}
	ask := askText(brief)
	switch {
	case securityAsk.MatchString(ask):
		a.Class, a.Risk = Security, "high"
	case migrationAsk.MatchString(ask):
		a.Class, a.Risk = Migration, "high"
	case architectureAsk.MatchString(ask):
		a.Class, a.Risk = Architecture, "high"
	case rescueAsk.MatchString(ask):
		a.Class, a.Risk = Rescue, "high"
	case kind == "" && scoutAsk.MatchString(ask):
		a.Class = Scout
	case reviewAsk.MatchString(ask):
		a.Class = Review
	case debugAsk.MatchString(ask):
		a.Class = Debug
	case deploymentAsk.MatchString(ask):
		a.Class = Deployment
	case mechanicalAsk.MatchString(ask):
		a.Class = Mechanical
	}
	return a
}

// deliveryKind reads the "kind:" line a generated brief's Delivery section
// carries, lowercased, or "" when the brief has none.
func deliveryKind(brief string) string {
	m := kindLine.FindStringSubmatch(brief)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// askText is the brief lowercased with the dropped sections, code spans and
// path tokens removed: the words that say what the brief asks for.
func askText(brief string) string {
	var kept []string
	drop := false
	for _, line := range strings.Split(brief, "\n") {
		if strings.HasPrefix(line, "## ") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			drop = false
			for _, name := range droppedSections {
				if strings.HasPrefix(heading, name) {
					drop = true
				}
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	text := strings.ToLower(strings.Join(kept, "\n"))
	text = codeSpan.ReplaceAllString(text, " ")
	return pathToken.ReplaceAllString(text, " ")
}
