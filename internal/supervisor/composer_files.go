package supervisor

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// composerFilesDir holds the pictures and files he pastes or adds to a
// message, where the session he writes to reads them by the path the message
// names.
const composerFilesDir = "composer-files"

// composerExtension accepts only an extension a file name may carry, so a
// name he chose never adds a path segment.
var composerExtension = regexp.MustCompile(`^\.[A-Za-z0-9]{1,10}$`)

// composerDir is where the files he adds to a message to task are kept: the
// task's folder for a goblin, or the state folder for the CFO, which task
// leaves empty. It is not ok for a task whose folder is gone.
func (h *HTTP) composerDir(task string) (string, bool) {
	if task == "" {
		return filepath.Join(h.Service.Store.Home.State, composerFilesDir), true
	}
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, task)
	if err != nil || meta.TaskTmp == "" || !exists(meta.TaskTmp) {
		return "", false
	}
	return filepath.Join(meta.TaskTmp, composerFilesDir), true
}

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
	dir, ok := h.composerDir(r.URL.Query().Get("task"))
	if !ok {
		apiError(w, http.StatusConflict, "The goblin that task names has ended, so the file was not kept.")
		return
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

// composerPicture reads back a picture keepComposerFile kept for a message to
// task, by the path the message names, for the box's thumbnail of it and the
// conversation's preview. It reads nothing outside that task's composer
// folder and serves only a picture. An <img> cannot send the board's token in
// a header, so it comes in the query, as the messages' own pictures' does.
func (h *HTTP) composerPicture(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(query.Get("token")), []byte(h.Service.Instance)) != 1 {
		apiError(w, http.StatusForbidden, "Refresh the board before reading its pictures")
		return
	}
	dir, ok := h.composerDir(query.Get("task"))
	if !ok {
		apiError(w, http.StatusConflict, "The goblin that task names has ended, so its pictures are gone.")
		return
	}
	path := query.Get("path")
	if !filepath.IsAbs(path) || filepath.Dir(filepath.Clean(path)) != dir {
		apiError(w, http.StatusForbidden, "Only a picture kept for a message is shown.")
		return
	}
	file, err := fsx.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		apiError(w, http.StatusNotFound, "That picture is no longer kept.")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The picture could not be read.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReviewImage+1))
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The picture could not be read.")
		return
	}
	media := http.DetectContentType(data)
	if answerImageExtensions[media] == "" || len(data) > maxReviewImage {
		apiError(w, http.StatusUnsupportedMediaType, "Only a picture is shown.")
		return
	}
	// A kept file is never written again, so a thumbnail drawn again, as a
	// row scrolls back into view, is read from the browser's cache.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", media)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
