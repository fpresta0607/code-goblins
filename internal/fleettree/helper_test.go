package fleettree

import (
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A helper goblin hangs under its parent as a child whose state is its own:
// what its latest report and its lifecycle say, else working while its own
// tree shows activity and silent once it has gone quiet, with its own
// memory.
func TestAHelperIsAChildWhoseStateIsItsOwnReportAndLifecycle(t *testing.T) {
	spawned := at.Add(-time.Hour)
	meta := state.TaskMeta{ID: "g1-h1", Parent: "g1", Title: "Accounts migration", SpawnGen: "s" + itoa(spawned.UnixNano())}
	working := Tree{Memory: 300 << 20, ConversationAt: at.Add(-time.Minute), FetchedAt: at}
	for name, test := range map[string]struct {
		tree     Tree
		standing HelperStanding
		state    State
		detail   string
	}{
		"at work":           {working, HelperStanding{}, Working, "Helper goblin g1-h1"},
		"quiet for a while": {Tree{ConversationAt: at.Add(-20 * time.Minute), FetchedAt: at}, HelperStanding{}, Silent, "Helper goblin g1-h1"},
		"done":              {working, HelperStanding{Report: "done", ReportedAt: at.Add(-2 * time.Minute)}, Done, "ready to merge"},
		"failed":            {working, HelperStanding{Report: "failed", ReportedAt: at.Add(-2 * time.Minute)}, Failed, "failed"},
		"asking its parent": {working, HelperStanding{Report: "blocked", ReportedAt: at.Add(-2 * time.Minute)}, Waiting, "asks"},
		"paused":            {Tree{}, HelperStanding{Paused: true}, Waiting, "paused"},
	} {
		t.Run(name, func(t *testing.T) {
			node := HelperNode(meta, test.tree, test.standing, at)

			if node.Kind != KindHelper || node.ID != "helper:g1-h1" || node.Label != "Accounts migration" {
				t.Errorf("node = %+v, want the helper named by its title", node)
			}
			if node.State != test.state || !strings.Contains(node.Detail, test.detail) {
				t.Errorf("state %s, detail %q; want %s naming %q", node.State, node.Detail, test.state, test.detail)
			}
			if node.Memory != test.tree.Memory || !node.Started.Equal(spawned) {
				t.Errorf("memory %d started %v; want its own tree's %d, started at its spawn %v", node.Memory, node.Started, test.tree.Memory, spawned)
			}
			if isFinished := test.state == Done || test.state == Failed; isFinished != !node.Finished.IsZero() {
				t.Errorf("finished at %v for a %s helper", node.Finished, test.state)
			}
		})
	}
}
