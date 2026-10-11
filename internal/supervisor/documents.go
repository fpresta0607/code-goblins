package supervisor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// maxDocument caps one delivered file.
const maxDocument = 64 << 20

// maxShownText and maxShownPDF cap the text and the PDF a delivered file's
// page carries in itself to show; a larger one is only offered to download.
const (
	maxShownText = 1 << 20
	maxShownPDF  = 16 << 20
)

// documentPage is a delivered file's Scrawl page in its item's folder, and
// documentDir the folder beside it that holds the file under its own name.
const (
	documentPage = "page.html"
	documentDir  = "file"
)

// shownDocumentTypes are the sniffed types a delivered file's page shows, and
// how: a raster image inline, a PDF drawn page by page, plain text as text.
// Anything else, HTML and SVG included, is only ever offered to download.
var shownDocumentTypes = map[string]string{"image/png": "image", "image/jpeg": "image", "image/gif": "image", "image/webp": "image", "application/pdf": "pdf", "text/plain; charset=utf-8": "text"}

// ReviewDocument is a file delivered to the Overlord as a Scrawl page. The
// board sees its name and size, never its digest or where it came from.
type ReviewDocument struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Sum is the SHA-256 of the copy, so a republish with the same ID and
	// content changes nothing and other content is refused.
	Sum string `json:"sum,omitempty"`
	// Kind is the sniffed type when the page shows the file, else empty.
	Kind string `json:"kind,omitempty"`
	// Download says the page offers the file to download: a generated asset
	// or a file he asked to keep.
	Download bool `json:"download,omitempty"`
}

// validDocumentName refuses a name that is not one plain file name, which
// names the copy in its item's folder.
func validDocumentName(name string) error {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || filepath.Base(name) != name || strings.ContainsAny(name, "\x00\r\n\"\\/:*?<>|") {
		return errors.New("a delivered file needs a plain file name of at most 255 characters")
	}
	return nil
}

func validDocument(d ReviewDocument) error {
	if err := validDocumentName(d.Name); err != nil {
		return err
	}
	if d.Size <= 0 || d.Size > maxDocument {
		return fmt.Errorf("a delivered file must hold 1 byte to %d MiB", maxDocument>>20)
	}
	if _, err := hex.DecodeString(d.Sum); err != nil || len(d.Sum) != 64 {
		return errors.New("invalid document digest")
	}
	if d.Kind != "" && shownDocumentTypes[d.Kind] == "" {
		return errors.New("invalid document type")
	}
	if d.Kind == "" && !d.Download {
		return errors.New("its page cannot show " + d.Name + ", so it is offered only with --download, for a generated asset or a file the Overlord asked to keep")
	}
	return nil
}

// DeliverDocument hands the Overlord a file from the reporter's own process,
// a goblin's from its worktree, task scratch or data directory, the registered
// CFO's from anywhere it can read, as a Scrawl page that shows it. The file is
// copied beside the page under state/reviews before the item is recorded, so
// it outlives the original. open opens the page as a review session and
// returns its address, and the supervisor watches it, so what the Overlord
// writes there reaches whoever delivered it. With isDownload the page offers
// the file to download.
func DeliverDocument(h home.Home, taskID, id, title, file string, isDownload bool, open func(page string) (string, error)) error {
	r := Review{ID: id, Task: taskID, Title: title}
	return publishItem(h, r, func(r *Review, dir string) (bool, error) {
		document, err := stageDocument(h, taskID, file, dir, title, isDownload)
		if err != nil {
			return false, err
		}
		r.Document = &document
		return true, nil
	}, func(r *Review, dir string) error {
		page := filepath.Join(dir, documentPage)
		link, err := open(page)
		if err != nil {
			return err
		}
		r.Lavish, r.LavishPage = link, page
		return nil
	})
}

