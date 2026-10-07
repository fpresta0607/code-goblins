import { expect, ORIGIN, test, type Page, type Request, type Route } from "./site";

// Chromium's fake microphone (periodic beeps) stands in for the Overlord's. The
// supervisor's speech model is a route that keeps each sound it is posted and
// answers one phrase; the browser's speech service is a recognizer that records
// the track it is given and hears another phrase when released, so a test can
// tell which of the two typed.
test.use({
  permissions: ["microphone", "clipboard-read", "clipboard-write"],
  launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
});

declare global {
  interface Window {
    voiceProbe?: { captures: MediaStreamTrack[]; started: unknown[]; frames: number; replies: number };
    voiceSignal?: { context: AudioContext; gain: GainNode };
  }
}

// The microphone glyph the idle bubble shows, as the page draws it.
const MICROPHONE = "M12 3.5a3 3 0 0 0-3 3v5a3 3 0 0 0 6 0v-5a3 3 0 0 0-3-3ZM5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21M8.5 21h7";

// What a supervisor that still served the old SIQspeak endpoint would answer.
// The board must never ask it.
const OLD_VOICE_REPLY = { state: "running", entries: [{ text: "Show me what needs my attention.", timestamp: "2026-09-29T12:45:00", time_epoch: 1790000700 }], has_skipped: false };

// A sound the page posted to the supervisor, as its WAV header and samples say.
interface Posted { token: string; type: string; tags: string; channels: number; rate: number; bits: number; samples: number; peak: number }

function posted(body: Buffer, headers: Record<string, string>): Posted {
  let peak = 0;
  for (let at = 44; at + 1 < body.length; at += 2) peak = Math.max(peak, Math.abs(body.readInt16LE(at)));
  return {
    token: headers["x-cfo-token"] || "", type: headers["content-type"] || "",
    tags: [0, 8, 12, 36].map((at) => body.toString("latin1", at, at + 4)).join(" "),
    channels: body.readUInt16LE(22), rate: body.readUInt32LE(24), bits: body.readUInt16LE(34), samples: (body.length - 44) / 2, peak,
  };
}

// With app the page is as the desktop window shows it: WebView2's own page
// object is there, and the window has taken the browser's recognizer away.
// With browser the Overlord has turned the browser's speech recognition on,
// and with refusal the supervisor refuses every dictation in those words.
// With status the supervisor answers what it says of its speech model from
// that, read at each ask, in place of always ready.
async function openPane(page: Page, { hint = false, dictations = [] as { text: string; at: number }[], panes = 1, app = false, browser = false, refusal = "", replies = null as Route[] | null, status = null as (() => { state: string; note?: string }) | null } = {}) {
  const asked: string[] = [], posts: Posted[] = [];
  await page.route("**/api/voice", (route) => { asked.push(route.request().url()); return route.fulfill({ json: OLD_VOICE_REPLY }); });
  await page.route("**/api/dictation", (route) => {
    const request = route.request();
    if (request.method() === "GET") return route.fulfill({ json: { engine: "test-model", ...(status ? status() : { state: "ready" }) } });
    posts.push(posted(request.postDataBuffer()!, request.headers()));
    if (replies) { replies.push(route); return; }
    return refusal ? route.fulfill({ status: 503, json: { error: refusal } }) : route.fulfill({ json: { text: "ship the voice bubble", engine: "test-model" } });
  });
  await page.addInitScript(({ hint, dictations, app, browser, is_held }) => {
    if (!hint) localStorage.setItem("cfo-voice-hint-v1", "dismissed");
    if (browser) localStorage.setItem("cfo-dictation-browser-v1", "on");
    if (dictations.length) localStorage.setItem("cfo-dictations-v1", JSON.stringify({ "task:voice": dictations }));
    const probe = { captures: [] as MediaStreamTrack[], started: [] as unknown[], frames: 0, replies: 0 };
    window.voiceProbe = probe;
    if (is_held) {
      const fetch = window.fetch.bind(window);
      window.fetch = async (input, init) => {
        const response = await fetch(input, init);
        if (input === "/api/dictation" && init?.method === "POST") {
          const json = response.json.bind(response);
          response.json = async () => { const value: unknown = await json(); probe.replies++; return value; };
        }
        return response;
      };
    }
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
      stop() { if (!this.live) return; this.live = false; this.onresult?.({ resultIndex: 0, results: [[{ transcript: "heard by the browser" }]] }); this.onend?.(); }
      abort() { this.live = false; this.onend?.(); }
    }
    if (!app) { Object.defineProperty(window, "SpeechRecognition", { configurable: true, value: Recognition }); return; }
    const host = window as unknown as { chrome?: object };
    host.chrome = { ...host.chrome, webview: { postMessage: () => {} } };
    for (const name of ["SpeechRecognition", "webkitSpeechRecognition"]) Object.defineProperty(window, name, { configurable: true, writable: true, value: undefined });
  }, { hint, dictations, app, browser, is_held: replies !== null });
  await page.goto("/tests/fixtures/voice-bubble.html" + (panes === 2 ? "?panes=2" : ""));
  return { bubble: page.locator(".voice-bubble"), pane: page.getByRole("region", { name: "Goblin terminal" }), asked, posts };
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

