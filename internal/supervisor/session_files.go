package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fileListingLife is how long the board keeps a session folder's file list,
// so a burst of keystrokes in the composer's "@" list runs git once.
const fileListingLife = 10 * time.Second

// maxFileMatches bounds the files one "@" answer names.
const maxFileMatches = 50

// fileListing is one session folder's files, as git listed them at a time.
type fileListing struct {
	paths []string
	at    time.Time
}

// sessionFiles answers the files of one session's own folder whose names
// match what the composer's "@" list was typed with: the files git tracks and
// the untracked ones it does not ignore, never a folder the browser names.
func (h *HTTP) sessionFiles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	session, ok := h.boardSession(w, query, "files")
	if !ok {
		return
	}
	paths, err := h.folderFiles(r.Context(), session.folder)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The session's files cannot be read")
		return
	}
	respond(w, http.StatusOK, struct {
		Files []string `json:"files"`
	}{rankFiles(paths, query.Get("q"))})
}

// folderFiles answers the files git lists in folder, sorted by path, from the
// listing the board keeps when it is fresh. One listing runs at a time, so
// requests that arrive while it runs read its answer rather than run git
// again, and a listing finishes though the request that started it went, as
// the composer drops one at each keystroke. A folder outside any git
// repository holds no files to list.
func (h *HTTP) folderFiles(ctx context.Context, folder string) ([]string, error) {
	h.fileListingsMu.Lock()
	defer h.fileListingsMu.Unlock()
	if listing, ok := h.fileListings[folder]; ok && time.Since(listing.at) < fileListingLife {
		return listing.paths, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	cmd := execx.CommandContext(ctx, "git", "-C", folder, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_OPTIONAL_LOCKS=0")
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	var paths []string
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		paths = strings.Split(strings.TrimSuffix(out.String(), "\x00"), "\x00")
		paths = slices.DeleteFunc(paths, func(file string) bool { return file == "" })
	case errors.As(err, &exit) && ctx.Err() == nil && exit.ExitCode() == 128 && strings.Contains(strings.ToLower(diagnostic.String()), "fatal: not a git repository"):
		paths = []string{}
	default:
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	for kept, listing := range h.fileListings {
		if time.Since(listing.at) >= fileListingLife {
			delete(h.fileListings, kept)
		}
	}
	h.fileListings[folder] = fileListing{paths: paths, at: time.Now()}
	return paths, nil
}

// rankFiles answers at most maxFileMatches of paths that match query,
// ignoring case: those whose base name starts with it, then those whose base
// name holds it, then those whose path holds it, each in path order.
func rankFiles(paths []string, query string) []string {
	query = strings.ToLower(query)
	var starts, holds, inPath []string
	for _, file := range paths {
		lower := strings.ToLower(file)
		base := path.Base(lower)
		switch {
		case strings.HasPrefix(base, query):
			starts = append(starts, file)
		case strings.Contains(base, query):
			holds = append(holds, file)
		case strings.Contains(lower, query):
			inPath = append(inPath, file)
		}
	}
	ranked := append(append(append([]string{}, starts...), holds...), inPath...)
	return ranked[:min(len(ranked), maxFileMatches)]
}
