import { expect, openTaskList, type Page, test } from "./site";

const GB = 2 ** 30;
const since = "2026-10-05T12:00:00Z";
const tasks = [
  { id: "polish-settings", title: "Polish settings", project: "example", phase: "queued", brief: true, verified: false, generation: "", since },
  { id: "working-one", title: "working-one", project: "example", phase: "working", verified: false, generation: "working-one-1", since },
];
const memory = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, available: 9.4 * GB, commit_available: 20 * GB, paged_pool: 0.6 * GB, nonpaged_pool: 0.4 * GB };
const marks = { floor: 15 * GB, wake: 10 * GB };
// The home's drive alone, as on a machine without a Dev Drive, with free disk
// well above the floor, under the floor where nothing starts, and under the
// lower mark where the CEO is woken.
const oneDrive = (free: number) => ({ drive: "C:", free, total: 953 * GB, ...marks, drives: [{ drive: "C:", free, total: 953 * GB }] });
const states = {
  roomy: oneDrive(74 * GB),
  floor: oneDrive(12.6 * GB),
  wake: oneDrive(6.2 * GB),
};
// The home on C: and the worktrees, scratch and caches on the Dev Drive D:,
// with the gate home on one of them, so the meter shows two lines.
const home = { drive: "C:", free: 331.3 * GB, total: 953 * GB };
const devDrive = { drive: "D:", free: 12.6 * GB, total: 200 * GB, dev_drive: true };
const twoDrives = { ...devDrive, ...marks, drives: [home, devDrive] };

