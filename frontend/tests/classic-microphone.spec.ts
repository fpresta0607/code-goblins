import { expect, holdStream, test, type Page } from "./site";

// The Overlord, 2026-11-25: "Plus microphone not working in classic during
// that reconnect or load saying not enabled in browser but it's not asking me
// like it does I modemr". In the classic style a terminal's microphone starts
// from its own keys, Ctrl+Shift+Space held in the terminal, and asks the
// browser for the microphone as the Modern composer's hold does, whether the
// terminal is still loading or loaded. Chromium's fake microphone stands in
// for his once allowed; a question closed unanswered is stubbed as Chrome
// refuses it, and is not called blocked (#2904).
test.use({
  chatStyle: "classic",
  launchOptions: { args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
});

const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const SNAPSHOT = {
  healthy: true, instance: "fixture", revision: 1, attention: [], ceo_runs: true, afk: { state: "off" },
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [{ id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness: "claude", phase: "working" }],
};
const WORDS = "export the march invoices";

// openTerminal opens the task's terminal in the classic style, whose relay
// holds its history back until loaded is called, and counts the browser's
// microphone requests. typed is what the terminal was sent.
async function openTerminal(page: Page) {
  const typed: string[] = [];
  let loaded = () => {};
  await page.addInitScript(() => {
    localStorage.setItem("ceo-voice-hint-v1", "dismissed");
    const asked = { count: 0 };
    (window as unknown as { microphoneAsked: { count: number } }).microphoneAsked = asked;
    const ask = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
    navigator.mediaDevices.getUserMedia = (constraints) => { asked.count++; return ask(constraints); };
  });
  await holdStream(page, SNAPSHOT);
  await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [] } }));
  await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
  await page.route("**/api/dictation", (route) => route.request().method() === "GET"
    ? route.fulfill({ json: { engine: "test-model", state: "ready" } })
    : route.fulfill({ json: { text: WORDS, engine: "test-model" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    let size = { cols: 80, rows: 24 };
    let isLoaded = false;
    loaded = () => {
      isLoaded = true;
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(JSON.stringify({ type: "size", ...size }));
      socket.send(Buffer.from("READY> "));
    };
    socket.onMessage((data) => {
      if (typeof data !== "string") { typed.push(data.toString()); return; }
      const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (control.type === "resize") {
        size = { cols: control.cols!, rows: control.rows! };
        if (isLoaded) socket.send(JSON.stringify({ type: "size", ...size }));
      } else if (!control.type) typed.push(data);
    });
  });
  await page.goto("/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  await page.locator(".task-board .task-card").filter({ hasText: "Export invoices as CSV" }).click();
  // He clicks into the terminal to type, as he does whether it is loaded.
  await page.locator(".host-terminal").first().click();
  const asked = () => page.evaluate(() => (window as unknown as { microphoneAsked: { count: number } }).microphoneAsked.count);
  return { typed, asked, loaded: () => loaded() };
}

async function dictate(page: Page) {
  const warmed = page.waitForRequest((request) => new URL(request.url()).pathname === "/api/dictation/warm");
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.down("Space");
  await warmed;
  await page.waitForTimeout(600);
  await page.keyboard.up("Space");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");
}

test.describe("with the microphone allowed", () => {
  test.use({ permissions: ["microphone"] });

  test("a classic terminal's keys ask the browser for the microphone while it loads, and dictate into it once loaded", async ({ page }) => {
    // Arrange: the terminal has not been sent its history yet.
    const terminal = await openTerminal(page);

    // Act: hold the keys during the load, then again once it is loaded.
    await dictate(page);
    const askedWhileLoading = await terminal.asked();
    terminal.loaded();
    await expect(page.locator(".host-terminal .terminal-view:not(.staged)")).toHaveCount(1);
    await expect(page.getByRole("textbox", { name: "Terminal input" }), "the shown view took the keyboard").toBeFocused();
    await page.keyboard.type("y");
    await dictate(page);

    // Assert
    expect(askedWhileLoading, "the keys asked for the microphone during the load").toBe(1);
    await expect.poll(() => terminal.typed.join("")).toContain("y");
    await expect.poll(() => terminal.typed.join("")).toContain(WORDS);
    await expect(page.locator(".terminal-float"), "no note about the microphone").toHaveCount(0);
  });
});

test("a classic terminal clicked while it loads leaves the keyboard where he moved it once it is shown", async ({ page }) => {
  // Arrange: he clicks the loading terminal, then moves the keyboard away.
  const terminal = await openTerminal(page);
  await expect(page.locator(".host-terminal .terminal-surface")).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.locator(".host-terminal .terminal-surface")).not.toBeFocused();

  // Act
  terminal.loaded();
  await expect(page.locator(".host-terminal .terminal-view:not(.staged)")).toHaveCount(1);

  // Assert
  await expect(page.getByRole("textbox", { name: "Terminal input" })).not.toBeFocused();
});

test("a classic terminal whose microphone question was closed unanswered says to answer it, not that it is blocked", async ({ page }) => {
  // Arrange: Chrome refuses a question closed unanswered, and would ask again.
  await page.addInitScript(() => {
    navigator.mediaDevices.getUserMedia = async () => { throw new DOMException("Permission dismissed", "NotAllowedError"); };
  });
  const terminal = await openTerminal(page);
  terminal.loaded();
  await expect(page.locator(".host-terminal .terminal-view:not(.staged)")).toHaveCount(1);
  await page.locator(".host-terminal").first().click();

  // Act
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.down("Space");
  await page.keyboard.up("Space");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");

  // Assert
  expect(await page.evaluate(async () => (await navigator.permissions.query({ name: "microphone" as PermissionName })).state)).toBe("prompt");
  await expect(page.locator(".terminal-float")).toHaveText("The browser's question about the microphone closed before it was answered. Hold Ctrl+Shift+Space again and choose Allow when it asks.");
});
