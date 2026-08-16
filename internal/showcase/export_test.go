package showcase

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMarkdownIsSelfContained(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n\nBody text.\n")
	if _, err := Open(artifact, KindMarkdown, false); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := AppendFeedback(artifact, Feedback{Type: "message", Text: "note for the record"}); err != nil {
		t.Fatalf("AppendFeedback: %v", err)
	}

	out, err := Export(artifact, "")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	wantOut := filepath.Join(filepath.Dir(artifact), "plan-export.html")
	if out != wantOut {
		t.Errorf("default export path = %q, want %q", out, wantOut)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "Body text.") {
		t.Errorf("export lacks the rendered document")
	}
	if !strings.Contains(page, "note for the record") {
		t.Errorf("export lacks the frozen conversation")
	}
	assertSelfContained(t, page)
}

func TestExportMarkdownInlinesLocalImages(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n\n![logo](logo.png)\n")
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(filepath.Join(filepath.Dir(artifact), "logo.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Export(artifact, filepath.Join(filepath.Dir(artifact), "out.html"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Errorf("export did not inline the markdown image")
	}
	if strings.Contains(page, `src="logo.png"`) {
		t.Errorf("export still references the markdown image by path")
	}
	assertSelfContained(t, page)
}

func TestExportHTMLInlinesLocalAssets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body { color: red; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('mock');"), 0o644); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "mock.html")
	source := `<!DOCTYPE html><html><head>
<link rel="stylesheet" href="style.css">
<script src="app.js"></script>
</head><body><h1>Mock</h1><img src="logo.png"></body></html>`
	if err := os.WriteFile(artifact, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Export(artifact, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "body { color: red; }") {
		t.Errorf("export did not inline the stylesheet")
	}
	if !strings.Contains(page, "console.log(&#39;mock&#39;)") && !strings.Contains(page, "console.log('mock')") {
		t.Errorf("export did not inline the script")
	}
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Errorf("export did not inline the image")
	}
	if strings.Contains(page, `href="style.css"`) || strings.Contains(page, `src="app.js"`) || strings.Contains(page, `src="logo.png"`) {
		t.Errorf("export still references sibling files")
	}
	if strings.Contains(page, "data-width=") {
		t.Errorf("static export renders the non-functional device switcher")
	}
	assertSelfContained(t, page)
}

func TestExportHTMLInlinesSingleQuotedAssets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body { color: red; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('single');"), 0o644); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "mock.html")
	source := `<!DOCTYPE html><html><head>
<link rel='stylesheet' href='style.css'>
<script src='app.js'></script>
</head><body><h1>Mock</h1><img src='logo.png'></body></html>`
	if err := os.WriteFile(artifact, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Export(artifact, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "body { color: red; }") {
		t.Errorf("export did not inline the single-quoted stylesheet")
	}
	if !strings.Contains(page, "console.log(") {
		t.Errorf("export did not inline the single-quoted script")
	}
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Errorf("export did not inline the single-quoted image")
	}
	assertSelfContained(t, page)
}

func TestExportHTMLInlinesScriptWithDataSrc(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('app');"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "analytics.js"), []byte("console.log('analytics');"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "mock.html")
	source := `<!DOCTYPE html><html><head><script src="app.js" data-src="analytics.js"></script></head><body><h1>Mock</h1></body></html>`
	if err := os.WriteFile(artifact, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Export(artifact, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	unescaped := html.UnescapeString(page)
	if !strings.Contains(unescaped, "console.log('app')") {
		t.Errorf("export did not inline the src script when a data-src attribute is present")
	}
	if strings.Contains(unescaped, "console.log('analytics')") {
		t.Errorf("export inlined data-src instead of src")
	}
	if strings.Contains(unescaped, `src="app.js"`) {
		t.Errorf("export left the script src dangling")
	}
	assertSelfContained(t, page)
}

func TestExportHTMLInlinesModuleScriptPreservesAttributes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("export function greet() { return 'hi'; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "mock.html")
	source := `<!DOCTYPE html><html><head><script type="module" src="app.js" defer></script></head><body><h1>Mock</h1></body></html>`
	if err := os.WriteFile(artifact, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Export(artifact, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	unescaped := html.UnescapeString(string(data))
	if !strings.Contains(unescaped, `<script type="module" defer>`) {
		t.Errorf("export dropped module/script attributes from the inlined script")
	}
	if !strings.Contains(unescaped, "export function greet") {
		t.Errorf("export did not inline the module script body")
	}
	if strings.Contains(unescaped, "app.js") {
		t.Errorf("export left the module script src dangling")
	}
	assertSelfContained(t, string(data))
}

func TestExportHTMLInlinesImageWithDataSrc(t *testing.T) {
	dir := t.TempDir()
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(filepath.Join(dir, "lazy.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "placeholder.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "mock.html")
	source := `<!DOCTYPE html><html><body><img data-src="lazy.png" src="placeholder.png"></body></html>`
	if err := os.WriteFile(artifact, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Export(artifact, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Errorf("export did not inline the src attribute of an image with a data-src attribute")
	}
	if strings.Contains(page, "placeholder.png") {
		t.Errorf("export left src as a dangling local reference")
	}
	if !strings.Contains(page, "lazy.png") {
		t.Errorf("export rewrote data-src instead of src")
	}
	assertSelfContained(t, page)
}

func TestExportHTMLInlinesCSSURLAssets(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(filepath.Join(dir, "img", "bg.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "css", "style.css"), []byte(`body { background: url(../img/bg.png); }`), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "mock.html")
	source := `<!DOCTYPE html><html><head><link rel="stylesheet" href="css/style.css"></head><body><h1>Mock</h1></body></html>`
	if err := os.WriteFile(artifact, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Export(artifact, filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Errorf("export did not inline the CSS url() asset")
	}
	if strings.Contains(page, "bg.png") {
		t.Errorf("export still references the CSS url() asset by path")
	}
	assertSelfContained(t, page)
}

// assertSelfContained checks the export never calls back to a server and
// pulls no local asset over a root or parent relative path.
func assertSelfContained(t *testing.T, page string) {
	t.Helper()
	for _, bad := range []string{`src="/`, `href="/`, `fetch(`, `/api/`, `/s/`} {
		if strings.Contains(page, bad) {
			t.Errorf("export is not self-contained; it references %q", bad)
		}
	}
}
