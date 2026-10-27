import { devices, expect, servePages, test, type Page } from "./site";

// The Overlord, 2026-10-22, on his iPhone over the private board: "also i
// cant paste", and "theres no copy and paste of screenshots or adding of
// attached images". A harness takes a pasted picture from the PC's clipboard
// on its own image key, and a phone's picture is on the phone. On a touch
// screen a terminal has Paste, Add a picture and Copy text beside its
// microphone: text pastes as a desktop paste does, and a picture is put on
// the PC's clipboard and then the image key is pressed, as a desktop paste of
// a screenshot presses it. Its long-press Paste of a picture does the same.

test.use({ ...devices["Pixel 7"], launchOptions: { args: ["--disable-webgl"] } });

// The private board's address, as his phone opens it.
const PRIVATE = "https://pc.example.ts.net:4311";
// Claude Code attaches the PC's clipboard image on Alt+V.
const IMAGE_KEY = "\x1bv";

interface Terminal { typed: () => string; pictures: Buffer[]; answerPicture: () => void }

// openTerminal opens a Claude Code terminal of backend on the private board,
// with this PC's clipboard played by a route that holds each picture until
// answerPicture lets it through, and the phone's clipboard by clipboardItems.
async function openTerminal(page: Page, backend: "native" | "herdr"): Promise<Terminal> {
  const inputs: Buffer[] = [];
  const pictures: Buffer[] = [];
  const held: (() => void)[] = [];
  await page.addInitScript(() => {
    localStorage.setItem("cfo-voice-hint-v1", "dismissed");
    Object.defineProperty(navigator, "clipboard", { value: { read: async () => {
      if (Reflect.get(window, "clipboardDenied")) throw new DOMException("Denied", "NotAllowedError");
      return (window as unknown as { clipboardItems: ClipboardItem[] }).clipboardItems ?? [];
    }, writeText: async () => {} } });
  });
  await page.route("**/api/terminal/image", async (route) => {
    pictures.push(route.request().postDataBuffer()!);
    expect(route.request().headers()["content-type"]).toBe("image/png");
    await new Promise<void>((answer) => held.push(answer));
    await route.fulfill({ json: { pasted: true } });
  });
  if (backend === "native") {
    // The terminal's host repaints after each size the view asks for, as the
    // real one does.
    const screen = Buffer.from("\x1b[2J\x1b[HSELECTABLE TEXT\r\n$ \x1b[?2004h");
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(screen);
      socket.onMessage((data) => {
        if (typeof data !== "string") { inputs.push(data); return; }
        const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
        if (control.type !== "resize") return;
        socket.send(JSON.stringify({ type: "size", cols: control.cols, rows: control.rows }));
        socket.send(screen);
      });
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
          controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 40, height: 20, bytes: btoa("\x1b[2J\x1b[HSELECTABLE TEXT\r\n$ \x1b[?2004h") }) + "\n"));
          options?.signal?.addEventListener("abort", () => controller.close(), { once: true });
        } })));
      };
    });
    await page.route("**/api/terminal/input", async (route) => {
      const { command, seq } = route.request().postDataJSON();
      if (command.type === "terminal.input") inputs.push(Buffer.from(command.text));
      await route.fulfill({ json: { seq } });
    });
    await page.route("**/api/terminal/history", (route) => route.fulfill({ json: { text: "$", agent: "claude" } }));
  }
  await servePages(page.context(), PRIVATE);
  await page.goto(PRIVATE + "/tests/fixtures/terminal-input.html?harness=claude#" + backend);
  await expect(page.getByText("Connecting", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  return { typed: () => Buffer.concat(inputs).toString("utf8"), pictures, answerPicture: () => held.shift()?.() };
}

// A picture of width by height as the phone holds it, of type.
const picture = (page: Page, width: number, height: number, type: string) => page.evaluate(async ({ width, height, type }) => {
  const canvas = Object.assign(document.createElement("canvas"), { width, height });
  canvas.getContext("2d")!.fillRect(0, 0, width / 2, height / 2);
  const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, type));
  return [...new Uint8Array(await blob!.arrayBuffer())];
}, { width, height, type });

// A PNG's width and height, from its header.
const pngSize = (png: Buffer) => ({ isPNG: png.subarray(1, 4).toString() === "PNG", width: png.readUInt32BE(16), height: png.readUInt32BE(20) });

// pictureReachesTheHarness lets the picture the PC was handed through and
// asserts the image key is pressed only then.
async function pictureReachesTheHarness(page: Page, terminal: Terminal) {
  await expect.poll(() => terminal.pictures.length).toBe(1);
  await page.waitForTimeout(300);
  expect(terminal.typed(), "no key before the picture is on the PC's clipboard").toBe("");
  terminal.answerPicture();
  await expect.poll(terminal.typed).toBe(IMAGE_KEY);
}

