package main

import (
	"net/url"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// openMessage is what the board's page sends the window before the address of
// a link to open in the default browser.
const openMessage = "open:"

// linksScript runs in the board's page after each load. The board opens a
// pull request, a review's page, a handoff and a delivered file in a new
// browser tab. In the window a new tab would be a bare WebView2 window that
// holds none of the Overlord's sign-ins, so the page hands the address to the
// window instead, which opens it in his default browser.
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
