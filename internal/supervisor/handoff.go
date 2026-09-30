package supervisor

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const maxHandoff = 1 << 20

func archivedTasks(h home.Home) func() ([]os.DirEntry, error) {
	return sync.OnceValues(func() ([]os.DirEntry, error) {
		return os.ReadDir(filepath.Join(h.Data, "archive", "finished"))
	})
}

func openTaskHandoff(h home.Home, id string, archived func() ([]os.DirEntry, error)) (*os.File, error) {
	type note struct{ root, path string }
	var paths []note
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if record, readErr := state.ReadLifecycle(h.State, id); readErr == nil && record.Generation == meta.SpawnGen && record.HandoffSaved {
			name := filepath.Base(record.Handoff)
			operation := strings.TrimSuffix(strings.TrimPrefix(name, "pause-"), ".md")
			folder := filepath.Join(h.State, "tasktmp", id)
			if strings.HasPrefix(name, "pause-") && strings.HasSuffix(name, ".md") && state.ValidTaskID(operation) == nil && strings.EqualFold(filepath.Dir(record.Handoff), folder) {
				paths = append(paths, note{h.State, filepath.Join("tasktmp", id, name)})
			}
		}
	}
	paths = append(paths, note{h.Data, filepath.Join(id, "handoff.md")})
	if errors.Is(err, os.ErrNotExist) {
		archive := filepath.Join("archive", "finished")
		entries, err := archived()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for i := len(entries) - 1; i >= 0; i-- {
			name := entries[i].Name()
			match := archivedTaskDir.FindStringSubmatch(name)
			if name == id || match != nil && match[1] == id {
				paths = append(paths, note{h.Data, filepath.Join(archive, name, "handoff.md")})
			}
		}
	}
	for _, path := range paths {
		root, err := os.OpenRoot(path.root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		file, err := openRegular(root, path.path)
		root.Close()
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

func (h *HTTP) taskHandoff(w http.ResponseWriter, id string) {
	file, err := openTaskHandoff(h.Service.Store.Home, id, archivedTasks(h.Service.Store.Home))
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
