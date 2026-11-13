package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The uninstall's window and its page speak to each other by name: each
// function the window calls is one the page defines, and each message the
// page sends is one the window answers.
func TestTheUninstallWindowAndItsPageAgreeOnEveryCall(t *testing.T) {
	// Arrange
	source, err := os.ReadFile("uninstall.go")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	calls := regexp.MustCompile(`call\("(\w+)"`).FindAllStringSubmatch(string(source), -1)
	sends := regexp.MustCompile(`data-send="([\w-]+)"`).FindAllStringSubmatch(uninstallPage, -1)

	// Assert
	if len(calls) < 5 || len(sends) < 4 {
		t.Fatalf("read %d calls and %d buttons, too few to be the window and its page", len(calls), len(sends))
	}
	for _, call := range calls {
		if !strings.Contains(uninstallPage, "function "+call[1]+"(") {
			t.Errorf("the window calls %s, which the page does not define", call[1])
		}
	}
	for _, send := range sends {
		if !strings.Contains(string(source), `"uninstall:`+send[1]) {
			t.Errorf("the page sends uninstall:%s, which the window does not answer", send[1])
		}
	}
	body := uninstallPage[strings.Index(uninstallPage, "<body"):]
	words := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(body[:strings.Index(body, "<script>")], " ")
	for _, sentence := range regexp.MustCompile(`"([A-Z][^"]* [^"]*)"`).FindAllStringSubmatch(body[strings.Index(body, "<script>"):], -1) {
		words += " " + sentence[1]
	}
	if strings.ContainsAny(words, ";—–") {
		t.Errorf("the page's words hold a semicolon or a dash:\n%s", words)
	}
}
