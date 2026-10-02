package supervisor

import (
	"errors"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/siqspeak"
)

func voiceReader(projectsRoot func() (string, error)) func() (siqspeak.Snapshot, error) {
	var lock sync.Mutex
	resolved := ""
	isResolved := false
	resolve := func() (string, error) {
		lock.Lock()
		defer lock.Unlock()
		if isResolved {
			return resolved, nil
		}
		root, err := projectsRoot()
		if err != nil {
			return "", err
		}
		resolved, isResolved = root, true
		return root, nil
	}
	return func() (siqspeak.Snapshot, error) {
		root := ""
		override := os.Getenv("CFO_SIQSPEAK_DIR")
		if override == "" {
			var err error
			root, err = resolve()
			if err != nil {
				return siqspeak.Snapshot{}, errors.New("SIQspeak installation folder could not be located")
			}
		}
		directory, err := siqspeak.Locate(root, override)
		if err != nil {
			return siqspeak.Snapshot{}, err
		}
		return siqspeak.Read(directory)
	}
}

func (h *HTTP) voice(w http.ResponseWriter, r *http.Request) {
	if h.Service.Instance == "" {
		apiError(w, 403, "Refresh the board before reading voice history")
		return
	}
	// The request carries nothing, so a body is refused unread.
	if n, _ := io.ReadFull(r.Body, make([]byte, 1)); n != 0 {
		apiError(w, 400, "Voice history takes no request body")
		return
	}
	value, err := h.readVoice()
	if err != nil {
		apiError(w, 503, "SIQspeak could not be read. Check its installation and CFO_SIQSPEAK_DIR on the board computer.")
		return
	}
	respond(w, 200, value)
}
