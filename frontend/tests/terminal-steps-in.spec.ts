import { expect, openTaskList, test, type Page } from "./site";
import { at, box, card, field, NAME, open, openGoblin, pasted, type Relay, snapshot, typed } from "./session-board";

// A screen of the harness's own, a question, a permission prompt, a picker or
// a panel, is drawn in Modern Chat above the box as a list he picks from, and
// answered there with the terminal's own keys: the conversation stays, and
// nothing steps the terminal in by itself (the Overlord, 2026-10-27: "why am
// I getting an answer in terminal? ... all functionality should be available
// in the modern chat"). Show the terminal steps it in for a screen the list
// does not cover, and it steps out by itself once the harness's input line is
// back. The relay is played (tests/session-board.ts) and tells the view what
// it read of the screen, its panel too, as the supervisor's relay does
// (internal/harness/panel.go).

const terminal = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const back = (page: Page) => page.getByRole("button", { name: "Back to messages" });
const messages = (page: Page) => page.getByRole("region", { name: "Messages", exact: true });
const list = (page: Page) => box(page).getByRole("group", { name: NAME + " asks" });
const choice = (page: Page, text: string) => list(page).getByRole("button", { name: text, exact: true });
const UP = "\x1b[A", DOWN = "\x1b[B";

// Each harness's own screen as its relay reads it, from the recorded screens
// internal/harness/panel_test.go reads.
const PANELS = {
  claude: { rows: ["Do you want to create nine.txt?"], choices: [{ text: "Yes", number: 1, focused: true }, { text: "Yes, and switch to accept edits", number: 2 }, { text: "No", number: 3 }], hints: [] },
  codex: { rows: ["Hooks need review", "2 hooks are new or changed."], choices: [{ text: "Review hooks", number: 1, focused: true }, { text: "Trust all and continue", number: 2 }, { text: "Continue without trusting (hooks won't run)", number: 3 }], hints: ["Press enter to confirm or esc to go back"] },
  pi: { rows: ["Trust project folder?", "C:\\dev\\app"], choices: [{ text: "Trust", focused: true }, { text: "Trust parent folder (C:\\dev)" }, { text: "Trust (this session only)" }, { text: "Do not trust" }], hints: ["↑↓ navigate enter select escape/ctrl+c cancel"] },
} as const;
const asking = (panel: object) => ({ type: "screen", asking: true, open: true, working: false, submission: "", panel });
const closed = { type: "screen", asking: false, open: false, working: false, submission: "" };

for (const harness of ["claude", "pi", "codex"] as const) {
  test(`${harness} mode and interrupt keys reach the native terminal while the box keeps the keyboard and the draft`, async ({ page }) => {
    const relay = await open(page, snapshot({}, { harness }));
    await openGoblin(page);
    await field(page).fill("keep this draft");
    await field(page).press("Control+End");
    await field(page).press("Control+c");
    await expect.poll(() => typed(relay)).toBe("\x03");
    await expect(field(page)).toHaveValue("keep this draft");
    await field(page).press("Shift+Tab");
    await expect.poll(() => typed(relay)).toBe("\x03\x1b[Z");
    await expect(field(page)).toBeFocused();
    await expect(messages(page)).toBeVisible();
    await expect(field(page)).toHaveValue("keep this draft");
    relay.say({ type: "held", reason: "pipeline owns this task" });
    await field(page).press("Shift+Tab");
    expect(typed(relay)).toBe("\x03\x1b[Z");
  });

  test(`${harness}'s own screen opens in the chat as a list its choices, keys and Send answer`, async ({ page }, testInfo) => {
    // Arrange
    const relay = await open(page, snapshot({}, { harness }));
    await openGoblin(page);
    const panel = PANELS[harness];

    // Act
    relay.say(asking(panel));

    // Assert: the question and every choice, in the chat, the harness's
    // focus marked, the conversation still there and the terminal not in.
    await expect(list(page)).toContainText(panel.rows[0]);
    for (const each of panel.choices) await expect(choice(page, each.text)).toBeVisible();
    await expect(choice(page, panel.choices[0].text)).toHaveAttribute("aria-current", "true");
    await expect(messages(page)).toBeVisible();
    await expect(back(page)).toHaveCount(0);
    // No row of key chips: the keys are the box's own, and a harness that
    // names a way out gets one plain Cancel.
    await expect(list(page).getByRole("button", { name: /^Press / })).toHaveCount(0);
    await expect(list(page).getByRole("button", { name: "Cancel", exact: true })).toHaveCount(panel.hints.some((hint) => /esc/i.test(hint)) ? 1 : 0);
    await page.screenshot({ path: testInfo.outputPath(`${harness}-panel.png`) });

    // A choice is the arrows from the harness's focus to it, then Enter.
    await choice(page, panel.choices[2].text).click();
    await expect.poll(() => typed(relay)).toBe(DOWN + DOWN + "\r");

    // The empty box hands the screen the terminal's keys, and a number picks.
    await field(page).focus();
    for (const key of ["ArrowDown", "ArrowUp", "Escape", "Tab"]) await field(page).press(key);
    const expected = DOWN + DOWN + "\r" + DOWN + UP + "\x1b" + "\t";
    await expect.poll(() => typed(relay)).toBe(expected);
    if ("number" in panel.choices[1]) {
      await field(page).press("2");
      await expect.poll(() => typed(relay)).toBe(expected + DOWN + "\r");
      await expect(field(page)).toHaveValue("");
    }

    // What he writes is typed into the screen on one line, with no Enter.
    const before = typed(relay);
    await field(page).fill("use the\nother file");
    await field(page).press("Enter");
    await expect.poll(() => typed(relay)).toBe(before + "use the other file");
    await expect(field(page)).toHaveValue("");

    // Once the harness is back at its input line the list goes.
    relay.say(closed);
    await expect(list(page)).toHaveCount(0);
  });
}

