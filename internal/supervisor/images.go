package supervisor

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// maxReviewImage caps one image a goblin attaches to a question.
const maxReviewImage = 10 << 20

// reviewImageTypes are the formats the board serves: raster images a browser
// renders without running anything. SVG is left out because it can carry
// script.
var reviewImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// reviewImageRoots are the only places a goblin's review image may live: its
// own worktree, its task scratch directory and its data directory.
func reviewImageRoots(h home.Home, meta state.TaskMeta) []string {
	roots := []string{meta.Worktree, filepath.Join(h.Data, meta.ID)}
	if meta.TaskTmp != "" {
		roots = append(roots, meta.TaskTmp)
	}
	return roots
}

// ReviewImages checks each image a goblin attaches before anything is
// published, and returns them as absolute paths.
func ReviewImages(h home.Home, taskID string, images []string) ([]string, error) {
	meta, err := state.ReadTaskMeta(h.State, taskID)
	if err != nil {
		return nil, fmt.Errorf("task %s has no live record: %w", taskID, err)
	}
	checked := make([]string, 0, len(images))
	for _, image := range images {
		abs, err := fsx.AbsClean(image)
		if err != nil {
			return nil, err
		}
		f, _, err := openReviewImage(reviewImageRoots(h, meta), abs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", image, err)
		}
		f.Close()
		checked = append(checked, abs)
	}
	return checked, nil
}

// openReviewImage opens path only when it is a regular image file inside one
// of roots, reached through plain directories, and within the size cap. It
// returns the file at its start with its sniffed type. The path is never
// resolved through links: it must already be spelled under a root, either as
// the task records it or in its canonical form.
func openReviewImage(roots []string, path string) (*os.File, string, error) {
	abs, err := fsx.AbsClean(path)
	if err != nil {
		return nil, "", err
	}
	for _, dir := range roots {
		canonical, err := fsx.Canonical(dir)
		if err != nil {
			continue
		}
		rel := ""
		for _, spelling := range []string{filepath.Clean(dir), canonical} {
			r, err := filepath.Rel(spelling, abs)
			if err == nil && r != "." && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r) {
				rel = r
				break
			}
		}
		if rel == "" {
			continue
		}
		root, err := os.OpenRoot(canonical)
		if err != nil {
			return nil, "", err
		}
		f, err := openRegular(root, rel)
		root.Close()
		if err != nil {
			return nil, "", err
		}
		kind, err := sniffReviewImage(f)
		if err != nil {
			f.Close()
			return nil, "", err
		}
		return f, kind, nil
	}
	return nil, "", errors.New("an image must be inside the task's worktree, task scratch or data directory")
}

func sniffReviewImage(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > maxReviewImage {
		return "", fmt.Errorf("an image may be at most %d MiB", maxReviewImage>>20)
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	kind := http.DetectContentType(head[:n])
	if !reviewImageTypes[kind] {
		return "", errors.New("only PNG, JPEG, GIF and WebP images can be reviewed")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return kind, nil
}

// questionImage serves image n of a goblin's question at
// /api/questions/<id>/images/<n>: only a path the question names, only while
// the asking task is the same generation in the same pane, and only after the
// file passes the same checks again.
func (h *HTTP) questionImage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/questions/"), "/")
	n, err := -1, error(nil)
	if len(parts) == 3 && parts[1] == "images" {
		n, err = strconv.Atoi(parts[2])
	}
	questions := h.Service.Store.Snapshot().Questions
	i := slices.IndexFunc(questions, func(q Question) bool { return len(parts) == 3 && q.ID == parts[0] })
	if err != nil || i < 0 || n < 0 || n >= len(questions[i].Images) {
		apiError(w, 404, "Unknown question image")
		return
	}
	q := questions[i]
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, q.Task)
	if err != nil || goblinIdentity(meta) != q.Identity {
		apiError(w, 410, "The goblin's task restarted or ended, so its images are no longer served")
		return
	}
	f, kind, err := openReviewImage(reviewImageRoots(h.Service.Store.Home, meta), q.Images[n])
	if err != nil {
		apiError(w, 403, "The image is no longer a review image inside the goblin's task")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", "inline")
	_, _ = io.Copy(w, io.LimitReader(f, maxReviewImage))
}
