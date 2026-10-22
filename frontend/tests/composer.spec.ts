import { expect, test, type Page } from "./site";

// The Overlord chose design A on 2026-10-22: a Claude Code session's panel
// opens on its Messages view, and the box under the messages takes the place
// of the terminal's input. Send types what he wrote through the terminal's own
// view, as one paste and an Enter, and what keeps it from the terminal says so
// beside the box, his draft kept. The supervisor is played here: the board's
// snapshot and each session's messages on held streams, the speech model, and
// the terminal's relay, which records what reached the terminal, counts the
// sizes the board asks for and tells the view what a relay tells it. Every
// conversation is made up.
const at = "2026-10-22T09:00:00Z";
// Chromium's fake microphone (periodic beeps) stands in for the Overlord's,
// which the speech model played here hears as words.
test.use({ permissions: ["microphone"], launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });
const GOBLIN = {
  id: "cg-touch", title: "Swipes scroll the phone terminal; Claude Code", goblin_name: "Zeke", goblin_title: "Touch Wrangler", project: "code-goblins", phase: "working", verified: false,
  generation: "cg-touch-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", since: at,
};
const OTHER = { ...GOBLIN, id: "cg-other", title: "Another goblin; Claude Code", goblin_name: "Ivy", goblin_title: "Window Painter", generation: "cg-other-1" };
const snapshot = (fields: Record<string, unknown> = {}, goblin: Record<string, unknown> = {}) => ({
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, cfo_terminal: "cfo-term", cfo_harness: "claude", afk: { state: "off" }, tasks: [{ ...GOBLIN, ...goblin }, OTHER], ...fields,
});
const PAGE = { messages: [{ id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "Read the brief" }, { id: "m1", seq: 1, at, kind: "reply", blocks: [{ kind: "text", text: "On it." }] }], more: false };
const NAME = "Zeke - Touch Wrangler";
// A paste in a program that turned bracketed paste on.
const pasted = (text: string) => "\x1b[200~" + text + "\x1b[201~";

interface Relay {
  // Every view's typing in order, its resizes, and how many views connected.
  typed: string[];
  resizes: { cols: number; rows: number }[];
  views: number;
  // say tells the newest view a relay message; drop closes it as a relay
  // that went away does, which the view reconnects from.
  say: (message: object) => void;
  drop: () => void;
}

async function open(page: Page, first: object = snapshot(), heard = "rebase onto main"): Promise<Relay> {
  const relay: Relay = { typed: [], resizes: [], views: 0, say: () => {}, drop: () => {} };
  await page.addInitScript(() => {
    if (!localStorage.getItem("cfo-first-open")) localStorage.setItem("cfo-first-open", "shown");
    localStorage.setItem("cfo-voice-hint-v1", "dismissed");
  });
  await page.addInitScript(({ data, messages }) => {
    // The board's snapshot stream, which a test sends a later snapshot down,
    // and each session's message stream.
    class Stream extends EventTarget {
      static CONNECTING = 0; static OPEN = 1; static CLOSED = 2;
      onerror: (() => void) | null = null;
      readyState = 1;
      private publish = (event: Event) => { if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) })); };
      constructor(address: string | URL) {
        super();
        const url = new URL(address, location.href);
        if (url.pathname === "/api/messages") {
          setTimeout(() => this.dispatchEvent(new MessageEvent("page", { data: JSON.stringify(messages) })));
          return;
        }
        window.addEventListener("fixture-snapshot", this.publish);
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(data) })));
      }
      close() { this.readyState = 2; window.removeEventListener("fixture-snapshot", this.publish); }
    }
    Object.defineProperty(window, "EventSource", { value: Stream });
  }, { data: first, messages: PAGE });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
  await page.route("**/api/dictation", (route) => route.request().method() === "GET" ? route.fulfill({ json: { engine: "test-model", state: "ready" } }) : route.fulfill({ json: { text: heard, engine: "test-model" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    relay.views++;
    relay.say = (message) => socket.send(JSON.stringify(message));
    relay.drop = () => socket.close({ code: 1001 });
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") { relay.typed.push(data.toString("utf8")); return; }
      const sent: unknown = JSON.parse(data);
      if (typeof sent !== "object" || sent === null || !("type" in sent) || sent.type !== "resize" || !("cols" in sent) || !("rows" in sent)) return;
      const size = { cols: Number(sent.cols), rows: Number(sent.rows) };
      relay.resizes.push(size);
      socket.send(JSON.stringify({ type: "size", ...size }));
      // A program that turned bracketed paste on, as Claude Code does.
      socket.send(Buffer.from("\x1b[?2004h> ready\r\n"));
    });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  return relay;
}

const card = (page: Page, name: string) => page.locator(".task-board .task-card-shell").filter({ hasText: name }).locator(".task-card");
const view = (page: Page, name: string) => page.locator(".context-pane .panel-pill").getByRole("button", { name, exact: true });
const box = (page: Page, name = NAME) => page.getByRole("region", { name: "Messages to " + name });
const field = (page: Page, name = NAME) => box(page, name).getByRole("textbox", { name: "Message " + name });
const send = (page: Page, name = NAME) => box(page, name).getByRole("button", { name: "Send", exact: true });
const typed = (relay: Relay) => relay.typed.join("");

// openGoblin opens Zeke's panel from his card, on the Messages view, with
// its terminal whole behind it.
async function openGoblin(page: Page) {
  await card(page, NAME).click();
  await expect(page.locator("#panel-title")).toHaveText(NAME);
  await expect(view(page, "Messages")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
}

test("Send types what he wrote into the session's terminal once, as one paste, then Enter, and empties the box", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);

  await field(page).fill("fix the swipe bug");
  await send(page).click();

  await expect.poll(() => typed(relay)).toContain(pasted("fix the swipe bug") + "\r");
  expect(typed(relay).split(pasted("fix the swipe bug")).length - 1, "typed once").toBe(1);
  await expect(field(page)).toHaveValue("");
  await field(page).fill("and test it");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toContain(pasted("and test it") + "\r");
});

