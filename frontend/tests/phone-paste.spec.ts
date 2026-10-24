import { devices, expect, test, type Page } from "./site";

// The Overlord, 2026-10-21, on his phone in the board over his tailnet: "no
// ability to paste text into the terminal". A phone's long-press menu offers
// Paste only over something that can be typed into, and xterm keeps its text
// box far off the screen, so a long press on a terminal offered none. On a
// touch screen the box lies, unseen, over the terminal's whole screen.

test.use({ ...devices["Pixel 7"] });

// What a long press anywhere on the terminal's screen, as far as the phone
// shows it, lands on.
const pressedOn = (page: Page) => page.locator(".terminal-surface:not(.away) .xterm-screen").first().evaluate((screen) => {
  const box = screen.getBoundingClientRect(), panel = screen.closest(".terminal-surface")!.getBoundingClientRect();
  const width = Math.min(box.right, panel.right, innerWidth) - box.left, height = Math.min(box.bottom, panel.bottom, innerHeight) - box.top;
  return [[0.1, 0.1], [0.5, 0.5], [0.9, 0.9]].map(([x, y]) => {
    const found = document.elementFromPoint(box.left + width * x, box.top + height * y);
    return found?.getAttribute("aria-label") ?? `${found?.tagName}.${found?.className}`;
  });
});

for (const backend of ["native", "herdr"] as const) {
  test.describe(backend + " terminal on a touch screen", () => {
    let inputs: Buffer[];

    test.beforeEach(async ({ page }) => {
      inputs = [];
      // The first visit's voice hint lies over the screen's corner, above
      // the text box.
      await page.addInitScript(() => localStorage.setItem("cfo-voice-hint-v1", "dismissed"));
      if (backend === "native") {
        await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
          socket.send(JSON.stringify({ type: "history", bytes: 0 }));
          socket.send(Buffer.from("\x1b[2J\x1b[H$ \x1b[?2004h"));
          socket.onMessage((data) => { if (typeof data !== "string") inputs.push(data); });
        });
      } else {
        await page.addInitScript(() => {
          const originalFetch = window.fetch;
          window.fetch = (resource, options) => {
            if (resource !== "/api/terminal/stream") return originalFetch(resource, options);
            const body = JSON.parse(String(options?.body));
            const encoder = new TextEncoder();
            return Promise.resolve(new Response(new ReadableStream({ start(controller) {
              controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.ready", lease: body.control ? "sized" : "observed", identity: "proof" }) + "\n"));
              controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 40, height: 20, bytes: btoa("\x1b[2J\x1b[H$ \x1b[?2004h") }) + "\n"));
              options?.signal?.addEventListener("abort", () => controller.close(), { once: true });
            } })));
          };
        });
        await page.route("**/api/terminal/input", async (route) => {
          const { command, seq } = route.request().postDataJSON();
          if (command.type === "terminal.input") inputs.push(Buffer.from(command.text));
          await route.fulfill({ json: { seq } });
        });
        await page.route("**/api/terminal/history", (route) => route.fulfill({ json: { text: "$", agent: "codex" } }));
      }
      await page.goto("/tests/fixtures/terminal-input.html#" + backend);
      await expect(page.getByText("Connecting", { exact: true })).toHaveCount(0);
      await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
    });

    test("a long press anywhere on the screen lands on the terminal's text box", async ({ page }) => {
      // Act
      const targets = await pressedOn(page);

      // Assert
      expect(targets).toEqual(["Terminal input", "Terminal input", "Terminal input"]);
    });

    test("the long-press menu's Paste reaches the program once", async ({ page }) => {
      // Arrange
      const box = page.getByRole("textbox", { name: "Terminal input", exact: true });
      await page.locator(".terminal-surface:not(.away) .xterm-screen").first().tap();
      await expect(box).toBeFocused();

      // Act: Paste from the phone's menu is a paste event on the box.
      await box.evaluate((element) => {
        const clipboardData = new DataTransfer();
        clipboardData.setData("text/plain", "git status\nnpm test");
        element.dispatchEvent(new ClipboardEvent("paste", { clipboardData, bubbles: true, cancelable: true }));
      });

      // Assert
      await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\x1b[200~git status\rnpm test\x1b[201~");
    });
  });
}

test.describe("terminal with a mouse", () => {
  test.use({ isMobile: false, hasTouch: false, viewport: { width: 1440, height: 1200 } });

  test("the text box stays off the screen, so a drag selects the screen's text", async ({ page }) => {
    // Arrange
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("\x1b[2J\x1b[H$ "));
    });
    await page.goto("/tests/fixtures/terminal-input.html#native");
    await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);

    // Act
    const targets = await pressedOn(page);

    // Assert
    expect(targets).not.toContain("Terminal input");
  });
});
