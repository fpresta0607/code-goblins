import { expect, test, type Locator, type Page } from "./site";

// The panel shows a Claude Code session's conversation as the supervisor
// reads it (GET /api/messages), in the box the terminal fills, so stepping
// the terminal in and out never resizes it. The
// supervisor is played here: the board's snapshot and each session's message
// stream on held streams, older pages and pictures from routes, and the
// terminal's relay, which counts the sizes the board asks for. Every
// conversation is made up.
const at = "2026-10-22T09:00:00Z";
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");

const GOBLIN = {
  id: "cg-touch", title: "Swipes scroll the phone terminal; Claude Code", goblin_name: "Zeke", goblin_title: "Touch Wrangler", project: "code-goblins", phase: "working", verified: false,
  generation: "cg-touch-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", since: at,
  tree: { task_id: "cg-touch", generation: "cg-touch-1", harness: "claude", children: [{ id: "subagent:toolu_A", kind: "subagent", label: "Find the touch handlers", detail: "Explore", task: "Find the touch handlers", state: "done", started: at, finished: at }] },
};
const CODEX = { id: "cg-codex", title: "A Codex task", project: "code-goblins", phase: "working", verified: false, generation: "cg-codex-1", harness: "codex", backend: "native", since: at };
const SNAPSHOT = { healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, cfo_terminal: "cfo-term", cfo_harness: "claude", afk: { state: "off" }, tasks: [GOBLIN, CODEX] };

const message = (seq: number, fields: Record<string, unknown>) => ({ id: "m" + seq, seq, at, kind: "prompt", ...fields });
const reply = (seq: number, ...blocks: Record<string, unknown>[]) => message(seq, { kind: "reply", blocks });

// The goblin's conversation, one of every kind of message the view draws.
const TALK = [
  message(0, { source: "cfo", text: "Read the brief at brief.md and follow it exactly." }),
  reply(1, { kind: "text", text: "I'll read the brief, then the phone terminal code." }, { kind: "call", tool: "Read", subject: "frontend/src/terminalView.ts", state: "done", result: "1\timport { Terminal } from \"@xterm/xterm\";" }),
  reply(2, { kind: "thinking", text: "The swipe lands on the text box." },
    { kind: "text", text: "**Found the scroll bug.** On a touch screen the text box lies over the whole screen.\n\n- Hand a vertical swipe to the history\n- Keep a long press for Paste\n\n```ts\nconst swipe = true;\n```\n\n| Item | State |\n| --- | --- |\n| S11 | fixed |" },
    { kind: "call", tool: "Edit", subject: "frontend/src/terminalView.ts", state: "done", edit: { added: 1, removed: 1, diff: ["  this.element.addEventListener(\"pointerdown\", this.startCopy);", "+  this.element.addEventListener(\"touchmove\", this.swipe);", "-  // A swipe was left to the page."] } },
    { kind: "call", tool: "Agent", subject: "Find the touch handlers", state: "done", agent: { node: "subagent:toolu_A", type: "Explore" } }),
  message(3, { source: "overlord", text: "here's what it looks like on my phone", pictures: [{ id: "pic1", media_type: "image/png", bytes: PNG.length }] }),
  message(4, { source: "answer", text: "Ship it once the phone spec passes" }),
  message(5, { source: "slash", text: "/cost", output: "Total cost: $0.12" }),
  message(6, { source: "shell", text: "git status", output: "nothing to commit" }),
  message(7, { kind: "compaction", text: "The work so far: the swipe fix.", trigger: "auto" }),
  message(8, { kind: "interrupt", text: "[Request interrupted by user]" }),
  message(9, { source: "notice", text: "Background command finished" }),
  reply(10, { kind: "text", text: "That matches. Running the phone spec now." }, { kind: "call", tool: "Bash", subject: "npx playwright test phone-paste.spec.ts", state: "running" }, { kind: "call", tool: "Read", subject: "missing.ts", state: "failed", result: "Error: File does not exist." }),
];

