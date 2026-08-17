package showcase

import (
	"strings"
	"testing"
)

func TestRenderMarkdownProducesTOCHeadingsCodeAndTables(t *testing.T) {
	source := []byte(`# Plan

Intro paragraph.

## First phase

Do things.

### Detail

More.

## Second phase

` + "```go\nfunc main() {}\n```" + `

| col | value |
| --- | ----- |
| a   | 1     |
`)
	body, toc, err := RenderMarkdown(source)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	if len(toc) != 4 {
		t.Fatalf("toc = %+v, want 4 entries", toc)
	}
	if toc[0].Level != 1 || toc[0].Text != "Plan" || toc[0].ID == "" {
		t.Errorf("toc[0] = %+v, want level 1 heading with an id", toc[0])
	}
	if toc[1].Level != 2 || toc[2].Level != 3 {
		t.Errorf("toc levels = %d, %d, want 2 then 3", toc[1].Level, toc[2].Level)
	}
	if !strings.Contains(body, `id="`+toc[0].ID+`"`) {
		t.Errorf("body lacks the heading anchor for TOC id %q", toc[0].ID)
	}
	if !strings.Contains(body, "<table>") || !strings.Contains(body, "<th>col</th>") {
		t.Errorf("body lacks a rendered table: %s", body)
	}
	// Chroma highlights the fenced Go block with styled spans.
	if !strings.Contains(body, "chroma") && !strings.Contains(body, "<span style=") {
		t.Errorf("fenced code block is not syntax highlighted: %s", body)
	}
}

func TestRenderMarkdownEscapesRawHTML(t *testing.T) {
	body, _, err := RenderMarkdown([]byte("hello <script>alert(1)</script>"))
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	if strings.Contains(body, "<script>") {
		t.Errorf("raw HTML reached the page unescaped: %s", body)
	}
}
