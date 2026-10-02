import { expect, test } from "@playwright/test";

// The Overlord, 2026-09-28: the Command Center must not "prevent input when
// the editor has text annotation input", and decision 3596: it never opens
// on its own while he types. A question that arrives while he types in a text
// field waits under the badge; one that arrives while he is not typing still
// opens it.
test("a new question waits under the badge while he types and never takes his input", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/command-typing.html");
  const comment = page.getByRole("textbox", { name: "Comment to the CFO" });
  await comment.click();
  await page.keyboard.type("Half a sent");

  // Act
  await page.evaluate(() => window.ask?.("notify-cg-board-polish-1"));
  await page.waitForTimeout(500);
  await page.keyboard.type("ence");

  // Assert
  await expect(page.getByRole("dialog")).toBeHidden();
  await expect(comment).toBeFocused();
  await expect(comment).toBeEnabled();
  await expect(comment).toHaveValue("Half a sentence");
  await expect(page.locator(".command-center-menu .count-badge")).toHaveText("1");
});

test("a new question still opens the Command Center when he is not typing", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/command-typing.html");

  // Act
  await page.evaluate(() => window.ask?.("notify-cg-board-polish-1"));

  // Assert
  await expect(page.getByRole("dialog").getByText("May I build the layout switch as drawn?")).toBeVisible();
});