// The CFO's conversation, long enough to read up in: its newest page here,
// and the page before it on request.
const exchange = (seq: number) => seq % 2
  ? reply(seq, { kind: "text", text: `Answer ${seq}\n\nIt runs over a few lines\nso the page has height to read up in.` })
  : message(seq, { source: "overlord", text: "Question " + seq });
const range = (from: number, to: number) => Array.from({ length: to - from }, (_, at) => exchange(from + at));
const NEWEST = { messages: range(50, 100), more: true };
const OLDER = { messages: range(0, 50), more: false };

interface Board { resizes: { cols: number; rows: number }[]; older: string[]; say: (message: object) => void }

async function open(page: Page): Promise<Board> {
  const board: Board = { resizes: [], older: [], say: () => {} };
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  // No WebGL, so a baby goblin's terminal draws its text with xterm's DOM
  // renderer, where a test reads it.
  await page.addInitScript(() => {
    const getContext = HTMLCanvasElement.prototype.getContext as (this: HTMLCanvasElement, kind: string, options?: unknown) => RenderingContext | null;
    Object.defineProperty(HTMLCanvasElement.prototype, "getContext", { value(this: HTMLCanvasElement, kind: string, options?: unknown) { return kind === "webgl2" ? null : getContext.call(this, kind, options); } });
  });
  await page.addInitScript(({ snapshot, pages }) => {
    // Every stream the board opens: the snapshot's, each session's messages,
    // and a baby goblin's output. A test sends a message stream an event.
    const streams: { url: URL; source: EventTarget & { readyState: number } }[] = [];
    Object.assign(window, { messageStreams: streams });
    class Stream extends EventTarget {
      static CONNECTING = 0; static OPEN = 1; static CLOSED = 2;
      onerror: (() => void) | null = null;
      readyState = 1;
      constructor(address: string | URL) {
        super();
        const url = new URL(address, location.href);
        const send = (type: string, data: unknown) => setTimeout(() => this.dispatchEvent(new MessageEvent(type, { data: typeof data === "string" ? data : JSON.stringify(data) })));
        if (url.pathname === "/api/messages") {
          streams.push({ url, source: this });
          send("page", url.searchParams.get("cfo") ? pages.cfo : pages.goblin);
        } else if (url.pathname.endsWith("/agent")) send("output", { entries: [{ kind: "prompt", text: "Find the touch handlers" }, { kind: "reply", text: "Found them in terminal-touch.ts" }] });
        else send("snapshot", snapshot);
      }
      close() { this.readyState = 2; }
    }
    Object.defineProperty(window, "EventSource", { value: Stream });
    window.addEventListener("fixture-stream", (event) => {
      const { type, data } = (event as CustomEvent<{ type: string; data: unknown }>).detail;
      for (const { source } of streams) if (source.readyState === 1) source.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }));
    });
  }, { snapshot: JSON.stringify(SNAPSHOT), pages: { cfo: NEWEST, goblin: { messages: TALK, more: false } } });
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/messages" && url.searchParams.get("token") === "fixture" && url.searchParams.get("picture") === "pic1") await route.fulfill({ body: PNG, contentType: "image/png" });
    else if (url.pathname === "/api/messages" && url.searchParams.has("before")) {
      board.older.push(url.searchParams.get("before")!);
      await route.fulfill({ json: OLDER });
    } else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    board.say = (message) => socket.send(JSON.stringify(message));
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") return;
      const sent: unknown = JSON.parse(data);
      if (typeof sent !== "object" || sent === null || !("type" in sent) || sent.type !== "resize" || !("cols" in sent) || !("rows" in sent)) return;
      const size = { cols: Number(sent.cols), rows: Number(sent.rows) };
      board.resizes.push(size);
      socket.send(JSON.stringify({ type: "size", ...size }));
      socket.send(Buffer.from("ready\r\n"));
    });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  return board;
}

