import { expect, test } from "./site";

// The Overlord's brief, 2026-09-30, approved as shown on 2026-10-06: after a
// restart the board says plainly what is coming back and then what resumed, a
// goblin that did not come back says so on its card.
// Why it did not come back is the CFO's to hear
// (2026-10-08, "everything error wise goes to cfo"). The window's size is his: 1707 CSS pixels wide
// at device scale 1.5.
test.use({ viewport: { width: 1707, height: 900 }, deviceScaleFactor: 1.5 });

test("while the fleet comes back the line says what is back, and a waiting card that it waits for memory", async ({ page }, testInfo) => {
  // Act
  await page.goto("/tests/fixtures/restart.html?state=coming");

  // Assert
  const line = page.getByRole("status").filter({ hasText: "after a restart" });
  await expect(line).toHaveText("Coming back after a restart: the CFO is back, 1 of 6 goblins is back.");
  await expect(line.getByRole("button")).toHaveCount(0);
  await expect(page.getByText("Comes back after the restart when memory allows")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("restart-coming.png") });
});

test("once all is back the line says what resumed, the card of one that did not come back says so, and the line stays dismissed", async ({ page }, testInfo) => {
  // Arrange
  await page.goto("/tests/fixtures/restart.html");
  const line = page.getByRole("status").filter({ hasText: "after a restart" });

  // Assert
  await expect(line).toContainText("Resumed the CFO and 5 of 6 goblins after a restart.");
  await expect(line).not.toContainText("cg-site-hero");
  await expect(page.getByText("Did not come back after the restart", { exact: true })).toBeVisible();
  await expect(page.getByText("Codex is not signed in")).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath("restart-resumed.png") });

  // Act
  await line.getByRole("button", { name: "Dismiss" }).click();
  await page.reload();

  // Assert
  await expect(page.getByText("Did not come back after the restart", { exact: true })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "after a restart" })).toHaveCount(0);
});
