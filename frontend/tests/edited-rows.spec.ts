import { expect, test, type Locator, type Page } from "./site";
import { at, NAME, open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-11-13, of how the board shows a file edit (#2534):
// "editted formattgin shoudl align with goblins mdoern temrinal plan that we
// approve for terminal panels...follows standardidzed appraoch". An edited
// row opens to its diff drawn as terminal text is (#2524): in the same card,
// under a header with Copy as an icon and Full screen, each line wrapped at
// its words with its later rows set in, an added line on a green ground and
// a removed one on a red ground by its mark alone, in a goblin's panel and
// the CEO's chat alike.

// An edit as Claude Code records one: the lines around the change, one of
// them code that reads like an error, a removed line, and two added lines,
// one longer than any card is wide.
const SWIPE = [
  " export const MESSAGES = {",
  "   error: \"the swipe was lost\",",
  " };",
  " ",
  " export function listen(element: HTMLElement, swipe: (event: TouchEvent) => void) {",
  "-  // A swipe was left to the page.",
  "+  // A vertical swipe scrolls the terminal's history as a mouse wheel does, and a sideways one is left to the page, so the panels still slide under a finger the way a phone's reader expects of a terminal that fills its screen.",
  "+  element.addEventListener(\"touchmove\", swipe, { passive: false });",
  " }",
];
// A file written anew, every line of it added.
const NOTES = ["+# Phone swipes", "+", "+A vertical swipe scrolls the terminal's history.", "+A sideways swipe slides the panels."];
// An edit of more lines than a card shows at once.
const KEYS = Array.from({ length: 30 }, (_, line) => [`-  { key: "F${line + 1}", sends: "old-${line + 1}" },`, `+  { key: "F${line + 1}", sends: "new-${line + 1}" },`]).flat();
// A command that edited a file, as a Codex session's apply_patch through its
// shell is recorded: the change folds into the command's row.
const PATCH = "apply_patch <<'PATCH'\n*** Begin Patch\n*** Update File: src/example.ts\n@@\n-const value = \"old\";\n+const value = \"new\";\n*** End Patch\nPATCH";
const EXAMPLE = ["@@ -1,2 +1,2 @@", " const kept = true;", "-const value = \"old\";", "+const value = \"new\";"];

const call = (id: string, tool: string, subject: string, fields: object) => ({ kind: "call", call_id: id, tool, subject, state: "done", ...fields });
const ROWS = { conversation: "fixture-conversation", more: false, messages: [
  { id: "prompt-0", seq: 0, at, kind: "prompt", source: "overlord", text: "Let a vertical swipe scroll the phone terminal" },
  { id: "reply-1", seq: 1, at, kind: "reply", blocks: [
    { kind: "text", text: "Handing a vertical swipe to the history." },
    call("edit-1", "Edit", "frontend/src/terminal-swipe.ts", { edit: { added: 2, removed: 1, diff: SWIPE } }),
    { kind: "text", text: "Writing down how swipes behave." },
    call("write-1", "Write", "docs/phone-swipes.md", { edit: { added: 4, removed: 0, diff: NOTES } }),
    { kind: "text", text: "Renaming what each key sends." },
    call("edit-2", "Edit", "frontend/src/terminal-keys.ts", { edit: { added: 30, removed: 30, diff: KEYS } }),
    { kind: "text", text: "Patching the example." },
    call("exec-1", "exec_command", "apply_patch", { input: PATCH, result: "Success. Updated the following files:\nM src/example.ts", edit: { added: 1, removed: 1, diff: EXAMPLE } }),
    { kind: "text", text: "Done." },
  ] },
] };

const SIZES = [{ width: 1440, height: 1100, label: "desktop" }, { width: 390, height: 844, label: "phone" }];
const messages = (page: Page, name: string) => page.getByRole("log", { name: "Messages of " + name, exact: true });
const fitsWidth = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);
const clipboard = async (page: Page) => (await page.evaluate(() => navigator.clipboard.readText())).replaceAll("\r\n", "\n");
const over = (page: Page) => page.getByRole("dialog", { name: "Terminal text", exact: true });

// openChat opens Zeke's panel on the conversation above.
async function openChat(page: Page) {
  await open(page, snapshot(), undefined, ROWS);
  await openGoblin(page);
}

// openRow opens the row of the file or command given in the chat named,
// Zeke's unless another is, and gives the row once its card shows.
async function openRow(page: Page, subject: string, name = NAME) {
  const row = messages(page, name).locator("details.message-call", { hasText: subject });
  await row.locator("> summary").click();
  await expect(row.locator(".terminal-block")).toBeVisible();
  return row;
}

// ground is a line's own background as its red, green, blue and alpha.
const ground = (line: Locator) => line.evaluate((element) => getComputedStyle(element).backgroundColor.match(/[\d.]+/g)!.map(Number));

// contrast is how far a line's text stands out from what is behind it, as
// WCAG counts it: the line's own ground laid over its card's.
const contrast = (line: Locator) => line.evaluate((element) => {
  const parts = (color: string) => color.match(/[\d.]+/g)!.map(Number);
  const card = parts(getComputedStyle(element.closest(".message-code")!).backgroundColor);
  const [red, green, blue, alpha = 1] = parts(getComputedStyle(element).backgroundColor);
  const behind = [red, green, blue].map((value, at) => value * alpha + card[at] * (1 - alpha));
  const luminance = (rgb: number[]) => rgb.map((value) => value / 255)
    .map((value) => value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4)
    .reduce((sum, value, at) => sum + value * [0.2126, 0.7152, 0.0722][at], 0);
  const [light, dark] = [luminance(parts(getComputedStyle(element).color).slice(0, 3)), luminance(behind)].sort((one, other) => other - one);
  return (light + 0.05) / (dark + 0.05);
});

for (const { width, height, label } of SIZES) {
  test.describe(`at ${label} width`, () => {
    test.use({ viewport: { width, height } });

    test("an edit opens to its lines in one terminal card under diff, Copy its icon alone and Full screen beside it, as wide as its row", async ({ page }) => {
      // Arrange and Act
      await openChat(page);
      const row = await openRow(page, "terminal-swipe.ts");
      const card = row.locator(".terminal-block");

      // Assert: one card, headed by what it holds, with Copy as an icon and Full screen.
      await expect(card).toHaveCount(1);
      await expect(card.locator("figcaption")).toHaveText("diff");
      const copy = card.getByRole("button", { name: "Copy", exact: true });
      await expect(copy).toHaveText("");
      await expect(copy.locator("svg")).toBeVisible();
      await expect(card.getByRole("button", { name: "Full screen", exact: true })).toBeVisible();

      // Assert: every line as the diff writes it, each a line of terminal text.
      await expect(card.locator(".terminal-diff > .terminal-line")).toHaveText(SWIPE);

      // Assert: the card starts where the row does and is as wide.
      const [summary, box] = [await row.locator("> summary").boundingBox(), await card.boundingBox()];
      expect(Math.abs(box!.x - summary!.x), "it starts where the row does").toBeLessThanOrEqual(1);
      expect(Math.abs(box!.width - summary!.width), "it is as wide as the row").toBeLessThanOrEqual(1);
      expect(await fitsWidth(page)).toBe(true);
    });

    test("a long line of an edit wraps at its words, its later rows set in, and nothing scrolls sideways", async ({ page }) => {
      // Arrange and Act
      await openChat(page);
      const diff = (await openRow(page, "terminal-swipe.ts")).locator(".terminal-diff");
      const long = diff.locator(".terminal-line", { hasText: "A vertical swipe" });

      // Assert: it takes more rows than a short line, inside the card.
      expect((await long.boundingBox())!.height, "the long line wraps").toBeGreaterThan((await diff.locator(".terminal-line", { hasText: "A swipe was left" }).boundingBox())!.height * 1.5);
      expect(await diff.evaluate((element) => element.scrollWidth <= element.clientWidth + 1), "nothing scrolls sideways").toBe(true);
      expect(await fitsWidth(page)).toBe(true);

      // Assert: its first row starts at its mark, and each later row sets in.
      const layout = await long.evaluate((line) => {
        const words = document.createRange();
        words.selectNodeContents(line);
        const starts = new Map<number, number>();
        for (const rect of words.getClientRects()) {
          if (rect.width > 0) starts.set(Math.round(rect.top), Math.min(starts.get(Math.round(rect.top)) ?? Infinity, rect.left));
        }
        return { indent: line.getBoundingClientRect().left + parseFloat(getComputedStyle(line).paddingLeft), starts: [...starts].sort((one, other) => one[0] - other[0]).map(([, left]) => left) };
      });
      expect(layout.starts.length, "the long line wraps").toBeGreaterThan(1);
      expect(layout.starts[0], "its first row starts at its mark").toBeLessThan(layout.indent - 1);
      for (const start of layout.starts.slice(1)) expect(Math.abs(start - layout.indent), "a later row sets in").toBeLessThan(1);
    });

    test("an edit of many lines shows its first ones and scrolls the rest in its card, Copy takes every line, and Full screen shows it over the window", async ({ page, context }) => {
      // Arrange
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      await openChat(page);
      const row = await openRow(page, "terminal-keys.ts");
      const shown = await row.locator(".terminal-diff").evaluate((element) => ({ height: element.clientHeight, whole: element.scrollHeight, line: parseFloat(getComputedStyle(element).lineHeight) }));

      // Assert: sixteen lines at most show, and the rest scroll inside the card.
      expect(shown.height, "it shows sixteen lines at most").toBeLessThanOrEqual(16 * shown.line + 1);
      expect(shown.whole, "the rest scroll inside the card").toBeGreaterThan(shown.height);

      // Act
      await row.getByRole("button", { name: "Copy", exact: true }).click();

      // Assert: every line, the ones scrolled out of view too.
      await expect.poll(() => clipboard(page)).toBe(KEYS.join("\n"));

      // Act
      await row.getByRole("button", { name: "Full screen", exact: true }).click();

      // Assert: the window, whole, with every line of the edit in it.
      expect(await over(page).boundingBox(), "it fills the window").toEqual({ x: 0, y: 0, width, height });
      await expect(over(page).locator(".terminal-diff > .terminal-line")).toHaveCount(KEYS.length);
      expect((await over(page).locator(".terminal-diff").boundingBox())!.height, "it takes the window's height").toBeGreaterThan(shown.height * 2);
    });
  });
}

test("each line of an edit reads in its mark's colour, an added one on a green ground and a removed one on a red ground, and none by its words", async ({ page }) => {
  // Arrange and Act
  await openChat(page);
  const lines = (await openRow(page, "terminal-swipe.ts")).locator(".terminal-diff > .terminal-line");
  const removed = lines.filter({ hasText: "A swipe was left to the page." });
  const added = lines.filter({ hasText: "addEventListener" });
  const around = lines.filter({ hasText: "the swipe was lost" });

  // Assert: its mark says what a line is, and a line of code that reads like
  // an error is still a line around the change.
  await expect(removed).toHaveClass("terminal-line removed");
  await expect(added).toHaveClass("terminal-line added");
  await expect(around).toHaveClass("terminal-line");

  // Assert: an added line on a green ground, a removed one on a red ground,
  // and a line around them on the card's own.
  const [addedGround, removedGround] = [await ground(added), await ground(removed)];
  expect(addedGround[1], "an added line's ground is green").toBeGreaterThan(addedGround[0]);
  expect(removedGround[0], "a removed line's ground is red").toBeGreaterThan(removedGround[1]);
  expect((await ground(around))[3], "a line around the change has no ground of its own").toBe(0);

  // Assert: each reads clearly on what is behind it.
  for (const line of [added, removed, around]) expect(await contrast(line), "its text stands out from its ground").toBeGreaterThanOrEqual(4.5);
});

test("a new file opens to its lines, every one of them added", async ({ page }) => {
  // Arrange and Act
  await openChat(page);
  const card = (await openRow(page, "phone-swipes.md")).locator(".terminal-block");

  // Assert
  await expect(card.locator("figcaption")).toHaveText("diff");
  await expect(card.locator(".terminal-diff > .terminal-line")).toHaveText(NOTES);
  await expect(card.locator(".terminal-diff > .terminal-line.added")).toHaveCount(NOTES.length);
});

test("a command that edited a file reads in one card: its lines, its diff in place of what it printed, and how it ended", async ({ page }) => {
  // Arrange and Act
  await openChat(page);
  const card = (await openRow(page, "apply_patch")).locator(".terminal-block");

  // Assert: one card, its command and its diff each under its own header.
  await expect(card).toHaveCount(1);
  await expect(card.locator("figcaption > span")).toHaveText(["shell", "diff"]);
  await expect(card.locator(".terminal-command > code > .terminal-line").first()).toHaveText("apply_patch <<'PATCH'");
  await expect(card.locator(".terminal-diff > .terminal-line")).toHaveText(EXAMPLE);
  await expect(card.locator(".terminal-output"), "what it printed gives way to its diff").toHaveCount(0);
  await expect(card.locator(".terminal-result")).toHaveText("Completed");

  // Assert: each part has its own Copy, and the card one Full screen.
  await expect(card.getByRole("button", { name: "Copy command", exact: true })).toBeVisible();
  await expect(card.getByRole("button", { name: "Copy diff", exact: true })).toBeVisible();
  await expect(card.getByRole("button", { name: "Full screen", exact: true })).toHaveCount(1);
});

test("the CEO's chat draws an edit as a goblin's panel does", async ({ page }) => {
  // Arrange
  await openChat(page);
  const look = async (name: string) => (await openRow(page, "terminal-swipe.ts", name)).locator(".terminal-block").evaluate((card) => ({
    card: getComputedStyle(card).backgroundColor + " " + getComputedStyle(card).borderRadius,
    headers: Array.from(card.querySelectorAll("figcaption"), (header) => header.textContent),
    lines: Array.from(card.querySelectorAll(".terminal-line"), (line) => [line.className, getComputedStyle(line).color, getComputedStyle(line).backgroundColor].join(" ")),
  }));
  const goblin = await look(NAME);

  // Act
  await page.keyboard.press("Control+Alt+1");

  // Assert
  expect(await look("CEO")).toEqual(goblin);
});
