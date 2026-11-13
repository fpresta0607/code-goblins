package supervisor

import (
	"testing"
)

// pastUndoWindow ages each of his queued messages to the CEO past its undo
// window, as if it had waited it out on the board.
func pastUndoWindow(t *testing.T, store *Store) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := range store.db.Actions {
		if a := &store.db.Actions[i]; a.Kind == "message" && a.TaskID == "" && a.Status == "queued" {
			a.CreatedAt = a.CreatedAt.Add(-ceoMessageUndo)
		}
	}
}
