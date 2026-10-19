import { expect, test, type Page } from "./site";

const GB = 2 ** 30;
const tasks = [
  { id: "polish-settings", title: "Polish settings", project: "example", phase: "queued", brief: true, verified: false },
  { id: "onboarding-tour", title: "onboarding-tour", project: "acme-web", phase: "queued", brief: true, verified: false },
];
const machine = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB, pool_line: 7.5 * GB, pool_growth: [] };
const holders = [{ name: "ChatGPT", commit: 11.2 * GB }, { name: "claude", commit: 5.7 * GB }, { name: "cfo", commit: 4.3 * GB }];
// The three states of the approved mockup: memory is the tighter, commit is
// the tighter, and the kernel's pools are past their line, as they were on
// 2026-10-18 four days into a boot. Then the two that put
// the start mark at the end of the bar: a snapshot with no commit figures,
// and a machine with little more memory than the mark.
const states = {
  memory: { ...machine, available: 7.3 * GB, commit_available: 20 * GB },
  commit: { ...machine, available: 3.4 * GB, commit_available: 2.5 * GB, holders },
  pool: { ...machine, available: 6.1 * GB, commit_available: 18 * GB, paged_pool: 7.61 * GB, nonpaged_pool: 3.35 * GB, pool_growth: [{ tag: "Toke", pool: "paged", grown: 3.2 * GB, holds: 3.5 * GB }] },
  unreported: { ...machine, available: 5.6 * GB, commit_limit: 0, commit_available: 0 },
  small: { ...machine, total: 6 * GB, commit_limit: 9 * GB, available: 3 * GB, commit_available: 6 * GB },
};

