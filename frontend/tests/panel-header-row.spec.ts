import { expect, test, type Locator, type Page } from "./site";
import { NAME, open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-10-29: "Minimize height and maximize the space", and of
// the Chat and Task switch, "also put the switch in the middle less wide". A
// panel's header is one short row: the session's small picture, its name and
// its harness's mark at the left, the switch in the middle and only as wide
// as its two icons, and the panel's controls at the right, led on the CFO's
// by the AFK switch. He kept the picture and the mark ("I like the avatar and
// the icon") and moved the last AFK report into the Task view ("could be
// something that you know is part of like the task panel"). No status line:
// the working line in the conversation says what the session does.

const ROW_PX = 56;
const REPORT = "/api/afk/report/index.html";
const header = (page: Page) => page.locator(".goblin-panel > .panel-header");

async function openCfo(page: Page) {
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CFO");
}

async function box(part: Locator) {
  await expect(part).toBeVisible();
  return (await part.boundingBox())!;
}

for (const width of [1440, 393, 360]) {
  for (const who of ["goblin", "CFO"]) {
    test(`${who}'s panel header at ${width}px is one short row with the switch in or near its middle`, async ({ page }, testInfo) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      await open(page, snapshot({ afk: { state: "off", page: REPORT } }));

      // Act
      if (who === "goblin") await openGoblin(page);
      else await openCfo(page);

      // Assert: one row, no taller than a control and its padding, with no
      // separate row above it or under it.
      const row = await box(header(page));
      expect(row.height).toBeLessThanOrEqual(ROW_PX);
      await expect(page.locator(".goblin-panel > .panel-top")).toHaveCount(0);
      await expect(header(page).locator(".panel-status")).toHaveCount(0);
      const parts = [".goblin-avatar", "#panel-title", ".panel-identity > .mark", ".panel-view-switch", ".panel-corner"];
      if (who === "CFO") parts.push(".afk-header");
      for (const part of parts) {
        const each = await box(header(page).locator(part));
        const middle = each.y + each.height / 2;
        expect(middle, part + " in the row").toBeGreaterThan(row.y);
        expect(middle, part + " in the row").toBeLessThan(row.y + row.height);
        expect(each.x + each.width, part + " inside the screen").toBeLessThanOrEqual(width);
      }
      expect(await header(page).evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);

      // Assert: the switch is only as wide as its two icons, between the
      // name and the controls, in the middle while both sides leave room,
      // and otherwise as near it as the controls leave room for, right
      // against them. In the panel's default width beside the board every
      // control shows, and the CFO's name is whole.
      const views = header(page).locator(".panel-view-switch");
      const switchBox = await box(views);
      const icons = await views.getByRole("button").evaluateAll((buttons) => buttons.reduce((sum, button) => sum + button.getBoundingClientRect().width, 0));
      expect(switchBox.width).toBeLessThanOrEqual(icons + 8 + 2 * 3 + 2 + 1);
      const identity = await box(header(page).locator(".panel-identity"));
      const controls = await box(header(page).locator(".panel-top"));
      expect(switchBox.x).toBeGreaterThanOrEqual(identity.x + identity.width);
      expect(switchBox.x + switchBox.width).toBeLessThanOrEqual(controls.x);
      const offset = switchBox.x + switchBox.width / 2 - (row.x + row.width / 2);
      const gap = await header(page).evaluate((element) => parseFloat(getComputedStyle(element).columnGap));
      const isAgainstControls = Math.abs(controls.x - (switchBox.x + switchBox.width) - gap) <= 1;
      expect(Math.abs(offset) <= 2 || (offset < 0 && isAgainstControls), `the switch ${offset} px from the middle`).toBe(true);
      if (width === 1440) {
        await expect(header(page).getByRole("button", { name: "More", exact: true })).toHaveCount(0);
        if (who === "CFO") expect(await header(page).locator("#panel-title").evaluate((title) => title.scrollWidth - title.clientWidth)).toBe(0);
      }
      await testInfo.attach(`header-${who}-${width}.png`, { body: await page.screenshot({ clip: { x: row.x, y: row.y, width: row.width, height: row.height } }), contentType: "image/png" });
    });
  }
}

test("a goblin's name is cut short in the header row, never wrapped, and shows whole in its Task view", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 360, height: 900 });
  await open(page);
  await openGoblin(page);
  const title = header(page).locator("#panel-title");

  // Assert: the row cuts the name rather than wrap it onto a second line.
  await expect(title).toHaveCSS("text-overflow", "ellipsis");
  await expect(title).toHaveCSS("white-space", "nowrap");

  // Act
  await header(page).getByRole("button", { name: "Task", exact: true }).click();

  // Assert
  await expect(page.locator(".goblin-panel > .panel-task .panel-whole-name")).toHaveText(NAME + " · cg-touch");
  await expect(page.locator(".goblin-panel > .panel-task .panel-status")).toBeVisible();
});

test("the CFO's last AFK report opens from its Task view, not from the header", async ({ page }) => {
  // Arrange
  await open(page, snapshot({ afk: { state: "off", page: REPORT } }));
  await openCfo(page);
  const report = page.getByRole("button", { name: "Open your last AFK report", exact: true });
  await expect(header(page).getByRole("switch", { name: "AFK mode", exact: true })).toBeVisible();
  await expect(report).toHaveCount(0);

  // Act
  await header(page).getByRole("button", { name: "Task", exact: true }).click();

  // Assert
  await expect(page.locator(".goblin-panel > .panel-task").getByRole("button", { name: "Open your last AFK report", exact: true })).toBeVisible();
  await expect(header(page).getByRole("button", { name: "Open your last AFK report", exact: true })).toHaveCount(0);
});

test("the divider beside the panel is drawn before the pointer reaches it", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);
  const divider = page.getByRole("separator", { name: "Resize the panel", exact: true });
  await page.mouse.move(0, 0);

  // Act
  const handle = await divider.evaluate((element) => {
    const style = getComputedStyle(element, "::after");
    return { opacity: style.opacity, height: style.height };
  });

  // Assert
  expect(handle).toEqual({ opacity: "1", height: "56px" });
});
