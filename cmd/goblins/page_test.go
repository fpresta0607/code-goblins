package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageCheckRefusesWhatIsNoPageBeforeOpeningAnything(t *testing.T) {
	notes := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notes, []byte("not a page"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"no subcommand":          {nil, "usage: goblins page check <html-file>"},
		"another subcommand":     {[]string{"open", "page.html"}, "usage: goblins page check <html-file>"},
		"no page":                {[]string{"check"}, "usage: goblins page check <html-file>"},
		"a flag before the page": {[]string{"check", "--out", "pictures", "page.html"}, "usage: goblins page check <html-file>"},
		"a stray argument":       {[]string{"check", notes, "extra"}, "unexpected argument"},
		"a file that is no page": {[]string{"check", notes}, "is not an HTML file"},
		"a page that is missing": {[]string{"check", filepath.Join(t.TempDir(), "gone.html")}, "is not an HTML file"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			exit := runPage(c.args, &stdout, &stderr)

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), c.says) || stdout.Len() != 0 {
				t.Errorf("exit=%d stdout=%q stderr=%q, want 2 naming %q", exit, stdout.String(), stderr.String(), c.says)
			}
		})
	}
}
