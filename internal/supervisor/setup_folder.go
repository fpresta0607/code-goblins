package supervisor

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (h *HTTP) chooseProjectsFolder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Root string `json:"root"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	setup := h.Service.Options.FirstRun
	if setup == nil {
		apiError(w, 503, "Setup is unavailable.")
		return
	}
	root := strings.TrimSpace(input.Root)
	if root == "" {
		var err error
		root, err = setup.ProjectsRoot()
		if err != nil {
			apiError(w, 503, "The recorded projects folder could not be read.")
			return
		}
		if root == "" {
			root = setup.Home
		}
	}
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
		apiError(w, 400, notAbsolute)
		return
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		apiError(w, 400, "Enter an existing folder or clear the field to use the default.")
		return
	}
	if !h.folderPickerMu.TryLock() {
		apiError(w, 409, "A folder picker is already open.")
		return
	}
	defer h.folderPickerMu.Unlock()
	pick := h.pickFolder
	if pick == nil {
		pick = pickProjectsFolder
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	selected, err := pick(ctx, root)
	if err != nil {
		apiError(w, 503, "The folder picker could not finish: "+err.Error())
		return
	}
	respond(w, 200, struct {
		Root string `json:"root"`
	}{selected})
}
