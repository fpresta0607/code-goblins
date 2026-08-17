package showcase

import (
	"bytes"
	"errors"
	htmltemplate "html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Export writes one portable, self-contained HTML file for an artifact: the
// rendered review page with every local asset inlined and the conversation
// frozen into the panel. Remote references (the Mermaid CDN) stay links.
func Export(artifact, out string) (string, error) {
	abs, err := filepath.Abs(artifact)
	if err != nil {
		return "", err
	}
	kind, err := DetectFile(abs)
	if err != nil {
		return "", err
	}
	content, toc, srcdoc, err := buildContent(kind, abs, true)
	if err != nil {
		return "", err
	}
	session, err := Load(abs)
	if errors.Is(err, fs.ErrNotExist) {
		session = nil
	} else if err != nil {
		return "", err
	}

	if out == "" {
		base := filepath.Base(abs)
		base = strings.TrimSuffix(base, filepath.Ext(base)) + "-export.html"
		out = filepath.Join(filepath.Dir(abs), base)
	}
	var page bytes.Buffer
	err = renderPage(&page, pageData{
		Title:        filepath.Base(abs),
		Kind:         kind,
		TOC:          toc,
		Content:      htmltemplate.HTML(content),
		Srcdoc:       srcdoc,
		Device:       kind == KindHTML,
		Mermaid:      kind == KindMermaid,
		Static:       true,
		Conversation: htmltemplate.HTML(renderConversation(session)),
	})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(out, page.Bytes(), 0o644); err != nil {
		return "", err
	}
	return out, nil
}
