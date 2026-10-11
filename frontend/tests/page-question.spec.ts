import { expect, test } from "./site";

// One question, one place: a goblin that asks a question about its open
// review page gets one card in the Command Center, the page's, which shows
// the question and the page itself and opens it from one button. The page is
// where he answers, so the card offers no choices of its own and explains
// nothing (the Overlord, 2026-10-11).
test("a question with an open review page is one card that shows the question and the page", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/page-question.html");

  // Act
  await page.getByRole("button", { name: "Open", exact: true }).click();

  // Assert
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("May I build item 4's Paused divider and layout switch as drawn?")).toBeVisible();
  await expect(dialog.getByRole("radio")).toHaveCount(0);
  await expect(dialog.getByRole("link", { name: "Open review" })).toHaveCount(1);
  await expect(dialog.getByRole("link", { name: "Open review" })).toHaveAttribute("href", "http://127.0.0.1:4387/session/ec2ef7d06dddccbb");
  await expect(dialog.locator(".page-preview iframe")).toHaveAttribute("src", "/api/reviews/waiting-cg-board-polish-3584/preview");
  await expect(dialog.getByText(/Waiting for your answer|conversation box|closes this card/)).toHaveCount(0);
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await page.locator(".command-center-menu summary").click();
  await expect(page.locator(".inbox-list li")).toHaveCount(1);
});

test("a page whose window closed explains nothing, and its one button opens it again", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/page-question.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();

  // Act
  await page.evaluate(() => window.closeWindow?.());

  // Assert
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText(/window closed|Reopen it|nothing you send/i)).toHaveCount(0);
  await expect(dialog.getByRole("link", { name: "Open review" })).toHaveCount(1);
});
