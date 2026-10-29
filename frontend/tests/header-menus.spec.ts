import { expect, test, type Locator, type Page } from "./site";
import { open } from "./session-board";

// The board's header at a phone's widths and on a desktop: the connection's
// Live pill sits right before the Command Center's button, and every part of
// the header and every menu it opens lies inside the screen. The supervisor
// is played by tests/session-board.ts.

const live = (page: Page) => page.locator(".topbar-controls > .notice");
const commandCenter = (page: Page) => page.locator(".command-center-menu");

async function expectInsideTheScreen(page: Page, part: Locator, name: string) {
  const box = (await part.boundingBox())!;
  expect(box.x, name + " starts inside the screen").toBeGreaterThanOrEqual(0);
  expect(box.x + box.width, name + " ends inside the screen").toBeLessThanOrEqual(page.viewportSize()!.width);
}

for (const width of [360, 393, 430, 1440]) {
  test(`at ${width}px the Live pill sits right before the Command Center's button, on its row`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 844 });

    // Act
    await open(page);

    // Assert
    await expect(live(page)).toHaveText("Live");
    expect(await commandCenter(page).evaluate((menu) => menu.previousElementSibling?.classList.contains("notice"))).toBe(true);
    const pill = (await live(page).boundingBox())!;
    const button = (await commandCenter(page).locator("summary").boundingBox())!;
    expect(pill.x + pill.width).toBeLessThanOrEqual(button.x);
    expect(Math.abs(pill.y + pill.height / 2 - (button.y + button.height / 2))).toBeLessThan(button.height / 2);
  });

  test(`at ${width}px the header and the Command Center it opens lie inside the screen`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 844 });
    await open(page);
    await expectInsideTheScreen(page, live(page), "the Live pill");
    await expectInsideTheScreen(page, page.getByRole("group", { name: "Workspace view", exact: true }), "the view switch");

    // Act
    await commandCenter(page).locator("summary").click();

    // Assert
    const menu = page.locator(".command-center-updates");
    await expect(menu).toBeVisible();
    await expectInsideTheScreen(page, menu, "the Command Center");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "no sideways scroll").toBe(true);
    await testInfo.attach(`command-center-${width}.png`, { body: await page.screenshot(), contentType: "image/png" });
  });
}
