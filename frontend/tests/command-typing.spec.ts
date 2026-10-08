import { expect, test } from "./site";

// The Overlord, 2026-09-28: the Command Center must not "prevent input when
// the editor has text annotation input", and 2026-10-02: one item is one
// signal, so it never opens on its own. A question that arrives waits under
// the badge, whether he types or not, and opens when he asks for it.
test("a new question waits under the badge while he types and never takes his input", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/command-typing.html");
  const comment = page.getByRole("textbox", { name: "Comment to the CFO" });
  await comment.click();
  await page.keyboard.type("Half a sent");

  // Act
  await page.evaluate(() => window.ask?.("build-the-layout-switch"));
  await page.waitForTimeout(500);
  await page.keyboard.type("ence");

  // Assert
  await expect(page.getByRole("dialog")).toBeHidden();
  await expect(comment).toBeFocused();
  await expect(comment).toBeEnabled();
  await expect(comment).toHaveValue("Half a sentence");
  await expect(page.locator(".command-center-menu .count-badge")).toHaveText("1");
});

test("a new question never opens the Command Center by itself, and opens from the list when he asks", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/command-typing.html");

  // Act
  await page.evaluate(() => window.ask?.("build-the-layout-switch"));
  await expect(page.locator(".command-center-menu .count-badge")).toHaveText("1");
  await page.waitForTimeout(500);

  // Assert
  await expect(page.getByRole("dialog")).toBeHidden();

  // Act
  await page.getByLabel("Command Center, 1 waiting on you").click();
  await page.getByRole("button", { name: /^Answer The CFO: / }).click();

  // Assert
  await expect(page.getByRole("dialog").getByText("May I build the layout switch as drawn?")).toBeVisible();
});
