package supervisor

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// answerImagesDir holds the pictures he pastes into his answer to a card.
const answerImagesDir = "answer-images"

// answerImageExtensions names a kept picture by its sniffed type.
var answerImageExtensions = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// keepAnswerImage keeps one picture he pasted into his answer to a card, where
// the asker can read it: the asking goblin's task folder, or the state folder
// for the CFO, which asks with no task. It answers with the path his answer
// names, so the asker reads the picture from there.
func (h *HTTP) keepAnswerImage(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(h.Service.Store.Home.State, answerImagesDir)
	if task := r.URL.Query().Get("task"); task != "" {
		meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, task)
		if err != nil || meta.TaskTmp == "" || !exists(meta.TaskTmp) {
			apiError(w, http.StatusConflict, "The goblin that asked has ended, so the picture was not kept.")
			return
		}
		dir = filepath.Join(meta.TaskTmp, answerImagesDir)
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReviewImage))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		apiError(w, http.StatusRequestEntityTooLarge, "A pasted picture may be at most 10 MiB.")
		return
	}
	if err != nil {
		apiError(w, http.StatusBadRequest, "The picture could not be read.")
		return
	}
	extension := answerImageExtensions[http.DetectContentType(data)]
	if extension == "" {
		apiError(w, http.StatusBadRequest, "Only a PNG, JPEG, GIF or WebP picture can be pasted.")
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		apiError(w, http.StatusInternalServerError, "The picture could not be kept: "+err.Error())
		return
	}
	f, err := os.CreateTemp(dir, "pasted-*"+extension)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The picture could not be kept: "+err.Error())
		return
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		apiError(w, http.StatusInternalServerError, "The picture could not be kept: "+err.Error())
		return
	}
	respond(w, http.StatusCreated, struct {
		Path string `json:"path"`
	}{f.Name()})
}
