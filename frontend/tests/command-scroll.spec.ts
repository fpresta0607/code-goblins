import { expect, test, type Locator } from "./site";

// The Overlord, 2026-09-30: "when command center notification opens make sure
// its scrolled to top by default". Each opening starts at the top of its item;
// within one visit the scroll stays where he puts it, and moving to another
// item shows that item from its top.
const scrollTop = (content: Locator) => content.evaluate((element) => element.scrollTop);

test.use({ viewport: { width: 1280, height: 720 } });

test("the Command Center opens at the top of its item every time", async ({ page }) => {
  // Arrange: he opens the first question, scrolls down its card and closes
  // the Command Center.
  await page.goto("/tests/fixtures/command-scroll.html");
  const dialog = page.getByRole("dialog");
  const content = dialog.locator(".command-center-content");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  await expect(dialog.getByText("May I build the Paused divider as drawn?")).toBeVisible();
  expect(await content.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
  await content.evaluate((element) => element.scrollTo(0, element.scrollHeight));
  expect(await scrollTop(content)).toBeGreaterThan(0);
  await page.waitForTimeout(200);
  expect(await scrollTop(content)).toBeGreaterThan(0);

  // Act
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "Open", exact: true }).click();

  // Assert
  await expect(dialog.getByText("May I build the Paused divider as drawn?")).toBeVisible();
  await expect.poll(() => scrollTop(content)).toBe(0);
  await page.waitForTimeout(300);
  expect(await scrollTop(content)).toBe(0);
});

test("moving to the next item shows it from its top", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/command-scroll.html");
  const dialog = page.getByRole("dialog");
  const content = dialog.locator(".command-center-content");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  await expect(dialog.getByText("May I build the Paused divider as drawn?")).toBeVisible();
  await content.evaluate((element) => element.scrollTo(0, element.scrollHeight));

  // Act
  await dialog.getByRole("button", { name: "Next", exact: true }).click();

  // Assert
  await expect(dialog.getByText("May I restyle the header as drawn?")).toBeVisible();
  await expect.poll(() => scrollTop(content)).toBe(0);
});

test("desktop scrolling stays inside the content and closing restores focus and the page", async ({ page }, testInfo) => {
  // Arrange
  await page.goto("/tests/fixtures/command-scroll.html");
  await page.evaluate(() => window.scrollTo(0, 400));
  const background = await page.evaluate(() => ({ top: window.scrollY, root: document.documentElement.style.cssText, body: document.body.style.cssText }));
  const opener = page.getByRole("button", { name: "Open", exact: true });
  await opener.click();
  const dialog = page.getByRole("dialog");
  const content = dialog.locator(".command-center-content");
  const heading = dialog.locator(".command-center-heading");
  const header = await heading.boundingBox();

  // Act
  await content.hover();
  await page.mouse.wheel(0, 500);
  await expect.poll(() => scrollTop(content)).toBeGreaterThan(0);
  await heading.hover();
  await page.mouse.wheel(0, 500);

  // Assert
  expect(await heading.boundingBox()).toEqual(header);
  expect(await dialog.evaluate((element) => element.scrollTop)).toBe(0);
  expect(await page.evaluate(() => window.scrollY)).toBe(background.top);
  await page.screenshot({ path: testInfo.outputPath("desktop-command-center.png") });
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(opener).toBeFocused();
  expect(await page.evaluate(() => ({ top: window.scrollY, root: document.documentElement.style.cssText, body: document.body.style.cssText }))).toEqual(background);
});

for (const ending of ["completion", "unmount"]) {
  test(`${ending} restores an existing page scroll lock`, async ({ page }) => {
    // Arrange
    await page.goto("/tests/fixtures/command-scroll.html?single");
    await page.evaluate(() => {
      window.scrollTo(0, 400);
      document.documentElement.style.overflow = "hidden";
      document.body.style.overscrollBehavior = "contain";
    });
    const before = await page.evaluate(() => ({ top: window.scrollY, root: document.documentElement.style.cssText, body: document.body.style.cssText }));
    const opener = page.getByRole("button", { name: "Open", exact: true });
    await opener.click();
    const dialog = page.getByRole("dialog");

    // Act
    if (ending === "completion") {
      let isFirstAttempt = true;
      await page.route("**/api/actions", async (route) => {
        if (isFirstAttempt) {
          isFirstAttempt = false;
          return route.fulfill({ status: 503, json: { error: "Fixture delivery refused" } });
        }
        const action: Record<string, unknown> = route.request().postDataJSON();
        await route.fulfill({ json: { ...action, status: "succeeded" } });
      });
      await dialog.getByRole("radio", { name: /Build as drawn/ }).check();
      await dialog.getByRole("button", { name: "Send decision" }).click();
      await expect(dialog.getByText("Fixture delivery refused.", { exact: true })).toBeVisible();
      await dialog.getByRole("button", { name: "Retry", exact: true }).click();
    } else {
      await page.getByRole("button", { name: "Remove Command Center" }).evaluate((element: HTMLButtonElement) => element.click());
    }

    // Assert
    await expect(dialog).toBeHidden();
    await expect(opener).toBeFocused();
    expect(await page.evaluate(() => ({ top: window.scrollY, root: document.documentElement.style.cssText, body: document.body.style.cssText }))).toEqual(before);
  });
}

test.describe("mobile touch scrolling", () => {
  test.use({ viewport: { width: 393, height: 659 }, isMobile: true, hasTouch: true });

  test("touch swipes scroll content without moving the shell or chaining to the page", async ({ page }) => {
    // Arrange
    await page.goto("/tests/fixtures/command-scroll.html");
    await page.evaluate(() => window.scrollTo(0, 400));
    const background = await page.evaluate(() => window.scrollY);
    await page.getByRole("button", { name: "Open", exact: true }).tap();
    const dialog = page.getByRole("dialog");
    const content = dialog.locator(".command-center-content");
    const header = await dialog.locator(".command-center-heading").boundingBox();
    const session = await page.context().newCDPSession(page);
    const bounds = (await content.boundingBox())!;
    const start = { x: bounds.x + bounds.width / 2, y: bounds.y + bounds.height * .8 };
    const distance = bounds.height * .6;

    // Act
    await session.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [start] });
    for (let step = 1; step <= 10; step++) await session.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: start.y - distance * step / 10 }] });
    await session.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });

    // Assert
    await expect.poll(() => scrollTop(content)).toBeGreaterThan(0);
    expect(await dialog.evaluate((element) => element.scrollTop)).toBe(0);
    expect(await dialog.locator(".command-center-heading").boundingBox()).toEqual(header);
    await content.evaluate((element) => element.scrollTo(0, element.scrollHeight));
    await session.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [start] });
    for (let step = 1; step <= 10; step++) await session.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: start.y - distance * step / 10 }] });
    await session.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    expect(await page.evaluate(() => window.scrollY)).toBe(background);
    expect(await dialog.evaluate((element) => element.scrollTop)).toBe(0);
    await session.detach();
  });
});
