import { expect, test } from "./site";

// The Overlord, 2026-10-01: "they shouldn't say withdrawn it should say
// complete", and 2026-10-02: "The Command Center should only give me
// questions that the CFO has for the Overlord, for me." A goblin's question
// to the CFO is never a card of his and never waits on him. Once the CFO has
// answered and acked it, it is in History as answered, with the check.
test("a goblin's question the CFO answered and acked was never his to answer, and is in History with the check", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/question-acked.html");
  const dialog = page.getByRole("dialog");
  const menu = page.getByLabel("Command Center", { exact: true });
  const history = page.locator(".inbox-history");

  // Assert: while it waits on the CFO, nothing waits on him and nothing of
  // it shows.
  await expect(menu).toBeVisible();
  await expect(dialog).toBeHidden();
  await menu.click();
  await expect(page.locator(".command-center-menu")).toContainText("Nothing is waiting on you.");
  await expect(history).toHaveCount(0);

  // Act
  await page.evaluate(() => window.ackByCFO?.());

  // Assert
  await expect(dialog).toBeHidden();
  await expect(menu).toBeVisible();
  await history.locator("summary").click();
  await expect(history).toContainText("Which accent should the board use?");
  await expect(history).toContainText("The CFO answered it");
  await expect(history.locator(".delivery.succeeded")).toHaveCount(1);
});
