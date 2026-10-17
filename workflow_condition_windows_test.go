package codegoblins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"gopkg.in/yaml.v3"
)

// pullRequestRun is a pull request's run as a job's condition reads it. Its
// plan, where it succeeds, starts every job it answers for.
type pullRequestRun struct {
	// isDraftSeen says the run's event saw a draft.
	isDraftSeen bool
	// draft is the draft job's result, and read its draft output.
	draft, read string
	// others is the result of every other job that starts.
	others      string
	isCancelled bool
}

// workflowJobs is each job of a workflow as GitHub Actions reads it to decide
// whether to start it.
type workflowJobs map[string]struct {
	If    string          `yaml:"if"`
	Needs goWorkflowNeeds `yaml:"needs"`
}

// readWorkflowJobs is the jobs of the workflow in the file named name.
func readWorkflowJobs(t *testing.T, name string) workflowJobs {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs workflowJobs `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow.Jobs
}

// jobStarts says whether GitHub Actions starts job of jobs in run. It reads
// the job's condition as GitHub's expressions are read (docs.github.com,
// "Evaluate expressions in workflows and actions", and "Using jobs in a
// workflow"): a condition that calls no status function is
// success() && condition, and success() holds while the run goes on and
// every job above it, those it needs and those they need in turn, succeeded,
// so a job skipped anywhere above skips it; a && or || gives the operand that
// decides it; strings compare ignoring case; an empty string, null and false
// are false. A job above that does not start is skipped. An output or field
// run does not give is null, which compares as the empty string here. A
// condition it does not follow fails the test.
func jobStarts(t *testing.T, jobs workflowJobs, job string, run pullRequestRun) bool {
	t.Helper()
	result := func(name string) string {
		switch {
		case name == "draft":
			return run.draft
		case jobStarts(t, jobs, name, run):
			return run.others
		}
		return "skipped"
	}
	values := map[string]string{"github.event_name": "pull_request", "needs.draft.outputs.draft": run.read}
	if run.isDraftSeen {
		values["github.event.pull_request.draft"] = "true"
	}
	for _, need := range jobs[job].Needs {
		values["needs."+need+".result"] = result(need)
	}
	if values["needs.plan.result"] == "success" {
		values["needs.plan.outputs.frontend"] = "true"
		values["needs.plan.outputs.browser"] = "true"
		values["needs.plan.outputs.go"] = `[{"shard":"rest"}]`
	}
	succeeded := !run.isCancelled
	for above := slices.Clone(jobs[job].Needs); len(above) > 0; {
		name := above[len(above)-1]
		above = append(above[:len(above)-1], jobs[name].Needs...)
		succeeded = succeeded && result(name) == "success"
	}

	condition := jobs[job].If
	expression := strings.TrimSpace(condition)
	if inner, ok := strings.CutPrefix(expression, "${{"); ok {
		expression = strings.TrimSuffix(inner, "}}")
	}
	var tokens []string
	for rest := strings.TrimSpace(expression); rest != ""; rest = strings.TrimSpace(rest) {
		size := 1
		switch {
		case rest[0] == '\'':
			size = strings.IndexByte(rest[1:], '\'') + 2
			if size < 2 {
				t.Fatalf("condition %q has a string with no end", condition)
			}
		case strings.HasPrefix(rest, "&&"), strings.HasPrefix(rest, "||"), strings.HasPrefix(rest, "=="), strings.HasPrefix(rest, "!="):
			size = 2
		case strings.ContainsRune("!()", rune(rest[0])):
		default:
			size = strings.IndexFunc(rest, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-", r)
			})
			if size == 0 {
				t.Fatalf("condition %q has %q, which this reading does not follow", condition, rest)
			}
			if size < 0 {
				size = len(rest)
			}
		}
		tokens = append(tokens, rest[:size])
		rest = rest[size:]
	}

	position := 0
	peek := func() string {
		if position < len(tokens) {
			return tokens[position]
		}
		return ""
	}
	take := func(token string) bool {
		if peek() != token {
			return false
		}
		position++
		return true
	}
	isTrue := func(value any) bool { return value == true || (value != false && value != "") }
	isStatusRead := false
	var or, and, compare, unary func() any
	or = func() any {
		value := and()
		for take("||") {
			if right := and(); !isTrue(value) {
				value = right
			}
		}
		return value
	}
	and = func() any {
		value := compare()
		for take("&&") {
			if right := compare(); isTrue(value) {
				value = right
			}
		}
		return value
	}
	compare = func() any {
		value := unary()
		for {
			isEqual := take("==")
			if !isEqual && !take("!=") {
				return value
			}
			left, isLeftString := value.(string)
			right, isRightString := unary().(string)
			if !isLeftString || !isRightString {
				t.Fatalf("condition %q compares what is not a string, which this reading does not follow", condition)
			}
			value = strings.EqualFold(left, right) == isEqual
		}
	}
	unary = func() any {
		if take("!") {
			return !isTrue(unary())
		}
		token := peek()
		position++
		switch {
		case token == "(":
			value := or()
			if !take(")") {
				t.Fatalf("condition %q opens a bracket it does not close", condition)
			}
			return value
		case strings.HasPrefix(token, "'"):
			return strings.Trim(token, "'")
		case token == "" || !unicode.IsLetter(rune(token[0])):
			t.Fatalf("condition %q has %q where a value belongs", condition, token)
		case take("("):
			if !take(")") {
				t.Fatalf("condition %q passes %s() something, which this reading does not follow", condition, token)
			}
			isStatusRead = true
			switch token {
			case "always":
				return true
			case "cancelled":
				return run.isCancelled
			}
			t.Fatalf("condition %q calls %s(), which this reading does not follow", condition, token)
		}
		return values[token]
	}

	value := or()
	if position != len(tokens) {
		t.Fatalf("condition %q has %v left over", condition, tokens[position:])
	}
	if !isStatusRead {
		return succeeded && isTrue(value)
	}
	return isTrue(value)
}
