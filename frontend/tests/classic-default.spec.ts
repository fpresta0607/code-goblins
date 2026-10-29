import { expect, test, type Page } from "./site";
import { box, boxMenu, card, field, NAME, open, typed } from "./session-board";

// The board draws every conversation as Classic, the harness's own terminal,
// until Modern Chat is ready to be the default (the Overlord, 2026-10-28:
// "the classic terminal worked well ... It should be the default for now").
// These browsers saved no style of their own.
test.use({ chatStyle: null });

const terminalInput = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const modern = (page: Page) => page.locator(".context-pane").getByRole("button", { name: "Switch to Modern chat", exact: true });
const saved = (page: Page) => page.evaluate(() => localStorage.getItem("cfo-chat-style"));

const more = (page: Page) => page.locator(".context-pane .panel-top").getByRole("button", { name: "More", exact: true });

// classicShown says the panel draws the terminal as it was before Modern
// Chat: no box, no Back to messages bar, no terminal copy button beside it,
// and Modern in the panel's row, or in its More where the row has no room
// for it, as beside the CFO's AFK switch at the panel's default width.
async function classicShown(page: Page, name: string): Promise<void> {
  await expect(terminalInput(page)).toBeVisible();
  await expect(box(page, name)).toBeHidden();
  await expect(page.getByRole("button", { name: "Back to messages", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Copy terminal text", exact: true })).toHaveCount(0);
  await expect(modern(page).or(more(page))).toBeVisible();
}

async function pressModern(page: Page): Promise<void> {
  if (await modern(page).count()) return modern(page).click();
  await more(page).click();
  await page.locator(".context-pane .panel-top").getByRole("menuitem", { name: "Modern chat", exact: true }).click();
}

async function modernShown(page: Page, name: string): Promise<void> {
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
  await expect(field(page, name)).toBeVisible();
  await expect(modern(page)).toHaveCount(0);
}

for (const who of ["CFO", "goblin"] as const) {
  test(`the ${who}'s panel opens in Classic with no saved choice, and the Style menu switches it both ways at once`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    const name = who === "CFO" ? "CFO" : NAME;

    // Act
    if (who === "CFO") await page.keyboard.press("Control+Alt+1");
    else await card(page, NAME).click();

    // Assert: the terminal alone, and opening it saved nothing.
    await classicShown(page, name);
    expect(await saved(page)).toBeNull();

    // Act and Assert: twice each way, since a style chosen once must not keep
    // the next choice from showing.
    for (let round = 0; round < 2; round++) {
      await pressModern(page);
      await modernShown(page, name);
      await expect(field(page, name)).toBeFocused();
      expect(await saved(page)).toBe("modern");

      await (await boxMenu(page, name)).getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).click();
      await classicShown(page, name);
      expect(await saved(page)).toBe("classic");
      // The keyboard goes to the terminal Classic draws.
      await expect(terminalInput(page)).toBeFocused();
    }
    await page.keyboard.type("x");
    await expect.poll(() => typed(relay)).toBe("x");
  });
}

for (const style of ["modern", "classic"] as const) {
  test(`a saved choice of ${style} is kept through a reload`, async ({ page }) => {
    // Arrange
    await page.addInitScript((chosen) => { if (!localStorage.getItem("cfo-chat-style")) localStorage.setItem("cfo-chat-style", chosen); }, style);
    await open(page);

    // Act
    await card(page, NAME).click();

    // Assert
    if (style === "modern") await modernShown(page, NAME);
    else await classicShown(page, NAME);
    await page.reload();
    await card(page, NAME).click();
    if (style === "modern") await modernShown(page, NAME);
    else await classicShown(page, NAME);
    expect(await saved(page)).toBe(style);
  });
}

test("a held terminal in Classic says typing is paused, with no box beside it", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await card(page, NAME).click();
  await classicShown(page, NAME);

  // Act
  relay.say({ type: "held", reason: "pipeline owns this task" });

  // Assert
  await expect(page.locator(".context-pane").getByText("Typing is paused.", { exact: true })).toBeVisible();
  await expect(box(page, NAME)).toBeHidden();
  await expect(page.locator(".classic-queue")).toBeHidden();
});