async function tasksColumn(page: Page, memory: object, scheduling: object | null = null, disk: object | null = null) {
  const snapshot = { instance: "memory-fixture", revision: 1, healthy: true, example: true, cfo_runs: false, memory, scheduling, disk, tasks };
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
      // The Overlord, 2026-10-07, on the labels under the bars: "dont need
      // extra text under". Each meter keeps its name and value, and its bar's
      // marks are explained in the bar's tip.
      test(`the ${state} meter is its name, value and bar in body-size text inside its box, with nothing written under its bar`, async ({ page }, testInfo) => {
        // Arrange
        const column = await tasksColumn(page, memory);
        const meter = column.getByRole("group", { name: "Memory" });

        // Act
        const layout = await meter.evaluate((box) => {
          const inside = box.getBoundingClientRect();
          const texts = [...box.querySelectorAll<HTMLElement>(":scope > :not(.sr-only), :scope > * > span, :scope > * > strong")].filter((element) => element.textContent?.trim());
          const bar = box.querySelector<HTMLElement>(":scope > .memory-bar")!;
          return {
            smallest: Math.min(...texts.map((element) => parseFloat(getComputedStyle(element).fontSize))),
            outside: [...box.children].filter((child) => { const rect = child.getBoundingClientRect(); return rect.left < inside.left || rect.right > inside.right || rect.bottom > inside.bottom; }).map((child) => child.className),
            underBar: bar.nextElementSibling && !bar.nextElementSibling.matches(".sr-only, .disk-meter") ? bar.nextElementSibling.className : "",
            overflows: box.scrollWidth > box.clientWidth,
          };
        });
        await column.screenshot({ path: testInfo.outputPath(`${state}-${width}.png`) });

        // Assert
        expect(layout).toEqual({ smallest: 16, outside: [], underBar: "", overflows: false });
        await expect(meter.locator(":scope > .memory-bar")).toHaveAttribute("data-tip", /^Red mark: the 4 GB floor\. Nothing starts under it\. White mark: 5 GB, where the next task starts\./);
      });
    }

    test("memory the tighter keeps today's meter", async ({ page }) => {
      const meter = (await tasksColumn(page, states.memory)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(":scope > .memory-line")).toHaveText("Memory free7.3 GB");
      await expect(meter.locator(":scope > .memory-bar")).not.toHaveAttribute("data-tip", /Most commit|Paged pool/);
    });

    test("a snapshot with no commit figures shows memory, not zero commit", async ({ page }) => {
      const meter = (await tasksColumn(page, states.unreported)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(":scope > .memory-line")).toHaveText("Memory free5.6 GB");
      await expect(meter.locator(":scope > .memory-bar > .memory-fill")).toHaveClass(/ready/);
    });

    test("commit the tighter shows commit, names the apps holding it, and leaves Start one click", async ({ page }) => {
      const column = await tasksColumn(page, states.commit);
      const meter = column.getByRole("group", { name: "Memory" });
      await expect(meter.locator(":scope > .memory-line")).toHaveText("Commit free (memory plus page file)2.5 GB");
      await expect(meter.locator(":scope > .memory-bar")).toHaveAttribute("data-tip", /Most commit: ChatGPT 11\.2 GB, claude 5\.7 GB, cfo 4\.3 GB$/);
      await expect(column.locator(".next-chip")).toHaveText("Next at 5 GB");
      // Start is in the task's panel, never on its card.
      await column.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("Polish settings", { exact: true }) }).locator(".task-card").click();
      const start = page.locator(".goblin-panel").getByRole("button", { name: "Start Polish settings", exact: true });
      await expect(start).toBeVisible();
      await expect(start).not.toHaveAttribute("aria-disabled", "true");
    });

    // The Overlord, 2026-10-09, on the line the scheduler wrote under the
    // memory bar: "dont display nothing starts text too much text". Whatever
    // the scheduler says, the box holds each meter's name and value and
    // nothing else a person reads: no text under a bar and none between two
    // meters. Why nothing starts reaches the CFO as a wake.
    for (const [said, text] of Object.entries({
      "started a task": "starting polish-settings",
      "resumed a goblin": "resuming paused-task",
      "found nothing could start": "nothing starts: polish-settings: its last start failed: Only 4.9 GB of memory is free; a start needs 5 GB of memory and commit to keep the 4 GB floor",
    })) {
      for (const [state, memory] of Object.entries(states)) {
        test(`the ${state} meters hold no text but their names and values when the scheduler ${said}`, async ({ page }) => {
          // Arrange
          const scheduling = { at: "2026-10-09T16:05:00Z", text, waiting: [{ id: "polish-settings", why: "its last start failed" }] };
          const disk = { drive: "C:", free: 330.3 * GB, total: 900 * GB, floor: 15 * GB, wake: 10 * GB };

          // Act
          const column = await tasksColumn(page, memory, scheduling, disk);
          const read = await column.locator(".task-meters").evaluate((box) => {
            const walker = document.createTreeWalker(box, NodeFilter.SHOW_TEXT);
            const stray: string[] = [];
            for (let node = walker.nextNode(); node; node = walker.nextNode()) {
              const parent = node.parentElement!;
              if (node.textContent!.trim() && !parent.closest(".sr-only") && !parent.closest(".memory-line")) stray.push(node.textContent!.trim());
            }
            return {
              meters: [...box.querySelectorAll('[role="group"]')].map((meter) => meter.getAttribute("aria-label")),
              lines: box.querySelectorAll(".memory-line").length,
              stray,
              spoken: [...box.querySelectorAll(".sr-only")].map((element) => element.textContent).join(" "),
            };
          });

          // Assert
          expect(read.meters).toEqual(["Memory", "Kernel pools", "Disk"]);
          expect(read.lines).toBe(3);
          expect(read.stray).toEqual([]);
          expect(read.spoken).not.toContain("polish-settings");
        });
      }
    }

    // The kernel's pools are a meter of their own beside memory, never a
    // warning box in the column (the Overlord, 2026-10-07: "any alerts that
    // are critical go through cfo to me as needed"): red past the line
    // measured on this PC, with each pool and the tags that grew in its tip.
    test("the kernel's pools are a meter beside memory, red past their line, whose tip names each pool and what grew", async ({ page }, testInfo) => {
      // Arrange
      const column = await tasksColumn(page, states.pool);
      const pools = column.getByRole("group", { name: "Kernel pools" });

      // Assert
      await expect(pools.locator(".memory-line")).toHaveText("Kernel pools11.0 GB");
      await expect(pools.locator(".memory-fill")).toHaveClass(/under/);
      await expect(pools.locator(".memory-bar")).toHaveAttribute("data-tip", "Paged 7.6 GB, nonpaged 3.4 GB. Red mark: 7.5 GB, past which free memory cannot hold the fleet and the CFO's panel offers Restart PC. Grew most since sign-in: Toke 3.2 GB paged.");
      await expect(column.getByRole("group", { name: "Memory" }).locator(":scope > .memory-bar")).not.toHaveAttribute("data-tip", /pool/i);
      // A screen reader hears the meter's state and tip in its hidden line.
      await expect(pools.locator(".sr-only")).toHaveText(/^The kernel's pools hold 11\.0 GB, past the 7\.5 GB line: Restart PC in the CFO's panel frees them\. Paged 7\.6 GB/);
      await column.locator(".task-meters").screenshot({ path: testInfo.outputPath(`pools-past-the-line-${width}.png`) });
    });

    test("healthy pools sit under their line, not red", async ({ page }) => {
      const pools = (await tasksColumn(page, states.memory)).getByRole("group", { name: "Kernel pools" });
      await expect(pools.locator(".memory-line")).toHaveText("Kernel pools1.0 GB");
      await expect(pools.locator(".memory-fill")).toHaveClass(/ready/);
    });
  });
}
