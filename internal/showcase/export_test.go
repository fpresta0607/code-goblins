package showcase

import (
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
