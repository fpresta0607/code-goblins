import { expect, test, type Page } from "./site";
import { at, NAME, open, openGoblin, pickStyle, snapshot } from "./session-board";

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
const screen = (page: Page) => page.getByRole("region", { name: NAME + "'s browser", exact: true });
const saved = (page: Page) => page.evaluate(() => localStorage.getItem("ceo-chat-style"));
const fitsWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

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
