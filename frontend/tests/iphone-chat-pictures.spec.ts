import { devices, expect, servePages, test } from "./site";
import { fold, hoverCard, isDrawn, openPictured, placeOf, viewer } from "./chat-pictures";

const PRIVATE = "https://pc.example.ts.net:4311";
test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

// On a phone a row's thumbnails are as large as a finger needs, a tap opens
// the viewer with no card in between, and its arrows, a swipe and Close work
// by touch (#2305).
test("a tap on a row's thumbnail opens the viewer on an iPhone, where the arrows and a swipe step and Close puts it away", async ({ page }, testInfo) => {
  // Arrange
  await servePages(page.context(), PRIVATE);
  await openPictured(page);
  const thumbnails = fold(page).locator("> summary .call-picture");
  const place = viewer(page).getByRole("status");
  const stage = viewer(page).locator(".picture-stage");
  await expect(thumbnails).toHaveCount(3);
  for (const thumbnail of await thumbnails.all()) {
    await expect.poll(() => thumbnail.locator("img").evaluate(isDrawn)).toBe(true);
    const box = await thumbnail.boundingBox();
    expect(box && box.width >= 44 && box.height >= 44, "a thumbnail a finger can tap").toBe(true);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("thumbnails-iphone.png") });

  // Act
  await thumbnails.nth(0).tap();

  // Assert: the viewer on that picture, with no card, and the row still folded.
  await expect(viewer(page)).toBeVisible();
  await expect(place).toHaveText(placeOf("home"));
  await expect.poll(() => viewer(page).getByRole("img").evaluate(isDrawn)).toBe(true);
  await expect(hoverCard(page)).toHaveCount(0);
  await expect(fold(page)).toHaveJSProperty("open", false);
  await page.screenshot({ path: testInfo.outputPath("viewer-iphone.png") });

  // Act + Assert: the arrows step by touch.
  await viewer(page).getByRole("button", { name: "Next picture" }).tap();
  await expect(place).toHaveText(placeOf("team"));
  await viewer(page).getByRole("button", { name: "Previous picture" }).tap();
  await expect(place).toHaveText(placeOf("home"));

  // Act + Assert: a swipe to the left shows the next, and one to the right
  // the one before.
  const swipe = async (from: number, to: number) => {
    await stage.dispatchEvent("pointerdown", { pointerType: "touch", clientX: from, clientY: 300 });
    await stage.dispatchEvent("pointerup", { pointerType: "touch", clientX: to, clientY: 310 });
  };
  await swipe(300, 80);
  await expect(place).toHaveText(placeOf("team"));
  await swipe(80, 300);
  await expect(place).toHaveText(placeOf("home"));

  // Act
  await viewer(page).getByRole("button", { name: "Close" }).tap();

  // Assert
  await expect(viewer(page)).toHaveCount(0);
});
