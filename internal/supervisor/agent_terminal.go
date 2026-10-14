package supervisor

import (
	"errors"
	"net/http"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// agentLines answers the newest lines of a child's own output, for its
// terminal on the board: a child of the task's goblin that the board's last
// read of the goblin's tree holds, read as that read found it
// (fleettree.Reader.ChildLines), never from a path the browser names.
func (h *HTTP) agentLines(w http.ResponseWriter, meta state.TaskMeta, node string) {
	h.Service.mu.Lock()
	tree, isRead := h.Service.trees[meta.ID]
	h.Service.mu.Unlock()
	if !isRead || tree.Generation != meta.SpawnGen || !slices.ContainsFunc(tree.Children, func(child fleettree.Node) bool {
		return child.ID == node && child.Kind != fleettree.KindHelper
	}) || h.Service.Options.Tree == nil {
		apiError(w, 404, "No child of this goblin by that id")
		return
	}
	lines, err := h.Service.Options.Tree.ChildLines(meta.ID, node)
	if errors.Is(err, fleettree.ErrNoRecord) {
		apiError(w, 404, "The child's output is not kept")
		return
	}
	if err != nil {
		apiError(w, 503, err.Error())
		return
	}
	respond(w, 200, struct {
		Lines []string `json:"lines"`
	}{lines})
}