const pill = (page: Page) => page.locator(".context-pane .panel-pill");
const list = (page: Page, name: string) => page.getByRole("log", { name: "Messages of " + name });
const send = (page: Page, type: string, data: unknown) => page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-stream", { detail })), { type, data });
const clipboard = async (page: Page) => (await page.evaluate(() => navigator.clipboard.readText())).replaceAll("\r\n", "\n");
const streams = (page: Page) => page.evaluate(() => (window as unknown as { messageStreams: unknown[] }).messageStreams.length);
const olderPage = (url: URL) => url.pathname === "/api/messages" && url.searchParams.has("before");

async function openCfo(page: Page) {
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CFO");
}

async function openGoblin(page: Page) {
  await page.locator(".task-board .task-card-shell").filter({ hasText: "Zeke - Touch Wrangler" }).locator(".task-card").click();
  await expect(page.locator("#panel-title")).toHaveText("Zeke - Touch Wrangler");
}

// settled waits until the terminal stops asking for sizes, as it asks again
// once its font loads, and returns how many it asked for.
async function settled(board: Board): Promise<number> {
  let seen = -1;
  await expect.poll(() => {
    const isQuiet = board.resizes.length > 0 && board.resizes.length === seen;
    seen = board.resizes.length;
    return isQuiet;
  }, { intervals: [500] }).toBe(true);
  return seen;
}

// Where a message's top lies below the list's top, which reading up keeps,
// and how far it moved from where it was, to the subpixel a scroll rounds to.
const moved = async (box: Locator, id: string, was: number) => Math.abs(await offset(box, id) - was);
const offset = (box: Locator, id: string) => box.evaluate((element, id) => element.querySelector(`[data-message="${id}"]`)!.getBoundingClientRect().top - element.getBoundingClientRect().top, id);
const scroll = (box: Locator) => box.evaluate((element) => ({ top: element.scrollTop, end: element.scrollHeight - element.clientHeight }));
const scrollTo = (box: Locator, top: number) => box.evaluate((element, top) => new Promise<void>((done) => {
  element.addEventListener("scroll", () => done(), { once: true });
  element.scrollTop = top;
}), top);

test("a Claude Code session's panel is one view with its messages, and a Codex goblin's shows its terminal", async ({ page }) => {
  await open(page);

  await openCfo(page);
  await expect(pill(page)).toHaveCount(0);
  await expect(list(page, "CFO")).toBeVisible();
  await openGoblin(page);
  await expect(list(page, "Zeke - Touch Wrangler")).toBeVisible();
  await page.locator(".task-board .task-card-shell").filter({ hasText: "A Codex task" }).locator(".task-card").click();

  await expect(pill(page)).toHaveCount(0);
  await expect(page.locator(".messages-view")).toHaveCount(0);
});

