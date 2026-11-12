import { readFileSync } from "node:fs";
import { expect, ORIGIN, test, type Page } from "./site";
import { at, field, NAME, open, openGoblin, OTHER, pickStyle, snapshot } from "./session-board";

// A board address opens one place (#2363): ?task=<id> a goblin's panel and
// ?item=<key> one Command Center item, as a link in a review page, a phone's
// push or a goblin's message names it. The board opens only a goblin it holds
// or an item waiting on him, never acts on what it opens, and keeps what he
// was typing: a link inside the board opens its place in this page, with no
// reload.

const IVY = OTHER.goblin_name + " - " + OTHER.goblin_title;
const QUESTION = { id: "release-asks", identity: "c".repeat(64), task: "", status: "pending", created_at: at, text: "May the release train merge now?", options: ["Merge it", "Hold it"], recommended: "Merge it" };
const ANSWERED = { ...QUESTION, id: "theme-asked", text: "Which accent colour?", status: "succeeded", answer_id: "a-1", answered_by: "overlord" };
const BOARD = snapshot({ questions: [QUESTION, ANSWERED] });
const TO_IVY = ORIGIN + "/?task=" + OTHER.id;
const TO_ZEKE = ORIGIN + "/?task=cg-touch";
const TO_QUESTION = ORIGIN + "/?item=question:" + QUESTION.id;
const TO_NOBODY = ORIGIN + "/?task=cg-gone";
const CHAT = { conversation: "fixture-conversation", more: false, messages: [
  { id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "Where do I look?" },
  { id: "m1", seq: 1, at, kind: "reply", blocks: [{ kind: "text", text: `Ivy has it: ${TO_IVY}. Zeke: ${TO_ZEKE}. The release asks ${TO_QUESTION}, and ${TO_NOBODY} is gone.` }] },
] };

const panelTitle = (page: Page) => page.locator("#panel-title");
const ivysPanel = (page: Page) => panelTitle(page).filter({ hasText: IVY });
const questionCard = (page: Page) => page.locator("dialog.question-modal");
const talk = (page: Page) => page.getByRole("log", { name: "Messages of " + NAME });
const search = (page: Page) => page.evaluate(() => location.search);
// mark leaves a value on the page's window, which a reload would lose.
const mark = (page: Page) => page.evaluate(() => { (window as { boardMark?: number }).boardMark = 1; });
const isMarked = (page: Page) => page.evaluate(() => (window as { boardMark?: number }).boardMark === 1);

// acts records every request the board makes that would change something:
// an answer, a lifecycle step, a start, a run or a stored value.
function acts(page: Page): string[] {
  const made: string[] = [];
  page.on("request", (request) => {
    if (request.method() !== "GET" && /\/api\/(actions|tasks|credentials|runs|reviews|questions)/.test(request.url())) made.push(request.method() + " " + request.url());
  });
  return made;
}

async function openChat(page: Page) {
  const relay = await open(page, BOARD, undefined, CHAT, "> opened " + TO_IVY + "\r\n");
  await openGoblin(page);
  await pickStyle(page, "Modern chat");
  await expect(talk(page).getByText("is gone.", { exact: false })).toBeVisible();
  return relay;
}

