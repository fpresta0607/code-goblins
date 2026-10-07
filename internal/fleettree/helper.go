package fleettree

import (
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// HelperStanding is where a helper goblin stands by its own records: its
// latest report's kind (done, failed, blocked, working or waiting) and when
// it made it, and whether it is paused.
type HelperStanding struct {
	Report     string
	ReportedAt time.Time
	Paused     bool
}

// HelperNode is a helper goblin as a child of its parent's tree: a goblin of
// its own that the supervisor started for the parent, named by its title,
// with its own tree's memory. A paused helper waits, one that reported done
// or failed has finished, and one that asked its parent waits on the answer;
// else it works while its own tree shows activity, and is silent once that
// has stopped for SilentAfter.
func HelperNode(meta state.TaskMeta, tree Tree, standing HelperStanding, now time.Time) Node {
	node := Node{
		ID: "helper:" + meta.ID, Kind: KindHelper, Label: meta.Title, Detail: "Helper goblin " + meta.ID, State: Working,
		Started: spawned(meta.SpawnGen), LastActivity: tree.ActivityAt(), Memory: tree.Memory,
		SourceUpdatedAt: tree.SourceUpdatedAt, FetchedAt: tree.FetchedAt,
	}
	if node.Label == "" {
		node.Label = meta.ID
	}
	if node.FetchedAt.IsZero() {
		node.FetchedAt = now
	}
	if standing.ReportedAt.After(node.LastActivity) {
		node.LastActivity = standing.ReportedAt
	}
	switch {
	case standing.Paused:
		node.State, node.Detail = Waiting, node.Detail+", paused"
	case standing.Report == "done":
		node.State, node.Detail, node.Finished = Done, node.Detail+", ready to merge", standing.ReportedAt
	case standing.Report == "failed":
		node.State, node.Detail, node.Finished = Failed, node.Detail+", failed", standing.ReportedAt
	case standing.Report == "blocked":
		node.State, node.Detail = Waiting, node.Detail+", asks its parent"
	}
	settle(&node, now)
	return node
}

// spawned is when a task's generation started, zero for one that names no
// time.
func spawned(generation string) time.Time {
	nanos, err := strconv.ParseInt(strings.TrimPrefix(generation, "s"), 10, 64)
	if err != nil || !strings.HasPrefix(generation, "s") {
		return time.Time{}
	}
	return time.Unix(0, nanos).UTC()
}
