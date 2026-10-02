import { expect, test, type BrowserContext, type Page } from "./site";

// The Overlord, 2026-10-01: "fix the issue of idempotent cleared notification
// so no double fire or display happens from command center". One item is
// announced once: one alert and one opening of the Command Center, whichever
// tab is open, through a reload, a supervisor restart and a browser that
// remembers nothing; and what he already acted on never comes back.
const ASKS = "May I finish the remaining heavy steps now, or stay paused?";
const QUESTION = "question:notify-cg-board-theme-3753";

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

async function open(page: Page) {
  await page.goto("/tests/fixtures/announce-once.html");
  await page.waitForFunction(() => "advance" in window);
}
// Each step waits until the board has drawn its snapshot, so none is skipped.
async function step(page: Page, name: "working" | "asked" | "answered" | "both" | "cfoOnly" | "restarting") {
  await page.evaluate((to) => (window as unknown as { advance: (name: string) => void }).advance(to), name);
  await expect(page.locator("main")).toHaveAttribute("data-step", name);
}
const toasts = (page: Page) => page.locator(".toasts .toast");
const commandCenter = (page: Page) => page.locator("dialog.question-modal");
// The board has asked the supervisor about everything this step brought, and
// has had time to act on the answer.
async function settled(page: Page, record: ReturnType<typeof supervisor>, before: number) {
  await expect.poll(record.requests).toBeGreaterThan(before);
  await page.waitForTimeout(400);
}

test("a question he saw and closed never opens the Command Center or alerts again after a reload", async ({ page, context }) => {
  // Arrange
  const record = supervisor();
  await record.serve(context);
  await open(page);
  await step(page, "asked");
  await expect(commandCenter(page)).toContainText(ASKS);
  await expect(toasts(page)).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(commandCenter(page)).toBeHidden();

  // Act: the board reloads, as it does after an update, and the same question
  // is still waiting in the supervisor's next snapshot.
  await page.reload();
  await page.waitForFunction(() => "advance" in window);
  const before = record.requests();
  await step(page, "asked");
  await settled(page, record, before);

  // Assert
  await expect(commandCenter(page)).toBeHidden();
  await expect(page.locator(".toasts")).toHaveCount(0);
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
});

test("two tabs of the board announce one question once between them", async ({ page, context }) => {
  // Arrange
  const record = supervisor();
  await record.serve(context);
  const other = await context.newPage();
  await open(page);
  await open(other);

  // Act
  await step(page, "asked");
  await expect(toasts(page)).toHaveCount(1);
  await expect(commandCenter(page)).toContainText(ASKS);
  const before = record.requests();
  await step(other, "asked");
  await settled(other, record, before);

  // Assert
  await expect(other.locator(".toasts")).toHaveCount(0);
  await expect(commandCenter(other)).toBeHidden();
  await expect(other.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
});

test("a tab he cannot see leaves the announcement to the one he is looking at", async ({ page, context }) => {
  // Arrange
  const record = supervisor();
  await record.serve(context);
  const hidden = await context.newPage();
  await hidden.addInitScript(() => Object.defineProperty(document, "hidden", { configurable: true, get: () => true }));
  await open(page);
  await open(hidden);

  // Act: the hidden tab hears of the question first.
  await step(hidden, "asked");
  await step(page, "asked");

  // Assert
  await expect(toasts(page)).toHaveCount(1);
  await expect(commandCenter(page)).toContainText(ASKS);
  await expect.poll(record.requests).toBe(4);
  await hidden.waitForTimeout(400);
  await expect(hidden.locator(".toasts")).toHaveCount(0);
  await expect(commandCenter(hidden)).toBeHidden();
  await expect(hidden.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
});

test("a supervisor restart and a browser that remembers nothing bring no second alert or opening", async ({ page, context, browser }) => {
  // Arrange
  const record = supervisor();
  await record.serve(context);
  await open(page);
  await step(page, "asked");
  await expect(toasts(page)).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(commandCenter(page)).toBeHidden();
  await toasts(page).getByRole("button", { name: /^Dismiss/ }).click();

  // Act: the supervisor restarts under the open board, and the board is then
  // opened in a browser with no storage of its own.
  const before = record.requests();
  for (const name of ["restarting", "working", "asked"] as const) await step(page, name);
  const fresh = await browser.newContext();
  await record.serve(fresh);
  const elsewhere = await fresh.newPage();
  await open(elsewhere);
  await step(elsewhere, "asked");
  await settled(elsewhere, record, before);

  // Assert
  await expect(page.locator(".toasts")).toHaveCount(0);
  await expect(commandCenter(page)).toBeHidden();
  await expect(elsewhere.locator(".toasts")).toHaveCount(0);
  await expect(commandCenter(elsewhere)).toBeHidden();
  await fresh.close();
});

test("an alert leaves the board when its item is answered elsewhere", async ({ page, context }) => {
  // Arrange
  const record = supervisor();
  await record.serve(context);
  await open(page);
  await step(page, "asked");
  await expect(commandCenter(page)).toContainText(ASKS);
  await page.keyboard.press("Escape");
  await expect(commandCenter(page)).toBeHidden();
  await expect(toasts(page)).toHaveCount(1);

  // Act
  await step(page, "answered");

  // Assert: at once, not when the toast would have timed out.
  await expect(page.locator(".toasts")).toHaveCount(0, { timeout: 1500 });
});

test("two tabs behind another window send one Windows notification between them, and it closes with its item", async ({ page, context }) => {
  // Arrange: a browser that allows notifications, with the board's windows
  // behind another, records each notification it is asked to show.
  await context.addInitScript(() => {
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
    document.hasFocus = () => false;
  });
  const record = supervisor();
  await record.serve(context);
  const other = await context.newPage();
  await open(page);
  await open(other);
  const notes = (tab: Page) => tab.evaluate(() => (window as unknown as { notes: { options: { tag: string }; closed: boolean }[] }).notes.map((note) => ({ tag: note.options.tag, closed: note.closed })));

  // Act
  await step(page, "asked");
  await expect(toasts(page)).toHaveCount(1);
  const before = record.requests();
  await step(other, "asked");
  await settled(other, record, before);
  const sent = [...await notes(page), ...await notes(other)];
  await step(page, "answered");
  await expect(page.locator(".toasts")).toHaveCount(0, { timeout: 1500 });

  // Assert
  expect(sent).toEqual([{ tag: QUESTION, closed: false }]);
  expect(await notes(page)).toEqual([{ tag: QUESTION, closed: true }]);
});

test("a notification for an item he already answered opens the list, never another item's card", async ({ page, context }) => {
  // Arrange
  const record = supervisor();
  await record.serve(context);
  await open(page);
  await step(page, "both");
  await expect(commandCenter(page)).toContainText("Lift the merge freeze?");
  await page.keyboard.press("Escape");
  await expect(commandCenter(page)).toBeHidden();
  await step(page, "cfoOnly");

  // Act: the click lands on the notification of the question answered since.
  await page.evaluate((key) => (window as unknown as { focusOn: (key: string) => void }).focusOn(key), QUESTION);

  // Assert
  await expect(page.locator(".command-center-menu[open]")).toBeVisible();
  await expect(commandCenter(page)).toBeHidden();
});
