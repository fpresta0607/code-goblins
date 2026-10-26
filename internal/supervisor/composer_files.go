package supervisor

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// composerFilesDir holds the pictures and files he pastes or adds to a
// message, where the session he writes to reads them by the path the message
// names.
const composerFilesDir = "composer-files"

// composerExtension accepts only an extension a file name may carry, so a
// name he chose never adds a path segment.
var composerExtension = regexp.MustCompile(`^\.[A-Za-z0-9]{1,10}$`)

// keepComposerFile keeps one picture or file he pasted into, or added to, a
// message: the task's folder for a goblin, or the state folder for the CFO.
// The board's composer names the returned path in the message it sends, so
// the session reads the file from there, as answer_images.go does for a
// picture pasted into an answer.
func (h *HTTP) keepComposerFile(w http.ResponseWriter, r *http.Request) {
	if media := r.Header.Get("Content-Type"); media != "application/octet-stream" && media != "image/png" {
		apiError(w, http.StatusBadRequest, "Send the file as binary data.")
		return
	}
	dir := filepath.Join(h.Service.Store.Home.State, composerFilesDir)
	if task := r.URL.Query().Get("task"); task != "" {
		meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, task)
		if err != nil || meta.TaskTmp == "" || !exists(meta.TaskTmp) {
			apiError(w, http.StatusConflict, "The goblin that task names has ended, so the file was not kept.")
			return
		}
		dir = filepath.Join(meta.TaskTmp, composerFilesDir)
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReviewImage))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		apiError(w, http.StatusRequestEntityTooLarge, "A pasted file may be at most 10 MiB.")
		return
	}
	if err != nil || len(data) == 0 {
		apiError(w, http.StatusBadRequest, "The file could not be read.")
		return
	}
	extension := answerImageExtensions[http.DetectContentType(data)]
	if extension == "" {
		extension = ""
		if named := composerExtension.FindString(strings.ToLower(filepath.Ext(r.URL.Query().Get("name")))); named != "" {
			extension = named
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		apiError(w, http.StatusInternalServerError, "The file could not be kept: "+err.Error())
		return
	}
	f, err := os.CreateTemp(dir, "pasted-*"+extension)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The file could not be kept: "+err.Error())
		return
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		apiError(w, http.StatusInternalServerError, "The file could not be kept: "+err.Error())
		return
	}
	respond(w, http.StatusCreated, struct {
		Path string `json:"path"`
	}{f.Name()})
}
