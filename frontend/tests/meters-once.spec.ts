import { CFO_DOOR, expect, holdStream, openTaskList, type Page, test } from "./site";

const GB = 2 ** 30;
const since = "2026-10-05T12:00:00Z";
// The board the UI audit opened (#1598): a queued task and every machine
// meter, memory, kernel pools, disk, CPU and GPU.
const snapshot = {
  instance: "meters-once-fixture", revision: 1, healthy: true, cfo_runs: true, attention: [],
  tasks: [{ id: "polish-settings", title: "Polish settings", project: "example", phase: "queued", brief: true, verified: false, generation: "", since }],
  memory: { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, available: 6 * GB, commit_available: 20 * GB, paged_pool: 0.5 * GB, nonpaged_pool: 0.3 * GB },
  disk: { drive: "D:", total: 953 * GB, floor: 15 * GB, wake: 10 * GB, free: 130 * GB, drives: [{ drive: "D:", total: 953 * GB, free: 130 * GB, dev_drive: true }] },
  processors: { performance_cores: 6, efficiency_cores: 4, efficiency_free: 0, next: 0.25, busiest: ["WmiPrvSE"], free: 0.4 },
  gpu: { adapters: [{ name: "Intel(R) Graphics", busy: 0.6, busiest: "siqshift-desktop" }] },
};

// The meters a person can see, wherever they are drawn.
const shownMeters = (page: Page) => page.locator('[role="group"][aria-label="Memory"]:visible');

// maximize maximizes the panel from its top row, or from the row's More menu
// when the row has no room for the button.
async function maximize(page: Page) {
  const row = page.locator(".context-pane .panel-top");
  const button = row.getByRole("button", { name: "Maximize the panel", exact: true });
  if (await button.count()) return button.click();
  await row.getByRole("button", { name: "More", exact: true }).click();
  await row.getByRole("menuitem", { name: "Maximize", exact: true }).click();
}

async function board(page: Page) {
  await holdStream(page, snapshot);
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Tasks", exact: true })).toBeVisible();
}

// The CFO's Task view lists the queue as the Tasks column does, and the
// machine meters are drawn once on screen: in the Tasks column while it
// shows, and in the panel only where no column shows them, under no heading
// of a column the panel does not hold.
const cases = [
  { name: "beside the board at 1440 px, in the Tasks column", viewport: { width: 1440, height: 900 }, metersIn: "Tasks column",
    open: async (page: Page) => { await page.keyboard.press("Control+Alt+1"); } },
  { name: "on a phone at 390 px, where the panel fills the screen, in the panel", viewport: { width: 390, height: 844 }, metersIn: "panel",
    open: async (page: Page) => { await page.locator(".cfo-pin").getByRole("button", { name: CFO_DOOR }).last().click(); } },
  { name: "in Orchestration at 1440 px, which has no Tasks column, in the panel", viewport: { width: 1440, height: 900 }, metersIn: "panel",
    open: async (page: Page) => { await page.getByRole("button", { name: "Orchestration", exact: true }).click(); } },
  { name: "maximized at 1440 px, which hides the board, in the panel", viewport: { width: 1440, height: 900 }, metersIn: "panel",
    open: async (page: Page) => { await page.keyboard.press("Control+Alt+1"); await maximize(page); } },
] as const;

for (const { name, viewport, metersIn, open } of cases) {
  test(`with the CFO's Task view open ${name}, the meters show once`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize(viewport);
    await board(page);

    // Act
    await open(page);
    await openTaskList(page);
    const panel = page.locator(".context-pane");
    const queue = panel.getByRole("region", { name: "Queued tasks", exact: true });
    await expect(queue.getByText("Polish settings", { exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`meters-once-${viewport.width}-${metersIn === "panel" ? "panel" : "column"}.png`) });

    // Assert
    await expect(shownMeters(page)).toHaveCount(1);
    const owner = metersIn === "panel" ? queue : page.getByRole("region", { name: "Tasks", exact: true });
    await expect(owner.getByRole("group", { name: "Memory", exact: true })).toBeVisible();
    await expect(panel.getByRole("heading", { name: /^Tasks/ })).toHaveCount(0);
  });
}
