package supervisor

import (
	"crypto/subtle"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// commands answers the slash commands one session's harness publishes for
// itself: its own command files and skills, read from the worktree and the
// user's home, never a guessed list. A harness that publishes none answers
// none, which the composer shows as no list rather than inventing one.
func (h *HTTP) commands(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(query.Get("token")), []byte(h.Service.Instance)) != 1 {
		apiError(w, http.StatusForbidden, "Refresh the board before reading its commands")
		return
	}
	names := []string{}
	if task := query.Get("task"); task != "" {
		meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, task)
		if err != nil {
			apiError(w, http.StatusNotFound, "Task metadata unavailable")
			return
		}
		if meta.Harness == "claude" {
			names = append(names, claudeCommands(meta.Worktree)...)
		}
	}
	if h.Service.Options.Tree != nil {
		names = append(names, claudeCommands(filepath.Join(h.Service.Options.Tree.Home, ".claude"))...)
	}
	respond(w, http.StatusOK, struct {
		Commands []string `json:"commands"`
	}{uniqueSorted(names)})
}

// claudeCommands reads the command and skill names Claude Code publishes under
// one folder: commands/*.md and skills/*/SKILL.md.
func claudeCommands(root string) []string {
	names := []string{}
	for _, folder := range []struct{ path, suffix string }{
		{filepath.Join(root, "commands"), ".md"},
		{filepath.Join(root, "skills"), ""},
	} {
		entries, err := os.ReadDir(folder.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				if folder.suffix == "" {
					if _, err := os.Stat(filepath.Join(folder.path, entry.Name(), "SKILL.md")); err == nil {
						names = append(names, entry.Name())
					}
				}
				continue
			}
			if folder.suffix != "" && strings.EqualFold(filepath.Ext(entry.Name()), folder.suffix) {
				names = append(names, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
			}
		}
	}
	return names
}

// uniqueSorted sorts commands and keeps the first of each name.
func uniqueSorted(names []string) []string {
	sort.Slice(names, func(first, second int) bool { return strings.ToLower(names[first]) < strings.ToLower(names[second]) })
	unique := names[:0]
	for _, name := range names {
		if name != "" && (len(unique) == 0 || !strings.EqualFold(unique[len(unique)-1], name)) {
			unique = append(unique, name)
		}
	}
	return unique
}
