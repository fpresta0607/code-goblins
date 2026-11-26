package tickets

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/githubapi"
)

// workStateNotFound begins the message GitHub gives for a number that is
// neither an issue nor a pull request of the repository.
const workStateNotFound = "could not resolve to an issue or pull request"

// workStateAnswer is one number as its aliased field answers.
type workStateAnswer struct {
	Typename string `json:"__typename"`
	State    string `json:"state"`
}

// WorkStates reads the state of each of numbers in repository, written
// owner/name, issues and pull requests alike, since they share one number
// space, in one GraphQL query with the owner, the name and each number sent as
// query variables and never spliced into the query text. A duplicate number is
// read once, and no numbers read nothing. An issue is "open" or "closed", a
// pull request "open", "closed" or "merged", and a number GitHub answers null
// for "missing". GitHub's refusal of only the numbers it cannot find is no
// error; a refusal of anything else, and a repository GitHub answers null
// for, are.
func (g GitHub) WorkStates(ctx context.Context, repository string, numbers []int) (map[int]string, error) {
	states := map[int]string{}
	var asked []int
	for _, number := range numbers {
		if _, isDuplicate := states[number]; !isDuplicate {
			states[number] = ""
			asked = append(asked, number)
		}
	}
	if len(asked) == 0 {
		return states, nil
	}
	owner, name, isRepository := strings.Cut(repository, "/")
	if !isRepository || owner == "" || name == "" {
		return nil, fmt.Errorf("read the work states of %q: it is not owner/name", repository)
	}
	query, variables := workStatesQuery(owner, name, asked)
	var answer struct {
		Repository map[string]*workStateAnswer `json:"repository"`
	}
	var refused *githubapi.GraphQLError
	var messages []string
	if err := g.API.GraphQL(ctx, query, variables, &answer); errors.As(err, &refused) {
		messages = refused.Messages
	} else if err != nil {
		return nil, fmt.Errorf("read the work states of %s: %w", repository, err)
	}
	if answer.Repository == nil {
		if len(messages) > 0 {
			return nil, fmt.Errorf("read the work states of %s: %s", repository, strings.Join(messages, "; "))
		}
		return nil, fmt.Errorf("read the work states of %s: GitHub answered no repository, which is not visible to the account gh is signed in as", repository)
	}
	for _, message := range messages {
		if !strings.HasPrefix(strings.ToLower(message), workStateNotFound) {
			return nil, fmt.Errorf("read the work states of %s: GitHub answered %s", repository, strings.Join(messages, "; "))
		}
	}
	for index, number := range asked {
		work := answer.Repository["n"+strconv.Itoa(index)]
		if work == nil {
			states[number] = "missing"
			continue
		}
		state, isReadable := work.state()
		if !isReadable {
			return nil, fmt.Errorf("read the work states of %s: #%d is a %s in state %q, which is not one GitHub gives", repository, number, work.Typename, work.State)
		}
		states[number] = state
	}
	return states, nil
}

// state is the answer as WorkStates names it, and whether it is one an issue
// or a pull request can have.
func (work workStateAnswer) state() (string, bool) {
	switch work.Typename + " " + work.State {
	case "Issue OPEN", "PullRequest OPEN":
		return "open", true
	case "Issue CLOSED", "PullRequest CLOSED":
		return "closed", true
	case "PullRequest MERGED":
		return "merged", true
	}
	return "", false
}

// workStatesQuery is the query that reads each of numbers as an aliased
// field, n0, n1 and so on, of the repository, and the variables that carry the
// owner, the name and the numbers.
func workStatesQuery(owner, name string, numbers []int) (string, map[string]any) {
	variables := map[string]any{"owner": owner, "name": name}
	declarations := []string{"$owner: String!", "$name: String!"}
	var fields []string
	for index, number := range numbers {
		position := strconv.Itoa(index)
		variables["n"+position] = number
		declarations = append(declarations, "$n"+position+": Int!")
		fields = append(fields, "    n"+position+": issueOrPullRequest(number: $n"+position+") { __typename ... on Issue { state } ... on PullRequest { state } }")
	}
	return "query(" + strings.Join(declarations, ", ") + ") {\n  repository(owner: $owner, name: $name) {\n" + strings.Join(fields, "\n") + "\n  }\n}", variables
}
