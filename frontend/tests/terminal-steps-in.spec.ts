import { expect, openTaskList, test, type Page } from "./site";
import { at, box, field, open, openGoblin, pasted, type Relay, snapshot, typed } from "./session-board";

// PR 4 of the message view plan, which the Overlord asked be proved first:
// what the box sends that opens a screen of the harness's own, a picker, a
// panel or a question, brings the session's live terminal into its messages'
// place, in the same box and with the keyboard, and it steps out by itself
// once the harness's input line is back. The relay is played
// (tests/session-board.ts) and tells the view what it read of the screen, as
// the supervisor's relay does.

const terminal = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const back = (page: Page) => page.getByRole("button", { name: "Back to messages" });
const messages = (page: Page) => page.getByRole("region", { name: "Messages", exact: true });

// sendFromBox types text into Zeke's box and sends it with Enter, as he does.
async function sendFromBox(page: Page, relay: Relay, text: string) {
  await field(page).fill(text);
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toContain(pasted(text) + "\r");
}

test("a picker opens despite background work, takes the keyboard and Escape, and steps out at the input line", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { runtime: { state: "busy", reason: "Background shell running", at } }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(box(page).locator(".composer-working")).toBeVisible();
  await sendFromBox(page, relay, "/model");

  // Act
  relay.say({ type: "screen", asking: true, open: true, working: false });

  // Assert
  await expect(back(page)).toBeVisible();
  await expect(messages(page)).toBeHidden();
  await expect(terminal(page)).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r" + "\x1b[B" + "\x1b" + "\r");
  await expect(back(page), "Escape stays the terminal's while it is in").toBeVisible();
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(messages(page)).toBeVisible();
  await expect(back(page)).toHaveCount(0);
  await expect(field(page)).toBeFocused();
});

test("Back to messages returns to the messages with the keyboard in the box, and his draft there is the same", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("half a thought");
  relay.say({ type: "screen", asking: true, open: true, working: false });
  await box(page).getByRole("button", { name: "Answer in the terminal" }).click();
  await expect(terminal(page)).toBeFocused();
  await page.keyboard.press("1");

  // Act
  await back(page).click();

  // Assert
  await expect(messages(page)).toBeVisible();
  await expect(field(page)).toBeFocused();
  await expect(field(page)).toHaveValue("half a thought");
  expect(typed(relay)).toBe("1");
});

test("a panel that asks nothing brings the terminal in as a picker does, and while it stays open the box sends nothing into it", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await sendFromBox(page, relay, "/permissions");

  // Act
  relay.say({ type: "screen", asking: false, open: true, working: false });
  await expect(back(page)).toBeVisible();
  await expect(terminal(page)).toBeFocused();
  await back(page).click();

  // Assert
  await expect(box(page).locator(".composer-asking")).toContainText("Waiting on you in the terminal");
  await field(page).fill("hello");
  await field(page).press("Enter");
  await expect(box(page).getByRole("button", { name: "Send", exact: true })).toBeDisabled();
  expect(typed(relay)).not.toContain("hello");
});

test("a question that comes during a turn keeps the amber card rather than stepping in", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await sendFromBox(page, relay, "write nine.txt");
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await page.waitForTimeout(100);

  // Act
  relay.say({ type: "screen", asking: true, open: true, working: false });

  // Assert
  await expect(box(page).locator(".composer-asking")).toContainText("Waiting on you in the terminal");
  await expect(messages(page)).toBeVisible();
  await expect(back(page)).toHaveCount(0);
});

test("a completed harness turn clears send intent before an unrelated panel opens", async ({ page }) => {
  const relay = await open(page);
  await openGoblin(page);
  await sendFromBox(page, relay, "read the brief");
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await page.waitForTimeout(100);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await page.waitForTimeout(100);

  relay.say({ type: "screen", asking: false, open: true, working: false });

  await expect(box(page).locator(".composer-asking")).toBeVisible();
  await expect(messages(page)).toBeVisible();
  await expect(back(page)).toHaveCount(0);
});

test("a phone's tall Task fold scrolls while its messages keep room", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, snapshot({}, { teardown: Array.from({ length: 80 }, (_, index) => `shell-${index} pid ${index + 1}`) }));
  await openGoblin(page);

  const header = await openTaskList(page);
  await header.locator(".raw-details > summary").click();

  const fold = page.locator(".panel-task-fold");
  await expect.poll(() => fold.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
  const conversation = await messages(page).boundingBox();
  const pane = await page.locator(".context-pane").boundingBox();
  expect(conversation!.height).toBeGreaterThan(0);
  expect(conversation!.y + conversation!.height).toBeLessThanOrEqual(pane!.y + pane!.height + 1);
  await fold.evaluate((element) => { element.scrollTop = element.scrollHeight; });
  await expect.poll(() => fold.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
});

test("stepping in and out asks the terminal for no new size, counted at the relay", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  let seen = -1;
  await expect.poll(() => { const isQuiet = relay.resizes.length > 0 && relay.resizes.length === seen; seen = relay.resizes.length; return isQuiet; }, { intervals: [500] }).toBe(true);

  // Act
  await sendFromBox(page, relay, "/model");
  relay.say({ type: "screen", asking: true, open: true, working: false });
  await expect(back(page)).toBeVisible();
  await page.waitForTimeout(300);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(messages(page)).toBeVisible();
  await page.waitForTimeout(300);

  // Assert
  expect(relay.resizes.length).toBe(seen);
});