test("the Messages view draws the conversation as the session recorded it, under the terminal's compact header", async ({ page }) => {
  await open(page);
  await openGoblin(page);

  const talk = list(page, "Zeke - Touch Wrangler");
  await expect(talk).toBeVisible();
  await expect(page.locator(".panel-header.compact")).toHaveCount(1);
  await expect(talk.locator(".message-prompt.cfo .message-from")).toHaveText("CFO");
  await expect(talk.locator(".message-prompt.cfo .message-bubble")).toHaveText("Read the brief at brief.md and follow it exactly.");
  await expect(talk.locator(".message-reply .message-from")).toHaveText(["Zeke - Touch Wrangler", "Zeke - Touch Wrangler"]);
  await expect(talk.locator("strong").filter({ hasText: "Found the scroll bug." })).toBeVisible();
  await expect(talk.locator(".message-words li")).toHaveText(["Hand a vertical swipe to the history", "Keep a long press for Paste"]);
  await expect(talk.locator(".message-code")).toHaveText("const swipe = true;");
  await expect(talk.locator(".message-table td")).toHaveText(["S11", "fixed"]);
  await expect(talk.locator(".message-thinking")).not.toHaveAttribute("open");
  await expect(talk.locator(".message-thinking > summary")).toHaveText("Thinking");
  await expect(talk.locator(".message-call").filter({ hasText: "Edit" }).locator(".call-end")).toHaveText("+1−1");
  await expect(talk.locator(".message-call").filter({ hasText: "npx playwright test" }).locator(".call-end")).toHaveText("Running");
  await expect(talk.locator(".message-call").filter({ hasText: "missing.ts" }).locator(".call-end")).toHaveText("Failed");
  await expect(talk.locator(".message-prompt.overlord .message-from").first()).toHaveText("You");
  await expect.poll(() => talk.locator(".message-prompt.overlord img").evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth)).toBe(1);
  await expect(talk.locator(".message-prompt.answer .message-from")).toHaveText("Command Center answer");
  await expect(talk.locator(".message-prompt.slash .message-bubble")).toHaveText("/cost");
  await expect(talk.locator(".message-prompt.slash .message-output")).toHaveText("Total cost: $0.12");
  await expect(talk.locator(".message-prompt.shell .message-bubble")).toHaveText("!git status");
  await expect(talk.locator(".message-compaction summary")).toHaveText("Compacted automatically");
  await expect(talk.locator(".message-interrupt")).toHaveText("Interrupted");
  await expect(talk.locator(".message-notice")).toHaveText("Background command finished");
});

test("a call's result, an edit's diff and thinking unfold under their rows", async ({ page }) => {
  await open(page);
  await openGoblin(page);
  const talk = list(page, "Zeke - Touch Wrangler");

  await talk.locator(".message-call").filter({ hasText: "Read" }).filter({ hasText: "terminalView.ts" }).locator("summary").click();
  await talk.locator(".message-call").filter({ hasText: "Edit" }).locator("summary").click();
  await talk.locator(".message-thinking > summary").click();

  await expect(talk.locator(".call-result").first()).toHaveText("1\timport { Terminal } from \"@xterm/xterm\";");
  await expect(talk.locator(".message-diff > .line-added")).toHaveText("+  this.element.addEventListener(\"touchmove\", this.swipe);");
  await expect(talk.locator(".message-diff > .line-removed")).toHaveText("-  // A swipe was left to the page.");
  await expect(talk.locator(".message-thinking .message-words")).toHaveText("The swipe lands on the text box.");
});

test("a baby goblin's row opens that baby goblin's own terminal", async ({ page }) => {
  await open(page);
  await openGoblin(page);
  const row = list(page, "Zeke - Touch Wrangler").locator(".message-baby");
  await expect(row).toContainText("Zeke Jr. - ");
  await expect(row).toContainText("Explore · Find the touch handlers");

  await row.getByRole("button", { name: "Open" }).click();

  await expect(page.getByRole("log", { name: /^Terminal of Zeke Jr\./ })).toContainText("Found them in terminal-touch.ts");
});

test("a message that changes takes its place, and a new one shows at the end he reads", async ({ page }) => {
  await open(page);
  await openCfo(page);
  const talk = list(page, "CFO");
  await expect(talk.locator("[data-message]")).toHaveCount(50);
  await expect.poll(async () => { const { top, end } = await scroll(talk); return end - top; }).toBeLessThanOrEqual(1);

  await send(page, "messages", [{ ...exchange(99), blocks: [{ kind: "text", text: "Answer 99, finished" }] }, exchange(100), exchange(101)]);

  await expect(talk.locator('[data-message="m99"]')).toHaveText(/Answer 99, finished/);
  await expect(talk.locator("[data-message]").last()).toContainText("Answer 101");
  await expect.poll(async () => { const { top, end } = await scroll(talk); return end - top; }).toBeLessThanOrEqual(1);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
});

