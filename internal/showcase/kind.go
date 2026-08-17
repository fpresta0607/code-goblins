// Package showcase implements the review surface behind the showcase-axi
// command: artifact kind detection, type-aware rendering, session state, and
// the localhost server that lets a user annotate an artifact and queue
// feedback for the agent.
package showcase

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

// Kind identifies how an artifact is rendered.
type Kind string

const (
	KindMarkdown Kind = "markdown"
	KindHTML     Kind = "html"
	KindDiff     Kind = "diff"
	KindCSV      Kind = "csv"
	KindMermaid  Kind = "mermaid"
)

var kindByExt = map[string]Kind{
	".md":       KindMarkdown,
	".markdown": KindMarkdown,
	".html":     KindHTML,
	".htm":      KindHTML,
	".diff":     KindDiff,
	".patch":    KindDiff,
	".csv":      KindCSV,
	".mmd":      KindMermaid,
	".mermaid":  KindMermaid,
}

// mermaidStarters are the diagram keywords that open a Mermaid source file.
var mermaidStarters = []string{
	"graph ", "graph\n", "flowchart ", "sequenceDiagram", "classDiagram",
	"stateDiagram", "erDiagram", "gantt", "pie", "journey", "gitGraph",
	"mindmap", "timeline",
}

// Detect classifies an artifact by extension, falling back to a content
// sniff when the extension is unknown or absent. Unknown plain text is
// treated as markdown, the default agent-artifact format.
func Detect(path string, content []byte) (Kind, error) {
	if kind, ok := kindByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return kind, nil
	}
	return sniff(content), nil
}

func sniff(content []byte) Kind {
	head := strings.TrimSpace(string(bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))))
	if len(head) > 4096 {
		head = head[:4096]
	}
	lower := strings.ToLower(head)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return KindHTML
	}
	if strings.HasPrefix(head, "diff --git ") ||
		(strings.HasPrefix(head, "--- ") && strings.Contains(head, "\n+++ ")) {
		return KindDiff
	}
	for _, starter := range mermaidStarters {
		if strings.HasPrefix(head, starter) {
			return KindMermaid
		}
	}
	if strings.HasPrefix(head, "<") {
		return KindHTML
	}
	return KindMarkdown
}

// DetectFile reads the head of path and classifies it.
func DetectFile(path string) (Kind, error) {
	content, err := readHead(path, 8192)
	if err != nil {
		return "", fmt.Errorf("showcase: %w", err)
	}
	return Detect(path, content)
}
