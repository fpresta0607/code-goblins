package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// mergeQueueQuery asks GitHub whether a pull request's base requires a merge
// queue and whether the pull request is in it. GitHub answers for a queue a
// ruleset requires and for one classic branch protection requires alike.
const mergeQueueQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) { isMergeQueueEnabled isInMergeQueue }
  }
}`

// mergeQueue is what GitHub says of a pull request and its base's merge queue.
type mergeQueue struct {
	// IsRequired says the base merges only through its queue, which tests a
	// pull request on the base's current tip with the ones ahead of it and
	// merges it when that passes.
	IsRequired bool
	// IsQueued says the pull request is in the queue already.
	IsQueued bool
}

// readMergeQueue reads whether pullRequest's base requires a merge queue. An
// answer that cannot be read is an error and never a direct merge: merging
// around a queue the base requires is not a fallback.
func readMergeQueue(ctx context.Context, pullRequest string, commands execx.Runner) (mergeQueue, error) {
	repository, err := pullRequestRepository(pullRequest)
	if err != nil {
		return mergeQueue{}, err
	}
	owner, name, _ := strings.Cut(repository, "/")
	parsed, err := url.Parse(pullRequest)
	if err != nil {
		return mergeQueue{}, err
	}
	number := path.Base(strings.Trim(parsed.Path, "/"))
	answer, err := ghText(ctx, commands, "api", "graphql", "-f", "query="+mergeQueueQuery, "-f", "owner="+owner, "-f", "name="+name, "-F", "number="+number)
	if err != nil {
		return mergeQueue{}, fmt.Errorf("GitHub did not say whether %s's base requires a merge queue: %w", pullRequest, err)
	}
	var read struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					IsMergeQueueEnabled bool `json:"isMergeQueueEnabled"`
					IsInMergeQueue      bool `json:"isInMergeQueue"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(answer), &read); err != nil {
		return mergeQueue{}, fmt.Errorf("GitHub's answer on %s's merge queue could not be read: %w", pullRequest, err)
	}
	found := read.Data.Repository.PullRequest
	if found == nil {
		return mergeQueue{}, errors.New("GitHub did not find " + pullRequest + " to say whether its base requires a merge queue")
	}
	return mergeQueue{IsRequired: found.IsMergeQueueEnabled, IsQueued: found.IsInMergeQueue}, nil
}
