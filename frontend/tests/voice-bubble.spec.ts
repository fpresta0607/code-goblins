import { expect, test, type Page } from "@playwright/test";

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

const SIQSPEAK = [
  { text: "Show me what needs my attention.", timestamp: "2026-09-29T12:45:00", time_epoch: 1790000700 },
  { text: "Open the board task terminal.", timestamp: "2026-09-29T12:42:00", time_epoch: 1790000520 },
];

async function openPane(page: Page, state: string, { hint = false, dictations = [] as { text: string; at: number }[] } = {}) {
  await page.route("**/api/voice", (route) => route.fulfill({ json: { state, entries: state === "running" || state === "stopped" ? SIQSPEAK : [], has_skipped: false } }));
  await page.addInitScript(({ hint, dictations }) => {
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
    Object.defineProperty(window, "SpeechRecognition", { configurable: true, value: Recognition });
  }, { hint, dictations });
  await page.goto("/tests/fixtures/voice-bubble.html");
  return { bubble: page.locator(".voice-bubble"), pane: page.getByRole("region", { name: "Goblin terminal" }) };
}

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

test("the bubble sits in the pane's corner and opens its recent messages, newest first", async ({ page }) => {
  const { bubble, pane } = await openPane(page, "running", { dictations: [{ text: "Merge it when CI is green.", at: 1790000600000 }] });
  const corner = await bubble.boundingBox(), frame = await pane.boundingBox();
  expect(corner && frame && frame.x + frame.width - (corner.x + corner.width)).toBeLessThan(30);
  expect(corner && frame && frame.y + frame.height - (corner.y + corner.height)).toBeLessThan(30);
  await expect(bubble).toHaveAttribute("data-tip", "SIQspeak running");

  await bubble.click();
  const recent = page.getByRole("dialog", { name: "Recent messages" });
  await expect(recent.locator(".voice-text")).toHaveText(["Show me what needs my attention.", "Merge it when CI is green.", "Open the board task terminal."]);
  await expect(recent.locator(".voice-meta").nth(1)).toContainText("Board");
  await expect(recent.locator(".voice-meta").nth(0)).toContainText("SIQspeak");

  await recent.getByRole("button", { name: "Copy: Merge it when CI is green." }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("Merge it when CI is green.");
  await recent.getByRole("button", { name: "Paste into this terminal: Show me what needs my attention." }).click();
  await expect(page.locator("output")).toContainText("pasted: Show me what needs my attention.");
  await expect(recent).toHaveCount(0);
});

test("the first visit explains the shortcut once", async ({ page }) => {
  const { bubble } = await openPane(page, "running", { hint: true });
  const hint = page.getByRole("note");
  await expect(hint).toContainText("Speak into this terminal");
  await expect(hint).toContainText("hold Ctrl+Shift+Space");
  await page.getByRole("button", { name: "Dismiss hint" }).click();
  await expect(hint).toHaveCount(0);
  await page.reload();
  await expect(bubble).toBeVisible();
  await expect(page.getByRole("note")).toHaveCount(0);
});

for (const [state, tip, help] of [["stopped", "SIQspeak is not running", "Open SIQspeak from its desktop shortcut"], ["missing", "SIQspeak was not found", "Install SIQspeak on this computer"]]) {
  test(`a SIQspeak that is ${state} is named on the bubble and in its list`, async ({ page }) => {
    const { bubble } = await openPane(page, state);
    await expect(bubble).toHaveAttribute("data-tip", tip);
    await bubble.click();
    const recent = page.getByRole("dialog", { name: "Recent messages" });
    await expect(recent).toContainText(tip);
    await expect(recent).toContainText(help);
    await page.keyboard.press("Escape");
    await expect(recent).toHaveCount(0);
    await expect(bubble).toBeFocused();
  });
}

test("while SIQspeak runs, the shortcut is left to it and the board records nothing", async ({ page }) => {
  const { bubble } = await openPane(page, "running");
  await holdShortcut(page, 800);
  await expect(bubble).not.toHaveClass(/recording/);
  await releaseShortcut(page);
  const probe = await page.evaluate(() => ({ captures: window.voiceProbe?.captures.length, started: window.voiceProbe?.started.length }));
  expect(probe).toEqual({ captures: 0, started: 0 });
  await expect(page.locator("output")).toHaveText("");
});

test("without SIQspeak, holding the shortcut records one capture and its bars follow the voice", async ({ page }) => {
  const { bubble } = await openPane(page, "stopped");
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
  await expect(recent.locator(".voice-meta").first()).toContainText("Board");
});
