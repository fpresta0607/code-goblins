package showcase

import (
	"bytes"
	"fmt"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
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
		// Class-based highlighting so code blocks follow the reviewer's
		// system appearance like the rest of the surface; inline styles
		// would pin one theme into the markup.
		highlighting.NewHighlighting(
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		),
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

// highlightCSS is the syntax palette for both appearances: Xcode's light
// theme and its dark counterpart, each scoped to one appearance. The two
// styles do not cover the same token set, so they have to be mutually
// exclusive - overlaying the dark one leaves every token it omits reading in
// the light palette, which is black text on a dark block.
var highlightCSS = "@media not all and (prefers-color-scheme: dark) {\n" + chromaCSS("xcode") + "}\n" +
	"@media (prefers-color-scheme: dark) {\n" + chromaCSS("xcode-dark") + "}\n"

func chromaCSS(name string) string {
	style := styles.Get(name)
	// styles.Get substitutes a fallback for an unknown name, which silently
	// ships the wrong palette; refuse to start on that instead.
	if style == nil || style.Name != name {
		panic(fmt.Sprintf("showcase: chroma style %q is not registered", name))
	}
	var out bytes.Buffer
	if err := chromahtml.New(chromahtml.WithClasses(true)).WriteCSS(&out, style); err != nil {
		panic(fmt.Sprintf("showcase: chroma css %q: %v", name, err))
	}
	return out.String()
}

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
