import { expect, test } from "./site";

// The Overlord, 2026-10-01: "they shouldn't say withdrawn it should say
// complete". A goblin's question he answered in chat, which the CFO relayed
// and retired with --ack-blocking, read "The CFO already handled this
// question." beside an X. It finishes as answered, with the check, on its
// open card and in History.
test("a question the CFO answered and acked reads answered with the check, on its card and in History", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/question-acked.html");
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Which accent should the board use?")).toBeVisible();

  // Act
  await page.evaluate(() => window.ackByCFO?.());

  // Assert
  const outcome = dialog.getByRole("status");
  await expect(outcome).toHaveText(/^The CFO answered it/);
  await expect(outcome).toHaveClass(/succeeded/);
  await expect(outcome.locator("svg.icon")).toHaveCount(1);
  await expect(dialog.getByText(/already handled|Withdrawn|Superseded/)).toHaveCount(0);

  // Act
  await dialog.getByRole("button", { name: "Close the Command Center" }).click();
  await page.getByLabel("Command Center", { exact: true }).click();
  const history = page.locator(".inbox-history");
  await history.locator("summary").click();

  // Assert
  await expect(history).toContainText("The CFO answered it");
  await expect(history.locator(".delivery.succeeded")).toHaveCount(1);
});
