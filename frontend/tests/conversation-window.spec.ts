import { expect, test, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, snapshot } from "./session-board";

// A conversation loads only what it shows (the Overlord, 2026-10-27, 12:40Z:
// "every UI change must load intelligently ... An update re-renders only what
// it changed, never every line"): the page holds the rows within reach of
// where he reads however long the session is, a scroll draws the view again
// only when it changes which rows are drawn, an idle conversation changes
// nothing in the page while the board's snapshot keeps arriving, and a reply
// that grows as it streams changes its own message and at most the rows its
// growth pushes off the window's far edge, never every line. The supervisor
// is played by tests/session-board.ts; every conversation is made up.

const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const prompt = (seq: number) => ({ id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text: "Question " + seq });
const LONG = { messages: Array.from({ length: 1000 }, (_, seq) => seq % 2 ? reply(seq, `Answer ${seq}\n\nIt runs over a few lines so the page has height.`) : prompt(seq)), more: false };
const log = (page: Page) => page.getByRole("log", { name: "Messages of " + NAME });

// watch records every change made in the conversation's list from now on: the
// message a change was inside, each row that left or joined the list, a
// change to the room the list keeps above and below its rows, and anything
// else outside a message.
const watch = (page: Page) => page.evaluate(() => {
  const changes: string[] = [];
  const list = document.querySelector(".messages-view:not([hidden]) .conversation-items")!;
  new MutationObserver((records) => {
    for (const record of records) {
      const target = record.target instanceof Element ? record.target : record.target.parentElement;
      const message = target?.closest("[data-message]")?.getAttribute("data-message");
      if (message) changes.push(message);
      else if (record.target === list && record.type === "childList") {
        for (const row of record.removedNodes) changes.push("left:" + (row instanceof Element ? row.getAttribute("data-message") : row.nodeName));
        for (const row of record.addedNodes) changes.push("joined:" + (row instanceof Element ? row.getAttribute("data-message") : row.nodeName));
      } else if (record.attributeName === "style" && (record.target === list || record.target === list.querySelector(":scope > .conversation-room"))) changes.push("room");
      else changes.push("outside a message:" + record.type + ":" + (record.attributeName || ""));
    }
  }).observe(list, { subtree: true, childList: true, characterData: true, attributes: true });
  (window as unknown as { changes: string[] }).changes = changes;
});
const changes = (page: Page) => page.evaluate(() => (window as unknown as { changes: string[] }).changes);

test("a thousand-message conversation holds only the rows near where he reads, and still reads to its first message", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");

  // Act and Assert: at the end only a screenful and its reach is drawn.
  const drawn = () => log(page).locator("[data-message]").count();
  expect(await drawn()).toBeLessThan(60);

  // Reading back to the top draws the first messages, still a few at a time.
  for (let tries = 0; tries < 40 && !(await log(page).getByText("Question 0", { exact: true }).isVisible()); tries++) {
    await log(page).evaluate((element) => { element.scrollTop = 0; });
    await page.waitForTimeout(50);
  }
  await expect(log(page).getByText("Question 0", { exact: true })).toBeVisible();
  expect(await drawn()).toBeLessThan(60);
});

test("an idle conversation changes nothing in the page while the board's snapshot keeps arriving", async ({ page }) => {
  // Arrange
  await open(page, snapshot({ actions: [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: "queued while busy", status: "queued", at }] }));
  await openGoblin(page);
  await expect(log(page)).toContainText("queued while busy");
  await page.waitForTimeout(500);
  await watch(page);

  // Act: eight seconds of snapshots, one every half second, each a new
  // revision with nothing in the conversation changed.
  for (let revision = 2; revision < 18; revision++) {
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision, actions: [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: "queued while busy", status: "queued", at }] }));
    await page.waitForTimeout(500);
  }

  // Assert
  expect(await changes(page)).toEqual([]);
});

