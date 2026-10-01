import { expect, test } from "@playwright/test";
import snapshot from "./fixtures/queued-snapshot.json" with { type: "json" };

// Tall enough for eight cards on the first page and a short final page that
// ends the panel's scroll bar, the change that once drove a render loop.
test.use({ viewport: { width: 1440, height: 1750 } });

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
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.keyboard.press("Control+Alt+1");
  await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
  await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();
  await page.evaluate(() => document.fonts.ready);
  await page.locator(".context-pane").evaluate((pane) => { pane.style.width = "832px"; });
  const queue = page.locator(".cfo-queue");
  await expect(queue.locator(".pager")).toContainText("1\u20138 of 11");
  expect(await page.locator(".panel-task").evaluate((panel) => panel.scrollHeight > panel.clientHeight)).toBe(true);
  const width = await queue.locator(".fit-list").evaluate((list) => list.clientWidth);
  for (let turn = 0; turn < 3; turn++) {
    await queue.getByRole("button", { name: "Next cards", exact: true }).click();
    await expect(queue.locator(".pager")).toContainText("9\u201311 of 11");
    expect(await page.locator(".panel-task").evaluate((panel) => panel.scrollHeight > panel.clientHeight)).toBe(false);
    await expect(queue.getByText(snapshot.tasks[10].title, { exact: true })).toBeVisible();
    expect(await queue.locator(".fit-list").evaluate((list) => list.clientWidth)).toBe(width);
    await queue.getByRole("button", { name: "Earlier cards", exact: true }).click();
    await expect(queue.locator(".pager")).toContainText("1\u20138 of 11");
  }
  expect(errors).toEqual([]);
});
