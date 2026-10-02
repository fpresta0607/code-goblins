import { expect, servePages, test, type BrowserContext, type Page } from "./site";

// The Overlord, 2026-10-01: "fix the issue of idempotent cleared notification
// so no double fire or display happens from command center", and 2026-10-02:
// "same alert in desktop app hit me three times, make sure idempotency works
// smooth!" One item is announced once: on a board in sight it waits under the
// count and nothing else shows; out of sight it raises one Windows
// notification, whichever tab is open, through a reload, a supervisor restart
// and a browser that remembers nothing; and what he already acted on never
// comes back. A goblin's question to the CFO is never announced at all.
const ASKS = "May the goblin finish the remaining heavy steps now, or stay paused?";
const QUESTION = "question:finish-heavy-steps";

// The supervisor's record of what the board announced, as POST /api/announce
// keeps it: each key goes to the first request that asks for it. One record
// serves every tab and browser a test opens, as one supervisor does.
function supervisor() {
  const announced = new Set<string>();
  let requests = 0;
  return {
    serve: (context: BrowserContext) => context.route("**/api/announce", async (route) => {
      const asked: { keys?: string[]; news?: string[] } = route.request().postDataJSON();
      const claimed = [...asked.keys || [], ...asked.news || []].filter((key) => !announced.has(key));
      for (const key of claimed) announced.add(key);
      requests++;
      await route.fulfill({ json: { claimed } });
    }),
    requests: () => requests,
  };
}

// A browser that allows notifications records each one a page is asked to
// show.
const RECORD_NOTIFICATIONS = () => {
  class Note {
    static permission = "granted";
    static requestPermission = async () => "granted";
    closed = false;
    onclick: (() => void) | null = null;
    onclose: (() => void) | null = null;
    constructor(public title: string, public options: { body: string; tag: string }) { (window as unknown as { notes: Note[] }).notes.push(this); }
    close() { this.closed = true; this.onclose?.(); }
  }
  Object.assign(window, { notes: [], Notification: Note });
};
// A board out of sight: its tab hidden or its window minimized.
const OUT_OF_SIGHT = () => Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
const notes = (tab: Page) => tab.evaluate(() => (window as unknown as { notes: { options: { tag: string }; closed: boolean }[] }).notes.map((note) => ({ tag: note.options.tag, closed: note.closed })));

async function open(page: Page) {
  await page.goto("/tests/fixtures/announce-once.html");
  await page.waitForFunction(() => "advance" in window);
}
// Each step waits until the board has drawn its snapshot, so none is skipped.
async function step(page: Page, name: "working" | "asked" | "answered" | "both" | "second" | "goblinAsks" | "restarting") {
  await page.evaluate((to) => (window as unknown as { advance: (name: string) => void }).advance(to), name);
  await expect(page.locator("main")).toHaveAttribute("data-step", name);
}
const commandCenter = (page: Page) => page.locator("dialog.question-modal");
// The board has asked the supervisor about everything this step brought, and
// has had time to act on the answer.
async function settled(page: Page, record: ReturnType<typeof supervisor>, before: number) {
  await expect.poll(record.requests).toBeGreaterThan(before);
  await page.waitForTimeout(400);
}

test("on a board in sight a question waits under the count: no toast, no notification, and nothing opens by itself", async ({ page, context }) => {
  // Arrange
  await context.addInitScript(RECORD_NOTIFICATIONS);
  const record = supervisor();
  await record.serve(context);
  await open(page);

  // Act
  const before = record.requests();
  await step(page, "asked");
  await settled(page, record, before);

  // Assert
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
  await expect(page.locator(".toasts")).toHaveCount(0);
  await expect(commandCenter(page)).toBeHidden();
  expect(await notes(page)).toEqual([]);
});

test("a goblin's question to the CFO is never announced: the board asks the supervisor nothing about it", async ({ page, context }) => {
  // Arrange
  await context.addInitScript(RECORD_NOTIFICATIONS);
  await context.addInitScript(OUT_OF_SIGHT);
  const record = supervisor();
  await record.serve(context);
  await open(page);

  // Act: the goblin asks, and then the CFO asks the Overlord something, whose
  // notification proves the board took the goblin's question in before it.
  await step(page, "goblinAsks");
  await step(page, "asked");

  // Assert
  await expect.poll(() => notes(page)).toEqual([{ tag: QUESTION, closed: false }]);
  expect(record.requests()).toBe(1);
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
  await expect(commandCenter(page)).toBeHidden();
});

