package showcase

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// TOCItem is one heading in the markdown table of contents.
type TOCItem struct {
	Level int    `json:"level"`
	ID    string `json:"id"`
	Text  string `json:"text"`
}

var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.Table,
		extension.Strikethrough,
		extension.Linkify,
		extension.TaskList,
		highlighting.NewHighlighting(highlighting.WithStyle("github-dark")),
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

// RenderMarkdown renders source to HTML body markup plus the table of
// contents collected from its headings.
func RenderMarkdown(source []byte) (string, []TOCItem, error) {
	toc, err := markdownTOC(source)
	if err != nil {
		return "", nil, err
	}
	var body bytes.Buffer
	if err := markdown.Convert(source, &body); err != nil {
		return "", nil, fmt.Errorf("showcase: markdown render: %w", err)
	}
	return body.String(), toc, nil
}

func markdownTOC(source []byte) ([]TOCItem, error) {
	doc := markdown.Parser().Parse(text.NewReader(source))
	var toc []TOCItem
	walkErr := error(nil)
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || walkErr != nil {
			return ast.WalkContinue, walkErr
		}
		heading, ok := node.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		id := ""
		if value, ok := heading.AttributeString("id"); ok {
			switch typed := value.(type) {
			case string:
				id = typed
			case []byte:
				id = string(typed)
			}
		}
		toc = append(toc, TOCItem{
			Level: heading.Level,
			ID:    id,
			Text:  strings.TrimSpace(string(heading.Text(source))),
		})
		return ast.WalkContinue, nil
	})
	return toc, walkErr
}