test("a reply that grows as it streams changes its own message and at most the rows leaving the window's far edge", async ({ page }) => {
  // Arrange
  const page50 = { messages: Array.from({ length: 50 }, (_, seq) => seq % 2 ? reply(seq, "Answer " + seq) : prompt(seq)), more: false };
  await open(page, snapshot(), "", page50);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 49");
  const newest = log(page).locator('[data-message="m49"]');
  const height = await newest.evaluate((row) => row.getBoundingClientRect().height);
  const drawn = await log(page).evaluate((element) => [...element.querySelectorAll<HTMLElement>(":scope > .conversation-items > [data-message]")].map((row) => row.dataset.message || ""));
  await watch(page);

  // Act: the newest reply streams on in three updates, each three lines longer.
  for (const count of [3, 6, 9]) {
    const text = ["Answer 49", ...Array.from({ length: count }, (_, line) => "Streamed line " + (line + 1))].join("\n\n");
    await page.evaluate((changed) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: [changed] } })), reply(49, text));
    await expect(newest).toContainText("Streamed line " + count);
  }
  await page.waitForTimeout(300);

  // Assert: the reply grew by several lines, and every change in the list was
  // inside it, in the room kept above the rows drawn, or a row its growth
  // pushed off the window's far edge, which are the oldest rows drawn.
  expect(await newest.evaluate((row) => row.getBoundingClientRect().height)).toBeGreaterThan(height + 100);
  const seen = await changes(page);
  expect(seen).toContain("m49");
  expect(seen.filter((each) => each !== "m49" && each !== "room" && !each.startsWith("left:"))).toEqual([]);
  const left = seen.filter((each) => each.startsWith("left:")).map((each) => each.slice("left:".length));
  expect([...left].sort()).toEqual(drawn.slice(0, left.length).sort());
});

// countDraws counts each commit in which the conversation's view drew, by
// React's own devtools hook, as tests/render-budget.spec.ts counts what a
// commit drew: the build shortens component names, so the view is the
// component whose first element is .messages-view.
const countDraws = (page: Page) => page.addInitScript(() => {
  interface Fiber { tag: number; flags: number; child?: Fiber; sibling?: Fiber; stateNode?: unknown; alternate?: Fiber | null }
  const counted = { draws: 0 };
  Object.assign(window, {
    counted,
    __REACT_DEVTOOLS_GLOBAL_HOOK__: {
      supportsFiber: true, renderers: new Map(), inject() { return 1; }, checkDCE() {}, onCommitFiberUnmount() {}, onPostCommitFiberRoot() {}, onScheduleFiberRoot() {},
      onCommitFiberRoot(_: number, root: { current: Fiber }) {
        const stack = [root.current];
        while (stack.length) {
          const fiber = stack.pop()!;
          if (fiber.sibling) stack.push(fiber.sibling);
          // Children a bailout reused are the ones the commit before drew,
          // with their flags from then, so the walk stops there.
          if (fiber.child && fiber.child !== fiber.alternate?.child) stack.push(fiber.child);
          // A function, forwardRef or memo component that did work.
          if (!(fiber.flags & 1) || ![0, 11, 14, 15].includes(fiber.tag)) continue;
          let host = fiber.child;
          while (host && host.tag !== 5) host = host.child;
          if (host?.stateNode instanceof Element && host.stateNode.classList.contains("messages-view")) { counted.draws++; return; }
        }
      },
    },
  });
});

test("scrolling a few pixels at a time draws the view again only when it changes which rows are drawn", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute, so the board's
  // minute clock draws nothing while he scrolls, and he reads a thousand
  // pixels up from the end of a long conversation, every row below him drawn.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  await countDraws(page);
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");
  await log(page).evaluate((element) => { element.scrollTop -= 1000; });
  await page.waitForTimeout(1000);
  const sample = () => page.evaluate(() => ({
    rows: [...document.querySelectorAll<HTMLElement>(".messages-view:not([hidden]) .conversation-items > [data-message]")].map((row) => row.dataset.message).join(),
    draws: (window as unknown as { counted: { draws: number } }).counted.draws,
  }));

  // Act: sixty steps of five pixels down, each smaller than the list's top
  // padding, across the edges of the rows leaving the window above.
  const steps = [await sample()];
  for (let step = 0; step < 60; step++) {
    await log(page).evaluate((element) => { element.scrollTop += 5; });
    await page.waitForTimeout(50);
    steps.push(await sample());
  }

  // Assert: the rows drawn changed along the way, and each step that left
  // them as they were drew nothing.
  const moved = steps.slice(1).map((each, step) => each.rows !== steps[step].rows);
  expect(moved).toContain(true);
  expect(steps.slice(1).map((each, step) => moved[step] ? 0 : each.draws - steps[step].draws)).toEqual(moved.map(() => 0));
});