test("a question he was notified of never notifies again after a reload", async ({ page, context }) => {
  // Arrange
  await context.addInitScript(RECORD_NOTIFICATIONS);
  await context.addInitScript(OUT_OF_SIGHT);
  const record = supervisor();
  await record.serve(context);
  await open(page);
  await step(page, "asked");
  await expect.poll(() => notes(page)).toEqual([{ tag: QUESTION, closed: false }]);

  // Act: the board reloads, as it does after an update, and the same question
  // is still waiting in the supervisor's next snapshot.
  await page.reload();
  await page.waitForFunction(() => "advance" in window);
  await step(page, "asked");
  await page.waitForTimeout(2200);

  // Assert
  expect(await notes(page)).toEqual([]);
  await expect(commandCenter(page)).toBeHidden();
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
});

test("two tabs out of sight send one Windows notification between them, and it closes with its item", async ({ page, context }) => {
  // Arrange
  await context.addInitScript(RECORD_NOTIFICATIONS);
  await context.addInitScript(OUT_OF_SIGHT);
  const record = supervisor();
  await record.serve(context);
  const other = await context.newPage();
  await open(page);
  await open(other);

  // Act
  await step(page, "asked");
  await expect.poll(() => notes(page)).toEqual([{ tag: QUESTION, closed: false }]);
  const before = record.requests();
  await step(other, "asked");
  await settled(other, record, before);
  const sent = [...await notes(page), ...await notes(other)];
  await step(page, "answered");

  // Assert
  expect(sent).toEqual([{ tag: QUESTION, closed: false }]);
  await expect.poll(() => notes(page)).toEqual([{ tag: QUESTION, closed: true }]);
});

test("a tab he cannot see leaves the item to the board he is looking at, so nothing notifies", async ({ page, context }) => {
  // Arrange
  await context.addInitScript(RECORD_NOTIFICATIONS);
  const record = supervisor();
  await record.serve(context);
  const hidden = await context.newPage();
  await hidden.addInitScript(OUT_OF_SIGHT);
  await open(page);
  await open(hidden);

  // Act: the hidden tab hears of the question first.
  await step(hidden, "asked");
  await step(page, "asked");

  // Assert: each tab asked once, the one in sight was handed the item, and
  // it shows under that board's count alone.
  await expect.poll(record.requests).toBe(2);
  await hidden.waitForTimeout(400);
  expect([...await notes(page), ...await notes(hidden)]).toEqual([]);
  await expect(page.locator(".toasts")).toHaveCount(0);
  await expect(commandCenter(page)).toBeHidden();
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
  await expect(hidden.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
});

test("a supervisor restart and a browser that remembers nothing bring no second notification", async ({ page, context, browser }) => {
  // Arrange
  await context.addInitScript(RECORD_NOTIFICATIONS);
  await context.addInitScript(OUT_OF_SIGHT);
  const record = supervisor();
  await record.serve(context);
  await open(page);
  await step(page, "asked");
  await expect.poll(() => notes(page)).toEqual([{ tag: QUESTION, closed: false }]);

  // Act: the supervisor restarts under the open board, and the board is then
  // opened in a browser with no storage of its own.
  const before = record.requests();
  for (const name of ["restarting", "working", "asked"] as const) await step(page, name);
  const fresh = await browser.newContext();
  await servePages(fresh);
  await fresh.addInitScript(RECORD_NOTIFICATIONS);
  await fresh.addInitScript(OUT_OF_SIGHT);
  await record.serve(fresh);
  const elsewhere = await fresh.newPage();
  await open(elsewhere);
  await step(elsewhere, "asked");
  await settled(elsewhere, record, before);

  // Assert: the one notification still stands, and no other was raised.
  expect(await notes(page)).toEqual([{ tag: QUESTION, closed: false }]);
  expect(await notes(elsewhere)).toEqual([]);
  await expect(commandCenter(elsewhere)).toBeHidden();
  await fresh.close();
});

test("a notification for an item he already answered opens the list, never another item's card", async ({ page, context }) => {
  // Arrange
  const record = supervisor();
  await record.serve(context);
  await open(page);
  await step(page, "both");
  await expect(page.getByLabel("Command Center, 2 waiting on you")).toBeVisible();
  await step(page, "second");

  // Act: the click lands on the notification of the question answered since.
  await page.evaluate((key) => (window as unknown as { focusOn: (key: string) => void }).focusOn(key), QUESTION);

  // Assert
  await expect(page.locator(".command-center-menu[open]")).toBeVisible();
  await expect(commandCenter(page)).toBeHidden();
  await expect(page.locator(".command-center-menu")).toContainText(ASKS);
});
