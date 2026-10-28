package main

import (
	"net/url"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// popOutMessage is what the board's page sends the window before the address
// of one of the board's own pages to open in a window of its own: a session
// the Overlord pops out of its panel (the Overlord, 2026-10-27: "something
// that you can open in its own window. And that can be either the terminal or
// it can be the modern chat").
const popOutMessage = "window:"

// pageToPopOut returns the page a message from the window's page asks to open
// in a window of its own. Only a board's own page may ask, and only for a page
// of that same board, so the new window never shows another site.
func pageToPopOut(message, sender string) (string, bool) {
	address, ok := strings.CutPrefix(message, popOutMessage)
	if !ok || !fromBoard(sender) {
		return "", false
	}
	page, err := url.Parse(address)
	if err != nil {
		return "", false
	}
	board, err := url.Parse(sender)
	if err != nil || page.Scheme != board.Scheme || page.Host != board.Host {
		return "", false
	}
	return page.String(), true
}

// popOut opens address in a new window of the app, on the board's dark ground
// under a dark title bar as the board's own window is. Its page opens links in
// the default browser as the board's does, and raises no notification, which
// the board's own window raises once for every page. Closing it closes it.
func popOut(app *application.App, address string) {
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Code Goblins",
		URL:              address,
		Width:            1000,
		Height:           1000,
		BackgroundColour: application.NewRGB(0x0b, 0x11, 0x18),
		Windows:          application.WindowsWindow{Theme: application.Dark},
	})
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		window.HandleMessage("wails:runtime:ready")
		window.ExecJS(linksScript + speechScript)
	})
}
