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
// repository whose states cannot be read is a problem, and Review leaves out
// every item that names one of its issues or pull requests, with every item
// that could fold with one left out, so a run that reads part of the home
// finds nothing it would not find reading all of it.
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
	unread := map[string]bool{}
	for _, repository := range slices.Sorted(maps.Keys(numbers)) {
		answered, err := read(ctx, repository, numbers[repository])
		if err != nil {
			problems = append(problems, fmt.Sprintf("the states of %s are not read: %v", repository, err))
			unread[repository] = true
			continue
		}
		for number, state := range answered {
			states[tickets.IssueRef{Repository: repository, Number: number}] = state
		}
	}
	return Report{Findings: Review(fullyRead(items, unread), states, now, staleAfter), Problems: problems}, nil
}

// fullyRead are the items that name no issue or pull request of an unread
// repository and could fold with no item that does, directly or through
// others.
func fullyRead(items []Item, unread map[string]bool) []Item {
	var kept, leftOut []Item
	for _, item := range items {
		if slices.ContainsFunc(slices.Concat(item.Issues, item.Pulls), func(ref tickets.IssueRef) bool { return unread[ref.Repository] }) {
			leftOut = append(leftOut, item)
		} else {
			kept = append(kept, item)
		}
	}
	for isLeaving := len(leftOut) > 0; isLeaving; {
		isLeaving = false
		kept = slices.DeleteFunc(kept, func(item Item) bool {
			if !slices.ContainsFunc(leftOut, func(out Item) bool { return isSameFoldGroup(item, out) }) {
				return false
			}
			leftOut = append(leftOut, item)
			isLeaving = true
			return true
		})
	}
	return kept
}
