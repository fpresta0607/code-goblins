package showcase

import (
	"embed"
	"fmt"
	"html"
	htmltemplate "html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed assets/page.html assets/style.css assets/app.js
var assets embed.FS

var (
	pageTemplate = htmltemplate.Must(htmltemplate.New("page").Parse(mustAsset("assets/page.html")))
	pageCSS      = mustAsset("assets/style.css")
	pageJS       = mustAsset("assets/app.js")
)

func mustAsset(name string) string {
	data, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

type pageData struct {
	Title        string
	Kind         Kind
	CSS          htmltemplate.CSS
	JS           htmltemplate.JS
	TOC          []TOCItem
	Content      htmltemplate.HTML
	Srcdoc       string
	Device       bool
	Mermaid      bool
	Static       bool
	Conversation htmltemplate.HTML
}

func renderPage(w io.Writer, data pageData) error {
	data.CSS = htmltemplate.CSS(pageCSS)
	if !data.Static {
		data.JS = htmltemplate.JS(pageJS)
	}
	return pageTemplate.Execute(w, data)
}

// buildContent renders the artifact body for one kind. For HTML artifacts in
// static (export) mode the artifact source, with local assets inlined, is
// returned as srcdoc instead of page content so the export needs no server.
func buildContent(kind Kind, artifact string, static bool) (content string, toc []TOCItem, srcdoc string, err error) {
	source, err := os.ReadFile(artifact)
	if err != nil {
		return "", nil, "", err
	}
	switch kind {
	case KindMarkdown:
		body, toc, err := RenderMarkdown(source)
		if err != nil {
			return "", nil, "", err
		}
		return `<div class="markdown">` + body + `</div>`, toc, "", nil
	case KindDiff:
		return `<div class="diff">` + RenderDiff(ParseDiff(source)) + `</div>`, nil, "", nil
	case KindCSV:
		table, err := RenderCSV(source)
		if err != nil {
			return "", nil, "", err
		}
		return `<div class="csv-wrap">` + table + `</div>`, nil, "", nil
	case KindMermaid:
		escaped := html.EscapeString(string(source))
		return `<pre class="mermaid">` + escaped + `</pre>` +
			`<div id="mermaid-fallback" hidden><p class="fallback-note">The Mermaid CDN is unreachable, so the diagram source is shown instead.</p><pre><code>` +
			escaped + `</code></pre></div>`, nil, "", nil
	case KindHTML:
		if static {
			return "", nil, InlineLocalAssets(string(source), filepath.Dir(artifact)), nil
		}
		return `<div id="frame-wrap"><iframe id="frame" src="raw" title="artifact preview" sandbox="allow-scripts"></iframe><div id="annotate-overlay" hidden></div></div>`, nil, "", nil
	}
	return "", nil, "", fmt.Errorf("showcase: unsupported artifact kind %q", kind)
}

// renderConversation renders messages and queued feedback as static bubbles
// for exports. The live page renders the same state in the browser.
func renderConversation(session *Session) string {
	if session == nil || (len(session.Messages) == 0 && len(session.Feedback) == 0) {
		return `<p class="empty">No feedback yet.</p>`
	}
	type entry struct {
		at   time.Time
		html string
	}
	var entries []entry
	for _, message := range session.Messages {
		entries = append(entries, entry{message.CreatedAt, bubble(message.Role, "", "", message.Text)})
	}
	for _, feedback := range session.Feedback {
		entries = append(entries, entry{feedback.CreatedAt, bubble("user", feedback.Type, feedback.Quote, feedback.Text)})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	var out strings.Builder
	for _, item := range entries {
		out.WriteString(item.html)
	}
	return out.String()
}

func bubble(role, tag, quote, text string) string {
	var out strings.Builder
	fmt.Fprintf(&out, `<div class="msg %s">`, role)
	if tag != "" && tag != "message" {
		fmt.Fprintf(&out, `<span class="tag">%s</span>`, html.EscapeString(tag))
	}
	if quote != "" {
		out.WriteString(`<blockquote>`)
		out.WriteString(html.EscapeString(quote))
		out.WriteString(`</blockquote>`)
	}
	out.WriteString(`<div class="text">`)
	out.WriteString(html.EscapeString(text))
	out.WriteString(`</div></div>`)
	return out.String()
}
