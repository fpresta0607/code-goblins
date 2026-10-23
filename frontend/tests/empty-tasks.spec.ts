import { expect, type Locator, openTaskList, type Page, test } from "./site";

const GB = 2 ** 30;
const snapshot = {
  instance: "empty-tasks-fixture", revision: 1, healthy: true, cfo_runs: true, attention: [], tasks: [],
  memory: { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, available: 9.4 * GB, commit_available: 20 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB },
  disk: { drive: "C:", total: 953 * GB, floor: 15 * GB, wake: 10 * GB, free: 331.3 * GB },
  processors: { performance_cores: 6, efficiency_cores: 4, efficiency_free: 0, next: 0.25, busiest: ["WmiPrvSE"], free: 0.55 },
  gpu: { adapters: [{ name: "Intel(R) Graphics", busy: 0.2, busiest: "siqshift-desktop" }] },
};

async function board(page: Page) {
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/events") {
      await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    } else {
      await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    }
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

// Every word a person reads in a list of queued tasks other than its heading,
// its count and its meters.
async function wordsBesideTheMeters(list: Locator) {
  return list.evaluate((box) => {
    const walker = document.createTreeWalker(box, NodeFilter.SHOW_TEXT);
    const words: string[] = [];
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (node.textContent!.trim() && !node.parentElement!.closest("h2, h3, .task-meters, .sr-only")) words.push(node.textContent!.trim());
    }
    return words;
  });
}

async function expectMeters(list: Locator) {
  for (const name of ["Memory", "Disk", "CPU", "GPU"]) await expect(list.getByRole("group", { name, exact: true })).toBeVisible();
}

// The Overlord, 2026-10-12, with a picture of the Tasks column at 0 with its
// meters and "Nothing queued" under them: "when nothign queued dont need to
// show the text nothing queued".
for (const [where, viewport, deviceScaleFactor] of [["in the Overlord's window, 1707 px wide", { width: 1707, height: 1067 }, 1.5], ["on a phone, 390 px wide", { width: 390, height: 900 }, 3]] as const) {
  test.describe(where, () => {
    test.use({ viewport, deviceScaleFactor });

    test("an empty Tasks column shows its count and its meters and no empty-state words", async ({ page }, testInfo) => {
      // Arrange
      await board(page);
      const column = page.getByRole("region", { name: "Tasks", exact: true });

      // Act
      const words = await wordsBesideTheMeters(column);
      await column.screenshot({ path: testInfo.outputPath(`empty-tasks-${viewport.width}.png`) });

      // Assert
      await expect(column.locator(".column-count")).toHaveText("0");
      await expectMeters(column);
      expect(words).toEqual([]);
    });
  });
}

test("the CFO's Task view lists an empty queue the same way", async ({ page }) => {
  // Arrange
  await board(page);
  await page.keyboard.press("Control+Alt+1");
  await openTaskList(page);
  const queue = page.getByRole("region", { name: "Queued tasks", exact: true });

  // Act
  await expect(queue).toBeVisible();
  const words = await wordsBesideTheMeters(queue);

  // Assert
  await expect(queue.locator(".column-count")).toHaveText("0");
  await expectMeters(queue);
  expect(words).toEqual([]);
});
