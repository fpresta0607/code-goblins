import { expect, test } from "@playwright/test";

// One question, one place: a goblin that asks a question about its open
// review page gets one card in the Command Center, the page's, which shows
// the question, opens the page and says where the review stands. The page is
// where he answers, so the card offers no choices of its own.
test("a question with an open review page is one card that shows the question and the page", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/page-question.html");

  // Act
  await page.getByRole("button", { name: "Open", exact: true }).click();

  // Assert
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("May I build item 4's Paused divider and layout switch as drawn?")).toBeVisible();
  await expect(dialog.getByRole("radio")).toHaveCount(0);
  await expect(dialog.getByRole("link", { name: "Open review" }).first()).toHaveAttribute("href", "http://127.0.0.1:4387/session/ec2ef7d06dddccbb");
  await expect(dialog.getByText("Waiting for your answer. Reply in the page's conversation box; your answer closes this card.")).toBeVisible();
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await page.locator(".command-center-menu summary").click();
  await expect(page.locator(".inbox-list li")).toHaveCount(1);
});

test("a page whose window closed says so and that nothing is lost", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/page-question.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();

  // Act
  await page.evaluate(() => window.closeWindow?.());

  // Assert
  await expect(page.getByRole("dialog").getByText(/Its window closed at .+\. Reopen it to answer; nothing you send there is lost\./)).toBeVisible();
});
