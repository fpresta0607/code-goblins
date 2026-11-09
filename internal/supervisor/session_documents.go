package supervisor

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const UNAVAILABLE_DOCUMENT = "That PDF is unavailable for this session."

// Document reads use a header so opening the resulting browser-local PDF
// never puts the board token in a link or a new tab's address.
func (h *HTTP) messageDocument(w http.ResponseWriter, r *http.Request) {
	if h.Service.Instance == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CEO-Token")), []byte(h.Service.Instance)) != 1 {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	query := r.URL.Query()
	target := query.Get("document")
	filePath, err := url.PathUnescape(target)
	if err != nil || !isDocumentPath(filePath) || query.Get("ceo") != "" || h.Service.Options.Tree == nil {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	locate, err := h.messagesLocator(query)
	if err != nil {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	transcript, err := locate()
	if err != nil || transcript == "" || !h.hasDocumentReference(r, transcript, target) {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, query.Get("task"))
	if err != nil || meta.SpawnGen != query.Get("generation") {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	file, err := openInside(reviewImageRoots(h.Service.Store.Home, meta), filePath)
	if err != nil {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maxDocument {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	head := make([]byte, 512)
	n, err := file.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) || http.DetectContentType(head[:n]) != "application/pdf" {
		apiError(w, http.StatusForbidden, UNAVAILABLE_DOCUMENT)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(filePath)}))
	http.ServeContent(w, r, info.Name(), info.ModTime(), io.NewSectionReader(file, 0, info.Size()))
}

func isDocumentPath(path string) bool {
	if len(path) > 4096 || !filepath.IsAbs(path) || len(path) > 1 && strings.Trim(path[:2], `\/`) == "" || !strings.EqualFold(filepath.Ext(path), ".pdf") || strings.ContainsAny(path, "\x00\r\n<>\"|?*") {
		return false
	}
	if runtime.GOOS == "windows" {
		volume := strings.ToLower(filepath.VolumeName(path))
		if len(volume) != 2 || volume[0] < 'a' || volume[0] > 'z' {
			return false
		}
	}
	local := strings.ReplaceAll(path, `\`, "/")
	if len(local) > 2 && local[1] == ':' {
		local = local[2:]
	}
	if strings.Contains(local, ":") {
		return false
	}
	for _, part := range strings.Split(local, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// A guessed path beneath a permitted folder is insufficient: the selected
// conversation must actually name it.
func (h *HTTP) hasDocumentReference(r *http.Request, transcript, target string) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	before := -1
	for ctx.Err() == nil {
		page, _, err := h.Service.Options.Tree.MessagePageContext(ctx, transcript, before)
		if err != nil || ctx.Err() != nil {
			return false
		}
		for _, message := range page.Messages {
			if strings.Contains(message.Text, "]("+target+")") {
				return true
			}
			for _, block := range message.Blocks {
				if block.Kind == fleettree.BlockText && strings.Contains(block.Text, "]("+target+")") {
					return true
				}
			}
		}
		if !page.More || len(page.Messages) == 0 || before >= 0 && page.Messages[0].Seq >= before {
			return false
		}
		before = page.Messages[0].Seq
	}
	return false
}