test("reading up, new and changed messages move nothing, and Jump to latest returns to the end", async ({ page }) => {
  await open(page);
  await openCfo(page);
  const talk = list(page, "CFO");
  await expect(talk.locator("[data-message]")).toHaveCount(50);
  const { end } = await scroll(talk);
  await scrollTo(talk, Math.round(end / 2));
  const read = await talk.evaluate((element) => [...element.querySelectorAll<HTMLElement>("[data-message]")].find((row) => row.getBoundingClientRect().top >= element.getBoundingClientRect().top)!.dataset.message!);
  const before = await offset(talk, read);

  // One message above where he reads grows, and two arrive below it.
  await send(page, "messages", [{ ...exchange(51), blocks: [{ kind: "text", text: "Answer 51\n\nnow\nmuch\nlonger\nthan\nit\nwas" }] }, exchange(100), exchange(101)]);

  await expect(talk.locator("[data-message]")).toHaveCount(52);
  await expect.poll(() => moved(talk, read, before)).toBeLessThan(2);
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect.poll(async () => { const { top, end } = await scroll(talk); return end - top; }).toBeLessThanOrEqual(1);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
});

test("older messages are read a page at a time as he reads up, and his place is kept", async ({ page }) => {
  const board = await open(page);
  // The older page waits until his place is measured, so the place is the
  // one he read before it arrived.
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route(olderPage, async (route) => {
    await held;
    await route.fallback();
  });
  await openCfo(page);
  const talk = list(page, "CFO");
  await expect(talk.locator("[data-message]")).toHaveCount(50);
  expect(board.older, "nothing older is read while he reads the end").toEqual([]);

  await scrollTo(talk, 0);
  const before = await offset(talk, "m50");
  release();

  await expect(talk.locator("[data-message]")).toHaveCount(100);
  expect(board.older).toEqual(["50"]);
  await expect.poll(() => moved(talk, "m50", before)).toBeLessThan(2);
  await scrollTo(talk, 0);
  await page.waitForTimeout(300);
  expect(board.older, "no page is asked for once none is older").toEqual(["50"]);
  await expect(talk.locator("[data-message]").first()).toHaveText(/Question 0/);
});

test("an older page that fails to arrive goes to the CFO and is asked for again on his next approach to the top five seconds later", async ({ page }) => {
  await page.clock.install();
  const board = await open(page);
  let failures = 0;
  await page.route(olderPage, async (route) => {
    if (failures) return route.fallback();
    failures++;
    await route.fulfill({ status: 503, json: { error: "The supervisor is restarting" } });
  });
  await openCfo(page);
  const talk = list(page, "CFO");
  await expect(talk.locator("[data-message]")).toHaveCount(50);
  const reported = page.waitForRequest((request) => new URL(request.url()).pathname === "/api/cfo/report");

  await scrollTo(talk, 0);
  await reported;
  await scrollTo(talk, 300);
  await scrollTo(talk, 0);
  await page.waitForTimeout(300);
  expect(board.older, "no page is asked for again sooner than five seconds after the failure").toEqual([]);
  await page.clock.fastForward(5000);
  await scrollTo(talk, 300);

  await expect(talk.locator("[data-message]")).toHaveCount(100);
  expect(failures).toBe(1);
  expect(board.older).toEqual(["50"]);
});

test("Shift with Page Up and Page Down scrolls the messages, Shift+Escape goes to the pill and Escape goes back", async ({ page }) => {
  await open(page);
  await openCfo(page);
  const talk = list(page, "CFO");
  await expect(talk.locator("[data-message]")).toHaveCount(50);
  await talk.focus();
  const { top } = await scroll(talk);

  await page.keyboard.press("Shift+PageUp");
  await expect.poll(async () => (await scroll(talk)).top).toBeLessThan(top - 200);
  await page.keyboard.press("Shift+PageDown");
  await expect.poll(async () => (await scroll(talk)).top).toBeGreaterThan(top - 50);
  await page.keyboard.press("Shift+Escape");
  await expect(page.locator(".context-pane .panel-top button:not(:disabled)").first()).toBeFocused();
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await talk.focus();
  await page.keyboard.press("Escape");

  await expect(page.locator(".context-pane")).toBeHidden();
});

