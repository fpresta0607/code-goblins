import { expect, test, type Page } from "./site";

const GB = 2 ** 30;
const since = "2026-10-05T12:00:00Z";
const tasks = [
  { id: "polish-settings", title: "Polish settings", project: "example", phase: "queued", brief: true, verified: false, generation: "", since },
  { id: "working-one", title: "working-one", project: "example", phase: "working", verified: false, generation: "working-one-1", since },
];
const memory = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, available: 9.4 * GB, commit_available: 20 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB };
const disk = { drive: "C:", total: 953 * GB, floor: 15 * GB, wake: 10 * GB, free: 331.3 * GB };
// The Overlord's processor on 2026-10-09: six performance cores, four
// efficiency cores kept full by two programs outside the fleet.
const cores = { performance_cores: 6, efficiency_cores: 4, efficiency_free: 0, next: 0.25, busiest: ["WmiPrvSE", "MsMpEng"] };
// The performance cores with room for the next start, and under the mark at
// which the supervisor starts nothing by itself.
const states = {
  roomy: { ...cores, free: 0.55 },
  busy: { ...cores, free: 0.08, busiest: ["goblins", "WmiPrvSE"] },
};
const gpu = { adapters: [{ name: "Intel(R) Graphics", busy: 0.705, busiest: "siqshift-desktop" }, { name: "NVIDIA GeForce RTX 5060 Laptop GPU", busy: 0 }] };
const tips = {
  roomy: "6 performance cores 45% busy. 4 efficiency cores 100% busy. Most used by WmiPrvSE and MsMpEng. White mark: 25%, where the next task starts by itself.",
  busy: "6 performance cores 92% busy. 4 efficiency cores 100% busy. Most used by goblins and WmiPrvSE. White mark: 25%, where the next task starts by itself.",
  gpu: "Intel(R) Graphics 71% busy, most by siqshift-desktop. NVIDIA GeForce RTX 5060 Laptop GPU idle.",
};

async function board(page: Page, machine: object) {
  const snapshot = { instance: "machine-meters-fixture", revision: 1, healthy: true, cfo_runs: true, attention: [], memory, disk, ...machine, tasks };
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
  await expect(column.getByRole("group", { name: "Memory" })).toBeVisible();
  return column;
}

// The four meters' layout: each a row of its own in the memory meter's box,
// in the order memory, disk, CPU, GPU, every visible label at body size and
// the same look as disk's, nothing written under any bar, and nothing
// scrolling sideways or leaving the box.
async function layout(column: ReturnType<Page["getByRole"]>) {
  return column.evaluate((tasks) => {
    const memoryBox = tasks.querySelector<HTMLElement>('[aria-label="Memory"]')!;
    const box = memoryBox.getBoundingClientRect();
    const rows = ["Disk", "CPU", "GPU"].map((name) => tasks.querySelector<HTMLElement>(`[aria-label="${name}"]`)!);
    const memoryBar = memoryBox.querySelector<HTMLElement>(":scope > .memory-bar")!;
    const bars = [memoryBar, ...rows.map((row) => row.querySelector<HTMLElement>(":scope > .memory-bar")!)];
    const look = (row: HTMLElement) => { const style = getComputedStyle(row); const line = getComputedStyle(row.querySelector(".memory-line")!); return [style.display, style.rowGap, style.paddingTop, style.borderTopWidth, line.fontSize, line.justifyContent].join(" "); };
    const tops = [memoryBar.getBoundingClientRect().bottom, ...rows.map((row) => row.getBoundingClientRect().top)];
    return {
      smallest: Math.min(...rows.flatMap((row) => [...row.querySelectorAll<HTMLElement>(".memory-line > *")]).map((element) => parseFloat(getComputedStyle(element).fontSize))),
      underABar: bars.map((bar) => bar.nextElementSibling && !bar.nextElementSibling.matches(".sr-only, .disk-meter") ? bar.nextElementSibling.className : "").filter(Boolean),
      barHeights: [...new Set(bars.map((bar) => bar.getBoundingClientRect().height))],
      barWidths: [...new Set(bars.map((bar) => Math.round(bar.getBoundingClientRect().width)))].length,
      sameLookAsDisk: rows.every((row) => look(row) === look(rows[0])),
      inOrderInTheMemoryBox: rows.every((row, index) => row.parentElement === memoryBox && row.getBoundingClientRect().top >= tops[index] && row.getBoundingClientRect().left >= box.left && row.getBoundingClientRect().right <= box.right && row.getBoundingClientRect().bottom <= box.bottom),
      overflows: memoryBox.scrollWidth > memoryBox.clientWidth || rows.some((row) => row.scrollWidth > row.clientWidth),
    };
  });
}

