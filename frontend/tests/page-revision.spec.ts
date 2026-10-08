import { expect, test } from "./site";

// Item 3 of the review-flow brief: revisions sent from the editor update the
// editor. His revision on the page leaves the card saying it was received and
// that the next version replaces the page, off Waiting on you; the goblin's
// next version comes back in the same card, waiting on him again.
test("a revision on the page says what happens next, and the next version comes back in the same card", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/page-revision.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Waiting for your answer.", { exact: false })).toBeVisible();

  // Act
  await page.evaluate(() => window.revise?.());

  // Assert
  await expect(dialog.getByText("Revision received: the next version replaces this page.")).toBeVisible();
  await expect(page.getByLabel("Command Center", { exact: true })).toBeAttached();

  // Act
  await page.evaluate(() => window.nextVersion?.());

  // Assert
  await expect(dialog.getByRole("heading", { name: "approve the kanban mockups, now with the Paused divider" })).toBeVisible();
  await expect(dialog.getByText("Waiting for your answer.", { exact: false })).toBeVisible();
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeAttached();
});