// A conversation whose last reply ran a command, whose output folds under
// its row.
const WITH_CALL = { messages: [...LONG.messages.slice(0, 999), { id: "m999", seq: 999, at, kind: "reply", blocks: [
  { kind: "text", text: "Answer 999" },
  { kind: "call", tool: "Bash", subject: "go test ./...", state: "done", result: "ok  github.com/example/repo  1.2s" },
] }], more: false };
// jumpTo scrolls the list to top, repeating while the rows it reaches are drawn.
const jumpTo = async (page: Page, top: number) => {
  for (let tries = 0; tries < 20; tries++) {
    await log(page).evaluate((element, top) => { element.scrollTop = top < 0 ? element.scrollHeight : top; }, top);
    await page.waitForTimeout(60);
  }
};

test("a small scroll up from the end keeps the newest messages drawn", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");

  // Act: one small step up, just past where the list counts as at its end.
  await log(page).evaluate((element) => { element.scrollTop -= 30; });
  await page.waitForTimeout(300);

  // Assert
  await expect(log(page).getByText("Answer 999", { exact: true })).toBeVisible();
  await expect(log(page).getByText("Question 998", { exact: true })).toBeVisible();
});

test("a fold he opened stays open after its row leaves the window and comes back", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", WITH_CALL);
  await openGoblin(page);
  const call = log(page).locator('[data-message="m999"] .disclosure.message-call');
  await call.locator("summary").click();
  await expect(call.locator(".call-result")).toHaveText("ok  github.com/example/repo  1.2s");

  // Act: read far up, so the row leaves the page, then come back.
  await jumpTo(page, 0);
  await expect(log(page).locator('[data-message="m999"]')).toHaveCount(0);
  await jumpTo(page, -1);

  // Assert
  await expect(call.locator(".call-result")).toHaveText("ok  github.com/example/repo  1.2s");
});

test("the keyboard on a row that leaves the window goes to the list, whose keys keep working", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", WITH_CALL);
  await openGoblin(page);
  await log(page).locator('[data-message="m999"] summary').focus();

  // Act
  await jumpTo(page, 0);
  await expect(log(page).locator('[data-message="m999"]')).toHaveCount(0);

  // Assert: the list holds the keyboard, and Shift+PageDown scrolls it.
  await expect(log(page)).toBeFocused();
  const before = await log(page).evaluate((element) => element.scrollTop);
  await page.keyboard.press("Shift+PageDown");
  await expect.poll(() => log(page).evaluate((element) => element.scrollTop)).toBeGreaterThan(before);
});

test("a queued message stays pinned above the box while he reads far up a long conversation", async ({ page }) => {
  // Arrange: a message waits in the queue, and he reads about 3,000 px up
  // from the end of a long conversation.
  await open(page, snapshot({ actions: [{ id: "q1", kind: "message", task_id: GOBLIN.id, text: "queued while busy", status: "queued", at }] }), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");
  await log(page).evaluate((element) => { element.scrollTop = element.scrollHeight - element.clientHeight - 3000; });
  await page.waitForTimeout(500);
  const queued = page.locator('.queued-message[data-queued="q1"]');

  // Assert
  await expect(queued).toBeInViewport();
  await expect(log(page).locator(".queued-message")).toHaveCount(0);
});

test("a selection from an older message to the newest copies every message between, however far it reaches", async ({ page }) => {
  // Arrange: a caret at the start of an older message, as a click leaves it.
  await open(page, snapshot(), "", LONG);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 999");
  const older = log(page).locator('[data-message="m960"]');
  for (let tries = 0; tries < 40 && await older.count() === 0; tries++) {
    await log(page).evaluate((element) => { element.scrollTop -= 400; });
    await page.waitForTimeout(60);
  }
  await expect(older).toHaveCount(1);
  await older.evaluate((row) => { document.getSelection()?.collapse(row.querySelector(".message-bubble")?.firstChild ?? row, 0); });

  // Act: read down to the end, about 3,000 px, and extend the selection there,
  // as Shift+click does.
  await jumpTo(page, -1);
  await expect(log(page).locator('[data-message="m999"]')).toHaveCount(1);
  await expect(older, "the row the caret is in stays in the page").toHaveCount(1);
  await log(page).locator('[data-message="m999"]').evaluate((row) => {
    const text = row.querySelector("p")?.firstChild ?? row;
    document.getSelection()?.extend(text, (text.textContent ?? "").length);
  });

  // Assert: every message from the older one to the newest is selected.
  await expect.poll(() => page.evaluate(() => document.getSelection()?.toString() ?? "")).toContain("Answer 979");
  const selected = await page.evaluate(() => document.getSelection()?.toString() ?? "");
  for (const seq of [960, 961, 979, 998, 999]) expect(selected, "message " + seq).toContain((seq % 2 ? "Answer " : "Question ") + seq);
});

