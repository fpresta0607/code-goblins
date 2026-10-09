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
        await expect(meter.locator(".memory-bar")).toHaveAttribute("data-tip", /^Red mark: the 4 GB floor\. Nothing starts under it\. White mark: 5 GB, where the next task starts\./);
      });
    }

    test("memory the tighter keeps today's meter", async ({ page }) => {
      const meter = (await tasksColumn(page, states.memory)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-line")).toHaveText("Memory free7.3 GB");
      await expect(meter.locator(".memory-bar")).not.toHaveAttribute("data-tip", /Most commit|Paged pool/);
    });

    test("a snapshot with no commit figures shows memory, not zero commit", async ({ page }) => {
      const meter = (await tasksColumn(page, states.unreported)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-line")).toHaveText("Memory free5.6 GB");
      await expect(meter.locator(".memory-fill")).toHaveClass(/ready/);
    });

    test("commit the tighter shows commit, names the apps holding it, and leaves Start one click", async ({ page }) => {
      const column = await tasksColumn(page, states.commit);
      const meter = column.getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-line")).toHaveText("Commit free (memory plus page file)2.5 GB");
      await expect(meter.locator(".memory-bar")).toHaveAttribute("data-tip", /Most commit: ChatGPT 11\.2 GB, claude 5\.7 GB, cfo 4\.3 GB$/);
      await expect(column.locator(".next-chip")).toHaveText("Next at 5 GB");
      await expect(column.getByRole("button", { name: "Start Polish settings", exact: true })).not.toHaveAttribute("aria-disabled", "true");
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
          expect(read.meters).toEqual(["Memory", "Disk"]);
          expect(read.lines).toBe(2);
          expect(read.stray).toEqual([]);
          expect(read.spoken).not.toContain("polish-settings");
        });
      }
    }

    // A leaking paged pool is named in the bar's tip, never as a warning box in
    // the column: the Overlord, 2026-10-07, "any alerts that are critical go
    // through cfo to me as needed".
    test("a leaking paged pool is named in the bar's tip, with no warning box", async ({ page }) => {
      const meter = (await tasksColumn(page, states.pool)).getByRole("group", { name: "Memory" });
      await expect(meter.locator(".memory-bar")).toHaveAttribute("data-tip", /Paged pool 15\.6 GB/);
      // A screen reader hears the tip in the meter's hidden line, and only there.
      await expect(meter.getByText(/Paged pool/)).toHaveClass("sr-only");
    });
  });
}