test("a held session's open screen offers no list, and Send queues what he wrote", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const actions: { kind: string; text: string }[] = [];
  await page.route("**/api/actions", (route) => { actions.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: {} }); });
  await openGoblin(page);
  relay.say({ type: "held", reason: "pipeline owns this task" });
  relay.say(asking(PANELS.claude));

  // Act
  await field(page).fill("yes");
  await field(page).press("Enter");

  // Assert
  await expect.poll(() => actions.map((action) => action.kind + ":" + action.text)).toEqual(["message:yes"]);
  await expect(list(page)).toHaveCount(0);
  expect(typed(relay)).toBe("");
});

// sendFromBox types text into Zeke's box and sends it with Enter, as he does.
async function sendFromBox(page: Page, relay: Relay, text: string) {
  const sent = relay.submissions.length;
  await field(page).fill(text);
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toContain(pasted(text) + "\r");
  await expect.poll(() => relay.submissions.length).toBe(sent + 1);
}

test("a picker the box opens despite background work stays in the chat, and goes at the input line", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { runtime: { state: "busy", reason: "Background shell running", at } }));
  await openGoblin(page);
  relay.say(closed);
  await expect(page.locator(".session-working")).toBeVisible();
  await sendFromBox(page, relay, "/model");

  // Act
  relay.say(asking({ rows: ["Select model", "Switch between Claude models."], choices: [{ text: "Default (recommended)", number: 1, focused: true }, { text: "Sonnet", number: 2 }], hints: ["Enter to confirm · Esc to exit"] }));

  // Assert
  await expect(choice(page, "Sonnet")).toBeVisible();
  await expect(messages(page)).toBeVisible();
  await expect(back(page)).toHaveCount(0);
  await expect(field(page)).toBeFocused();
  await field(page).press("ArrowDown");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("/model") + "\r" + DOWN + "\r");
  relay.say(closed);
  await expect(list(page)).toHaveCount(0);
  await expect(messages(page)).toBeVisible();
});

test("a screen the relay could not read still offers Cancel, its keys from the box, and the terminal", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);

  // Act
  relay.say({ type: "screen", asking: false, open: true, working: false, submission: "" });

  // Assert
  await list(page).getByRole("button", { name: "Cancel", exact: true }).click();
  await expect.poll(() => typed(relay)).toBe("\x1b");
  await list(page).getByRole("button", { name: "Show the terminal" }).click();
  await expect(back(page)).toBeVisible();
  await expect(terminal(page)).toBeFocused();
});

test("Back to messages returns to the messages with the keyboard in the box, and his draft there is the same", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await field(page).fill("half a thought");
  relay.say(asking(PANELS.claude));
  await list(page).getByRole("button", { name: "Show the terminal" }).click();
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
  relay.say(asking(PANELS.claude));
  await list(page).getByRole("button", { name: "Show the terminal" }).click();
  await expect(back(page)).toBeVisible();
  await page.waitForTimeout(300);
  relay.say(closed);
  await expect(messages(page)).toBeVisible();
  await page.waitForTimeout(300);

  // Assert
  expect(relay.resizes.length).toBe(seen);
});

for (const [label, width, height] of [["desktop", 1440, 960], ["phone", 390, 844]] as const) {
  test(`an open list leaves the conversation and the box's controls whole, with nothing overlapping, on ${label}`, async ({ page }) => {
    // Arrange: a permission prompt with a long preview.
    await page.setViewportSize({ width, height });
    const relay = await open(page);
    await openGoblin(page);
    const preview = Array.from({ length: 30 }, (_, at) => "  " + (at + 1) + " a line of the file it wants to write");

    // Act
    relay.say(asking({ ...PANELS.claude, rows: ["Create file", "nine.txt", ...preview, "Do you want to create nine.txt?"] }));
    await expect(list(page)).toBeVisible();

    // Assert: the list scrolls inside itself, the conversation keeps its own
    // room above the box's dock, and the box with its controls is on screen.
    const frame = (await page.locator(".messages-view:not([hidden]) .conversation-frame").boundingBox())!;
    const dock = (await page.locator(".messages-view:not([hidden]) .composer-dock").boundingBox())!;
    expect(frame.y + frame.height, "the conversation ends where the dock begins").toBeLessThanOrEqual(dock.y + 0.5);
    expect(frame.height, "the conversation keeps a few lines").toBeGreaterThanOrEqual(80);
    expect(await list(page).evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
    for (const control of [box(page).getByRole("button", { name: "Send", exact: true }), box(page).getByRole("button", { name: "Change model", exact: true })]) {
      const bounds = (await control.boundingBox())!;
      expect(bounds.y + bounds.height, "the box's controls are inside the window").toBeLessThanOrEqual(height + 0.5);
    }
  });
}
