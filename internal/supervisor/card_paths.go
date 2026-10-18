package supervisor

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A card's text makes a link of each local file or folder it names (the
// Overlord, 2026-10-17: "if there is a folder, there should be a link to the
// open the file explorer folder on my PC"). These are the board's rules for
// finding one, which frontend/src/messageText.ts follows too: a drive letter,
// a colon and a separator, then everything up to a space or a character no
// Windows path holds, without the punctuation that ends a sentence (a closing
// bracket only when the path opened none it closes); or a whole code value
// that is such a path, spaces and all.
var (
	textPath = regexp.MustCompile(`(?:^|[^\w\\/])([A-Za-z]:[\\/][^\s<>"|?*:` + "`" + `]*)`)
	codeText = regexp.MustCompile("`([^`\n]+)`")
	codePath = regexp.MustCompile(`^[A-Za-z]:[\\/][^<>"|?*:` + "`" + `\n]*$`)
)

// namedPaths are the local paths text names, as its card's links name them.
func namedPaths(text string) []string {
	var paths []string
	for _, match := range textPath.FindAllStringSubmatch(text, -1) {
		paths = append(paths, trimPathEnd(match[1]))
	}
	for _, match := range codeText.FindAllStringSubmatch(text, -1) {
		if codePath.MatchString(match[1]) {
			paths = append(paths, match[1])
		}
	}
	return paths
}

// trimPathEnd drops the punctuation that ends a sentence from a path found in
// text, keeping a closing parenthesis the path itself opened, as in
// C:pp(v2).
func trimPathEnd(path string) string {
	for path != "" {
		last := path[len(path)-1]
		if !strings.ContainsRune(".,;!?)]}'", rune(last)) || last == ')' && strings.Count(path, "(") >= strings.Count(path, ")") {
			return path
		}
		path = path[:len(path)-1]
	}
	return path
}

// cardPaths are the paths a card on the board names where its text makes
// links of them, found by the card's kind and ID: a credential or step
// card's reason and steps, a question's text, a review item's or wait's
// words, and a run card's title and the folder it runs in.
func cardPaths(d Database, kind, id string) ([]string, bool) {
	switch kind {
	case "credential":
		if i := slices.IndexFunc(d.Credentials, func(r CredentialRequest) bool { return r.ID == id }); i >= 0 {
			return namedPaths(strings.Join(append([]string{d.Credentials[i].Why}, d.Credentials[i].Steps...), "\n")), true
		}
	case "question":
		if i := slices.IndexFunc(d.Questions, func(q Question) bool { return q.ID == id }); i >= 0 {
			return namedPaths(d.Questions[i].Text), true
		}
	case "review":
		if i := slices.IndexFunc(d.Reviews, func(r Review) bool { return r.ID == id }); i >= 0 {
			return namedPaths(d.Reviews[i].Title), true
		}
	case "run":
		if i := slices.IndexFunc(d.Runs, func(r Run) bool { return r.ID == id }); i >= 0 {
			return append(namedPaths(d.Runs[i].Title), d.Runs[i].Cwd), true
		}
	}
	return nil, false
}

// openCardPath opens File Explorer at a file or folder a card names, with the
// file selected, for the link the card's text makes of it. The browser names
// the card and the path, and the path opens only when that card's own text
// names it, letter case aside as Windows reads a path, and it exists on a
// drive letter of this PC: never a URL, a UNC or device path or a command,
// and a file is only ever selected, never opened. A drive letter mapped to a
// share is a drive letter like any other.
func (h *HTTP) openCardPath(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	paths, found := cardPaths(h.Service.Store.Snapshot(), input.Kind, input.ID)
	switch {
	case !found:
		apiError(w, http.StatusNotFound, "That card is no longer on the board.")
		return
	case !slices.ContainsFunc(paths, func(path string) bool { return strings.EqualFold(path, input.Path) }):
		apiError(w, http.StatusBadRequest, "That card names no such path.")
		return
	case len(filepath.VolumeName(input.Path)) != 2 || !filepath.IsAbs(input.Path) || strings.ContainsFunc(input.Path, unicode.IsControl):
		apiError(w, http.StatusBadRequest, "Only a path on a drive of this PC opens in File Explorer.")
		return
	// Explorer reads a comma as the end of an argument.
	case strings.ContainsAny(input.Path, `,"`):
		apiError(w, http.StatusBadRequest, "A path holding a comma or a quote cannot open in File Explorer.")
		return
	}
	target := filepath.Clean(input.Path)
	info, err := os.Stat(target)
	if err != nil {
		apiError(w, http.StatusNotFound, "Nothing is at that path now.")
		return
	}
	explorer := filepath.Join(os.Getenv("SystemRoot"), "explorer.exe")
	if program, err := os.Stat(explorer); !filepath.IsAbs(explorer) || err != nil || !program.Mode().IsRegular() {
		apiError(w, http.StatusServiceUnavailable, "Windows file manager is unavailable.")
		return
	}
	args := []string{target}
	if !info.IsDir() {
		args = []string{"/select,", target}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := h.editor.Start(ctx, execx.Request{Name: explorer, Args: args}); err != nil {
		apiError(w, http.StatusServiceUnavailable, "File Explorer could not be started.")
		return
	}
	respond(w, http.StatusOK, struct {
		Message string `json:"message"`
	}{"Opened File Explorer at " + target + "."})
}
