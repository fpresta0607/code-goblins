import { expect, test, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, snapshot } from "./session-board";

// A conversation loads only what it shows (the Overlord, 2026-10-27, 12:40Z:
// "every UI change must load intelligently ... An update re-renders only what
// it changed, never every line"): the page holds the rows within reach of
// where he reads however long the session is, an idle conversation changes
// nothing in the page while the board's snapshot keeps arriving, and a reply
// that grows as it streams changes only its own message. The supervisor is
// played by tests/session-board.ts; every conversation is made up.

const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const prompt = (seq: number) => ({ id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text: "Question " + seq });
const LONG = { messages: Array.from({ length: 1000 }, (_, seq) => seq % 2 ? reply(seq, `Answer ${seq}\n\nIt runs over a few lines so the page has height.`) : prompt(seq)), more: false };
const log = (page: Page) => page.getByRole("log", { name: "Messages of " + NAME });

// watch records every change made in the conversation's list from now on.
const watch = (page: Page) => page.evaluate(() => {
  const changes: string[] = [];
  const list = document.querySelector(".messages-view:not([hidden]) .conversation-items")!;
  new MutationObserver((records) => {
    for (const record of records) {
      const target = record.target instanceof Element ? record.target : record.target.parentElement;
      changes.push(target?.closest("[data-message]")?.getAttribute("data-message") || "outside a message:" + record.type + ":" + (record.attributeName || ""));
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

test("a reply that grows as it streams changes only its own message", async ({ page }) => {
  // Arrange
  const page50 = { messages: Array.from({ length: 50 }, (_, seq) => seq % 2 ? reply(seq, "Answer " + seq) : prompt(seq)), more: false };
  await open(page, snapshot(), "", page50);
  await openGoblin(page);
  await expect(log(page)).toContainText("Answer 49");
  await watch(page);

  // Act: the newest reply streams on in three updates.
  for (const text of ["Answer 49, and more", "Answer 49, and more words", "Answer 49, and more words still"]) {
    await page.evaluate((changed) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: [changed] } })), reply(49, text));
    await expect(log(page)).toContainText(text);
  }

  // Assert: every change in the list was inside that reply.
  const seen = await changes(page);
  expect(seen.length).toBeGreaterThan(0);
  expect([...new Set(seen)]).toEqual(["m49"]);
});
