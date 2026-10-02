import { expect, test, type Page } from "./site";

// Chromium's fake microphone (a steady beep) stands in for the Overlord's; the
// browser's speech service is replaced by a recognizer that records the track
// it is given and hears one phrase when released.
test.use({
  permissions: ["microphone", "clipboard-read", "clipboard-write"],
  launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
});

declare global {
  interface Window { voiceProbe?: { captures: MediaStreamTrack[]; started: unknown[]; frames: number } }
}

// The microphone glyph the idle bubble shows, as the page draws it.
const MICROPHONE = "M12 3.5a3 3 0 0 0-3 3v5a3 3 0 0 0 6 0v-5a3 3 0 0 0-3-3ZM5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21M8.5 21h7";

// What a supervisor that still served the old SIQspeak endpoint would answer.
// The board must never ask it.
const OLD_VOICE_REPLY = { state: "running", entries: [{ text: "Show me what needs my attention.", timestamp: "2026-09-29T12:45:00", time_epoch: 1790000700 }], has_skipped: false };

// With app the page is as the desktop window shows it: WebView2's own page
// object is there, and the window has taken the browser's recognizer away.
async function openPane(page: Page, { hint = false, dictations = [] as { text: string; at: number }[], panes = 1, app = false } = {}) {
  const asked: string[] = [];
  await page.route("**/api/voice", (route) => { asked.push(route.request().url()); return route.fulfill({ json: OLD_VOICE_REPLY }); });
  await page.addInitScript(({ hint, dictations, app }) => {
    if (!hint) localStorage.setItem("cfo-voice-hint-v1", "dismissed");
    if (dictations.length) localStorage.setItem("cfo-dictations-v1", JSON.stringify({ "task:voice": dictations }));
    const probe = { captures: [] as MediaStreamTrack[], started: [] as unknown[], frames: 0 };
    window.voiceProbe = probe;
    const capture = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
    navigator.mediaDevices.getUserMedia = async (constraints) => {
      const stream = await capture(constraints);
      probe.captures.push(stream.getAudioTracks()[0]);
      return stream;
    };
    const frame = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (callback) => { probe.frames++; return frame(callback); };
    class Recognition {
      continuous = false; interimResults = true; lang = "";
      onresult: ((event: { resultIndex: number; results: { transcript: string }[][] }) => void) | null = null;
      onerror: ((event: { error: string }) => void) | null = null;
      onend: (() => void) | null = null;
      private live = false;
      start(track?: unknown) { probe.started.push(track); this.live = true; }
      stop() { if (!this.live) return; this.live = false; this.onresult?.({ resultIndex: 0, results: [[{ transcript: "ship the voice bubble" }]] }); this.onend?.(); }
      abort() { this.live = false; this.onend?.(); }
    }
    if (!app) { Object.defineProperty(window, "SpeechRecognition", { configurable: true, value: Recognition }); return; }
    const host = window as unknown as { chrome?: object };
    host.chrome = { ...host.chrome, webview: { postMessage: () => {} } };
    for (const name of ["SpeechRecognition", "webkitSpeechRecognition"]) Object.defineProperty(window, name, { configurable: true, writable: true, value: undefined });
  }, { hint, dictations, app });
  await page.goto("/tests/fixtures/voice-bubble.html" + (panes === 2 ? "?panes=2" : ""));
  return { bubble: page.locator(".voice-bubble"), pane: page.getByRole("region", { name: "Goblin terminal" }), asked };
}

// Everything a person can read on the page: its text, tooltips and labels.
const readable = (page: Page) => page.evaluate(() => [document.body.innerText,
  ...[...document.querySelectorAll("[data-tip], [aria-label], [title]")].flatMap((element) => ["data-tip", "aria-label", "title"].map((name) => element.getAttribute(name) || ""))].join("\n"));

async function holdShortcut(page: Page, ms: number) {
  await page.getByRole("textbox", { name: "Terminal input" }).focus();
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.down("Space");
  await page.waitForTimeout(ms);
}
async function releaseShortcut(page: Page) {
  await page.keyboard.up("Space");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");
}

