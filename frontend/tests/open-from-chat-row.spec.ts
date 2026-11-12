import { expect, test, type Page } from "./site";
import { at, card, GOBLIN, NAME, open, openGoblin, OTHER, pickStyle, snapshot } from "./session-board";

// A row of a goblin's chat opens the view behind it (#2362): a command's row
// opens that goblin's terminal in its panel, and a row whose command drives
// the goblin's own browser opens its Screen view while that browser runs. The
// panel holds his pick until it shows another session or closes, nothing
// switches it back by itself, and the saved chat style stays as he chose it.

const call = (id: string, subject: string, result: string) => ({ kind: "call", call_id: id, tool: "Bash", subject, state: "done", result });
const REPLY = { id: "reply-1", seq: 1, at, kind: "reply", blocks: [
  { kind: "text", text: "Running the tests first." },
  call("bash-1", "npm test", "12 passing"),
  { kind: "text", text: "Now the sign-in page." },
  call("browser-1", "chrome-devtools-axi screenshot --output shot.png", "saved shot.png"),
  { kind: "text", text: "Both done." },
] };
const ROWS = { conversation: "fixture-conversation", more: false, messages: [{ id: "prompt-0", seq: 0, at, kind: "prompt", source: "overlord", text: "Check the sign-in page" }, REPLY] };
const BROWSING = snapshot({}, { browser: true });
const STATE = { type: "state", is_driven: false, can_drive: true, title: "Sign in", host: "shop.example.com", has_page: true };

const messages = (page: Page) => page.getByRole("log", { name: "Messages of " + NAME });
const messagesRegion = (page: Page) => page.getByRole("region", { name: "Messages", exact: true });
const terminalInput = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const screen = (page: Page, name = NAME) => page.getByRole("region", { name: name + "'s browser", exact: true });
const viewButton = (page: Page, name: "Chat" | "Screen") => page.getByRole("group", { name: "Panel view", exact: true }).getByRole("button", { name, exact: true });
const saved = (page: Page) => page.evaluate(() => localStorage.getItem("ceo-chat-style"));
const fitsWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);
const tell = (page: Page, next: object) => page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), next);
const relayScreen = (page: Page) => page.routeWebSocket("**/api/screen?*", (socket) => socket.send(JSON.stringify(STATE)));
const openScreenFromRow = async (page: Page) => (await openRow(page, "chrome-devtools-axi screenshot")).getByRole("button", { name: "Open screen", exact: true }).click();

// openRow opens the row of the command given, unless it is open, and gives
// what it holds.
async function openRow(page: Page, command: string) {
  const row = messages(page).locator("details.message-call", { hasText: command });
  if (await row.getAttribute("open") === null) await row.locator("> summary").click();
  return row.locator("> .disclosure-content");
}

for (const { width, height, label } of [{ width: 1440, height: 1100, label: "desktop" }, { width: 390, height: 844, label: "phone" }]) {
  test.describe(`at ${label} width`, () => {
    test.use({ viewport: { width, height } });

    test("a command row's Open in terminal shows the goblin's terminal from the keyboard and leaves the saved chat style alone", async ({ page }, testInfo) => {
      // Arrange
      await open(page, snapshot(), undefined, ROWS);
      await openGoblin(page);
      const body = await openRow(page, "npm test");
      const opener = body.getByRole("button", { name: "Open in terminal", exact: true });
      await expect(opener).toBeVisible();
      expect(await fitsWidth(page)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`row-${label}.png`) });

      // Act
      await opener.focus();
      await page.keyboard.press("Enter");

      // Assert
      await expect(terminalInput(page)).toBeFocused();
      await expect(messagesRegion(page)).toBeHidden();
      expect(await saved(page)).toBe("modern");
      expect(await fitsWidth(page)).toBe(true);
    });

    test("a browser row's Open screen shows the goblin's Screen view from the keyboard while its browser runs", async ({ page }, testInfo) => {
      // Arrange
      await page.routeWebSocket("**/api/screen?*", (socket) => socket.send(JSON.stringify(STATE)));
      await open(page, BROWSING, undefined, ROWS);
      await openGoblin(page);
      const body = await openRow(page, "chrome-devtools-axi screenshot");

      // Act
      await body.getByRole("button", { name: "Open screen", exact: true }).focus();
      await page.keyboard.press("Enter");

      // Assert
      await expect(screen(page)).toBeFocused();
      await expect(page.getByRole("group", { name: "Panel view", exact: true }).getByRole("button", { name: "Screen", exact: true })).toHaveAttribute("aria-pressed", "true");
      await expect(messagesRegion(page)).toBeHidden();
      expect(await fitsWidth(page)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`screen-${label}.png`) });
    });
  });
}

