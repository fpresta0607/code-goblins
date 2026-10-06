import { expect, test, type Page } from "./site";

const GB = 2 ** 30;
const tasks = [
  { id: "polish-settings", title: "Polish settings", project: "example", phase: "queued", brief: true, verified: false },
  { id: "onboarding-tour", title: "onboarding-tour", project: "acme-web", phase: "queued", brief: true, verified: false },
];
const machine = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB };
const holders = [{ name: "ChatGPT", commit: 11.2 * GB }, { name: "claude", commit: 5.7 * GB }, { name: "cfo", commit: 4.3 * GB }];
// The three states of the approved mockup: memory is the tighter, commit is
// the tighter, and the kernel's paged pool is leaking. Then the two that put
// the start mark at the end of the bar: a snapshot with no commit figures,
// and a machine with little more memory than the mark.
const states = {
  memory: { ...machine, available: 7.3 * GB, commit_available: 20 * GB },
  commit: { ...machine, available: 3.4 * GB, commit_available: 2.5 * GB, holders },
  pool: { ...machine, available: 6.1 * GB, commit_available: 18 * GB, paged_pool: 15.6 * GB },
  unreported: { ...machine, available: 5.6 * GB, commit_limit: 0, commit_available: 0 },
  small: { ...machine, total: 6 * GB, commit_limit: 9 * GB, available: 3 * GB, commit_available: 6 * GB },
};

async function tasksColumn(page: Page, memory: object) {
  const snapshot = { instance: "memory-fixture", revision: 1, healthy: true, example: true, cfo_runs: false, memory, tasks };
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/events") {
      await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    } else {
      await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    }
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.evaluate(() => document.fonts.ready);
  const column = page.getByRole("region", { name: "Tasks", exact: true });
  await expect(column.getByRole("group", { name: "Memory" })).toBeVisible();
  return column;
}

for (const width of [1440, 390]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 1100 } });

    for (const [state, memory] of Object.entries(states)) {
      test(`the ${state} meter is body-size text that stays inside its box`, async ({ page }, testInfo) => {
        // Arrange
        const column = await tasksColumn(page, memory);
        const meter = column.getByRole("group", { name: "Memory" });

        // Act
        const layout = await meter.evaluate((box) => {
          const inside = box.getBoundingClientRect();
          const texts = [...box.querySelectorAll<HTMLElement>(":scope > :not(.sr-only), :scope > * > span, :scope > * > strong")].filter((element) => element.textContent?.trim());
          const [scale, floor, next, mark] = [".memory-scale", ".memory-scale > .floor", ".memory-scale > .next", ".memory-mark.floor"].map((selector) => box.querySelector<HTMLElement>(selector)!.getBoundingClientRect());
          const hasRoomAtMark = mark.right + next.width <= scale.right;
          return {
            floorAtMark: !hasRoomAtMark || Math.abs(mark.left - floor.right) <= 8,
            smallest: Math.min(...texts.map((element) => parseFloat(getComputedStyle(element).fontSize))),
            outside: [...box.children].filter((child) => { const rect = child.getBoundingClientRect(); return rect.left < inside.left || rect.right > inside.right || rect.bottom > inside.bottom; }).map((child) => child.className),
            labelsApart: floor.right <= next.left,
            overflows: box.scrollWidth > box.clientWidth,
          };
        });
        await column.screenshot({ path: testInfo.outputPath(`${state}-${width}.png`) });

        // Assert
        expect(layout).toEqual({ smallest: 16, floorAtMark: true, outside: [], labelsApart: true, overflows: false });
      });
    }

    test("memory the tighter keeps today's meter", async ({ page }) => {
      const meter = (await tasksColumn(page, states.memory)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-line")).toHaveText("Memory free7.3 GB");
      await expect(meter.locator(".memory-holders, .memory-warning")).toHaveCount(0);
    });

    test("a snapshot with no commit figures shows memory, not zero commit", async ({ page }) => {
      const meter = (await tasksColumn(page, states.unreported)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-line")).toHaveText("Memory free5.6 GB");
      await expect(meter.locator(".memory-fill")).toHaveClass(/ready/);
    });

    test("commit the tighter shows commit, names the apps holding it, and holds Start", async ({ page }) => {
      const column = await tasksColumn(page, states.commit);
      const meter = column.getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-line")).toHaveText("Commit free (memory plus page file)2.5 GB");
      await expect(meter.locator(".memory-holders")).toHaveText("Most commit: ChatGPT 11.2 GB, claude 5.7 GB, cfo 4.3 GB");
      await expect(column.locator(".next-chip")).toHaveText("Next at 5 GB");
      await expect(column.getByRole("button", { name: "Start Polish settings", exact: true })).toHaveAttribute("data-tip", "Needs 5 GB of commit free to keep the 4 GB floor");
    });

    test("a leaking paged pool gets one warning line", async ({ page }) => {
      const meter = (await tasksColumn(page, states.pool)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-warning")).toHaveText("Paged pool 15.6 GB: Windows is holding this in its kernel paged pool, memory no goblin can use; restarting the PC frees it.");
      await expect(meter.locator(".memory-holders")).toHaveCount(0);
    });
  });
}
