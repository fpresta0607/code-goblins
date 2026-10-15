import { expect, openItem, servePages, test, type Browser, type Page } from "./site";

// The Overlord, 2026-10-15 about 00:10Z: "dictation in command center never
// pasted nor did it save to recent messgaes in main dictaiton they hsould all
// be tied evrey window". Every dictation, from any box in any window, joins
// the one list of recent dictations the supervisor keeps, which every window
// lists: a browser tab and the desktop window keep no storage in common.

const LAUNCH = { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] };
test.use({ timezoneId: "UTC", viewport: { width: 1440, height: 900 }, launchOptions: LAUNCH });

const AT = "2026-10-15T00:10:00Z";
// The CFO asks a question in the Command Center, and Bernie, paused, has the
// message box under his panel.
const SNAPSHOT = {
  healthy: true, instance: "everywhere", revision: 1, cfo_runs: true, actions: [],
  questions: [{ id: "notify-cfo-1", identity: "a".repeat(64), task: "", text: "Which train goes first?", options: ["The board train", "The docs train"], recommended: "The board train", created_at: AT, status: "pending" }],
  tasks: [{
    id: "bernie", title: "Land the merge train", goblin_name: "Bernie", goblin_title: "Merge Maestro", project: "code-goblins", backend: "native", harness: "claude",
    generation: "s1", session: "claude/bernie", phase: "paused", verified: false, at: AT, report: "working",
    lifecycle: { phase: "paused", action: "pause", at: AT, kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "overlord", until: "", at: AT }, kept_messages: [] },
  }],
};

// The supervisor, as every window reaches it: its speech model hears the
// next of words in each sound, and it keeps one list of recent dictations.
function supervisor(words: string[]) {
  const kept: { text: string; at: number }[] = [];
  let heard = 0;
  const serve = async (page: Page) => {
    await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => { socket.send(JSON.stringify({ type: "history", bytes: 0 })); });
    await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
    await page.route("**/api/dictation", (route) => route.request().method() === "GET"
      ? route.fulfill({ json: { engine: "test-model", state: "ready" } })
      : route.fulfill({ json: { text: words[heard++], engine: "test-model" } }));
    await page.route("**/api/dictations", (route) => {
      const request = route.request();
      if (request.method() === "POST") kept.unshift({ text: (request.postDataJSON() as { text: string }).text, at: Date.now() });
      return route.fulfill({ json: { messages: kept.slice(0, 10) } });
    });
  };
  return { kept, serve };
}

// openWindow opens the board in a window of its own, with storage of its own, the
// desktop window when app is set, and serves it the snapshot.
async function openWindow(browser: Browser, serve: (page: Page) => Promise<void>, app = false) {
  const context = await browser.newContext({ permissions: ["microphone"] });
  await servePages(context);
  const page = await context.newPage();
  await serve(page);
  await page.addInitScript(({ snapshot, app }) => {
    localStorage.setItem("cfo-first-open", "shown");
    localStorage.setItem("cfo-voice-hint-v1", "dismissed");
    if (app) (window as unknown as { chrome: object }).chrome = { webview: { postMessage: () => {} } };
    class SnapshotSource extends EventTarget {
      constructor() {
        super();
        queueMicrotask(() => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(snapshot) })));
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: SnapshotSource });
  }, { snapshot: SNAPSHOT, app });
  await page.goto("/");
  return page;
}

// hold presses on the microphone for a moment, as he does.
async function hold(page: Page, microphone: ReturnType<Page["getByRole"]>) {
  await microphone.hover();
  await page.mouse.down();
  await expect(page.getByRole("button", { name: "Listening" })).toHaveClass(/recording/);
  await page.waitForTimeout(1200);
  await page.mouse.up();
}

// shortcut holds Ctrl+Shift+Space where the keyboard is.
async function shortcut(page: Page) {
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.down("Space");
  await page.waitForTimeout(1200);
  await page.keyboard.up("Space");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");
}

// cfoTerminal opens the CFO's terminal, where the main dictation lists the
// recent dictations.
async function cfoTerminal(page: Page) {
  await page.getByRole("button", { name: "Open the CFO's terminal" }).first().click();
  const terminal = page.getByRole("textbox", { name: "Terminal input" });
  await expect(terminal).toHaveCount(1);
  return terminal;
}

async function recentIn(page: Page) {
  await cfoTerminal(page);
  await page.getByRole("button", { name: "Recent messages" }).click();
  return page.getByRole("dialog", { name: "Recent messages" }).locator(".voice-text");
}

test("a dictation from each box in one window shows in the one recent list of every window", async ({ browser }) => {
  // Arrange
  const words = ["the board train first", "rebase before the train", "merge it when green"];
  const { kept, serve } = supervisor(words);
  const tab = await openWindow(browser, serve);
  const desktop = await openWindow(browser, serve, true);

  // Act: the Command Center's box, by the shortcut right after choosing Other.
  await openItem(tab, "Which train goes first?");
  const card = tab.getByRole("dialog");
  await card.getByRole("radio", { name: /^Other/ }).check();
  await shortcut(tab);
  await expect(card.getByRole("textbox", { name: "Your written answer" })).toHaveValue(words[0]);
  await card.getByRole("button", { name: "Close the Command Center" }).click();

  // Act: the message box under a goblin, by its microphone.
  await tab.locator(".task-card").filter({ hasText: "Bernie" }).click();
  const box = tab.getByRole("region", { name: "Messages to Bernie - Merge Maestro" });
  await hold(tab, box.getByRole("button", { name: "Hold to dictate" }));
  await expect(box.getByRole("textbox", { name: "Message Bernie - Merge Maestro" })).toHaveValue(words[1]);

  // Act: the CFO's terminal in the desktop window, by the shortcut.
  await (await cfoTerminal(desktop)).focus();
  await shortcut(desktop);
  await expect.poll(() => kept.length).toBe(3);

  // Assert: the supervisor keeps all three, and each window lists them all.
  expect(kept.map((message) => message.text)).toEqual([...words].reverse());
  await expect(await recentIn(desktop)).toHaveText([...words].reverse());
  await expect(await recentIn(tab)).toHaveText([...words].reverse());
});
