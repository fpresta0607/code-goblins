package showcase

import "testing"

func TestDetectByExtension(t *testing.T) {
	tests := []struct {
		path string
		want Kind
	}{
		{path: "plan.md", want: KindMarkdown},
		{path: "REPORT.MARKDOWN", want: KindMarkdown},
		{path: "mock.html", want: KindHTML},
		{path: "wire.htm", want: KindHTML},
		{path: "change.diff", want: KindDiff},
		{path: "fix.patch", want: KindDiff},
		{path: "data.csv", want: KindCSV},
		{path: "flow.mmd", want: KindMermaid},
		{path: "flow.mermaid", want: KindMermaid},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			got, err := Detect(test.path, nil)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if got != test.want {
				t.Errorf("Detect(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestDetectFallsBackToContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Kind
	}{
		{name: "doctype", content: "<!DOCTYPE html><html><body>x</body></html>", want: KindHTML},
		{name: "html tag", content: "<html><body>x</body></html>", want: KindHTML},
		{name: "git diff", content: "diff --git a/x.go b/x.go\nindex 1..2 100644\n", want: KindDiff},
		{name: "unified diff", content: "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n", want: KindDiff},
		{name: "mermaid", content: "flowchart TD\n  A --> B\n", want: KindMermaid},
		{name: "sequence", content: "sequenceDiagram\n  A->>B: hi\n", want: KindMermaid},
		{name: "prose", content: "# A plan\n\nSome prose.\n", want: KindMarkdown},
		{name: "bom", content: "\xef\xbb\xbf<html></html>", want: KindHTML},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Detect("artifact", []byte(test.content))
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if got != test.want {
				t.Errorf("Detect(content %q) = %q, want %q", test.name, got, test.want)
			}
		})
	}
}