// dictate holds the shortcut until the microphone has opened, which takes a
// busy machine longer than a short hold, speaks for ms and lets go.
async function dictate(page: Page, ms = 1200) {
  await holdShortcut(page, 0);
  await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.length)).toBe(1);
  await page.waitForTimeout(ms);
  await releaseShortcut(page);
}

test("overlapping dictations reach the same terminal in capture order", async ({ page }) => {
  const replies: Route[] = [];
  const { bubble, posts } = await openPane(page, { app: true, replies });
  for (const count of [1, 2]) {
    await holdShortcut(page, 0);
    await expect(bubble).toHaveClass(/recording/);
    await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.length)).toBe(count);
    await page.waitForTimeout(1200);
    await releaseShortcut(page);
    await expect(bubble).not.toHaveClass(/recording/);
    await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.every((track) => track.readyState === "ended"))).toBe(true);
    await expect.poll(() => replies.length).toBe(count);
  }
  await replies[1].fulfill({ json: { text: "then run the tests" } });
  await expect.poll(() => page.evaluate(() => window.voiceProbe!.replies)).toBe(1);
  await expect.poll(() => page.locator("output").textContent()).toBe("");
  await replies[0].fulfill({ json: { text: "open the\npull request" } });
  await expect.poll(() => page.locator("output").textContent()).toBe("open the pull request\n then run the tests");
  expect(posts).toHaveLength(2);
  await expect(page.getByRole("textbox", { name: "Terminal input" })).toHaveValue("");
  await bubble.click();
  await expect(page.getByRole("dialog", { name: "Recent messages" }).locator(".voice-text")).toHaveText(["then run the tests", "open the pull request"]);
});

test("a dictation typed right after another starts with a space, and one after a typed key does not", async ({ page }) => {
  const replies: Route[] = [];
  const { bubble } = await openPane(page, { app: true, replies });
  const say = async (count: number, words: string) => {
    await holdShortcut(page, 0);
    await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.length)).toBe(count);
    await page.waitForTimeout(1200);
    await releaseShortcut(page);
    await expect.poll(() => replies.length).toBe(count);
    await replies[count - 1].fulfill({ json: { text: words } });
  };
  // The output shows each typing on a line of its own, exactly as typed.
  const typed = () => page.locator("output").textContent();
  await say(1, "open the pull request");
  await expect.poll(typed).toBe("open the pull request");
  await say(2, "then run the tests");
  await expect.poll(typed).toBe("open the pull request\n then run the tests");
  await page.keyboard.press("x");
  await say(3, "and merge it");
  await expect.poll(typed).toBe("open the pull request\n then run the tests\nand merge it");
  // The recent dictations keep the words alone, without the separating space.
  expect(await page.evaluate(() => (JSON.parse(localStorage.getItem("cfo-dictations-v1")!) as Record<string, { text: string }[]>)["task:voice"].map((message) => message.text)))
    .toEqual(["and merge it", "then run the tests", "open the pull request"]);
  await bubble.click();
  await expect(page.getByRole("dialog", { name: "Recent messages" }).locator(".voice-text")).toHaveText(["and merge it", "then run the tests", "open the pull request"]);
});

// What the supervisor says while the first dictation sets up its speech model.
const DOWNLOADING = (done: number) => `Dictation is being set up, once: downloading its speech model, ${done} of 125 MB, which stays on this PC. Dictate again when it is ready.`;