async function board(page: Page, disk: object, shown: object[] = tasks) {
  const snapshot = { instance: "disk-fixture", revision: 1, healthy: true, ceo_runs: true, attention: [], memory, disk, tasks: shown };
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

// The meter's layout: every visible label inside it at body size, nothing
// written under its bar, nothing scrolling sideways, and the disk meter the
// second meter in the memory meter's box, under memory's own bar.
async function layout(column: ReturnType<Page["getByRole"]>) {
  return column.evaluate((tasks) => {
    const meter = tasks.querySelector<HTMLElement>('[aria-label="Disk"]')!;
    const memoryBox = tasks.querySelector<HTMLElement>('[aria-label="Memory"]')!;
    const box = memoryBox.getBoundingClientRect();
    const memoryBar = memoryBox.querySelector<HTMLElement>(":scope > .memory-bar")!.getBoundingClientRect();
    const inside = meter.getBoundingClientRect();
    const rows = [...meter.querySelectorAll<HTMLElement>(":scope > .disk-drive")];
    const texts = rows.flatMap((row) => [...row.querySelectorAll<HTMLElement>(":scope > :not(.sr-only), :scope > * > span, :scope > * > strong")]).filter((element) => element.textContent?.trim());
    const shown = rows.flatMap((row) => [...row.children]).filter((child) => !child.classList.contains("sr-only"));
    return {
      smallest: Math.min(...texts.map((element) => parseFloat(getComputedStyle(element).fontSize))),
      outside: shown.filter((child) => { const rect = child.getBoundingClientRect(); return rect.left < inside.left || rect.right > inside.right || rect.bottom > inside.bottom; }).map((child) => child.className),
      underBar: rows.map((row) => row.querySelector(":scope > .memory-bar")?.nextElementSibling).filter((next) => next && !next.matches(".sr-only")).map((next) => next!.className).join(" "),
      overflows: meter.scrollWidth > meter.clientWidth,
      stackedInTheMemoryBox: meter.parentElement === memoryBox && inside.top >= memoryBar.bottom && inside.left >= box.left && inside.right <= box.right && inside.bottom <= box.bottom,
    };
  });
}

// The Overlord's window: 2560 by 1600 at 150 percent, so 1707 CSS pixels wide,
// with the CEO's panel open beside the board.
test.describe("in the Overlord's window, 1707 px wide with the CEO's panel open", () => {
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  for (const [state, disk] of Object.entries(states)) {
    test(`free disk ${state === "roomy" ? "above the floor" : state === "floor" ? "under the floor" : "under the wake mark"} shows under memory in the same box`, async ({ page }, testInfo) => {
      // Arrange: the CEO's own panel, on its Task view, beside the board.
      const column = await board(page, disk);
      await page.keyboard.press("Control+Alt+1");
      await openTaskList(page);
      await expect(page.locator(".context-pane")).toBeVisible();

      // Act
      const measured = await layout(column);
      await page.screenshot({ path: testInfo.outputPath(`disk-${state}-1707.png`) });

      // Assert
      expect(measured).toEqual({ smallest: 16, outside: [], underBar: "", overflows: false, stackedInTheMemoryBox: true });
      const meter = column.getByRole("group", { name: "Disk" });
      await expect(meter.locator(".memory-line")).toHaveText([`Disk free (C:)${(disk.free / GB).toFixed(1)} GB`]);
      await expect(meter.locator(".memory-fill")).toHaveClass(new RegExp(state === "roomy" ? "ready" : state === "floor" ? "waiting" : "under"));
      await expect(meter.locator(".memory-bar")).toHaveAttribute("data-tip", "Red mark: 10 GB, where the CEO is woken. White mark: the 15 GB floor. No goblin or gate test run starts under it.");
    });
  }

  // The Overlord, 2026-11-09: the window "should be displaying whatever drive
  // disk is actually being utilized" (#2196).
  test("the home's drive and the Dev Drive each show their own line and bar", async ({ page }, testInfo) => {
    // Arrange
    const column = await board(page, twoDrives);
    await openTaskList(page);

    // Act
    const measured = await layout(column);
    await column.getByRole("group", { name: "Memory" }).screenshot({ path: testInfo.outputPath("disk-two-drives-1707.png") });

    // Assert
    expect(measured).toEqual({ smallest: 16, outside: [], underBar: "", overflows: false, stackedInTheMemoryBox: true });
    const meter = column.getByRole("group", { name: "Disk" });
    await expect(meter.locator(".memory-line")).toHaveText(["Disk free (C:)331.3 GB", "Disk free (D: Dev Drive)12.6 GB"]);
    await expect(meter.locator(".memory-fill")).toHaveClass([/ready/, /waiting/]);
    const animations = await meter.evaluate((element) => element.getAnimations({ subtree: true }).length);
    expect(animations, "nothing in the disk meter animates at idle").toBe(0);
  });

  test("a drive that cannot be read says so and draws no bar", async ({ page }) => {
    // Arrange: the Dev Drive's disk is not attached.
    const detached = { drive: "D:", free: 0, total: 0, dev_drive: true, error: "The device is not ready." };

    // Act
    const column = await board(page, { drive: "", free: 0, total: 0, ...marks, drives: [home, detached] });

    // Assert
    const meter = column.getByRole("group", { name: "Disk" });
    await expect(meter.locator(".memory-line")).toHaveText(["Disk free (C:)331.3 GB", "Disk free (D: Dev Drive)Unreadable"]);
    await expect(meter.locator(".memory-bar")).toHaveCount(1);
    await expect(meter.locator(".disk-drive.unreadable .memory-line")).toHaveAttribute("data-tip", "D: Dev Drive cannot be read: The device is not ready.");
  });

  // Start is in the task's panel, never on its card, and a labelled Start
  // carries a tip only to say why it cannot run.
  test("a queued task's Start under the floor is one click", async ({ page }) => {
    // Arrange
    const column = await board(page, states.floor);

    // Act
    await column.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("Polish settings", { exact: true }) }).locator(".task-card").click();
    const start = page.locator(".goblin-panel").getByRole("button", { name: "Start Polish settings", exact: true });

    // Assert
    await expect(start).toBeVisible();
    await expect(start).not.toHaveAttribute("aria-disabled", "true");
    await expect(start).not.toHaveAttribute("data-tip");
  });

  test("a Start that waits for disk says so on its card", async ({ page }) => {
    // Arrange: the supervisor holds the clicked Start until disk frees.
    const waiting = tasks.map((task) => task.id === "polish-settings" ? { ...task, starting: true, asked: true } : task);

    // Act
    const column = await board(page, states.floor, waiting);

    // Assert
    const card = column.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText("Polish settings", { exact: true }) });
    await expect(card.locator(".card-status-text")).toHaveText("Starts once disk frees");
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
    expect(measured).toEqual({ smallest: 16, outside: [], underBar: "", overflows: false, stackedInTheMemoryBox: true });
  });

  test("the home's drive and the Dev Drive each show their own line inside the box", async ({ page }, testInfo) => {
    // Arrange
    const column = await board(page, twoDrives);

    // Act
    const measured = await layout(column);
    await column.getByRole("group", { name: "Memory" }).screenshot({ path: testInfo.outputPath("disk-two-drives-390.png") });

    // Assert
    expect(measured).toEqual({ smallest: 16, outside: [], underBar: "", overflows: false, stackedInTheMemoryBox: true });
    await expect(column.getByRole("group", { name: "Disk" }).locator(".memory-line")).toHaveText(["Disk free (C:)331.3 GB", "Disk free (D: Dev Drive)12.6 GB"]);
  });
});
