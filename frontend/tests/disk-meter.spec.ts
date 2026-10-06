import { expect, test, type Page } from "./site";

const GB = 2 ** 30;
const since = "2026-10-05T12:00:00Z";
const tasks = [
  { id: "polish-settings", title: "Polish settings", project: "example", phase: "queued", brief: true, verified: false, generation: "", since },
  { id: "working-one", title: "working-one", project: "example", phase: "working", verified: false, generation: "working-one-1", since },
];
const memory = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, available: 9.4 * GB, commit_available: 20 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB };
const drive = { drive: "C:", total: 953 * GB, floor: 15 * GB, wake: 10 * GB };
// Free disk well above the floor, under the floor where nothing starts, and
// under the lower mark where the CFO is woken.
const states = {
  roomy: { ...drive, free: 74 * GB },
  floor: { ...drive, free: 12.6 * GB },
  wake: { ...drive, free: 6.2 * GB },
};

async function board(page: Page, disk: object) {
  const snapshot = { instance: "disk-fixture", revision: 1, healthy: true, cfo_runs: true, attention: [], memory, disk, tasks };
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
  const column = page.getByRole("region", { name: "Tasks", exact: true });
  await expect(column.getByRole("group", { name: "Disk" })).toBeVisible();
  return column;
}

// The meter's layout: every visible label inside it at body size, the two marks'
// labels apart, nothing scrolling sideways, and the disk meter the second
// meter in the memory meter's box, under memory's own scale.
async function layout(column: ReturnType<Page["getByRole"]>) {
  return column.evaluate((tasks) => {
    const meter = tasks.querySelector<HTMLElement>('[aria-label="Disk"]')!;
    const memoryBox = tasks.querySelector<HTMLElement>('[aria-label="Memory"]')!;
    const box = memoryBox.getBoundingClientRect();
    const memoryScale = memoryBox.querySelector<HTMLElement>(":scope > .memory-scale")!.getBoundingClientRect();
    const inside = meter.getBoundingClientRect();
    const texts = [...meter.querySelectorAll<HTMLElement>(":scope > :not(.sr-only), :scope > * > span, :scope > * > strong")].filter((element) => element.textContent?.trim());
    const [wake, floor] = [".memory-scale > .floor", ".memory-scale > .next"].map((selector) => meter.querySelector<HTMLElement>(selector)!.getBoundingClientRect());
    return {
      smallest: Math.min(...texts.map((element) => parseFloat(getComputedStyle(element).fontSize))),
      outside: [...meter.children].filter((child) => !child.classList.contains("sr-only")).filter((child) => { const rect = child.getBoundingClientRect(); return rect.left < inside.left || rect.right > inside.right || rect.bottom > inside.bottom; }).map((child) => child.className),
      labelsApart: wake.right <= floor.left,
      overflows: meter.scrollWidth > meter.clientWidth,
      stackedInTheMemoryBox: meter.parentElement === memoryBox && inside.top >= memoryScale.bottom && inside.left >= box.left && inside.right <= box.right && inside.bottom <= box.bottom,
    };
  });
}

// The Overlord's window: 2560 by 1600 at 150 percent, so 1707 CSS pixels wide,
// with the CFO's panel open beside the board.
test.describe("in the Overlord's window, 1707 px wide with the CFO's panel open", () => {
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  for (const [state, disk] of Object.entries(states)) {
    test(`free disk ${state === "roomy" ? "above the floor" : state === "floor" ? "under the floor" : "under the wake mark"} shows under memory in the same box`, async ({ page }, testInfo) => {
      // Arrange: the CFO's own panel, on its Task view, beside the board.
      const column = await board(page, disk);
      await page.keyboard.press("Control+Alt+1");
      await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
      await expect(page.locator(".context-pane")).toBeVisible();

      // Act
      const measured = await layout(column);
      await page.screenshot({ path: testInfo.outputPath(`disk-${state}-1707.png`) });

      // Assert
      expect(measured).toEqual({ smallest: 16, outside: [], labelsApart: true, overflows: false, stackedInTheMemoryBox: true });
      const meter = column.getByRole("group", { name: "Disk" });
      await expect(meter.locator(".memory-line")).toHaveText(`Disk free (C:)${(disk.free / GB).toFixed(1)} GB`);
      await expect(meter.locator(".memory-fill")).toHaveClass(new RegExp(state === "roomy" ? "ready" : state === "floor" ? "waiting" : "under"));
    });
  }

  test("a queued task's Start names free disk under the floor", async ({ page }) => {
    // Arrange
    const column = await board(page, states.floor);

    // Act
    const start = column.getByRole("button", { name: "Start Polish settings" });

    // Assert
    await expect(start).toHaveAttribute("aria-disabled", "true");
    await expect(start).toHaveAttribute("data-tip", "Needs 15 GB of free disk (12.6 GB free)");
  });
});

test.describe("on a phone, 390 px wide", () => {
  test.use({ viewport: { width: 390, height: 900 } });

  test("the disk meter stays inside its box", async ({ page }, testInfo) => {
    // Arrange
    const column = await board(page, states.floor);

    // Act
    const measured = await layout(column);
    await column.screenshot({ path: testInfo.outputPath("disk-floor-390.png") });

    // Assert
    expect(measured).toEqual({ smallest: 16, outside: [], labelsApart: true, overflows: false, stackedInTheMemoryBox: true });
  });
});
