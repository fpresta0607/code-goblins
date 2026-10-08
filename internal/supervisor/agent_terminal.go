package supervisor

import (
	"net/http"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// agentLines answers the newest lines of a sub-agent's own record, for its
// terminal on the board: the sub-agent node of the task's goblin, which the
// board's last read of the goblin's tree holds. The record is the one that
// read found, never a path the browser names.
func (h *HTTP) agentLines(w http.ResponseWriter, meta state.TaskMeta, node string) {
	h.Service.mu.Lock()
	tree, isRead := h.Service.trees[meta.ID]
	h.Service.mu.Unlock()
	if !isRead || tree.Generation != meta.SpawnGen || !slices.ContainsFunc(tree.Children, func(child fleettree.Node) bool {
		return child.ID == node && child.Kind == fleettree.KindSubagent
	}) || h.Service.Options.Tree == nil {
		apiError(w, 404, "No sub-agent of this goblin by that id")
		return
	}
	path, harness := h.Service.Options.Tree.AgentTranscript(meta.ID, node)
	if path == "" {
		apiError(w, 404, "The sub-agent's record is not kept")
		return
	}
	lines, err := fleettree.AgentLines(path, harness)
	if err != nil {
		apiError(w, 503, err.Error())
		return
	}
	respond(w, 200, struct {
		Lines []string `json:"lines"`
	}{lines})
}
