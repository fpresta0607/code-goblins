package supervisor

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/clipboard"
)

// terminalImageLimit bounds one picture added to a terminal, which the board
// sends at most 2048 pixels a side.
const terminalImageLimit = 16 << 20

// pasteTerminalImage serves POST /api/terminal/image: a picture added to a
// terminal on a board opened on another device, such as his phone. A harness
// reads a pasted image from this PC's clipboard when its image key is
// pressed, and a desktop paste finds the picture there already; one from
// another device is put there first, and the board then presses the key.
func (h *HTTP) pasteTerminalImage(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "image/png") {
		apiError(w, http.StatusBadRequest, "A PNG picture is required")
		return
	}
	picture, err := io.ReadAll(http.MaxBytesReader(w, r.Body, terminalImageLimit))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		apiError(w, http.StatusRequestEntityTooLarge, "A picture may be at most 16 MiB.")
		return
	}
	if err != nil {
		apiError(w, http.StatusBadRequest, "The picture could not be read.")
		return
	}
	err = h.putImage(picture)
	switch {
	case errors.Is(err, clipboard.ErrNotPNG):
		apiError(w, http.StatusBadRequest, "That picture is not a PNG this PC can read, so nothing was added.")
	case err != nil:
		apiError(w, http.StatusInternalServerError, sentence("The picture could not be put on this PC's clipboard: "+err.Error()))
	default:
		respond(w, http.StatusOK, struct {
			Pasted bool `json:"pasted"`
		}{true})
	}
}
