import { readdirSync, readFileSync } from "node:fs";
import { chromium, type Page } from "@playwright/test";

const SCREENS = new URL("./fixtures/harness-screens/", import.meta.url);

// The terminal's own fonts until the board's JetBrains Mono has loaded, as
// FALLBACK_FONT in src/terminalView.ts names them. The terminal's harness
// screen tests fail if the two part.
const TERMINAL_FONTS = '"Cascadia Mono", Consolas, monospace';

// A page that sets no font of its own, as the page a Scrawl preview test shows
// in its frame, is drawn in the browser's default font, its heading bold, and
// a runner had never read the bold face's file either (#1330).
const UNSTYLED_PAGE = "<article><h1>The plan</h1><p>The plan</p></article>";

// A browser opens a system font from its file, on the page's main thread, the
// first time the page lays text out in it. A Claude Code screen holds symbols
// the terminal's own fonts lack, ※ ⎿ ⏸ ✻, which Windows draws in Times New
// Roman, Cambria Math and Segoe UI Symbol, and a runner on CI has never read
// those files from its disk: the first test to draw such a screen found its
// page stopped for 5 to 19 s (#1264). So the run opens every system font the
// recorded harness screens and an unstyled page are drawn in once, before its
// first test, by laying each of the screens' characters out in the terminal's
// fonts beside such a page, and tells the tests in BOARD_TEST_FONTS which
// fonts it opened. Each character is in a block of its own: after ⏸, ⎿ is
// drawn in the font ⏸ took, where alone, as the terminal draws it, it needs
// Cambria Math.
export default async function openSystemFonts(): Promise<void> {
  const text = readdirSync(SCREENS).map((name) => JSON.parse(readFileSync(new URL(name, SCREENS), "utf8")).output).join("");
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage();
    const started = performance.now();
    await page.setContent(`<main style='font-family: ${TERMINAL_FONTS}'></main>${UNSTYLED_PAGE}`);
    // Reading the height lays the text out. A picture would also wait the
    // second or more a browser just started takes to draw its first frame.
    await page.locator("main").evaluate((main, characters) => {
      main.replaceChildren(...characters.map((character) => Object.assign(document.createElement("div"), { textContent: character })));
      return main.getBoundingClientRect().height;
    }, [...new Set(text)]);
    const seconds = (performance.now() - started) / 1000;
    const fonts = await systemFonts(page, "main > div, article > *");
    process.env.BOARD_TEST_FONTS = JSON.stringify(fonts);
    console.log(`Opened the system fonts the terminal draws the harness screens in and an unstyled page is drawn in (${fonts.join(", ")}) in ${seconds.toFixed(1)} s`);
  } finally {
    await browser.close();
  }
}

// systemFonts is every font installed on the machine that the text of the
// nodes selector finds is drawn in, by PostScript name, which tells a bold or
// italic face from the regular one. A font the page loaded itself, as the
// board loads JetBrains Mono, is not one.
export async function systemFonts(page: Page, selector: string): Promise<string[]> {
  const devtools = await page.context().newCDPSession(page);
  await devtools.send("DOM.enable");
  await devtools.send("CSS.enable");
  const { root } = await devtools.send("DOM.getDocument", { depth: -1 });
  const { nodeIds } = await devtools.send("DOM.querySelectorAll", { nodeId: root.nodeId, selector });
  const fonts = new Set<string>();
  for (const nodeId of nodeIds) {
    for (const font of (await devtools.send("CSS.getPlatformFontsForNode", { nodeId })).fonts) if (!font.isCustomFont) fonts.add(font.postScriptName);
  }
  await devtools.detach();
  return [...fonts].sort();
}
