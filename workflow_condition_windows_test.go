package codegoblins

import (
	"strings"
	"testing"
	"unicode"
)

// pullRequestRun is a pull request's run as a job's condition reads it.
type pullRequestRun struct {
	// isDraftSeen says the run's event saw a draft.
	isDraftSeen bool
	// draft is the draft job's result, and read its draft output.
	draft, read string
	// others is the result of every other job the job needs.
	others      string
	isCancelled bool
}

// jobStarts says whether GitHub Actions starts a job that needs needs and
// runs if condition, in run. It reads condition as GitHub's expressions are
// read (docs.github.com, "Evaluate expressions in workflows and actions"): a
// condition that calls no status function is success() && condition, and
// success() holds while the run goes on and every job it needs succeeded; a
// && or || gives the operand that decides it; strings compare ignoring case;
// an empty string, null and false are false. An output or field run does not
// give is null, which compares as the empty string here. A condition it does
// not follow fails the test.
func jobStarts(t *testing.T, condition string, needs []string, run pullRequestRun) bool {
	t.Helper()
	values := map[string]string{"needs.draft.outputs.draft": run.read}
	if run.isDraftSeen {
		values["github.event.pull_request.draft"] = "true"
	}
	for _, need := range needs {
		values["needs."+need+".result"] = run.others
	}
	values["needs.draft.result"] = run.draft
	succeeded := !run.isCancelled
	for _, need := range needs {
		succeeded = succeeded && values["needs."+need+".result"] == "success"
	}

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
