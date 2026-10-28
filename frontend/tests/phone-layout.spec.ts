import { expect, test, type Page } from "./site";
import { box, field, NAME, open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-10-28, from his phone: "mobile view is messy", then of
// Claude's own phone app "This is the mobile layout we need to emulate" and
// "I can't even see anything on my screen", then "back button and header
// takes up too much space. More menus and openable functionality". On a phone
// a panel's top is one row, Back as a round icon, the name with its status
// under it and "..." holding the rest; every control of the box sits inside
// its border on one row, each a 44 px target; while the session works the
// box's Stop takes Send's place; and with the keyboard up the conversation
// keeps lines in view. The supervisor is played by tests/session-board.ts.

const header = (page: Page) => page.locator(".phone-header");
const more = (page: Page) => header(page).getByRole("button", { name: "More", exact: true });
const sheet = (page: Page) => page.getByRole("group", { name: "More", exact: true });
const screen = (fields: Record<string, unknown> = {}) => ({ type: "screen", asking: false, open: false, working: false, submission: "", ...fields });

test.use({ isMobile: true, hasTouch: true, deviceScaleFactor: 3 });

for (const width of [360, 393, 430]) {
  test.describe(`at ${width} px`, () => {
    test.beforeEach(async ({ page }) => page.setViewportSize({ width, height: 852 }));

    test("a panel's top is one row, and the rest is in its \"...\" menu", async ({ page }) => {
      // Arrange
      const relay = await open(page);
      await openGoblin(page);
      relay.say(screen({ mode: "auto mode" }));

      // Assert: one row no taller than the reference's, with Back a round
      // icon, the name, and "...", and no selector or second row under it.
      const top = (await header(page).boundingBox())!;
      expect(top.height).toBeLessThanOrEqual(56);
      const back = header(page).getByRole("button", { name: /^Back to / });
      expect((await back.boundingBox())!.width).toBeLessThanOrEqual(44);
      await expect(back.getByText("Back", { exact: true }), "Back is an icon with no word").toBeHidden();
      await expect(header(page).locator("#panel-title")).toHaveText(NAME);
      await expect(page.locator(".panel-view-switch")).toHaveCount(0);
      const conversation = (await page.getByRole("log").boundingBox())!;
      expect(conversation.y).toBeGreaterThanOrEqual(top.y + top.height - 1);

      // Act
      await more(page).click();

      // Assert: the selector, the box's own menu and the mode are there.
      for (const name of ["Chat", "Task", "Modern", "Classic", "Speech history", "Mode: Auto"]) await expect(sheet(page).getByRole("button", { name, exact: true })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);

      // Act: a choice closes it.
      await sheet(page).getByRole("button", { name: "Classic", exact: true }).click();

      // Assert
      await expect(sheet(page)).toHaveCount(0);
      await expect(page.getByRole("region", { name: "Goblin terminal", exact: true })).toBeVisible();
    });

    test("every control of the box is inside its border on one row, whole and a thumb's width, with none under it", async ({ page }) => {
      // Arrange: a session at rest, so Send shows.
      const relay = await open(page, snapshot({}, { phase: "settled" }));
      await openGoblin(page);
      relay.say(screen({ mode: "auto mode" }));
      await expect(box(page).getByRole("button", { name: "Change model", exact: true })).toBeVisible();

      // Assert
      const measured = await box(page).locator(".composer-input").evaluate((input) => {
        const edge = input.getBoundingClientRect();
        const dock = input.closest(".composer-dock")!.getBoundingClientRect();
        return {
          edge: { left: edge.left, right: edge.right, bottom: edge.bottom }, dockBottom: dock.bottom,
          controls: [...input.querySelectorAll<HTMLElement>(".composer-row button")].filter((button) => button.offsetParent).map((button) => {
            const place = button.getBoundingClientRect();
            return { name: button.getAttribute("aria-label") ?? "", left: place.left, right: place.right, middle: place.top + place.height / 2, width: place.width, height: place.height, isCut: button.scrollWidth > button.clientWidth + 1 };
          }),
        };
      });
      expect(measured.controls.map((control) => control.name)).toEqual(["Add files", "Change model", "Change effort", "Dictate", "Send"]);
      for (const control of measured.controls) {
        expect(control.left, control.name).toBeGreaterThanOrEqual(measured.edge.left);
        expect(control.right, control.name).toBeLessThanOrEqual(measured.edge.right);
        expect(Math.abs(control.middle - measured.controls[0].middle), control.name + " on the row").toBeLessThanOrEqual(2);
        expect(Math.min(control.width, control.height), control.name + " a thumb's width").toBeGreaterThanOrEqual(44);
        expect(control.isCut, control.name + " whole").toBe(false);
      }
      expect(measured.dockBottom - measured.edge.bottom, "nothing under the box").toBeLessThanOrEqual(24);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
    });

    test("while the session works the box's Stop takes Send's place and the box says a message waits its turn", async ({ page }) => {
      // Arrange
      const relay = await open(page);
      await openGoblin(page);

      // Act
      relay.say(screen({ working: true }));

      // Assert
      await expect(box(page).locator(".composer-input").getByRole("button", { name: "Stop", exact: true })).toBeVisible();
      await expect(box(page).getByRole("button", { name: "Send", exact: true })).toHaveCount(0);
      await expect(field(page)).toHaveAttribute("placeholder", "Queue for after this…");
    });
  });
}

test("with the keyboard up the box sits on it and the conversation keeps lines in view", async ({ page }) => {
  // Arrange: an iPhone's width, its height with the keyboard up.
  await page.setViewportSize({ width: 393, height: 420 });
  const talk = { messages: Array.from({ length: 12 }, (_, seq) => ({ id: "m" + seq, seq, at: "2026-10-22T09:00:00Z", kind: seq % 2 ? "reply" : "prompt", source: seq % 2 ? undefined : "overlord", text: seq % 2 ? undefined : "Question " + seq, blocks: seq % 2 ? [{ kind: "text", text: "Answer " + seq + " runs over a line or two of plain prose." }] : undefined })), more: false };
  await open(page, snapshot({}, { phase: "settled" }), "", talk);
  await openGoblin(page);

  // Act
  await field(page).focus();

  // Assert: the box's bottom is on the screen's, and five lines or more of
  // the conversation show above it.
  const shown = await page.evaluate(() => {
    const log = document.querySelector<HTMLElement>(".conversation")!.getBoundingClientRect();
    const input = document.querySelector<HTMLElement>(".composer-input")!.getBoundingClientRect();
    const line = parseFloat(getComputedStyle(document.querySelector(".message-words")!).lineHeight);
    return { lines: (Math.min(log.bottom, input.top) - log.top) / line, inputBottom: input.bottom, height: innerHeight };
  });
  expect(shown.lines).toBeGreaterThanOrEqual(5);
  expect(shown.inputBottom).toBeLessThanOrEqual(shown.height);
  expect(shown.height - shown.inputBottom).toBeLessThanOrEqual(24);
  const fontSize = await field(page).evaluate((element) => parseFloat(getComputedStyle(element).fontSize));
  expect(fontSize, "16 px or more, so iOS never zooms on focus").toBeGreaterThanOrEqual(16);
});
