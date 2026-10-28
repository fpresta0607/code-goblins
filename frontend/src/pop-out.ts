// A session popped out of its panel into a window of its own (the Overlord,
// 2026-10-27: "something that you can open in its own window. And that can be
// either the terminal or it can be the modern chat"): the board's own page
// with ?session= naming the session, a goblin's task ID or "cfo", which opens
// on that session's panel, maximized, in the chat style chosen in this
// browser. Both windows are views of the same session.

// What the board's page sends the goblins-window app before the page to open
// in a new window of its own (cmd/goblins-window/popout.go).
const POP_OUT_MESSAGE = "window:";

interface WebViewHost { postMessage: (message: string) => void }

// poppedSession is the session this page was popped out for, or "" for the
// board itself.
export function poppedSession(): string {
  return new URLSearchParams(location.search).get("session") ?? "";
}

// popOut opens session in a window of its own: a new window of the
// goblins-window app inside it, whose page has WebView2's host object, and a
// new browser window elsewhere.
export function popOut(session: string): void {
  const address = new URL("?" + new URLSearchParams({ session }).toString(), location.href).href;
  const host = (window as { chrome?: { webview?: WebViewHost } }).chrome?.webview;
  if (host) host.postMessage(POP_OUT_MESSAGE + address);
  else window.open(address, "_blank", "popup,noopener,width=1000,height=1000");
}