// stageDocument copies file into dir, beside the page it writes for it, and
// describes the copy.
func stageDocument(h home.Home, taskID, file, dir, title string, isDownload bool) (ReviewDocument, error) {
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
			return ReviewDocument{}, errors.New("a goblin's file must be inside its worktree, task scratch or data directory")
		}
		if err != nil {
			return ReviewDocument{}, err
		}
	} else if source, err = fsx.Open(abs); err != nil {
		return ReviewDocument{}, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return ReviewDocument{}, err
	}
	if !info.Mode().IsRegular() {
		return ReviewDocument{}, errors.New("a delivered file must be a regular file")
	}
	if info.Size() > maxDocument {
		return ReviewDocument{}, fmt.Errorf("a delivered file may be at most %d MiB", maxDocument>>20)
	}
	document := ReviewDocument{Name: filepath.Base(abs), Download: isDownload}
	if err := validDocumentName(document.Name); err != nil {
		return ReviewDocument{}, err
	}
	if err := os.MkdirAll(filepath.Join(dir, documentDir), 0o700); err != nil {
		return ReviewDocument{}, err
	}
	copied := filepath.Join(dir, documentDir, document.Name)
	out, err := os.OpenFile(copied, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
		return ReviewDocument{}, fmt.Errorf("a delivered file may be at most %d MiB", maxDocument>>20)
	}
	document.Size, document.Sum = size, hex.EncodeToString(hash.Sum(nil))
	if kind := http.DetectContentType(head.bytes); shownDocumentTypes[kind] == "image" || shownDocumentTypes[kind] == "text" && size <= maxShownText || shownDocumentTypes[kind] == "pdf" && size <= maxShownPDF {
		document.Kind = kind
	}
	if err := validDocument(document); err != nil {
		return ReviewDocument{}, err
	}
	if err := writeDocumentPage(filepath.Join(dir, documentPage), copied, title, document); err != nil {
		return ReviewDocument{}, err
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

// documentPageTemplate is a delivered file's Scrawl page, in the board's own
// look as the review server serves it: the title, the file itself, and
// Download when the page offers it. A picture shows inline, plain text as
// text, and a PDF page by page, drawn by pdf.js from its own bytes, since a
// review page runs sandboxed and no browser shows a PDF in a sandboxed page.
// html/template escapes every value for where it lands.
var documentPageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<link rel="stylesheet" href="/design/board-tokens.css">
<link rel="stylesheet" href="/design/board-page.css">
<style>
main { display: grid; gap: 20px; max-width: 1200px; margin: 0 auto; }
header { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px 20px; }
.delivered { display: grid; justify-items: center; gap: 16px; margin: 0; }
.delivered img, .delivered canvas { display: block; max-width: 100%; height: auto; border: 1px solid var(--glass-border); border-radius: var(--radius); background: var(--field-bg); }
.delivered pre { width: 100%; white-space: pre-wrap; }
.download { display: inline-flex; align-items: center; min-height: 44px; padding: 0 20px; border: 1px solid var(--accent-green); border-radius: 999px; background: var(--accent-green); color: #03130c; font-weight: 700; text-decoration: none; box-shadow: 0 0 20px #00e59b16; }
.download:hover { background: var(--accent-green-deep); border-color: var(--accent-green-deep); }
</style>
</head>
<body>
<main>
<header><h1>{{.Title}}</h1>{{if .Download}}<a class="download" href="{{.Href}}" download="{{.Name}}">Download</a>{{end}}</header>
{{if eq .Shown "image"}}<figure class="delivered"><img src="{{.Href}}" alt="{{.Title}}"></figure>
{{else if eq .Shown "text"}}<figure class="delivered"><pre>{{.Text}}</pre></figure>
{{else if eq .Shown "pdf"}}<figure class="delivered" id="pages" aria-label="{{.Title}}" data-pdf="{{.PDF}}"></figure>
<script type="module">
import * as pdfjs from "https://cdnjs.cloudflare.com/ajax/libs/pdf.js/4.10.38/pdf.min.mjs";
pdfjs.GlobalWorkerOptions.workerSrc = "https://cdnjs.cloudflare.com/ajax/libs/pdf.js/4.10.38/pdf.worker.min.mjs";
const pages = document.getElementById("pages");
const bytes = Uint8Array.from(atob(pages.dataset.pdf), (c) => c.charCodeAt(0));
const pdf = await pdfjs.getDocument({ data: bytes }).promise;
for (let n = 1; n <= pdf.numPages; n++) {
  const page = await pdf.getPage(n);
  const viewport = page.getViewport({ scale: 2 });
  const canvas = document.createElement("canvas");
  canvas.width = viewport.width;
  canvas.height = viewport.height;
  canvas.style.width = viewport.width / 2 + "px";
  pages.append(canvas);
  await page.render({ canvasContext: canvas.getContext("2d"), viewport }).promise;
}
</script>
{{else}}<p class="muted">{{.Name}}</p>
{{end}}</main>
</body>
</html>
`))

// writeDocumentPage writes the Scrawl page at path for the delivered copy at
// copied, which sits in the documentDir beside it.
func writeDocumentPage(path, copied, title string, document ReviewDocument) error {
	values := struct {
		Title, Name, Href, Shown, Text, PDF string
		Download                            bool
	}{Title: title, Name: document.Name, Href: documentDir + "/" + url.PathEscape(document.Name), Shown: shownDocumentTypes[document.Kind], Download: document.Download}
	if values.Shown == "text" || values.Shown == "pdf" {
		data, err := fsx.ReadFile(copied)
		if err != nil {
			return err
		}
		values.Text, values.PDF = string(data), base64.StdEncoding.EncodeToString(data)
		if values.Shown == "text" {
			values.PDF = ""
		} else {
			values.Text = ""
		}
	}
	var page strings.Builder
	if err := documentPageTemplate.Execute(&page, values); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, []byte(page.String()))
}
