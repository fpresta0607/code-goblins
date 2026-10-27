import { expect, openTaskList, test, type Page } from "./site";
import { at, box, card, field, NAME, open, openGoblin, pasted, type Relay, snapshot, typed } from "./session-board";

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

for (const harness of ["claude", "pi", "codex"]) {
  test(`${harness} mode and interrupt keys reach the native terminal while preserving the Modern draft`, async ({ page }) => {
    const relay = await open(page, snapshot({}, { harness }));
    await openGoblin(page);
    await field(page).fill("keep this draft");
    await field(page).press("Control+End");
    await field(page).press("Control+c");
    await expect.poll(() => typed(relay)).toBe("\x03");
    await expect(field(page)).toHaveValue("keep this draft");
    await field(page).press("Shift+Tab");
    await expect.poll(() => typed(relay)).toBe("\x03\x1b[Z");
    await expect(terminal(page)).toBeFocused();
    await back(page).click();
    await expect(field(page)).toHaveValue("keep this draft");
    relay.say({ type: "held", reason: "pipeline owns this task" });
    await field(page).press("Shift+Tab");
    expect(typed(relay)).toBe("\x03\x1b[Z");
  });
}

// sendFromBox types text into Zeke's box and sends it with Enter, as he does.
async function sendFromBox(page: Page, relay: Relay, text: string) {
  const sent = relay.submissions.length
  await field(page).fill(text);
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toContain(pasted(text) + "\r");
  await expect.poll(() => relay.submissions.length).toBe(sent + 1)
}

test("a picker opens despite background work, takes the keyboard and Escape, and steps out at the input line", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { runtime: { state: "busy", reason: "Background shell running", at } }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect(page.locator(".session-working")).toBeVisible();
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

for (const timing of ["cached", "in flight"] as const) {
  test(`a ${timing} working sample from before Send cannot cancel its picker`, async ({ page }) => {
    const relay = await open(page)
    await openGoblin(page)
    const note = box(page).locator(".composer-note")
    relay.say({ type: "screen", asking: false, open: false, working: true, submission: "earlier-turn" })
    relay.say({ type: "unread", unread: true })
    await expect(note).toHaveText("Program busy. Typed keys are waiting.")
    relay.say({ type: "unread", unread: false })
    await expect(note).toHaveText("")

    await sendFromBox(page, relay, "/model")
    if (timing === "in flight") {
      relay.say({ type: "screen", asking: false, open: false, working: true, submission: "earlier-turn" })
      relay.say({ type: "unread", unread: true })
      await expect(note).toHaveText("Program busy. Typed keys are waiting.")
      relay.say({ type: "unread", unread: false })
      await expect(note).toHaveText("")
    }
    relay.say({ type: "screen", asking: true, open: true, working: false })

    await expect(back(page)).toBeVisible()
    await expect(messages(page)).toBeHidden()
    await expect(terminal(page)).toBeFocused()
  })
}

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

test("Back to messages saves Modern, so the conversation stays after the step out", async ({ page }) => {
  // Arrange: this browser draws its conversations as Classic, so the panel
  // opens on the harness's terminal with its Back to messages bar.
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page);
  await card(page, NAME).click();
  await expect(page.locator("#panel-title")).toHaveText(NAME);
  await expect(terminal(page)).toBeVisible();

  // Act
  await back(page).click();

  // Assert: he stands in the conversation, and the board keeps Modern from
  // then on, on the same session, with its history and the box ready.
  await expect(messages(page)).toBeVisible();
  await expect(field(page)).toBeFocused();
  await expect(page.getByText("On it.", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style")), "Back to messages chose Modern").toBe("modern");
  expect(relay.views, "the session is the one the terminal already kept").toBe(1);
  await expect(back(page)).toHaveCount(0);
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

for (const isSampled of [true, false]) {
  test(`a question that comes during a ${isSampled ? "sampled" : "short"} turn keeps the amber card rather than stepping in`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    await openGoblin(page);
    await sendFromBox(page, relay, "write nine.txt");
    if (isSampled) relay.say({ type: "screen", asking: false, open: false, working: true });
    relay.say({ type: "unread", unread: true })
    await expect(box(page).locator(".composer-note")).toHaveText("Program busy. Typed keys are waiting.")
    relay.say({ type: "unread", unread: false })
    await expect(box(page).locator(".composer-note")).toHaveText("")

    // Act
    relay.say({ type: "screen", asking: true, open: true, working: false, started: true });

    // Assert
    await expect(box(page).locator(".composer-asking")).toContainText("Waiting on you in the terminal");
    await expect(messages(page)).toBeVisible();
    await expect(back(page)).toHaveCount(0);
  });

  test(`a completed ${isSampled ? "sampled" : "short"} harness turn clears send intent before an unrelated panel opens`, async ({ page }) => {
    const relay = await open(page);
    await openGoblin(page);
    await sendFromBox(page, relay, "read the brief");
    if (isSampled) relay.say({ type: "screen", asking: false, open: false, working: true });
    relay.say({ type: "unread", unread: true })
    await expect(box(page).locator(".composer-note")).toHaveText("Program busy. Typed keys are waiting.")
    relay.say({ type: "unread", unread: false })
    await expect(box(page).locator(".composer-note")).toHaveText("")
    relay.say({ type: "screen", asking: false, open: false, working: false, started: true });
    relay.say({ type: "unread", unread: true })
    await expect(box(page).locator(".composer-note")).toHaveText("Program busy. Typed keys are waiting.")
    relay.say({ type: "unread", unread: false })
    await expect(box(page).locator(".composer-note")).toHaveText("")

    relay.say({ type: "screen", asking: false, open: true, working: false });

    await expect(box(page).locator(".composer-asking")).toBeVisible();
    await expect(messages(page)).toBeVisible();
    await expect(back(page)).toHaveCount(0);
  });
}

test("a phone's tall task panel scrolls, and the conversation returns with its draft", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, snapshot({}, { teardown: Array.from({ length: 80 }, (_, index) => `shell-${index} pid ${index + 1}`) }));
  await openGoblin(page);
  await field(page).fill("Keep this draft while reading the task");

  const header = await openTaskList(page);
  await header.locator(".raw-details > summary").click();

  const task = page.locator(".goblin-panel > .panel-task");
  await expect.poll(() => task.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
  await task.evaluate((element) => { element.scrollTop = element.scrollHeight; });
  await expect.poll(() => task.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);

  await page.locator(".panel-view-switch").getByRole("button", { name: "Chat", exact: true }).click();
  await expect(messages(page).getByRole("log")).toBeInViewport({ ratio: 1 });
  await expect(messages(page).getByText("On it.", { exact: true })).toBeInViewport({ ratio: 1 });
  await expect(box(page)).toBeInViewport({ ratio: 1 });
  await expect(field(page)).toBeVisible();
  await expect(field(page)).toHaveValue("Keep this draft while reading the task");
  await field(page).click();
  await expect(field(page)).toBeFocused();
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