test("the first dictation's set-up of the speech model stays shown as it downloads, then says dictation is ready", async ({ page }) => {
  const replies: Route[] = [];
  let setup: { state: string; note?: string } = { state: "fetching", note: DOWNLOADING(0) };
  const { pane } = await openPane(page, { app: true, replies, status: () => setup });
  await dictate(page);
  await expect.poll(() => replies.length).toBe(1);
  await replies[0].fulfill({ status: 503, json: { error: DOWNLOADING(0) } });
  await expect(pane.getByRole("status")).toHaveText(DOWNLOADING(0));
  setup = { state: "fetching", note: DOWNLOADING(45) };
  await expect(pane.getByRole("status")).toHaveText(DOWNLOADING(45));
  // It stays past the time a note is shown, for as long as the set-up lasts,
  // even when the supervisor says the same for a while.
  await page.waitForTimeout(7000);
  await expect(pane.getByRole("status")).toHaveText(DOWNLOADING(45));
  setup = { state: "ready" };
  await expect(pane.getByRole("status")).toHaveText("Dictation is ready: hold Ctrl+Shift+Space and speak.");
  await expect(pane.getByRole("status")).toHaveCount(0, { timeout: 10000 });
});

test("a set-up of the speech model that fails stays shown until the next dictation", async ({ page }) => {
  const replies: Route[] = [];
  const failure = "Parakeet-tdt-110m en-36000-int8 could not be downloaded: no such host. Connect to the internet and dictate again.";
  let setup: { state: string; note?: string } = { state: "fetching", note: DOWNLOADING(0) };
  const { pane } = await openPane(page, { app: true, replies, status: () => setup });
  await dictate(page);
  await expect.poll(() => replies.length).toBe(1);
  await replies[0].fulfill({ status: 503, json: { error: DOWNLOADING(0) } });
  await expect(pane.getByRole("status")).toHaveText(DOWNLOADING(0));
  setup = { state: "missing", note: failure };
  await expect(pane.getByRole("status")).toHaveText(failure);
  await page.waitForTimeout(7000);
  await expect(pane.getByRole("status")).toHaveText(failure);
  setup = { state: "ready" };
  await holdShortcut(page, 0);
  await expect(pane.getByText(failure)).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.length)).toBe(2);
  await page.waitForTimeout(1200);
  await releaseShortcut(page);
  await expect.poll(() => replies.length).toBe(2);
  await replies[1].fulfill({ json: { text: "now it types" } });
  await expect.poll(() => page.locator("output").textContent()).toBe("now it types");
});

test("a dictation refused for another reason only shows why, and asks nothing more of the speech model", async ({ page }) => {
  let asks = 0;
  const { pane } = await openPane(page, { app: true, refusal: "Dictation needs 1 GB of free memory and 1 GB of free commit, and this PC has 0.5 GB and 3.0 GB.", status: () => { asks++; return { state: "ready" }; } });
  await dictate(page);
  await expect(pane.getByRole("status")).toHaveText("Dictation needs 1 GB of free memory and 1 GB of free commit, and this PC has 0.5 GB and 3.0 GB.");
  await page.waitForTimeout(2500);
  const asked = asks;
  await page.waitForTimeout(2500);
  expect(asks, "the page stops asking once the model is ready").toBe(asked);
  await expect(pane.getByRole("status")).toHaveCount(0, { timeout: 10000 });
});

