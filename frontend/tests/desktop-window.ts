import { readFileSync } from "node:fs";

// The desktop window's own script (linksScript in cmd/goblins-window/links.go)
// runs in the board's page after each load. A spec runs it as the window runs
// it, read from its source, over a stand-in for WebView2's bridge that keeps
// what the page asks the window to open.
export const LINKS_GO = readFileSync(new URL("../../cmd/goblins-window/links.go", import.meta.url), "utf8");
export const OPEN_MESSAGE = /const openMessage = "([^"]+)"/.exec(LINKS_GO)![1];
const LINKS_START = LINKS_GO.indexOf("const linksScript = `") + "const linksScript = `".length;
const LINKS_SCRIPT = LINKS_GO.slice(LINKS_START, LINKS_GO.indexOf("})();`", LINKS_START) + "})();".length).replaceAll("` + openMessage + `", OPEN_MESSAGE);
export const RUN_LINKS = `window.addEventListener("DOMContentLoaded", () => { ${LINKS_SCRIPT} });`;
export const WINDOW = `const posted = []; Object.assign(window, { posted, chrome: { webview: { postMessage: (text) => posted.push(text) } } });
${RUN_LINKS}`;
