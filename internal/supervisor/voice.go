package supervisor

import (
	"errors"
	"net/http"
	"os"

	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/siqspeak"
)

func readVoiceSnapshot() (siqspeak.Snapshot, error) {
	root := ""
	override := os.Getenv("CFO_SIQSPEAK_DIR")
	if override == "" {
		var err error
		root, err = install.MachineProjectsRoot()
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

func (h *HTTP) voice(w http.ResponseWriter, _ *http.Request) {
	if h.Service.Instance == "" {
		apiError(w, 403, "Refresh the board before reading voice history")
		return
	}
	value, err := h.readVoice()
	if err != nil {
		apiError(w, 503, "SIQspeak could not be read. Check its installation and CFO_SIQSPEAK_DIR on the board computer.")
		return
	}
	respond(w, 200, value)
}
