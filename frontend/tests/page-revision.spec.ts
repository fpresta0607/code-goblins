import { expect, test } from "./site";

// Item 3 of the review-flow brief: revisions sent from the editor update the
// editor. His revision on the page takes the card off Waiting on you, with no
// line that explains it (the Overlord, 2026-10-11); the goblin's next version
// comes back in the same card, waiting on him again.
test("a revision on the page takes it off Waiting on you, and the next version comes back in the same card", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/page-revision.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("link", { name: "Open review" })).toHaveCount(1);

  // Act
  await page.evaluate(() => window.revise?.());

  // Assert
  await expect(page.getByLabel("Command Center", { exact: true })).toBeAttached();
  await expect(dialog.getByText(/Revision received|Waiting for your answer/)).toHaveCount(0);

  // Act
  await page.evaluate(() => window.nextVersion?.());

  // Assert
  await expect(dialog.getByRole("heading", { name: "approve the kanban mockups, now with the Paused divider" })).toBeVisible();
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeAttached();
});
