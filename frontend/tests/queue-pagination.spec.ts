import { expect, openBoardPastFirstRun, test } from "./site";
import snapshot from "./fixtures/queued-snapshot.json" with { type: "json" };

// Tall enough for a first page of most of the queue and a short final page
// that ends the panel's scroll bar, the change that once drove a render loop.
// How many cards the first page holds follows from how tall they are, as the
// pager fits them, so it is read from the page rather than fixed: the cards
// lost their buttons on 2026-10-16 and the page took nine instead of eight.
// The window holds most of the queue on its first page and leaves a final
// page short enough to end the scroll bar: a card became one row on
// 2026-10-17, and took its project's line back the same day, and the first
// page holds ten of them at 1160 px.
test.use({ viewport: { width: 1440, height: 1160 } });

test("the CFO queue can turn to its short final page without a render loop", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/events") {
      await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    } else if (path === "/api/workspace") {
      await route.fulfill({ json: { repository: "pager-proof", root: "C:\\workspace\\code-goblins\\.worktrees\\pagination-regression\\scratch-home", mcp: [], environment: [], notes: [] } });
    } else {
      await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    }
  });
  await openBoardPastFirstRun(page);
  await page.keyboard.press("Control+Alt+1");
  await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();
  await page.evaluate(() => document.fonts.ready);
  await page.locator(".context-pane").evaluate((pane) => { pane.style.width = "832px"; });
  const queue = page.locator(".cfo-queue");
  let held = 0;
  await expect.poll(async () => {
    held = await queue.locator(".task-card-shell").count();
    return (await queue.locator(".pager").textContent())?.includes(`1\u2013${held} of 11`) ?? false;
  }).toBe(true);
  expect(held).toBeGreaterThanOrEqual(5);
  expect(held).toBeLessThan(11);
  const first = `1\u2013${held} of 11`, last = held === 10 ? "11 of 11" : `${held + 1}\u201311 of 11`;
  await expect(queue.locator(".pager")).toContainText(first);
  expect(await page.locator(".panel-task").evaluate((panel) => panel.scrollHeight > panel.clientHeight)).toBe(true);
  const width = await queue.locator(".fit-list").evaluate((list) => list.clientWidth);
  for (let turn = 0; turn < 3; turn++) {
    await queue.getByRole("button", { name: "Next cards", exact: true }).click();
    await expect(queue.locator(".pager")).toContainText(last);
    await expect(queue.locator(".task-card-shell")).toHaveCount(11 - held);
    expect(await page.locator(".panel-task").evaluate((panel) => panel.scrollHeight > panel.clientHeight)).toBe(false);
    await expect(queue.getByText(snapshot.tasks[10].title, { exact: true })).toBeVisible();
    expect(await queue.locator(".fit-list").evaluate((list) => list.clientWidth)).toBe(width);
    await queue.getByRole("button", { name: "Earlier cards", exact: true }).click();
    await expect(queue.locator(".pager")).toContainText(first);
  }
  expect(errors).toEqual([]);
});
