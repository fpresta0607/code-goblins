package supervisor

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/home"
)

const maxHandoff = 1 << 20

func openTaskHandoff(h home.Home, id string) (*os.File, error) {
	root, err := os.OpenRoot(h.Data)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	paths := []string{filepath.Join(id, "handoff.md")}
	_, err = os.Stat(filepath.Join(h.State, id+".meta"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if errors.Is(err, os.ErrNotExist) {
		archive := filepath.Join("archive", "finished")
		entries, err := os.ReadDir(filepath.Join(h.Data, archive))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for i := len(entries) - 1; i >= 0; i-- {
			name := entries[i].Name()
			match := archivedTaskDir.FindStringSubmatch(name)
			if name == id || match != nil && match[1] == id {
				paths = append(paths, filepath.Join(archive, name, "handoff.md"))
			}
		}
	}
	for _, path := range paths {
		file, err := openRegular(root, path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil || info.Size() > maxHandoff {
			file.Close()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("handoff exceeds %d MiB", maxHandoff>>20)
		}
		return file, nil
	}
	return nil, os.ErrNotExist
}

func (h *HTTP) taskHandoff(w http.ResponseWriter, r *http.Request, id string) {
	file, err := openTaskHandoff(h.Service.Store.Home, id)
	if errors.Is(err, os.ErrNotExist) {
		apiError(w, 404, "No saved handoff is available for this task")
		return
	}
	if err != nil {
		apiError(w, 422, "The saved handoff cannot be opened safely")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxHandoff+1))
	if err != nil || len(data) > maxHandoff {
		apiError(w, 422, "The saved handoff could not be read within its size limit")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, redact(string(data)))
}