test("a stalled dictation is canceled without blocking later words", async ({ page }) => {
  await page.clock.install({ time: new Date("2026-10-04T20:00:00Z") });
  const replies: Route[] = [], failed: Request[] = [];
  const { bubble, pane, posts } = await openPane(page, { app: true, replies });
  page.on("requestfailed", (request) => failed.push(request));
  await page.clock.pauseAt(new Date("2026-10-04T21:00:00Z"));
  for (const count of [1, 2]) {
    await holdShortcut(page, 0);
    await expect(bubble).toHaveClass(/recording/);
    await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.length)).toBe(count);
    await page.waitForTimeout(1200);
    await releaseShortcut(page);
    await expect(bubble).not.toHaveClass(/recording/);
    await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.every((track) => track.readyState === "ended"))).toBe(true);
    await expect.poll(() => replies.length).toBe(count);
  }
  await replies[1].fulfill({ json: { text: "after the stalled capture" } });
  await expect.poll(() => page.evaluate(() => window.voiceProbe!.replies)).toBe(1);
  await expect.poll(() => page.locator("output").textContent()).toBe("");
  await page.clock.fastForward(119_999);
  await expect(page.locator("output")).toHaveText("");
  expect(failed).toEqual([]);
  await expect(pane.getByRole("status")).toHaveCount(0);
  await page.clock.fastForward(1);
  await expect(pane.getByRole("status")).toHaveText("Dictation did not finish within 120 seconds. Its words were not typed.");
  await expect(page.locator("output")).toHaveText("after the stalled capture");
  await expect.poll(() => failed.includes(replies[0].request())).toBe(true);
  expect(replies[0].request().failure()?.errorText).toBe("net::ERR_ABORTED");
  await replies[0].fulfill({ json: { text: "late words must not be typed" } });
  await page.clock.fastForward(6000);
  await expect.poll(() => page.locator("output").textContent()).toBe("after the stalled capture");
  expect(posts).toHaveLength(2);
  await expect(page.getByRole("textbox", { name: "Terminal input" })).toHaveValue("");
});

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
  const { bubble, asked, posts } = await openPane(page, { hint: true });
  await page.waitForTimeout(500);
  await dictate(page);
  await expect(page.locator("output")).toHaveText("ship the voice bubble");
  await bubble.click();
  await expect(page.getByRole("dialog", { name: "Recent messages" }).locator(".voice-text")).toHaveText(["ship the voice bubble"]);
  expect(await readable(page)).not.toMatch(/siqspeak/i);
  expect(asked).toEqual([]);
  // The browser has a speech recognition here, and the board did not use it.
  expect(posts.length).toBeGreaterThan(0);
  expect(await page.evaluate(() => window.voiceProbe!.started.length)).toBe(0);
});

test.describe("in the desktop app", () => {
  // The Overlord's window: maximized on a 2560 by 1600 screen at 150 percent.
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  test("the shortcut dictates through the supervisor's speech model, as in a browser", async ({ page }, testInfo) => {
    const { bubble, pane, posts } = await openPane(page, { app: true });
    await holdShortcut(page, 0);
    await expect(bubble).toHaveClass(/recording/);
    await expect(bubble).toHaveAttribute("data-tip", "Listening with test-model on this PC · release Ctrl+Shift+Space to type");
    await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures.length)).toBe(1);
    await page.waitForTimeout(1200);
    await testInfo.attach("app-dictation-listening", { body: await pane.screenshot(), contentType: "image/png" });
    await releaseShortcut(page);
    await expect(page.locator("output")).toHaveText("ship the voice bubble");
    await expect(pane.getByRole("status")).toHaveCount(0);
    // The supervisor was handed what the microphone heard, as the board: one
    // channel of 16-bit sound at the model's rate, about as long as the hold,
    // and not silence.
    expect(posts).toHaveLength(1);
    expect(posts[0]).toMatchObject({ token: "test-instance", type: "audio/wav", tags: "RIFF WAVE fmt  data", channels: 1, rate: 16000, bits: 16 });
    expect(posts[0].samples).toBeGreaterThan(16000 * .8);
    expect(posts[0].samples).toBeLessThan(16000 * 6);
    expect(posts[0].peak).toBeGreaterThan(300);
    // The app has no browser recognition to offer.
    await bubble.click();
    const recent = page.getByRole("dialog", { name: "Recent messages" });
    await expect(recent.locator(".voice-footer")).toContainText("Heard by test-model on this PC: what you say never leaves it.");
    await expect(recent.getByRole("checkbox")).toHaveCount(0);
    await testInfo.attach("app-dictation-recent", { body: await pane.screenshot(), contentType: "image/png" });
  });
});

test("dictating asks nothing outside the board's own address, and the supervisor only for the words", async ({ page }) => {
  await openPane(page);
  const requests: string[] = [];
  page.on("request", (request) => requests.push(request.method() + " " + request.url()));
  await dictate(page);
  await expect(page.locator("output")).toHaveText("ship the voice bubble");
  expect(requests.length).toBeGreaterThan(0);
  expect(requests.filter((request) => !request.split(" ")[1].startsWith(ORIGIN + "/"))).toEqual([]);
  // Besides the words, the page asks the supervisor only which model listens,
  // once, when the pane opens.
  expect(requests.filter((request) => request.includes("/api/") && request !== "GET " + ORIGIN + "/api/dictation")).toEqual(["POST " + ORIGIN + "/api/dictation"]);
});

