package supervisor

import (
	"errors"
	"net/http"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// errNotInProgress is an In progress order naming a goblin that has no live
// task record: it finished or was cleaned up after the board showed it.
var errNotInProgress = errors.New("The goblins in progress changed while you moved them; the board now shows who is working.")

// order serves POST /api/order: the Overlord's order of a list on the board,
// top first. Tasks is saved as data\backlog.md's Queued order, which the CFO
// dispatches in, and In progress as the attention order the CFO works in.
func (h *HTTP) order(w http.ResponseWriter, r *http.Request) {
	var input struct {
		List  string   `json:"list"`
		Order []string `json:"order"`
	}
	if err := decodeBody(w, r, &input, 64<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(input.Order) > maxSessions {
		apiError(w, http.StatusBadRequest, "The order names too many tasks")
		return
	}
	for _, id := range input.Order {
		if state.ValidTaskID(id) != nil {
			apiError(w, http.StatusBadRequest, "The order names an invalid task ID")
			return
		}
	}
	var err error
	switch input.List {
	case "queued":
		err = h.Service.orderQueued(input.Order)
	case "progress":
		err = h.Service.orderProgress(input.Order)
	default:
		apiError(w, http.StatusBadRequest, "Only Tasks and In progress have an order")
		return
	}
	switch {
	case errors.Is(err, fleet.ErrQueueChanged):
		apiError(w, http.StatusConflict, "The queue changed while you moved it; the board now shows its current order.")
		return
	case errors.Is(err, errNotInProgress):
		apiError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Service.notify()
	// Every snapshot from this revision on shows the saved order.
	h.Service.mu.Lock()
	revision := h.Service.revision
	h.Service.mu.Unlock()
	respond(w, http.StatusOK, struct {
		Saved    bool   `json:"saved"`
		Revision uint64 `json:"revision"`
	}{true, revision})
}

// orderQueued saves order as backlog.md's Queued order. A brief the board
// lists with no backlog row gets one where the Overlord placed it.
func (s *Service) orderQueued(order []string) error {
	s.ordering.Lock()
	defer s.ordering.Unlock()
	h := s.Store.Home
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		return err
	}
	added := map[string]string{}
	for _, brief := range queuedBriefs(h) {
		listed := func(row fleet.BacklogRow) bool { return row.Structured && row.ID == brief.ID }
		if !slices.Contains(order, brief.ID) || slices.ContainsFunc(backlog.Queued, listed) || slices.ContainsFunc(backlog.Parked, listed) {
			continue
		}
		row := "- **" + brief.ID + "** - " + brief.ID
		if brief.Project != "" {
			row += " (repo: " + brief.Project + ")"
		}
		added[brief.ID] = row
	}
	return fleet.ReorderQueued(h, order, added)
}

// orderProgress saves order as the attention order of the goblins in
// progress, each of which must still have its live task record.
func (s *Service) orderProgress(order []string) error {
	s.ordering.Lock()
	defer s.ordering.Unlock()
	for _, id := range order {
		if _, err := state.ReadTaskMeta(s.Store.Home.State, id); err != nil {
			return errNotInProgress
		}
	}
	return fleet.WriteAttention(s.Store.Home, order)
}