test("Copy puts a message's words on the clipboard, and its text can be selected", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await open(page);
  await openGoblin(page);
  const talk = list(page, "Zeke - Touch Wrangler");
  const command = talk.locator(".message-prompt.shell");

  await command.hover();
  await command.getByRole("button", { name: "Copy" }).click();
  await expect.poll(() => clipboard(page)).toBe("git status\n\nnothing to commit");
  const last = talk.locator(".message-reply").last();
  await last.hover();
  await last.getByRole("button", { name: "Copy" }).click();
  await expect.poll(() => clipboard(page)).toBe("That matches. Running the phone spec now.");
  const compaction = talk.locator(".message-compaction");
  await compaction.hover();
  await compaction.getByRole("button", { name: "Copy" }).click();
  await expect.poll(() => clipboard(page)).toBe("The work so far: the swipe fix.");
  await talk.locator(".message-reply").last().locator(".message-words p").click({ clickCount: 3 });

  expect(await page.evaluate(() => getSelection()?.toString().trim())).toBe("That matches. Running the phone spec now.");
});

// Where an element lies, and where its contents do, its words and icons, as
// against its own box, and how much area two of them share.
interface Box { left: number; right: number; top: number; bottom: number }
const box = (element: Locator) => element.evaluate((each): Box => {
  const { left, right, top, bottom } = each.getBoundingClientRect();
  return { left, right, top, bottom };
});
const contents = (element: Locator) => element.evaluate((each): Box => {
  const range = document.createRange();
  range.selectNodeContents(each);
  const { left, right, top, bottom } = range.getBoundingClientRect();
  return { left, right, top, bottom };
});
const shared = (one: Box, other: Box) => Math.max(0, Math.min(one.right, other.right) - Math.max(one.left, other.left)) * Math.max(0, Math.min(one.bottom, other.bottom) - Math.max(one.top, other.top));

test.describe("on a phone's touch screen", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

  test("Copy, always shown, covers no word or call state of a reply, and lies beside a reply's name", async ({ page }) => {
    await open(page);
    await openGoblin(page);
    const talk = list(page, "Zeke - Touch Wrangler");
    await expect(talk.locator("[data-message]")).toHaveCount(11);

    // Two replies after the last one, so neither is led by the name, one
    // with a path that breaks across the whole width and one led by a call.
    await send(page, "messages", [
      reply(11, { kind: "text", text: "Changed frontend/src/terminal-touch-handlers/hand-a-vertical-swipe-to-the-history-and-keep-a-long-press-for-paste.ts" }),
      reply(12, { kind: "call", tool: "Bash", subject: "npx playwright test phone-terminal.spec.ts", state: "running" }, { kind: "text", text: "Running the phone terminal spec." }),
    ]);

    const named = talk.locator('[data-message="m10"]');
    const words = talk.locator('[data-message="m11"]');
    const called = talk.locator('[data-message="m12"]');
    await expect(called).toBeVisible();
    for (const row of [named, words, called]) await expect(row.getByRole("button", { name: "Copy" })).toHaveCSS("opacity", "1");
    await expect(words.locator(".message-from")).toHaveCount(0);
    await expect(called.locator(".message-from")).toHaveCount(0);
    expect(shared(await box(words.getByRole("button", { name: "Copy" })), await contents(words.locator(".message-words"))), "Copy covers no word of the reply's text").toBe(0);
    expect(shared(await box(called.getByRole("button", { name: "Copy" })), await box(called.locator(".call-end"))), "Copy covers no call state").toBe(0);
    const header = await box(named.locator(".message-from"));
    const copy = await box(named.getByRole("button", { name: "Copy" }));
    expect(copy.top >= header.top - 1 && copy.bottom <= header.bottom + 1, "Copy lies on the name's line").toBe(true);
    expect(shared(copy, await contents(named.locator(".message-from"))), "Copy covers nothing of the name").toBe(0);
  });
});

