package workreview

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// StateReader reads the states of numbers of one repository in one query, as
// tickets.GitHub.WorkStates does.
type StateReader func(ctx context.Context, repository string, numbers []int) (map[int]string, error)

// Report is what a run of the review found: its findings, and what it could not
// read, which leaves the issues and pull requests it would have settled unread.
type Report struct {
	Findings []Finding
	Problems []string
}

// Run gathers the home's queued rows, parked rows and paused tasks, reads the
// state of every issue and pull request they name with one query for each
// repository, and returns what Review finds with what could not be read. A
// repository whose states cannot be read is a problem, and its issues and pull
// requests stay unread, so they settle nothing.
func Run(ctx context.Context, h home.Home, resolve Resolver, read StateReader, now time.Time, staleAfter time.Duration) (Report, error) {
	items, problems, err := Gather(h, resolve, now)
	if err != nil {
		return Report{}, err
	}
	numbers := map[string][]int{}
	for _, item := range items {
		for _, ref := range slices.Concat(item.Issues, item.Pulls) {
			if !slices.Contains(numbers[ref.Repository], ref.Number) {
				numbers[ref.Repository] = append(numbers[ref.Repository], ref.Number)
			}
		}
	}
	states := States{}
	for _, repository := range slices.Sorted(maps.Keys(numbers)) {
		answered, err := read(ctx, repository, numbers[repository])
		if err != nil {
			problems = append(problems, fmt.Sprintf("the states of %s are not read: %v", repository, err))
			continue
		}
		for number, state := range answered {
			states[tickets.IssueRef{Repository: repository, Number: number}] = state
		}
	}
	return Report{Findings: Review(items, states, now, staleAfter), Problems: problems}, nil
}
