package supervisor

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A failure only the board sees, such as a read that failed, a stream it
// could not parse or a card it could not draw, goes to the CFO rather than
// onto the board, as the Overlord ruled on 2026-10-08: "again everything error
// wise goes to cfo and cfo decides what to tell me in command center". The
// board reports it through POST /api/cfo/report; the supervisor's own errors
// reach the CFO from the supervisor. Each wake is a notify keyed board, and
// the same failure wakes the CFO at most once an hour.
const boardErrorQuiet = time.Hour

// boardErrors remembers when each error last woke the CFO.
type boardErrors struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (b *boardErrors) report(stateDir, where, text string, now time.Time) error {
	where, text = strings.TrimSpace(where), strings.TrimSpace(text)
	key := where + "\x00" + text
	b.mu.Lock()
	if last, isSeen := b.seen[key]; isSeen && now.Sub(last) < boardErrorQuiet {
		b.mu.Unlock()
		return nil
	}
	if b.seen == nil {
		b.seen = map[string]time.Time{}
	}
	b.seen[key] = now
	b.mu.Unlock()
	_, err := wake.Append(stateDir, "notify", "board", "board: "+bounded(where, 200)+": "+bounded(text, 1500))
	return err
}

// reportBoardError serves POST /api/cfo/report: a failure only the board
// saw, which it gives the CFO rather than showing him.
func (h *HTTP) reportBoardError(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Where string `json:"where"`
		Text  string `json:"text"`
	}
	if err := decodeBody(w, r, &input, 8192); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(input.Where) == "" || strings.TrimSpace(input.Text) == "" {
		apiError(w, http.StatusBadRequest, "a report needs where and text")
		return
	}
	if err := h.Service.boardErrors.report(h.Service.Store.Home.State, input.Where, input.Text, time.Now()); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, http.StatusAccepted, struct {
		Reported bool `json:"reported"`
	}{true})
}