test("Ctrl+Plus, Ctrl+Minus and Ctrl+0, and the right button with the wheel, size the message text, kept in this browser", async ({ page }) => {
  await open(page);
  await openCfo(page);
  const talk = list(page, "CFO");
  await expect(talk.locator("[data-message]")).toHaveCount(50);
  const size = () => talk.evaluate((element) => getComputedStyle(element).fontSize);
  await expect.poll(size).toBe("16px");
  await talk.focus();

  await page.keyboard.press("Control+Equal");
  await page.keyboard.press("Control+Equal");
  await expect.poll(size).toBe("18px");
  await page.keyboard.press("Control+Minus");
  await expect.poll(size).toBe("17px");
  const box = (await talk.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down({ button: "right" });
  await page.mouse.wheel(0, -100);
  await page.mouse.up({ button: "right" });
  await expect.poll(size).toBe("18px");
  expect(await page.evaluate(() => localStorage.getItem("cfo-message-font-size"))).toBe("18");
  expect(await page.evaluate(() => localStorage.getItem("cfo-terminal-font-size")), "the terminals keep their own size").toBeNull();
  await page.reload();
  await openCfo(page);
  await expect.poll(size).toBe("18px");
  await talk.focus();
  await page.keyboard.press("Control+0");

  await expect.poll(size).toBe("16px");
});

test("a stream the supervisor ends says why, and Reconnect reads the messages again", async ({ page }) => {
  await open(page);
  await openCfo(page);
  await expect(list(page, "CFO").locator("[data-message]")).toHaveCount(50);

  await send(page, "unavailable", "The CFO runs in another terminal now. Open the CFO again.");

  const closed = page.locator(".messages-view .terminal-closed");
  await expect(closed).toContainText("The CFO runs in another terminal now. Open the CFO again.");
  await closed.getByRole("button", { name: "Reconnect" }).click();
  await expect(closed).toHaveCount(0);
  expect(await streams(page)).toBe(2);
});

test("Back from a goblin's panel returns to the CFO's messages", async ({ page }) => {
  await open(page);
  await openCfo(page);
  await expect(list(page, "CFO").locator("[data-message]")).toHaveCount(50);
  await openGoblin(page);

  await page.getByRole("button", { name: "Back to the CFO", exact: true }).click();

  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await expect(list(page, "CFO")).toBeVisible();
});

// The Messages view lies in the box the terminal fills, so no switch between
// messages and terminal asks the terminal for another size: counted at the relay, for the
// CFO and a goblin, beside the board and maximized.
for (const [who, choose] of [["the CFO", openCfo], ["a goblin", openGoblin]] as const) {
  test(`stepping ${who}'s terminal in and out sends no resize, beside the board and maximized`, async ({ page }) => {
    const board = await open(page);
    await choose(page);
    await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
    await page.evaluate(() => document.fonts.ready.then(() => undefined));

    for (const isMaximized of [false, true]) {
      if (isMaximized) await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();
      const asked = await settled(board);
      const deck = await page.locator(".terminal-deck").boundingBox();
      board.say({ type: "screen", asking: false, open: true, working: false, submission: "" });
      await page.getByRole("button", { name: "Answer in the terminal" }).click();
      await expect(page.getByRole("button", { name: "Back to messages" })).toBeVisible();
      await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeFocused();
      await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeHidden();
      await page.waitForTimeout(300);
      expect(board.resizes.slice(asked), "stepping in sent no resize").toEqual([]);
      expect(await page.locator(".terminal-deck").boundingBox()).toEqual(deck);
      board.say({ type: "screen", asking: false, open: false, working: false, submission: "" });
      await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: "Back to messages" })).toHaveCount(0);
      await page.waitForTimeout(300);
      expect(await page.locator(".messages-view").boundingBox(), "the messages take the terminal's box").toEqual(deck);

      expect(board.resizes.slice(asked), `${isMaximized ? "maximized" : "beside the board"}, the switches sent no resize`).toEqual([]);
      expect(await page.locator(".terminal-deck").boundingBox()).toEqual(deck);
    }
  });
}
