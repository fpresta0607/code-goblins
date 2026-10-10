import { expect, test } from "./site";
import { caret, nativeMarkers, panelCaret } from "./caret";

// The Overlord, 2026-10-09 about 20:27Z, with a screenshot of the Update
// card's Output and one of the thin caret before Workspace in a task panel:
// "when output > should match task panel arrows in ui meaning the caret" and
// "should look more like this". Output opened on the browser's own black
// triangle, and the finished children in a goblin's panel on a caret of
// another size and weight. Whatever opens and closes on the board turns the
// task panel's caret: the same shape, size, color and weight, turned the same
// way.
const PRINTED = "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n[3/4] Install Code Goblins v0.5.0\nStopping the supervisor (pid 21116).\n";

test("the Update card's Output starts closed and opens on the task panel's caret, never the browser's triangle", async ({ page }) => {
  // Arrange: the task panel's caret, then an update under way.
  const panel = await panelCaret(page);
  await page.route("**/api/runs/update-v0.5.0-1/output", (route) => route.fulfill({ json: { state: "running", output: PRINTED } }));
  await page.goto("/tests/fixtures/update-item.html");
  await page.getByLabel("Command Center, 1 waiting on you").click();
  await page.locator(".inbox-list li").first().getByRole("button").click();
  const card = page.getByRole("dialog").locator(".update-card");
  await page.evaluate(() => window.updateStep("running"));
  const output = card.locator("details.update-output");
  const summary = output.locator("> summary");

  // Assert: closed, with the panel's caret and no triangle.
  await expect(summary).toHaveText("Output");
  await expect(output).not.toHaveAttribute("open");
  await expect(card.locator(".run-output")).toBeHidden();
  expect(await nativeMarkers(card)).toEqual([]);
  expect(await caret(summary)).toEqual(panel.closed);

  // Act: open it from the keyboard.
  await summary.focus();
  await page.keyboard.press("Enter");

  // Assert: what the update printed shows, and the caret turned as the
  // panel's does.
  await expect(output).toHaveAttribute("open");
  await expect(card.locator(".run-output")).toContainText("Stopping the supervisor");
  expect(await caret(summary)).toEqual(panel.opened);

  // Act
  await page.keyboard.press("Space");

  // Assert
  await expect(output).not.toHaveAttribute("open");
  expect(await caret(summary)).toEqual(panel.closed);
});

test("a goblin's finished children fold behind the task panel's caret", async ({ page }) => {
  // Arrange
  const panel = await panelCaret(page);
  await page.goto("/tests/fixtures/fleet-tree.html?view=panel");
  const finished = page.locator("details.working-finished");
  const summary = finished.locator("> summary");

  // Assert
  await expect(summary).toHaveText("2 finished");
  await expect(finished).not.toHaveAttribute("open");
  expect(await nativeMarkers(page.locator(".whats-working"))).toEqual([]);
  expect(await caret(summary)).toEqual(panel.closed);

  // Act
  await summary.click();

  // Assert
  await expect(page.getByRole("list", { name: "Finished" }).locator(".working-row")).toHaveCount(2);
  expect(await caret(summary)).toEqual(panel.opened);
});
