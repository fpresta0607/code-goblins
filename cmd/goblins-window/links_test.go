package main

import "testing"

// The board's page asks the window to open a link in the default browser. The
// window does so for a web address a board's own page asks for, and for
// nothing else: not for another site's page, and never for a file, a script
// or another program's protocol.
func TestOnlyABoardsWebLinkOpensInTheBrowser(t *testing.T) {
	const board = "http://127.0.0.1:4310/"
	for name, test := range map[string]struct {
		message, sender, want string
	}{
		"a pull request from the board": {"open:https://github.com/owner/repository/pull/12", board, "https://github.com/owner/repository/pull/12"},
		"a review page on this PC":      {"open:http://127.0.0.1:4392/plan?item=3#top", board, "http://127.0.0.1:4392/plan?item=3#top"},
		"a board on IPv6 loopback":      {"open:https://example.com/", "http://[::1]:4310/", "https://example.com/"},
		"a message that asks nothing":   {"wails:runtime:ready", board, ""},
		"another site's page":           {"open:https://example.com/", "https://example.com/page", ""},
		"a page on another computer":    {"open:https://example.com/", "http://192.0.2.20:4310/", ""},
		"a page with no address":        {"open:https://example.com/", "", ""},
		"a file":                        {"open:file:///C:/Windows/System32/calc.exe", board, ""},
		"a path":                        {`open:C:\Windows\System32\calc.exe`, board, ""},
		"a script":                      {"open:javascript:alert(1)", board, ""},
		"another program's protocol":    {"open:ms-settings:privacy", board, ""},
		"a web address with no host":    {"open:https:///path", board, ""},
		"an address with a space in it": {"open:https://example.com/ --flag", board, ""},
		"an address with a quote in it": {`open:https://example.com/"`, board, ""},
		"an address with a line break":  {"open:https://example.com/\ncalc", board, ""},
		"no address":                    {"open:", board, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := linkToOpen(test.message, test.sender)

			if got != test.want || ok != (test.want != "") {
				t.Errorf("linkToOpen(%q, %q) = %q, %v; want %q, %v", test.message, test.sender, got, ok, test.want, test.want != "")
			}
		})
	}
}
