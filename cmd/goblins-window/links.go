package main

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// openMessage is what the board's page sends the window before the address of
// a link to open in the default browser.
const openMessage = "open:"

// linkFailedEvent is the event the window raises in the board's page when a
// link the page asked it to open could not be opened, which the page says,
// with the address to copy.
const linkFailedEvent = "code-goblins-link-failed"

// linksScript runs in the board's page after each load. The board opens a
// pull request, a review's page, a delivered file's page and a handoff in a new
// browser tab. In the window a new tab would be a bare WebView2 window that
// holds none of the Overlord's sign-ins, so the page hands the address to the
// window instead, which opens it in his default browser. Any other way off the
// board, such as a link that opens in the same tab or a script that sets the
// page's location, opens in his default browser too, so the window always
// shows the board; a navigation the page cannot cancel, such as the window
// following the supervisor to a new address, goes ahead.
const linksScript = `(() => {
  if (window.codeGoblinsLinks) return;
  window.codeGoblinsLinks = true;
  const open = (address) => window.chrome.webview.postMessage("` + openMessage + `" + address);
  const follow = (event) => {
    const link = event.target instanceof Element ? event.target.closest('a[href][target="_blank"]') : null;
    if (!link) return;
    event.preventDefault();
    open(link.href);
  };
  window.addEventListener("click", follow, true);
  window.addEventListener("auxclick", follow, true);
  window.open = (address) => {
    if (address) open(new URL(address, location.href).href);
    return null;
  };
  if (window.navigation) window.navigation.addEventListener("navigate", (event) => {
    const destination = new URL(event.destination.url);
    if (destination.origin === location.origin || !event.cancelable) return;
    event.preventDefault();
    open(destination.href);
  });
})();`

// linkToOpen returns the address a message from the window's page asks for in
// the default browser. Only a board's own page may ask, and only for a web
// address as a browser writes one, with no space or quote in it: Windows
// would open anything else with whatever program claims it.
func linkToOpen(message, sender string) (string, bool) {
	address, ok := strings.CutPrefix(message, openMessage)
	if !ok || !fromBoard(sender) || strings.ContainsFunc(address, func(r rune) bool { return r <= ' ' || r == '"' || r == 0x7f }) {
		return "", false
	}
	link, err := url.Parse(address)
	if err != nil || (link.Scheme != "http" && link.Scheme != "https") || link.Host == "" {
		return "", false
	}
	return address, true
}

// refusedLink returns the address a board's own page asked the window to open
// that linkToOpen refused, which the page then says it could not open.
func refusedLink(message, sender string) (string, bool) {
	address, ok := strings.CutPrefix(message, openMessage)
	return address, ok && fromBoard(sender)
}

// linkFailed is the script that tells the board's page a link could not be
// opened. The address goes as a JSON string, so nothing in it runs.
func linkFailed(address string) string {
	quoted, _ := json.Marshal(address)
	return `window.dispatchEvent(new CustomEvent("` + linkFailedEvent + `", { detail: ` + string(quoted) + ` }));`
}

// openInBrowser opens address in the default browser through the URL protocol
// handler, with no console window, as goblins does.
func openInBrowser(address string) error {
	command := execx.Command("rundll32.exe", "url.dll,FileProtocolHandler", address)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
