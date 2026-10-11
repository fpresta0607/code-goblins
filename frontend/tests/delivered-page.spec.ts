import { expect, test } from "./site";

// The Overlord, 2026-10-11 about 00:55Z, of the card cfo deliver made for a
// picture: "the open downloaded golbin web p command didnt make snese it had
// a download adn open button things like that should be scrawl". A delivered
// file is its Scrawl page's review item, opened from the card, and no Open and
// Download card is left.
test.use({ locale: "en-US", timezoneId: "America/Chicago", viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

test("a delivered picture shows as its Scrawl page's card, with no Open and Download card", async ({ page }, testInfo) => {
  // Arrange
  await page.clock.install({ time: new Date("2026-10-11T01:30:00Z") });

  // Act
  await page.goto("/tests/fixtures/delivered-page.html");
  const dialog = page.getByRole("dialog");

  // Assert
  await expect(dialog.getByRole("heading", { name: "The orchestration card, after the fit" })).toBeVisible();
  await expect(dialog.getByRole("link", { name: /^Open review/ })).toHaveAttribute("href", "http://127.0.0.1:4387/session/after-card");
  await expect(dialog.getByRole("link", { name: /Download/ })).toHaveCount(0);
  await expect(dialog.getByRole("link", { name: /^Open$/ })).toHaveCount(0);
  await expect(dialog.getByText("after-card.webp")).toHaveCount(0);
  await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath("delivered-page.png") });
});
