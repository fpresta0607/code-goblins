package tickets

import (
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
)

// workStatesGitHub answers the GraphQL query WorkStates makes the way GitHub
// would: answers holds, by number, the aliased field each is answered with,
// and a number it holds none for is answered null. Each query's text and
// variables are recorded.
type workStatesGitHub struct {
	answers   map[int]map[string]any
	queries   []string
	variables []map[string]any
	// isRepositoryNull answers the repository null.
	isRepositoryNull bool
	// errors are GitHub's errors, answered beside the data.
	errors []string
}

func (f *workStatesGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query, variables, err := githubtest.Query(r)
	if err != nil {
		githubtest.Refuse(w, http.StatusBadRequest, err.Error())
		return
	}
	f.queries = append(f.queries, query)
	f.variables = append(f.variables, variables)
	var repository any
	if !f.isRepositoryNull {
		fields := map[string]any{}
		for name, value := range variables {
			if alias, isNumber := strings.CutPrefix(name, "n"); isNumber && name != "name" {
				fields["n"+alias] = f.answers[int(value.(float64))]
			}
		}
		repository = fields
	}
	body := map[string]any{"data": map[string]any{"repository": repository}}
	if len(f.errors) > 0 {
		var problems []map[string]any
		for _, message := range f.errors {
			problems = append(problems, map[string]any{"message": message})
		}
		body["errors"] = problems
	}
	githubtest.Write(w, http.StatusOK, body)
}

func workAnswer(typename, state string) map[string]any {
	return map[string]any{"__typename": typename, "state": state}
}

func TestWorkStatesReadsManyNumbersInOneQuery(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{answers: map[int]map[string]any{}}
	var numbers []int
	for number := 1; number <= 120; number++ {
		numbers = append(numbers, number)
		fake.answers[number] = workAnswer("Issue", "OPEN")
	}

	// Act
	states, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", numbers)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.queries) != 1 || len(fake.variables[0]) != 122 {
		t.Fatalf("queries = %d with %d variables, want one with the owner, the name and 120 numbers", len(fake.queries), len(fake.variables[0]))
	}
	if len(states) != 120 || states[1] != "open" || states[120] != "open" {
		t.Fatalf("states = %v, want 120 open", states)
	}
}

func TestWorkStatesNamesTheStateOfEachIssueAndPullRequest(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{answers: map[int]map[string]any{
		1: workAnswer("Issue", "OPEN"),
		2: workAnswer("Issue", "CLOSED"),
		3: workAnswer("PullRequest", "OPEN"),
		4: workAnswer("PullRequest", "CLOSED"),
		5: workAnswer("PullRequest", "MERGED"),
	}}

	// Act
	states, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", []int{1, 2, 3, 4, 5})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{1: "open", 2: "closed", 3: "open", 4: "closed", 5: "merged"}
	if !maps.Equal(states, want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
}

func TestWorkStatesCallsANumberGitHubAnswersNullForMissing(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{
		answers: map[int]map[string]any{1: workAnswer("Issue", "CLOSED")},
		errors:  []string{"Could not resolve to an issue or pull request with the number of 99."},
	}

	// Act
	states, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", []int{1, 99})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int]string{1: "closed", 99: "missing"}; !maps.Equal(states, want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
}

func TestWorkStatesFailsOnARefusalThatIsNotANumberNotFound(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{
		answers: map[int]map[string]any{1: workAnswer("Issue", "CLOSED")},
		errors:  []string{"Could not resolve to an issue or pull request with the number of 99.", "Something went wrong"},
	}

	// Act
	states, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", []int{1, 99})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "Something went wrong") {
		t.Fatalf("err = %v, want GitHub's message", err)
	}
	if states != nil {
		t.Fatalf("states = %v, want none from a failed read", states)
	}
}

func TestWorkStatesFailsOnAStateGitHubDoesNotGive(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{answers: map[int]map[string]any{1: workAnswer("Issue", "MERGED")}}

	// Act
	_, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", []int{1})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "MERGED") {
		t.Fatalf("err = %v, want the unreadable state named", err)
	}
}

func TestWorkStatesFailsWhenTheRepositoryIsNull(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		errors []string
		want   string
	}{
		{name: "with GitHub's messages", errors: []string{"Could not resolve to a Repository with the name 'acme/widgets'."}, want: "Could not resolve to a Repository"},
		{name: "without a message", want: "acme/widgets"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			fake := &workStatesGitHub{isRepositoryNull: true, errors: testCase.errors}

			// Act
			states, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", []int{1})

			// Assert
			if err == nil || !strings.Contains(err.Error(), "acme/widgets") || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err = %v, want one naming the repository and %q", err, testCase.want)
			}
			if states != nil {
				t.Fatalf("states = %v, want none", states)
			}
		})
	}
}

func TestWorkStatesRefusesARepositoryThatIsNotOwnerAndName(t *testing.T) {
	for _, repository := range []string{"", "widgets", "/widgets", "acme/"} {
		t.Run(repository, func(t *testing.T) {
			// Arrange
			fake := &workStatesGitHub{}

			// Act
			_, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), repository, []int{1})

			// Assert
			if err == nil || len(fake.queries) != 0 {
				t.Fatalf("err = %v, queries = %d, want a refusal before any query", err, len(fake.queries))
			}
		})
	}
}

func TestWorkStatesReadsNoNumbersWithoutAQuery(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{}

	// Act
	states, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", nil)

	// Assert
	if err != nil || len(states) != 0 || len(fake.queries) != 0 {
		t.Fatalf("states = %v, err = %v, queries = %d, want nothing", states, err, len(fake.queries))
	}
}

func TestWorkStatesReadsADuplicateNumberOnce(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{answers: map[int]map[string]any{7: workAnswer("PullRequest", "MERGED")}}

	// Act
	states, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", []int{7, 7})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.variables) != 1 || len(fake.variables[0]) != 3 || !maps.Equal(states, map[int]string{7: "merged"}) {
		t.Fatalf("variables = %v, states = %v, want one number read once", fake.variables, states)
	}
}

func TestWorkStatesSendsTheRepositoryAndNumbersAsVariablesNeverInTheQuery(t *testing.T) {
	// Arrange
	fake := &workStatesGitHub{answers: map[int]map[string]any{4242: workAnswer("Issue", "OPEN")}}

	// Act
	_, err := GitHub{API: githubtest.Client(fake)}.WorkStates(t.Context(), "acme/widgets", []int{4242})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, spliced := range []string{"acme", "widgets", "4242"} {
		if strings.Contains(fake.queries[0], spliced) {
			t.Fatalf("query spliced %q in: %s", spliced, fake.queries[0])
		}
	}
	want := map[string]any{"owner": "acme", "name": "widgets", "n0": float64(4242)}
	if !maps.Equal(fake.variables[0], want) {
		t.Fatalf("variables = %v, want %v", fake.variables[0], want)
	}
	if !strings.Contains(fake.queries[0], "$n0: Int!") || !strings.Contains(fake.queries[0], "issueOrPullRequest(number: $n0)") {
		t.Fatalf("query = %s, want the number declared as Int! and read through issueOrPullRequest", fake.queries[0])
	}
}
