package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// maxDocument caps one delivered document.
const maxDocument = 64 << 20

// documentFile is a document's copy inside its item's folder.
const documentFile = "document"

// inlineDocumentTypes are the only formats the board opens in the browser: a
// PDF and raster images, which run nothing on the board's origin. Every other
// document, HTML and SVG included, is only ever downloaded.
var inlineDocumentTypes = map[string]bool{"application/pdf": true, "image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// ReviewDocument is a file delivered to the Overlord as a Command Center item.
// The board sees its name, size and whether it opens in the browser, never
// its digest or where it came from.
type ReviewDocument struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Sum is the SHA-256 of the copy, so a republish with the same ID and
	// content changes nothing and other content is refused.
	Sum string `json:"sum,omitempty"`
	// Kind is the sniffed type when the browser may open it, else empty.
	Kind string `json:"kind,omitempty"`
	// Link is an optional page for Open, such as a hosted copy.
	Link string `json:"link,omitempty"`
}

func validDocument(d ReviewDocument) error {
	if d.Name == "" || len(d.Name) > 255 || !utf8.ValidString(d.Name) || filepath.Base(d.Name) != d.Name || strings.ContainsAny(d.Name, "\x00\r\n\"\\/:*?<>|") {
		return errors.New("a document needs a plain file name of at most 255 characters")
	}
	if d.Size <= 0 || d.Size > maxDocument {
		return fmt.Errorf("a document must hold 1 byte to %d MiB", maxDocument>>20)
	}
	if _, err := hex.DecodeString(d.Sum); err != nil || len(d.Sum) != 64 {
		return errors.New("invalid document digest")
	}
	if d.Kind != "" && !inlineDocumentTypes[d.Kind] {
		return errors.New("invalid document type")
	}
	if d.Link != "" {
		if problem := presentationURLProblem(d.Link); problem != "" {
			return errors.New("a document's link " + problem)
		}
	}
	return nil
}

// DeliverDocument reports a document for the Overlord from the reporter's own
// process: a goblin's from its worktree, task scratch or data directory, the
// registered CFO's from anywhere it can read. The file is copied under
// state/reviews before the item is recorded, so it outlives the original.
func DeliverDocument(ctx context.Context, h home.Home, terminals terminal.Opener, taskID, id, title, file, link string) error {
	return publishItem(ctx, h, terminals, Review{ID: id, Task: taskID, Title: title}, func(r *Review, dir string) (bool, error) {
		document, err := stageDocument(h, taskID, file, dir)
		if err != nil {
			return false, err
		}
		document.Link = link
		r.Document = &document
		return true, nil
	})
}

// stageDocument copies file into dir and describes the copy.
func stageDocument(h home.Home, taskID, file, dir string) (ReviewDocument, error) {
	abs, err := fsx.AbsClean(file)
	if err != nil {
		return ReviewDocument{}, err
	}
	var source *os.File
	if taskID != "" {
		meta, err := state.ReadTaskMeta(h.State, taskID)
		if err != nil {
			return ReviewDocument{}, fmt.Errorf("task %s has no live record: %w", taskID, err)
		}
		source, err = openInside(reviewImageRoots(h, meta), abs)
		if errors.Is(err, errOutsideRoots) {
			return ReviewDocument{}, errors.New("a goblin's document must be inside its worktree, task scratch or data directory")
		}
		if err != nil {
			return ReviewDocument{}, err
		}
	} else if source, err = os.Open(abs); err != nil {
		return ReviewDocument{}, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return ReviewDocument{}, err
	}
	if !info.Mode().IsRegular() {
		return ReviewDocument{}, errors.New("a document must be a regular file")
	}
	if info.Size() > maxDocument {
		return ReviewDocument{}, fmt.Errorf("a document may be at most %d MiB", maxDocument>>20)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ReviewDocument{}, err
	}
	out, err := os.OpenFile(filepath.Join(dir, documentFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ReviewDocument{}, err
	}
	hash := sha256.New()
	head := &headBuffer{}
	size, copyErr := io.Copy(io.MultiWriter(out, hash, head), io.LimitReader(source, maxDocument+1))
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return ReviewDocument{}, err
	}
	if size > maxDocument {
		return ReviewDocument{}, fmt.Errorf("a document may be at most %d MiB", maxDocument>>20)
	}
	document := ReviewDocument{Name: filepath.Base(abs), Size: size, Sum: hex.EncodeToString(hash.Sum(nil))}
	if kind := http.DetectContentType(head.bytes); inlineDocumentTypes[kind] {
		document.Kind = kind
	}
	return document, nil
}

// headBuffer keeps the first 512 bytes written to it, for sniffing a type.
type headBuffer struct{ bytes []byte }

func (b *headBuffer) Write(p []byte) (int, error) {
	if room := 512 - len(b.bytes); room > 0 {
		b.bytes = append(b.bytes, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// reviewDocument serves an item's document at /api/reviews/<id>/document:
// in the browser only when it is a PDF or a raster image and the Overlord did
// not ask to download it, otherwise as a download, never sniffed.
func (h *HTTP) reviewDocument(w http.ResponseWriter, r *http.Request, id string) {
	var item *Review
	for _, candidate := range h.Service.Store.Snapshot().Reviews {
		if candidate.ID == id && candidate.Document != nil {
			item = &candidate
			break
		}
	}
	if item == nil {
		apiError(w, 404, "Unknown document")
		return
	}
	f, err := os.Open(filepath.Join(reviewImageDir(h.Service.Store.Home.State, *item), documentFile))
	if err != nil {
		apiError(w, 403, "The document is no longer available")
		return
	}
	defer f.Close()
	disposition, kind := "attachment", "application/octet-stream"
	if item.Document.Kind != "" && r.URL.Query().Get("download") == "" {
		disposition, kind = "inline", item.Document.Kind
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Document.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(f, maxDocument))
}
