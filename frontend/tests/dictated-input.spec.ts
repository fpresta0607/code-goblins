import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-21, dictating into the CFO's terminal from his phone:
// "why is everything double printing". A phone keyboard's dictation writes
// each partial result over the one before it in the terminal's text box: it
// selects the words it wrote and types the longer guess in their place, as
// WebKit's dictation does. xterm sent each of those as new keys, so a
// sentence reached the program once per partial, each a little longer.
const PARTIALS = ["Why", "Why is", "Why is every", "Why is everything", "Why is everything double", "Why is everything double printing"];

// Plays a dictation into the focused text box: each partial selects what the
// last one wrote and is typed over it.
const dictate = (page: Page, partials: string[]) => page.getByRole("textbox", { name: "Terminal input", exact: true }).evaluate((element, guesses) => {
  const box = element as HTMLTextAreaElement;
  const start = box.value.length;
  for (const guess of guesses) {
    box.setSelectionRange(start, box.value.length);
    document.execCommand("insertText", false, guess);
  }
}, partials);

for (const backend of ["native", "herdr"] as const) {
  test.describe(backend + " terminal dictated input", () => {
    let inputs: Buffer[];

    test.beforeEach(async ({ page }) => {
      inputs = [];
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
              controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 80, height: 24, bytes: btoa("\x1b[2J\x1b[H$ \x1b[?2004h") }) + "\n"));
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
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
    });

    const typed = () => Buffer.concat(inputs).toString("utf8");

    if (backend === "native") {
      test("Codex completion flushes dictated text once after each harness switch", async ({ page }) => {
        for (const harness of ["codex", "claude", "codex"]) {
          // Arrange
          await page.evaluate((value) => (window as unknown as { reportHarness: (harness: string) => void }).reportHarness(value), harness);
          inputs.length = 0;

          // Act
          await dictate(page, PARTIALS);
          await page.keyboard.press("Enter");

          // Assert
          await expect.poll(typed).toBe(PARTIALS.at(-1) + (harness === "codex" ? "\x1b[F\r" : "\r"));
        }
      });

      test("Enter in a Codex picker keeps its selection after dictated input", async ({ page }) => {
        // Arrange
        await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
          socket.send(JSON.stringify({ type: "history", bytes: 0 }));
          socket.send(Buffer.from("\x1b[2J\x1b[HSelect Model and Effort\r\n"));
          socket.send(JSON.stringify({ type: "screen", submission: "", asking: false, open: true, working: false }));
          socket.onMessage((data) => { if (typeof data !== "string") inputs.push(data); });
        });
        await page.goto("/tests/fixtures/terminal-input.html?harness=codex#native");
        await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
        inputs.length = 0;

        // Act
        await dictate(page, PARTIALS);
        await page.getByRole("textbox", { name: "Terminal input", exact: true }).press("Enter");

        // Assert
        await expect.poll(typed).toBe(PARTIALS.at(-1) + "\r");
      });
    }

    test("a dictation's growing partial results reach the program once, as its final words", async ({ page }) => {
      // Act
      await dictate(page, PARTIALS);

      // Assert
      await expect.poll(typed).toBe(PARTIALS[PARTIALS.length - 1]);
    });

    test("a partial that changes words already sent erases them and types the change", async ({ page }) => {
      // Act
      await dictate(page, ["Why is every thing", "Why is everything", "Why is everything double printing?"]);

      // Assert: what the program reads, with each DEL taking back one
      // character, is the final words alone.
      await expect.poll(typed).toBe("Why is every thing" + "\x7f".repeat(6) + "thing double printing?");
    });

    test("a second dictation after Enter starts afresh", async ({ page }) => {
      // Arrange
      await dictate(page, PARTIALS.slice(0, 3));
      await page.keyboard.press("Enter");

      // Act
      await dictate(page, ["And", "And now"]);

      // Assert
      await expect.poll(typed).toBe("Why is every\rAnd now");
    });

    test("a key typed after a dictation is never erased by a later partial", async ({ page }) => {
      // Arrange
      await dictate(page, ["Why is"]);
      await page.keyboard.type("x");

      // Act: the phone revises words written before the key.
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).evaluate((element) => {
        const box = element as HTMLTextAreaElement;
        box.setSelectionRange(0, box.value.length);
        document.execCommand("insertText", false, "Why's");
      });

      // Assert
      await expect.poll(typed).toBe("Why isx");
    });

    test("desktop typing reaches the program unchanged", async ({ page }) => {
      // Act
      await page.keyboard.type("Hello, World! 1+1=2");
      await page.keyboard.press("Backspace");
      await page.keyboard.press("Enter");

      // Assert
      await expect.poll(typed).toBe("Hello, World! 1+1=2\x7f\r");
    });

    test("text a desktop input method inserts reaches the program once", async ({ page }) => {
      // Act: an emoji picker or a dead key types its character without a key
      // the terminal handled.
      await page.keyboard.insertText("é");
      await page.keyboard.insertText("🙂");
      await page.keyboard.type("x");

      // Assert
      await expect.poll(typed).toBe("é🙂x");
    });

    test("a paste reaches the program once and in brackets", async ({ page, context }) => {
      // Arrange
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      await page.evaluate(() => navigator.clipboard.writeText("one\ntwo"));

      // Act
      await page.keyboard.press("Control+v");

      // Assert
      await expect.poll(typed).toBe("\x1b[200~one\rtwo\x1b[201~");
    });
  });
}

test.describe("desktop input method composition", () => {
  test.skip(({ browserName }) => browserName !== "chromium", "composition is driven through Chromium's DevTools protocol");

  test("a composed word reaches the program once, when it is committed", async ({ page }) => {
    // Arrange
    const inputs: Buffer[] = [];
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("\x1b[2J\x1b[H$ "));
      socket.onMessage((data) => { if (typeof data !== "string") inputs.push(data); });
    });
    await page.goto("/tests/fixtures/terminal-input.html#native");
    await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
    await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
    const devtools = await page.context().newCDPSession(page);

    // Act: an input method composes にほん in steps, then commits 日本.
    for (const step of ["に", "にほ", "にほん"]) await devtools.send("Input.imeSetComposition", { text: step, selectionStart: step.length, selectionEnd: step.length });
    await devtools.send("Input.insertText", { text: "日本" });
    await page.keyboard.type("x");

    // Assert
    await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("日本x");
  });
});
