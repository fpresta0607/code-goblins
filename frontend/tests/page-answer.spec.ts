import { doneCards, expect, recordDoneCards, test, type Page } from "./site";

// The Overlord answered cg-board-theme's mockups on the review page while its
// card was open in the Command Center (2026-09-30): the card must finish with
// the check an answer sent from it shows, then move on, never read
// "Withdrawn" or show an error.
async function openTheWait(page: Page) {
  await page.goto("/tests/fixtures/page-answer.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "approve the mockups" })).toBeVisible();
  return dialog;
}

test("a review answered on its page finishes its open card with a check and moves on", async ({ page }) => {
  // Arrange
  const dialog = await openTheWait(page);
  await recordDoneCards(page);

  // Act
  await page.evaluate(() => window.answerOnPage?.());

  // Assert
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Answered/));
  for (const answered of (await doneCards(page)).filter((text) => text.startsWith("Answered"))) expect(answered).toContain("You answered on its page; the CFO relays it to the goblin.");
  await expect(dialog.getByRole("heading", { name: "Look at the new icons" })).toBeVisible();
  await expect(dialog.getByText(/Withdrawn/)).toHaveCount(0);
  await expect(dialog.getByRole("alert")).toHaveCount(0);
});

test("a clear refused because he just answered on the page shows the check, not an error", async ({ page }) => {
  // Arrange: the board refuses his Clear, since the review closed a moment
  // before, and only then does the snapshot saying so arrive.
  let refused = false;
  await page.route("**/api/actions", async (route) => {
    refused = true;
    await route.fulfill({ status: 409, json: { error: "that review is not open; refresh the board" } });
  });
  const dialog = await openTheWait(page);
  await recordDoneCards(page);

  // Act
  await dialog.getByRole("button", { name: "Dismiss" }).click();
  await expect.poll(() => refused).toBe(true);
  await page.evaluate(() => window.answerOnPage?.());

  // Assert
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Answered/));
  await expect(dialog.getByText(/that review is not open/)).toHaveCount(0);
  await expect(dialog.getByRole("heading", { name: "Look at the new icons" })).toBeVisible();
  // A refused send that moved on comes back only while it can be retried; this
  // card closed by his answer, so it stays gone.
  await page.waitForTimeout(2000);
  await expect(dialog.getByRole("heading", { name: "Look at the new icons" })).toBeVisible();
  await expect(dialog.getByText(/that review is not open/)).toHaveCount(0);
  await expect(dialog.getByRole("heading", { name: "approve the mockups" })).toHaveCount(0);
});