test("a browser row offers no Open screen while the goblin's browser is not running, and a row that runs no command offers neither", async ({ page }) => {
  // Arrange
  const reading = { id: "reply-2", seq: 2, at, kind: "reply", blocks: [{ kind: "text", text: "Reading the page's file." }, { kind: "call", call_id: "read-1", tool: "Read", subject: "frontend/src/screen-view.tsx", state: "done", result: "245 lines" }] };
  await open(page, snapshot(), undefined, { ...ROWS, messages: [...ROWS.messages, reading] });
  await openGoblin(page);

  // Act
  const browserBody = await openRow(page, "chrome-devtools-axi screenshot");
  const readBody = await openRow(page, "frontend/src/screen-view.tsx");

  // Assert
  await expect(browserBody.getByRole("button", { name: "Open in terminal", exact: true })).toBeVisible();
  await expect(browserBody.getByRole("button", { name: "Open screen", exact: true })).toHaveCount(0);
  await expect(readBody.getByText("245 lines")).toBeVisible();
  await expect(readBody.getByRole("button", { name: /^Open (in terminal|screen)$/ })).toHaveCount(0);
});

test("his pick holds as the goblin replies, Modern chat from the header brings the messages back, and a closed panel reopens in the saved style", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), undefined, ROWS);
  await openGoblin(page);
  await (await openRow(page, "npm test")).getByRole("button", { name: "Open in terminal", exact: true }).click();
  await expect(terminalInput(page)).toBeVisible();

  // Act: the goblin replies again.
  await page.evaluate((reply) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [reply] } } })),
    { id: "reply-3", seq: 3, at, kind: "reply", blocks: [{ kind: "text", text: "One more thing." }] });

  // Assert: the reply came in behind the terminal, which stays.
  await expect(page.getByRole("log", { name: "Messages of " + NAME, includeHidden: true }).getByText("One more thing.")).toBeAttached();
  await expect(terminalInput(page)).toBeVisible();
  await expect(messagesRegion(page)).toBeHidden();

  // Act and Assert: his choice of Modern chat in the header shows the
  // messages again.
  await pickStyle(page, "Modern chat");
  await expect(messagesRegion(page)).toBeVisible();

  // Act: he opens the terminal from the row again, then closes the panel
  // and opens it again.
  await (await openRow(page, "npm test")).getByRole("button", { name: "Open in terminal", exact: true }).click();
  await expect(terminalInput(page)).toBeVisible();
  await page.getByRole("button", { name: "Back to the CEO", exact: true }).click();
  await page.getByRole("button", { name: "Close panel", exact: true }).click();
  await expect(page.locator(".context-pane")).toBeHidden();

  // Assert: the panel opens on the messages, his saved style.
  await openGoblin(page);
  expect(await saved(page)).toBe("modern");
});

// A goblin paused or resuming takes messages in its box and has no terminal in
// the conversation's place, so a command's row has no Open in terminal.
for (const phase of ["paused", "resuming"]) {
  test(`a ${phase} goblin's command row offers no Open in terminal`, async ({ page }) => {
    // Arrange
    await open(page, snapshot({}, { phase }), undefined, ROWS);
    await card(page, NAME).click();
    await expect(messagesRegion(page)).toBeVisible();

    // Act
    const body = await openRow(page, "npm test");

    // Assert
    await expect(body.getByText("12 passing")).toBeVisible();
    await expect(body.getByRole("button", { name: "Open in terminal", exact: true })).toHaveCount(0);
  });
}