for (const backend of ["native", "herdr"] as const) {
  test.describe(backend + " terminal on a phone", () => {
    test("Paste types the phone's clipboard text as a desktop paste does", async ({ page }) => {
      // Arrange
      const terminal = await openTerminal(page, backend);
      await page.evaluate(() => Object.assign(window, { clipboardItems: [new ClipboardItem({ "text/plain": new Blob(["git status\nnpm test"], { type: "text/plain" }) })] }));

      // Act
      await page.getByRole("button", { name: "Paste", exact: true }).tap();

      // Assert
      await expect.poll(terminal.typed).toBe("\x1b[200~git status\rnpm test\x1b[201~");
      await expect(page.getByText("Nothing to paste: the clipboard is empty.")).toHaveCount(0);
    });

    test("Paste reports an empty or denied clipboard without typing", async ({ page }) => {
      const terminal = await openTerminal(page, backend);
      await page.getByRole("button", { name: "Paste", exact: true }).tap();
      await expect(page.getByText("Nothing to paste: the clipboard is empty.")).toBeVisible();
      expect(terminal.typed()).toBe("");
      await page.evaluate(() => Object.assign(window, { clipboardDenied: true }));
      await page.getByRole("button", { name: "Paste", exact: true }).tap();
      await expect(page.getByText("The clipboard was not read. Allow Paste when asked.")).toBeVisible();
      expect(terminal.typed()).toBe("");
    });

    test("Paste of a screenshot puts it on the PC's clipboard, then presses the harness's image key", async ({ page }) => {
      // Arrange
      const terminal = await openTerminal(page, backend);
      const screenshot = await picture(page, 390, 844, "image/png");
      await page.evaluate((bytes) => Object.assign(window, { clipboardItems: [new ClipboardItem({ "image/png": new Blob([new Uint8Array(bytes)], { type: "image/png" }) })] }), screenshot);

      // Act
      await page.getByRole("button", { name: "Paste", exact: true }).tap();

      // Assert
      await pictureReachesTheHarness(page, terminal);
      expect(pngSize(terminal.pictures[0])).toEqual({ isPNG: true, width: 390, height: 844 });
    });

    test("a photo added from the library reaches the harness as a PNG at most 2048 pixels a side", async ({ page }) => {
      // Arrange
      const terminal = await openTerminal(page, backend);
      const photo = await picture(page, 4032, 3024, "image/jpeg");

      // Act
      await page.getByLabel("Picture to add").setInputFiles({ name: "IMG_0001.jpg", mimeType: "image/jpeg", buffer: Buffer.from(photo) });

      // Assert
      await pictureReachesTheHarness(page, terminal);
      expect(pngSize(terminal.pictures[0])).toEqual({ isPNG: true, width: 2048, height: 1536 });
    });

    test("the long-press menu's Paste of a picture puts it on the PC's clipboard, then presses the image key", async ({ page }) => {
      // Arrange
      const terminal = await openTerminal(page, backend);
      const screenshot = await picture(page, 20, 10, "image/png");
      const box = page.getByRole("textbox", { name: "Terminal input", exact: true });
      await page.locator(".terminal-surface:not(.away) .xterm-screen").first().tap();

      // Act: the phone's Paste is a paste event on the box, holding the picture.
      await box.evaluate((element, bytes) => {
        const clipboardData = new DataTransfer();
        clipboardData.items.add(new File([new Uint8Array(bytes)], "screenshot.png", { type: "image/png" }));
        element.dispatchEvent(new ClipboardEvent("paste", { clipboardData, bubbles: true, cancelable: true }));
      }, screenshot);

      // Assert
      await pictureReachesTheHarness(page, terminal);
    });

    test("Copy text lays the terminal's text out to select", async ({ page }) => {
      // Arrange
      await openTerminal(page, backend);

      // Act
      await page.getByRole("button", { name: "Copy text", exact: true }).tap();

      // Assert: the text is there to select, and Done returns to the terminal.
      const sheet = page.getByRole("dialog", { name: "Terminal text" });
      await expect(sheet.locator("pre")).toContainText("SELECTABLE TEXT");
      expect(await sheet.locator("pre").evaluate((element) => getComputedStyle(element).userSelect)).toBe("text");
      await sheet.getByRole("button", { name: "Done" }).tap();
      await expect(sheet).toHaveCount(0);
    });
  });
}

// An iPhone offers no Paste over a box it reads as transparent, under 1%
// opacity, which is how xterm leaves its text box.
test("on a touch screen the terminal's text box is drawn at an opacity an iPhone offers Paste in, with nothing to see", async ({ page }) => {
  // Arrange
  await openTerminal(page, "native");

  const box = page.getByRole("textbox", { name: "Terminal input", exact: true });
  await page.locator(".terminal-surface:not(.away) .xterm-screen").first().tap();
  await expect(box).toBeFocused();

  // Act: as the box is drawn while it has the keyboard.
  const look = await box.evaluate((element) => {
    const style = getComputedStyle(element);
    return { opacity: Number(style.opacity), color: style.color, caret: style.caretColor, background: style.backgroundColor, outline: style.outlineStyle, border: style.borderTopWidth };
  });

  // Assert
  expect(look.opacity).toBeGreaterThanOrEqual(0.01);
  expect(look).toMatchObject({ color: "rgba(0, 0, 0, 0)", caret: "rgba(0, 0, 0, 0)", background: "rgba(0, 0, 0, 0)", outline: "none", border: "0px" });
});