test("what the supervisor refuses with is shown as it wrote it, and nothing is typed", async ({ page }) => {
  const refusal = "The speech model is being downloaded, once: 45 of 103 MB. Dictate again when it is there.";
  const { pane, posts } = await openPane(page, { refusal });
  await dictate(page);
  await expect(pane.getByRole("status")).toHaveText(refusal);
  expect(posts).toHaveLength(1);
  await expect(page.locator("output")).toHaveText("");
});

test("the browser's speech recognition is used only once he turns it on, and the bubble says which is listening", async ({ page }) => {
  const { bubble, posts } = await openPane(page);
  await bubble.click();
  const recent = page.getByRole("dialog", { name: "Recent messages" });
  await expect(recent.locator(".voice-footer")).toContainText("Heard by test-model on this PC: what you say never leaves it.");
  const choice = recent.getByRole("checkbox", { name: "Use this browser's speech recognition instead" });
  await expect(choice).not.toBeChecked();
  await choice.check();
  await expect(recent.locator(".voice-footer")).toContainText("Heard by this browser's speech recognition, which sends your voice to the browser's maker.");
  await page.keyboard.press("Escape");

  await holdShortcut(page, 0);
  await expect(bubble).toHaveAttribute("data-tip", "Listening with this browser's speech recognition · release Ctrl+Shift+Space to type");
  await expect.poll(() => page.evaluate(() => window.voiceProbe!.started.length)).toBe(1);
  await releaseShortcut(page);
  await expect(page.locator("output")).toHaveText("heard by the browser");
  expect(posts).toHaveLength(0);

  // The choice is kept, and turning it off brings the board's own back.
  await page.reload();
  await bubble.click();
  await expect(choice).toBeChecked();
  await choice.uncheck();
  await page.keyboard.press("Escape");
  await dictate(page);
  await expect(page.locator("output")).toHaveText("ship the voice bubble");
  expect(posts).toHaveLength(1);
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
  const { bubble, posts } = await openPane(page);
  // Short fake-device beeps can be weak or missed between meter samples. Give
  // the real analyser and recorder one stream whose loudness this test controls.
  await page.evaluate(() => {
    navigator.mediaDevices.getUserMedia = async () => {
      const context = new AudioContext();
      const tone = context.createOscillator();
      tone.frequency.value = 440;
      const gain = context.createGain();
      gain.gain.value = 0;
      const microphone = context.createMediaStreamDestination();
      tone.connect(gain).connect(microphone);
      tone.start();
      await context.resume();
      window.voiceSignal = { context, gain };
      window.voiceProbe!.captures.push(microphone.stream.getAudioTracks()[0]);
      return microphone.stream;
    };
  });
  await holdShortcut(page, 0);
  await expect(bubble).toHaveClass(/recording/);
  await expect(bubble).toHaveAttribute("data-tip", "Listening with test-model on this PC · release Ctrl+Shift+Space to type");
  expect(await page.evaluate(() => window.voiceSignal!.context.state)).toBe("running");
  const bars = bubble.locator(".voice-bars span");
  await expect(bars).toHaveCount(9);
  const heights = () => bars.evaluateAll((bars) => bars.map((bar) => new DOMMatrix(getComputedStyle(bar).transform).d));
  // Held in silence, the bars are a flat dotted line: each a 3 px square, one
  // eighth of its 24 px bar.
  const flat = async () => { const all = await heights(); return Math.max(...all) - Math.min(...all) < .001 && Math.abs(all[0] - .125) < .001; };
  await expect.poll(flat).toBe(true);
  expect(await bars.first().evaluate((bar) => getComputedStyle(bar).width)).toBe("3px");
  // A quiet room's hum keeps the line flat; only a voice moves it.
  await page.evaluate(() => { window.voiceSignal!.gain.gain.value = .015; });
  await page.waitForTimeout(1000);
  expect(await flat(), `a hum moved the bars to ${(await heights()).join(", ")}`).toBe(true);
  const scales = new Set<number>([13]);
  for (const [volume, minimum] of [[.04, .2], [.12, .4], [.3, .8]]) {
    await page.evaluate((volume) => { window.voiceSignal!.gain.gain.value = volume; }, volume);
    await expect.poll(async () => Math.min(...await heights())).toBeGreaterThan(minimum);
    for (const height of await heights()) scales.add(Math.round(height * 100));
  }
  expect(Math.max(...scales)).toBeGreaterThan(25);
  expect(scales.size).toBeGreaterThan(2);
  await page.evaluate(() => { window.voiceSignal!.gain.gain.value = 0; });
  await expect.poll(flat).toBe(true);
  // One microphone is open, for the bars and the words alike.
  expect(await page.evaluate(() => window.voiceProbe!.captures.length)).toBe(1);

  await releaseShortcut(page);
  await expect(page.locator("output")).toHaveText("ship the voice bubble");
  expect(posts).toHaveLength(1);
  expect(posts[0].peak).toBeGreaterThan(300);
  await expect(bubble).not.toHaveClass(/recording/);
  expect(await page.evaluate(() => window.voiceProbe!.captures[0].readyState)).toBe("ended");
  await page.evaluate(() => window.voiceSignal!.context.close());

  // Idle, the bubble asks for no frames and runs no animation.
  const frames = await page.evaluate(() => window.voiceProbe!.frames);
  await page.waitForTimeout(1000);
  expect(await page.evaluate(() => window.voiceProbe!.frames)).toBe(frames);
  expect(await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running").length)).toBe(0);

  await bubble.click();
  const recent = page.getByRole("dialog", { name: "Recent messages" });
  await expect(recent.locator(".voice-text").first()).toHaveText("ship the voice bubble");
});

for (const stop of ["release outside the terminal", "window blur"]) {
  test(`during microphone setup, ${stop} cancels capture before the bubble invites speech`, async ({ page }) => {
    const { bubble, posts } = await openPane(page, { app: true });
    await page.evaluate(() => {
      const capture = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
      navigator.mediaDevices.getUserMedia = async (constraints) => {
        await new Promise<void>((resolve) => window.addEventListener("allow-microphone", () => resolve(), { once: true }));
        return capture(constraints);
      };
    });
    await holdShortcut(page, 0);
    await expect(bubble).not.toHaveClass(/recording/);
    if (stop === "window blur") await page.evaluate(() => window.dispatchEvent(new Event("blur")));
    else {
      await bubble.focus();
      await releaseShortcut(page);
    }
    await page.evaluate(() => window.dispatchEvent(new Event("allow-microphone")));
    await expect.poll(() => page.evaluate(() => window.voiceProbe!.captures[0]?.readyState)).toBe("ended");
    await expect(bubble).not.toHaveClass(/recording/);
    await expect(page.locator("output")).toHaveText("");
    expect(posts).toHaveLength(0);
    await releaseShortcut(page);
  });
}

// The desktop app's WebView2 grants the page every permission itself, so a
// microphone it is refused was refused by Windows; a browser tab's was refused
// by the browser. Each is stubbed with the refusals WebView2 and Windows give.
const WINDOWS_BLOCKED = "The microphone is blocked for Code Goblins by Windows. Turn on Microphone access and Let desktop apps access your microphone in Windows Settings > Privacy & security > Microphone, then hold Ctrl+Shift+Space again.";
const BROWSER_BLOCKED = "The microphone is blocked for the board. Allow it in the browser's site settings, then hold Ctrl+Shift+Space again.";
for (const [where, app, expected] of [["the desktop app", true, WINDOWS_BLOCKED], ["a browser", false, BROWSER_BLOCKED]] as const) {
  for (const refusal of ["Permission denied", "Permission denied by system"]) {
    test(`in ${where}, a microphone refused with "${refusal}" says where to allow it`, async ({ page }) => {
      const { pane, posts } = await openPane(page, { app });
      await page.evaluate((refusal) => {
        navigator.mediaDevices.getUserMedia = async () => { throw new DOMException(refusal, "NotAllowedError"); };
      }, refusal);
      await holdShortcut(page, 0);
      await expect(pane.getByRole("status")).toHaveText(expected);
      await releaseShortcut(page);
      await expect(page.locator("output")).toHaveText("");
      expect(posts).toHaveLength(0);
    });
  }
}