test("Shift+Enter starts a new line, and the lines go as one paste", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);

  await field(page).click();
  await page.keyboard.type("first line");
  await page.keyboard.press("Shift+Enter");
  await page.keyboard.type("second line");
  await expect(field(page)).toHaveValue("first line\nsecond line");
  await page.keyboard.press("Enter");

  await expect.poll(() => typed(relay)).toContain(pasted("first line\rsecond line") + "\r");
});

test("a busy program, a reconnecting terminal and typing a gate holds each say so beside the box, and his draft stays, never sent late", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  const note = box(page).locator(".composer-note");

  relay.say({ type: "unread", unread: true });
  await expect(note).toHaveText("Program busy. Typed keys are waiting.");
  relay.say({ type: "unread", unread: false });
  await expect(note).toHaveText("");
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await field(page).fill("merge it");
  await expect(note).not.toHaveText("");
  await expect(send(page)).toBeDisabled();
  relay.say({ type: "held", reason: "" });
  await expect(send(page)).toBeEnabled();

  // Typing reaches a view only while it is connected, so the Send is made
  // while the view reconnects, before the relay answers its new connection.
  await page.routeWebSocket("**/api/terminal/native?*", () => {});
  relay.drop();
  await expect(page.locator(".terminal-reconnecting")).toHaveCount(1);
  const before = typed(relay);
  await send(page).click();

  await expect(note).toHaveText(/^Not sent\. The terminal is (reconnecting|connecting)\.$/);
  await expect(field(page)).toHaveValue("merge it");
  await page.waitForTimeout(1500);
  expect(typed(relay)).toBe(before);
});

test("while the harness asks in its terminal an amber card says so and Send waits, and Answer in the terminal shows the terminal with the keyboard in it", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("yes");

  relay.say({ type: "screen", asking: true });

  const asking = box(page).locator(".composer-asking");
  await expect(asking).toContainText("Waiting on you in the terminal");
  await expect(send(page)).toBeDisabled();
  await asking.getByRole("button", { name: "Answer in the terminal" }).click();
  await expect(view(page, "Terminal")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeFocused();
  expect(typed(relay), "nothing was typed into the harness's own screen").not.toContain("yes");
  relay.say({ type: "screen", asking: false });
  await view(page, "Messages").click();
  await expect(box(page).locator(".composer-asking")).toHaveCount(0);
  await expect(send(page)).toBeEnabled();
});

