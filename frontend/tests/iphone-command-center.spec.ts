import { devices, expect, servePages, test } from "./site";

const PRIVATE = "https://localhost:4311";

test.use({ ...devices["iPhone 15"], browserName: "webkit" });

test("mobile Command Center scrolls its content while the shell and page stay still", async ({ page }, testInfo) => {
  // Arrange
  await servePages(page.context(), PRIVATE);
  await page.goto(PRIVATE + "/tests/fixtures/command-scroll.html");
  await page.evaluate(() => window.scrollTo(0, 400));
  const background = await page.evaluate(() => window.scrollY);
  const returnFocus = await page.evaluateHandle(() => document.activeElement);
  await page.getByRole("button", { name: "Open", exact: true }).tap();
  const dialog = page.getByRole("dialog");
  const heading = dialog.locator(".command-center-heading");
  const before = await heading.boundingBox();

  // Act
  await page.keyboard.press("PageDown");
  await expect.poll(() => dialog.evaluate((element) => element.scrollTop + (element.querySelector(".command-center-content")?.scrollTop || 0))).toBeGreaterThan(0);

  // Assert
  expect(await dialog.evaluate((element) => element.scrollTop)).toBe(0);
  expect(await heading.boundingBox()).toEqual(before);
  expect(await page.evaluate(() => window.scrollY)).toBe(background);
  const shell = await dialog.boundingBox();
  expect(shell?.x).toBe(0);
  expect(shell?.y).toBe(0);
  expect(shell?.width).toBe(393);
  expect(shell?.height).toBe(page.viewportSize()?.height);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(page.viewportSize()?.width);
  await page.screenshot({ path: testInfo.outputPath("mobile-command-center.png") });
  await dialog.getByRole("button", { name: "Close the Command Center" }).tap();
  await expect(dialog).toBeHidden();
  expect(await page.evaluate(() => window.scrollY)).toBe(background);
  expect(await returnFocus.evaluate((element) => element === document.activeElement)).toBe(true);
  await returnFocus.dispose();
});

test("the Command Center stays inside the visual viewport when the keyboard pans it", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => {
    const viewport = window.visualViewport!;
    Object.defineProperties(viewport, { height: { configurable: true, value: 360 }, offsetTop: { configurable: true, value: 80 } });
  });
  await servePages(page.context(), PRIVATE);
  await page.goto(PRIVATE + "/tests/fixtures/command-scroll.html");
  await page.getByRole("button", { name: "Open", exact: true }).tap();

  // Assert
  const shell = await page.getByRole("dialog").boundingBox();
  expect(shell?.y).toBe(80);
  expect(shell?.height).toBe(360);
});

test("keyboard resize and rotation keep the focused answer and actions reachable", async ({ page }, testInfo) => {
  // Arrange
  await servePages(page.context(), PRIVATE);
  await page.goto(PRIVATE + "/tests/fixtures/command-scroll.html");
  await page.getByRole("button", { name: "Open", exact: true }).tap();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: "Other" }).tap();
  const answer = dialog.getByRole("textbox", { name: "Your written answer" });
  await answer.tap();
  await answer.fill("Use the fixed shell");

  // Act: Safari shrinks and pans the visual viewport without changing layout.
  await page.evaluate(() => {
    const viewport = window.visualViewport!;
    Object.defineProperties(viewport, { height: { configurable: true, value: 360 }, offsetTop: { configurable: true, value: 80 } });
    viewport.dispatchEvent(new Event("resize"));
    viewport.dispatchEvent(new Event("scroll"));
  });

  // Assert
  await expect.poll(async () => (await dialog.boundingBox())?.height).toBe(360);
  await expect(answer).toBeFocused();
  const input = await answer.boundingBox();
  expect(input!.y).toBeGreaterThanOrEqual(80);
  expect(input!.y + input!.height).toBeLessThanOrEqual(440);
  await page.screenshot({ path: testInfo.outputPath("keyboard-command-center.png") });
  const send = dialog.getByRole("button", { name: "Send decision" });
  await send.scrollIntoViewIfNeeded();
  const action = await send.boundingBox();
  expect(action!.y + action!.height).toBeLessThanOrEqual(440);
  expect(await dialog.evaluate((element) => element.scrollTop)).toBe(0);
  await page.evaluate(() => {
    const viewport = window.visualViewport!;
    Reflect.deleteProperty(viewport, "height");
    Reflect.deleteProperty(viewport, "offsetTop");
  });
  await page.setViewportSize({ width: 852, height: 393 });
  const rotated = await dialog.boundingBox();
  expect(rotated!.y).toBeGreaterThanOrEqual(0);
  expect(rotated!.y + rotated!.height).toBeLessThanOrEqual(393);
  await dialog.getByRole("button", { name: "Close the Command Center" }).tap();
  await expect(dialog).toBeHidden();
});

for (const card of [
  { key: "run:long-command", heading: "Run the board checks", action: "Run in PowerShell" },
  { key: "credential:long-credential", heading: "Choose the test account", field: "Account name" },
  { key: "review:long-review", heading: "Review the board changes", field: "Your answer" },
]) {
  test(`mobile ${card.key} keeps its content and footer reachable`, async ({ page }) => {
    // Arrange
    await servePages(page.context(), PRIVATE);
    await page.goto(PRIVATE + "/tests/fixtures/command-scroll.html?card=" + card.key);
    await page.getByRole("button", { name: "Open", exact: true }).tap();
    const dialog = page.getByRole("dialog");
    const content = dialog.locator(".command-center-content");
    await expect(dialog.getByText(card.heading, { exact: true })).toBeVisible();

    // Act
    await content.evaluate((element) => element.scrollTo(0, element.scrollHeight));
    if (card.field) {
      const field = dialog.getByRole("textbox", { name: card.field, exact: true });
      await field.tap();
      await field.fill("The test account");
      await expect(field).toBeFocused();
    }
    const action = card.action ? dialog.getByRole("button", { name: card.action, exact: true }) : dialog.locator(".card-actions button").last();
    await action.scrollIntoViewIfNeeded();

    // Assert
    const footer = await action.boundingBox();
    expect(footer!.y).toBeGreaterThan(0);
    expect(footer!.y + footer!.height).toBeLessThanOrEqual(page.viewportSize()!.height);
    expect(await dialog.evaluate((element) => element.scrollTop)).toBe(0);
    await dialog.getByRole("button", { name: "Close the Command Center" }).tap();
    await expect(dialog).toBeHidden();
  });
}