for (const [device, width, height] of [["phone", 390, 844], ["desktop", 1440, 1000]] as const) {
  test.describe(`on a ${device}`, () => {
    test.use({ viewport: { width, height } });

    test("an address that names a goblin opens its panel as the board loads, and the address goes back to plain", async ({ page }) => {
      // Arrange
      await open(page, BOARD);
      const made = acts(page);

      // Act
      await page.goto("/?task=" + OTHER.id);

      // Assert
      await expect(panelTitle(page)).toHaveText(IVY);
      expect(await search(page)).toBe("");
      await page.reload();
      await expect(page.locator(".board-column").first()).toBeVisible();
      await expect(ivysPanel(page)).toHaveCount(0);
      expect(made).toEqual([]);
    });

    test("an address that names an item waiting on him opens it in the Command Center, unanswered", async ({ page }) => {
      // Arrange
      await open(page, BOARD);
      const made = acts(page);

      // Act
      await page.goto("/?item=question:" + QUESTION.id);

      // Assert
      await expect(questionCard(page)).toContainText(QUESTION.text);
      expect(await search(page)).toBe("");
      expect(made).toEqual([]);
    });

    for (const [what, address] of [["a goblin the board does not hold", "/?task=cg-gone"], ["an item that no longer waits", "/?item=question:" + ANSWERED.id], ["an item the board does not hold", "/?item=question:nobody"], ["no place at all", "/?task=" + OTHER.id + "&item=question:" + QUESTION.id]] as const) {
      test(`an address that names ${what} opens nothing`, async ({ page }) => {
        // Arrange
        await open(page, BOARD);
        const made = acts(page);

        // Act
        await page.goto(address);
        await expect(page.locator(".board-column").first()).toBeVisible();

        // Assert
        await expect(ivysPanel(page)).toHaveCount(0);
        await expect(page.locator("dialog[open]")).toHaveCount(0);
        expect(made).toEqual([]);
      });
    }

    test("a goblin's address in a chat opens that goblin in this page, with no reload, and the draft he left waits for him", async ({ page, context }) => {
      // Arrange
      await openChat(page);
      await field(page).fill("half a thought");
      await mark(page);

      // Act
      await talk(page).getByRole("link", { name: TO_IVY }).click();

      // Assert
      await expect(panelTitle(page)).toHaveText(IVY);
      expect(await isMarked(page)).toBe(true);
      expect(context.pages()).toHaveLength(1);

      // Act: Ivy's chat names Zeke's address in turn.
      await page.getByRole("log", { name: "Messages of " + IVY }).getByRole("link", { name: TO_ZEKE }).click();

      // Assert
      await expect(panelTitle(page)).toHaveText(NAME);
      await expect(field(page)).toHaveValue("half a thought");
      expect(await isMarked(page)).toBe(true);
    });

    test("an item's address in a chat opens it in the Command Center from the keyboard, with no reload, and the draft stays", async ({ page, context }) => {
      // Arrange
      await openChat(page);
      await field(page).fill("half a thought");
      await mark(page);
      const made = acts(page);

      // Act
      await talk(page).getByRole("link", { name: TO_QUESTION }).focus();
      await page.keyboard.press("Enter");

      // Assert
      await expect(questionCard(page)).toContainText(QUESTION.text);
      expect(await isMarked(page)).toBe(true);
      expect(context.pages()).toHaveLength(1);
      await page.keyboard.press("Escape");
      await expect(questionCard(page)).toBeHidden();
      await expect(field(page)).toHaveValue("half a thought");
      expect(made).toEqual([]);
    });

    test("an address in a chat that names nothing the board holds opens nothing and leaves the board as it was", async ({ page, context }) => {
      // Arrange
      await openChat(page);
      await mark(page);

      // Act
      await talk(page).getByRole("link", { name: TO_NOBODY }).click();

      // Assert
      await expect(panelTitle(page)).toHaveText(NAME);
      await expect(page.locator("dialog[open]")).toHaveCount(0);
      expect(await isMarked(page)).toBe(true);
      expect(context.pages()).toHaveLength(1);
      expect(await search(page)).toBe("");
    });

    test("a tapped push hands its item's address to the open board, which opens it with no reload", async ({ page }) => {
      // Arrange
      await openChat(page);
      await field(page).fill("half a thought");
      await mark(page);

      // Act: the push worker's message, as public/push-worker.js posts it.
      await page.evaluate((open) => navigator.serviceWorker.dispatchEvent(new MessageEvent("message", { data: { open } })), "/?item=question:" + QUESTION.id);

      // Assert
      await expect(questionCard(page)).toContainText(QUESTION.text);
      expect(await isMarked(page)).toBe(true);
      await page.keyboard.press("Escape");
      await expect(field(page)).toHaveValue("half a thought");
    });

    test("a goblin's address its terminal draws opens that goblin in this page", async ({ page, context }) => {
      // Arrange
      const relay = await openChat(page);
      await pickStyle(page, "Terminal");
      const screen = page.locator(".terminal-view:not(.staged) .xterm-screen");
      await expect(screen).toBeVisible();
      const size = relay.resizes.at(-1);
      expect(size, "the terminal reported its grid").toBeTruthy();
      const box = (await screen.boundingBox())!;
      const cell = { width: box.width / size!.cols, height: box.height / size!.rows };
      // The address starts after "> opened " on the first row.
      const point = { x: box.x + (9 + 5.5) * cell.width, y: box.y + 0.5 * cell.height };
      await mark(page);

      // Act
      await page.mouse.move(point.x - cell.width, point.y);
      await page.mouse.move(point.x, point.y);
      await expect(page.locator(".xterm-cursor-pointer")).toHaveCount(1);
      await page.mouse.click(point.x, point.y);

      // Assert
      await expect(panelTitle(page)).toHaveText(IVY);
      expect(await isMarked(page)).toBe(true);
      expect(context.pages()).toHaveLength(1);
    });
  });
}

// The desktop window's own script (linksScript in cmd/goblins-window/links.go)
// hands every link that would leave the board to his default browser. It runs
// here as the window runs it, read from its source, over a stand-in for
// WebView2's bridge that keeps what the page asks the window to open.
const LINKS_GO = readFileSync(new URL("../../cmd/goblins-window/links.go", import.meta.url), "utf8");
const OPEN_MESSAGE = /const openMessage = "([^"]+)"/.exec(LINKS_GO)![1];
const LINKS_START = LINKS_GO.indexOf("const linksScript = `") + "const linksScript = `".length;
const LINKS_SCRIPT = LINKS_GO.slice(LINKS_START, LINKS_GO.indexOf("})();`", LINKS_START) + "})();".length).replaceAll("` + openMessage + `", OPEN_MESSAGE);
const WINDOW = `const posted = []; Object.assign(window, { posted, chrome: { webview: { postMessage: (text) => posted.push(text) } } });
window.addEventListener("DOMContentLoaded", () => { ${LINKS_SCRIPT} });`;

test("in the desktop window, a goblin's address in a chat opens that goblin in the window, never in his browser", async ({ page, context }) => {
  // Arrange
  await page.setViewportSize({ width: 1440, height: 1000 });
  await context.addInitScript({ content: WINDOW });
  await openChat(page);
  await mark(page);

  // Act
  await talk(page).getByRole("link", { name: TO_IVY }).click();

  // Assert
  await expect(panelTitle(page)).toHaveText(IVY);
  expect(await isMarked(page)).toBe(true);
  expect(await page.evaluate(() => (window as unknown as { posted: string[] }).posted)).toEqual([]);
});