test("while the harness works the working line's Stop presses Escape in its terminal", async ({ page }) => {
  const relay = await open(page, snapshot({}, { runtime: { state: "busy", reason: "", at } }));
  await openGoblin(page);
  const working = box(page).locator(".composer-working");
  await expect(working).toContainText("Working");

  await working.getByRole("button", { name: "Stop" }).click();

  await expect.poll(() => typed(relay)).toBe("\x1b");
});

test("his draft stays through a switch of views and of sessions, a reconnect and a reload of the board, as an update reloads it", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("half a thought");

  await view(page, "Terminal").click();
  await view(page, "Messages").click();
  await expect(field(page)).toHaveValue("half a thought");
  await card(page, "Ivy - Window Painter").click();
  await expect(page.locator("#panel-title")).toHaveText("Ivy - Window Painter");
  await card(page, NAME).click();
  await expect(field(page)).toHaveValue("half a thought");
  relay.drop();
  await expect.poll(() => relay.views).toBe(2);
  await expect(field(page)).toHaveValue("half a thought");
  await page.reload();
  await card(page, NAME).click();

  await expect(field(page)).toHaveValue("half a thought");
});

test("a session opens on its Messages view, then on the view he last chose, and a switch hands that view the keyboard", async ({ page }) => {
  await open(page);
  await openGoblin(page);
  await expect(field(page)).not.toBeFocused();
  await page.keyboard.press("Control+Alt+3");
  await expect(page.locator("#panel-title")).toHaveText("Ivy - Window Painter");
  await expect(field(page, "Ivy - Window Painter")).toBeFocused();

  await view(page, "Terminal").click();
  await card(page, NAME).click();
  await expect(view(page, "Terminal")).toHaveAttribute("aria-pressed", "true");
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeFocused();
  await page.reload();
  await card(page, NAME).click();

  await expect(view(page, "Terminal")).toHaveAttribute("aria-pressed", "true");
});

test("the terminal behind the messages takes its size unseen, so a switch to it asks for none", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  let seen = -1;
  await expect.poll(() => { const isQuiet = relay.resizes.length > 0 && relay.resizes.length === seen; seen = relay.resizes.length; return isQuiet; }, { intervals: [500] }).toBe(true);

  for (const name of ["Terminal", "Messages", "Terminal"]) {
    await view(page, name).click();
    await page.waitForTimeout(300);
  }

  expect(relay.resizes.length).toBe(seen);
});

test.describe("dictation into the box", () => {
  test("holding Ctrl+Shift+Space in the box dictates into it, and sends nothing by itself", async ({ page }) => {
    const relay = await open(page);
    await openGoblin(page);
    await field(page).fill("please");
    await field(page).focus();

    await page.keyboard.down("Control");
    await page.keyboard.down("Shift");
    await page.keyboard.down("Space");
    await page.waitForTimeout(1200);
    await page.keyboard.up("Space");
    await page.keyboard.up("Shift");
    await page.keyboard.up("Control");

    await expect(field(page)).toHaveValue("please rebase onto main");
    expect(typed(relay)).toBe("");
  });

  test.describe("on a touch screen", () => {
    test.use({ viewport: { width: 412, height: 915 }, hasTouch: true, isMobile: true });

    test("a tap on the box's microphone starts dictating, and the next adds what was said, sending nothing by itself", async ({ page }) => {
      const relay = await open(page);
      await card(page, NAME).tap();
      await expect(view(page, "Messages")).toHaveAttribute("aria-pressed", "true");

      await box(page).getByRole("button", { name: "Dictate" }).tap();
      await expect(box(page).getByRole("button", { name: "Stop dictating" })).toHaveClass(/recording/);
      await page.waitForTimeout(1200);
      await box(page).getByRole("button", { name: "Stop dictating" }).tap();

      await expect(field(page)).toHaveValue("rebase onto main");
      expect(typed(relay)).toBe("");
    });
  });

  test("the recent dictations paste into the box", async ({ page }) => {
    await open(page);
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [{ text: "check the phone spec", at: Date.parse("2026-10-22T09:30:00Z") }] } }));
    await openGoblin(page);

    await box(page).getByRole("button", { name: "Recent messages" }).click();
    await page.getByRole("dialog", { name: "Recent messages" }).getByRole("button", { name: "Paste into the message: check the phone spec" }).click();

    await expect(field(page)).toHaveValue("check the phone spec");
  });
});
