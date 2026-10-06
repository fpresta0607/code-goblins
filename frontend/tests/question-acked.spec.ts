import { expect, test } from "./site";

// The Overlord, 2026-10-01: "they shouldn't say withdrawn it should say
// complete". A goblin's question he answered in chat, which the CFO relayed
// and retired with --ack-blocking, read "The CFO already handled this
// question." beside an X. It finishes as answered, with the check, on its
// open card and in History. Since 2026-10-02 the open card finishes as one he
// sent from does: its check with Answered, then it leaves.
test("a question the CFO answered and acked reads answered with the check, on its card and in History", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/question-acked.html");
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Which accent should the board use?")).toBeVisible();

  // Act
  await page.evaluate(() => window.ackByCFO?.());

  // Assert
  // The check shows for three quarters of a second, so one look reads it all.
  await expect(dialog.locator(".done-card:has(svg.check-anim)")).toHaveText(/^Answered\s*The CFO answered it$/);
  await expect(dialog).toBeHidden();

  // Act
  await page.getByLabel("Command Center", { exact: true }).click();
  const history = page.locator(".inbox-history");
  await history.locator("summary").click();

  // Assert
  await expect(history).toContainText("The CFO answered it");
  await expect(history.getByRole("img", { name: "The CFO answered", exact: true })).toHaveCount(1);
});
