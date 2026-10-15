package afk

import (
	"regexp"
	"strings"
)

// specialists are the faces a goblin's work earns by the words of its title,
// in the order the board's personaFor in frontend/src/workflow.ts tries them,
// so a goblin wears the same face on its return page as on its card.
var specialists = []struct {
	pattern *regexp.Regexp
	persona string
}{
	{regexp.MustCompile(`\b(debug|debugger|crash|bug|regression)\b`), "debugger"},
	{regexp.MustCompile(`\b(security|vulnerability|threat|hardening)\b`), "security"},
	{regexp.MustCompile(`\b(database|postgres|sql|schema|migration)\b`), "database"},
	{regexp.MustCompile(`\b(designer|styling|visual|typography|brand)\b`), "designer"},
	{regexp.MustCompile(`\b(documentation|docs|readme|guide)\b`), "documentation"},
	{regexp.MustCompile(`\b(deploy|deployment|operations|infra|infrastructure)\b`), "operations"},
	{regexp.MustCompile(`\b(research|researcher|investigate|explore)\b`), "researcher"},
	{regexp.MustCompile(`\b(performance|latency|benchmark|optimize)\b`), "performance"},
	{regexp.MustCompile(`\b(integration|integrations|webhook|connector)\b`), "integrations"},
	{regexp.MustCompile(`\b(git|rebase|merge|conflict|version control)\b`), "git"},
	{regexp.MustCompile(`\b(accessibility|a11y|screen reader)\b`), "accessibility"},
	{regexp.MustCompile(`\b(release|releases|versioning|publish)\b`), "releases"},
}

var (
	builderStart = regexp.MustCompile(`^(build|implement|fix|repair|develop)\b`)
	testerWord   = regexp.MustCompile(`\b(test|tester|testing|qa|keyboard|validate|validation)\b`)
	reviewerWord = regexp.MustCompile(`\b(review|reviewer|audit|diff)\b`)
	plannerWord  = regexp.MustCompile(`\b(plan|planner|planning|design|research)\b`)
	builderWord  = regexp.MustCompile(`\b(build|builder|implement|fix|repair|develop)\b`)
)

// stablePersonas are the faces a task whose title names no specialist gets,
// one chosen by its ID.
var stablePersonas = []string{"builder", "reviewer", "tester", "planner", "debugger", "security", "database", "designer", "documentation", "operations", "researcher", "performance", "integrations", "git", "accessibility", "releases"}

// Persona is the face the board gives a live goblin of task with title, and
// the CFO's for no task.
func Persona(task, title string) string {
	if task == "" {
		return "cfo"
	}
	meaning := strings.ToLower(title)
	for _, specialist := range specialists {
		if specialist.pattern.MatchString(meaning) {
			return specialist.persona
		}
	}
	for _, rule := range []struct {
		pattern *regexp.Regexp
		persona string
	}{{builderStart, "builder"}, {testerWord, "tester"}, {reviewerWord, "reviewer"}, {plannerWord, "planner"}, {builderWord, "builder"}} {
		if rule.pattern.MatchString(meaning) {
			return rule.persona
		}
	}
	var hash uint32
	for _, character := range task {
		hash = hash*31 + uint32(character)
	}
	return stablePersonas[hash%uint32(len(stablePersonas))]
}