const clean = { smallest: 16, underABar: [], barHeights: [8], barWidths: 1, sameLookAsDisk: true, inOrderInTheMemoryBox: true, overflows: false };

// The Overlord's window: 2560 by 1600 at 150 percent, so 1707 CSS pixels
// wide. He asked on 2026-10-09 for "a meter bar for CPU and GPU as well ...
// clean no extra text below matching styling and layout of memory free and
// disk space in same container", and picked four stacked rows.
test.describe("in the Overlord's window, 1707 px wide", () => {
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  for (const [state, processors] of Object.entries(states)) {
    for (const beside of ["as the board opens", "the CFO's Task view open beside the board"]) {
      test(`CPU and GPU are rows under disk in the same box, ${state === "roomy" ? "with room" : "under the mark"}, ${beside}`, async ({ page }, testInfo) => {
        // Arrange
        const column = await board(page, { processors, gpu });
        if (beside !== "as the board opens") {
          await page.keyboard.press("Control+Alt+1");
          await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
          await expect(page.locator(".context-pane")).toBeVisible();
        }

        // Act
        const measured = await layout(column);
        await page.screenshot({ path: testInfo.outputPath(`meters-${state}-${beside === "as the board opens" ? "board" : "panel"}-1707.png`) });

        // Assert
        expect(measured).toEqual(clean);
        const cpu = column.getByRole("group", { name: "CPU" }), graphics = column.getByRole("group", { name: "GPU" });
        await expect(cpu.locator(".memory-line")).toHaveText(`CPU free${state === "roomy" ? "55%" : "8%"}`);
        await expect(cpu.locator(".memory-fill")).toHaveClass(new RegExp(state === "roomy" ? "ready" : "waiting"));
        await expect(cpu.locator(".memory-mark")).toHaveCount(1);
        await expect(cpu.locator(".memory-bar")).toHaveAttribute("data-tip", tips[state as keyof typeof states]);
        await expect(graphics.locator(".memory-line")).toHaveText("GPU free29%");
        await expect(graphics.locator(".memory-fill")).toHaveClass(/ready/);
        await expect(graphics.locator(".memory-mark")).toHaveCount(0);
        await expect(graphics.locator(".memory-bar")).toHaveAttribute("data-tip", tips.gpu);
      });
    }
  }

  test("a board that reads neither shows memory and disk as before", async ({ page }) => {
    // Act
    const column = await board(page, {});

    // Assert
    await expect(column.getByRole("group", { name: "Disk" })).toBeVisible();
    await expect(column.getByRole("group", { name: "CPU" })).toHaveCount(0);
    await expect(column.getByRole("group", { name: "GPU" })).toHaveCount(0);
  });

  test("a machine Windows counts no graphics adapter on shows the CPU meter alone", async ({ page }) => {
    // Act
    const column = await board(page, { processors: states.roomy, gpu: { adapters: [] } });

    // Assert
    await expect(column.getByRole("group", { name: "CPU" })).toBeVisible();
    await expect(column.getByRole("group", { name: "GPU" })).toHaveCount(0);
  });
});

test.describe("on a phone, 390 px wide", () => {
  test.use({ viewport: { width: 390, height: 900 } });

  test("the CPU and GPU meters stay inside the box", async ({ page }, testInfo) => {
    // Arrange
    const column = await board(page, { processors: states.busy, gpu });

    // Act
    const measured = await layout(column);
    await column.screenshot({ path: testInfo.outputPath("meters-busy-390.png") });

    // Assert
    expect(measured).toEqual(clean);
  });
});
