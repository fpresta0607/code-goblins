package main

import "testing"

// The board's page asks the window to open one of its own pages, a session
// popped out of its panel, in a window of its own. The window does so for a
// page of the board that asks, and for nothing else.
func TestOnlyABoardsOwnPagePopsOutIntoAWindow(t *testing.T) {
	const board = "http://127.0.0.1:4310/"
	for name, test := range map[string]struct {
		message, sender, want string
	}{
		"a goblin's session":           {"window:http://127.0.0.1:4310/?session=cg-touch", board, "http://127.0.0.1:4310/?session=cg-touch"},
		"the CFO's session":            {"window:http://127.0.0.1:4310/?session=cfo", "http://127.0.0.1:4310/?session=cg-touch", "http://127.0.0.1:4310/?session=cfo"},
		"a board on IPv6 loopback":     {"window:http://[::1]:4310/?session=cfo", "http://[::1]:4310/", "http://[::1]:4310/?session=cfo"},
		"a message that asks nothing":  {"wails:runtime:ready", board, ""},
		"a link for the browser":       {"open:http://127.0.0.1:4310/?session=cfo", board, ""},
		"another board's page":         {"window:http://127.0.0.1:4311/?session=cfo", board, ""},
		"another site's page":          {"window:https://example.com/", board, ""},
		"a page asked by another site": {"window:https://example.com/", "https://example.com/page", ""},
		"a page on another computer":   {"window:http://192.0.2.20:4310/", "http://192.0.2.20:4310/", ""},
		"a page with no sender":        {"window:http://127.0.0.1:4310/", "", ""},
		"a file":                       {"window:file:///C:/Windows/System32/calc.exe", board, ""},
		"a script":                     {"window:javascript:alert(1)", board, ""},
		"no address":                   {"window:", board, ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			got, ok := pageToPopOut(test.message, test.sender)

			// Assert
			if got != test.want || ok != (test.want != "") {
				t.Errorf("pageToPopOut(%q, %q) = %q, %v; want %q, %v", test.message, test.sender, got, ok, test.want, test.want != "")
			}
		})
	}
}