test("the bubble sits in the pane's corner and opens the pane's own recent dictations, newest first, five at most", async ({ page }) => {
  const dictations = ["Merge it when CI is green.", "Open the board.", "Run the tests.", "Pause the goblin.", "Show the canvas.", "Check the memory."].map((text, index) => ({ text, at: 1790000600000 - index * 60000 }));
  const { bubble, pane } = await openPane(page, { dictations });
  const corner = await bubble.boundingBox(), frame = await pane.boundingBox();
  expect(corner && frame && frame.x + frame.width - (corner.x + corner.width)).toBeLessThan(30);
  expect(corner && frame && frame.y + frame.height - (corner.y + corner.height)).toBeLessThan(30);
  await expect(bubble).toHaveAttribute("data-tip", "Hold Ctrl+Shift+Space to dictate");
  // At rest the bubble is a small microphone to click for the list.
  await expect(bubble.locator("svg path")).toHaveAttribute("d", MICROPHONE);

  await bubble.click();
  const recent = page.getByRole("dialog", { name: "Recent messages" });
  await expect(recent.locator(".voice-text")).toHaveText(dictations.slice(0, 5).map((dictation) => dictation.text));

  await recent.getByRole("button", { name: "Copy: Open the board." }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("Open the board.");
  await recent.getByRole("button", { name: "Paste into this terminal: Merge it when CI is green." }).click();
  await expect(page.locator("output")).toContainText("pasted: Merge it when CI is green.");
  await expect(recent).toHaveCount(0);
});

test("the board never names SIQspeak and never asks for it: holding the shortcut always records with its own dictation", async ({ page }) => {
  const { bubble, asked } = await openPane(page, { hint: true });
  await page.waitForTimeout(500);
  await holdShortcut(page, 300);
  await expect(bubble).toHaveClass(/recording/);
  // The recorder starts once the microphone has opened, which takes a busy
  // machine longer than the hold.
  await expect.poll(() => page.evaluate(() => window.voiceProbe!.started.length)).toBe(1);
  await releaseShortcut(page);
  await expect(page.locator("output")).toHaveText("ship the voice bubble");
  await bubble.click();
  await expect(page.getByRole("dialog", { name: "Recent messages" }).locator(".voice-text")).toHaveText(["ship the voice bubble"]);
  expect(await readable(page)).not.toMatch(/siqspeak/i);
  expect(asked).toEqual([]);
});

test.describe("in the desktop app", () => {
  // The Overlord's window: maximized on a 2560 by 1600 screen at 150 percent.
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  test("the shortcut says dictation is not in the app yet, and names no browser", async ({ page }, testInfo) => {
    const { bubble, pane } = await openPane(page, { app: true });
    await holdShortcut(page, 100);
    await releaseShortcut(page);
    const note = pane.getByRole("status");
    await expect(note).toHaveText("Dictation is not in the desktop app yet. It is being built.");
    await expect(bubble).not.toHaveClass(/recording/);
    expect(await page.evaluate(() => window.voiceProbe!.captures.length)).toBe(0);
    // The whole note is inside the pane, on one line.
    const box = await note.boundingBox(), frame = await pane.boundingBox();
    expect(box && frame && box.x >= frame.x && box.x + box.width <= frame.x + frame.width).toBe(true);
    expect(await note.evaluate((element) => element.scrollWidth <= element.clientWidth && element.getClientRects().length === 1)).toBe(true);
    await testInfo.attach("app-dictation-note", { body: await pane.screenshot(), contentType: "image/png" });
  });
});

test("an error note under the open list never covers it", async ({ page }) => {
  const { bubble, pane } = await openPane(page);
  await bubble.click();
  const recent = page.getByRole("dialog", { name: "Recent messages" });
  await expect(recent).toBeVisible();
  await pane.evaluate((section) => {
    const note = document.createElement("p");
    note.className = "terminal-error";
    note.textContent = "The microphone is blocked.";
    section.append(note);
  });
  const note = await pane.locator(".terminal-error").boundingBox(), card = await recent.boundingBox();
  expect(note && card && note.y < card.y + card.height).toBe(true);
  const covered = await page.evaluate(({ x, y }) => !document.elementFromPoint(x, y)?.closest(".voice-recent"), { x: card!.x + card!.width / 2, y: note!.y + note!.height / 2 });
  expect(covered).toBe(false);

  await page.keyboard.press("Escape");
  await expect(recent).toHaveCount(0);
  await expect(bubble).toBeFocused();
});

test("a multiline message pastes as one line, so it never presses Enter, and copies whole", async ({ page }) => {
  const { bubble } = await openPane(page, { dictations: [{ text: "run the tests\nthen commit", at: 1790000700000 }] });
  await bubble.click();
  const recent = page.getByRole("dialog", { name: "Recent messages" });
  await recent.getByRole("button", { name: /^Copy: run the tests/ }).click();
  expect(await page.evaluate(async () => (await navigator.clipboard.readText()).replace(/\r\n/g, "\n"))).toBe("run the tests\nthen commit");
  await recent.getByRole("button", { name: /^Paste into this terminal: run the tests/ }).click();
  await expect(page.locator("output")).toHaveText("pasted: run the tests then commit");
});

test("a hint dismissed in one pane stays dismissed in a pane already open", async ({ page }) => {
  await openPane(page, { hint: true, panes: 2 });
  await expect(page.getByRole("note")).toHaveCount(1);
  await page.getByRole("region", { name: "Goblin terminal" }).getByRole("button", { name: "Dismiss hint" }).click();
  await page.getByRole("button", { name: "Switch pane" }).click();
  await expect(page.getByRole("region", { name: "CFO terminal" }).locator(".voice-bubble")).toBeVisible();
  await expect(page.getByRole("note")).toHaveCount(0);
});

test("the first visit explains the shortcut once", async ({ page }) => {
  const { bubble } = await openPane(page, { hint: true });
  const hint = page.getByRole("note");
  await expect(hint).toContainText("Speak into this terminal");
  await expect(hint).toContainText("hold Ctrl+Shift+Space");
  await page.getByRole("button", { name: "Dismiss hint" }).click();
  await expect(hint).toHaveCount(0);
  await page.reload();
  await expect(bubble).toBeVisible();
  await expect(page.getByRole("note")).toHaveCount(0);
});

test("holding the shortcut records one capture and its bars follow the voice", async ({ page }) => {
  const { bubble } = await openPane(page);
  await holdShortcut(page, 300);
  await expect(bubble).toHaveClass(/recording/);
  await expect(bubble).toHaveAttribute("data-tip", "Listening · release Ctrl+Shift+Space to type");
  const scales = new Set<number>();
  for (let sample = 0; sample < 12; sample++) {
    const heights = await bubble.locator(".voice-bars span").evaluateAll((bars) => bars.map((bar) => new DOMMatrix(getComputedStyle(bar).transform).d));
    for (const height of heights) scales.add(Math.round(height * 100));
    await page.waitForTimeout(150);
  }
  expect(Math.max(...scales)).toBeGreaterThan(25);
  expect(scales.size).toBeGreaterThan(2);
  const shared = await page.evaluate(() => {
    const probe = window.voiceProbe!;
    return { captures: probe.captures.length, started: probe.started.length, same: probe.started[0] === probe.captures[0] };
  });
  expect(shared).toEqual({ captures: 1, started: 1, same: true });

  await releaseShortcut(page);
  await expect(page.locator("output")).toHaveText("ship the voice bubble");
  await expect(bubble).not.toHaveClass(/recording/);
  expect(await page.evaluate(() => window.voiceProbe!.captures[0].readyState)).toBe("ended");

  // Idle, the bubble asks for no frames and runs no animation.
  const frames = await page.evaluate(() => window.voiceProbe!.frames);
  await page.waitForTimeout(1000);
  expect(await page.evaluate(() => window.voiceProbe!.frames)).toBe(frames);
  expect(await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running").length)).toBe(0);

  await bubble.click();
  const recent = page.getByRole("dialog", { name: "Recent messages" });
  await expect(recent.locator(".voice-text").first()).toHaveText("ship the voice bubble");
});