test("while the goblin's browser runs, a command that does not drive it offers the terminal and no Open screen", async ({ page }) => {
  // Arrange
  await open(page, BROWSING, undefined, ROWS);
  await openGoblin(page);

  // Act
  const body = await openRow(page, "npm test");

  // Assert
  await expect(body.getByRole("button", { name: "Open in terminal", exact: true })).toBeVisible();
  await expect(body.getByRole("button", { name: "Open screen", exact: true })).toHaveCount(0);
});

test("a row's Open screen hands the keyboard to the Screen view once, so the panel's Screen tab later leaves it on the tab", async ({ page }) => {
  // Arrange
  await relayScreen(page);
  await open(page, BROWSING, undefined, ROWS);
  await openGoblin(page);
  await openScreenFromRow(page);
  await expect(screen(page)).toBeFocused();

  // Act
  await viewButton(page, "Chat").click();
  await expect(messagesRegion(page)).toBeVisible();
  await viewButton(page, "Screen").click();

  // Assert
  await expect(screen(page)).toBeVisible();
  await expect(viewButton(page, "Screen")).toBeFocused();
});

test("a row's Open in terminal with Modern saved reads back to the conversation from a held full-screen terminal and leaves Modern saved", async ({ page }) => {
  // Arrange: Zeke's full-screen Codex, opened from his row, is held.
  const relay = await open(page, snapshot({}, { harness: "codex" }), "", ROWS, "\x1b[?1049h> ready\r\n");
  await openGoblin(page);
  await (await openRow(page, "npm test")).getByRole("button", { name: "Open in terminal", exact: true }).click();
  await expect(terminalInput(page)).toBeVisible();
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(page.locator(".context-pane").getByText("Typing is paused.", { exact: true })).toBeVisible();

  // Act: a wheel up over the terminal.
  await page.locator(".terminal-view:not(.staged) > .xterm .xterm-screen").hover();
  await page.mouse.wheel(0, -45);

  // Assert
  await expect(messagesRegion(page)).toBeVisible();
  expect(await saved(page)).toBe("modern");
});

// The goblin's Chrome closes two minutes after nothing uses it and starts
// again on its next command: the Screen view gives way to Chat as it closes,
// and Chat stays as it starts again, however the Screen view was opened.
for (const [how, openScreen] of [["a browser row's Open screen", openScreenFromRow], ["the panel's Screen tab", (page: Page) => viewButton(page, "Screen").click()]] as const) {
  test(`a Screen view opened from ${how} gives way to Chat as the browser closes and never comes back by itself`, async ({ page }) => {
    // Arrange
    await relayScreen(page);
    await open(page, BROWSING, undefined, ROWS);
    await openGoblin(page);
    await openScreen(page);
    await expect(screen(page)).toBeVisible();

    // Act: the browser closes.
    await tell(page, snapshot({ revision: 2 }, { browser: false }));

    // Assert
    await expect(screen(page)).toHaveCount(0);
    await expect(messagesRegion(page)).toBeVisible();

    // Act: the browser starts again.
    await tell(page, snapshot({ revision: 3 }, { browser: true }));

    // Assert
    await expect(viewButton(page, "Screen")).toHaveAttribute("aria-pressed", "false");
    await expect(viewButton(page, "Chat")).toHaveAttribute("aria-pressed", "true");
    await expect(screen(page)).toHaveCount(0);
    await expect(messagesRegion(page)).toBeVisible();
  });
}

test("a row's Open screen on one goblin hands the keyboard to no other goblin's Screen view", async ({ page }) => {
  // Arrange: Zeke's and Ivy's browsers both run.
  const ivy = OTHER.goblin_name + " - " + OTHER.goblin_title;
  await relayScreen(page);
  await open(page, snapshot({ tasks: [{ ...GOBLIN, browser: true }, { ...OTHER, browser: true }] }), undefined, ROWS);
  await openGoblin(page);
  await openScreenFromRow(page);
  await expect(screen(page)).toBeFocused();

  // Act: the switch's keys show Ivy, whose Screen view the panel keeps.
  await page.keyboard.press("Control+Alt+3");

  // Assert
  await expect(page.locator("#panel-title")).toHaveText(ivy);
  await expect(screen(page, ivy)).toBeVisible();
  await expect(screen(page, ivy)).not.toBeFocused();
});
