import { expect, holdStream, test, type Page } from "./site";

// The board's part of automatic resume, as approved on its Scrawl page: each
// paused card says why it waits and what resumes it in place of Paused, Next
// sits on the card the order for a free slot takes first, the memory meter
// shows the goblins live against the cap, a Start or Resume with no free slot
// says so, a reported production defect says it jumps the queue, and a live
// goblin past 20 minutes without progress says for how long.
const GB = 2 ** 30;
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const head = "@" + "a".repeat(40);
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since: ago(95), ...fields });
const paused = (id: string, reason: string, until: string, minutes: number) => task(id, "paused", { at: ago(minutes),
  lifecycle: { phase: "paused", action: "pause", at: ago(minutes), kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason, until, at: ago(minutes) } } });
const PAUSED = [
  paused("paused-memory", "memory", "", 50),
  paused("paused-pull", "dependency", "pr:https://github.com/o/r/pull/331", 40),
  paused("paused-answer", "question", "q-7", 30),
  paused("paused-yours", "overlord", "", 20),
  paused("paused-ci", "ci", "run:https://github.com/o/r/actions/runs/9" + head, 10),
];
const WORKING = [
  task("working-moving", "working", { progress: { at: ago(4), source: "commit" } }),
  task("working-stalled", "working", { progress: { at: ago(23), source: "push" } }),
];
const machine = (available: number, capacity: object) => ({ available: available * GB, total: 32 * GB, commit_available: 20 * GB, commit_limit: 48 * GB, paged_pool: 0.5 * GB, nonpaged_pool: 0.3 * GB, floor: 4 * GB, next: 5 * GB, holders: [], capacity });
const DURATIONS = [12, 13, 15].map((minutes, index) => ({ repository: "o/r", kind: "ci", name: "test", url: "https://github.com/o/r/actions/runs/" + (100 + index), duration_seconds: minutes * 60, finished_at: ago(60) }));

async function open(page: Page, memory: object, tasks: object[]) {
  // The board reads the clock the fixture's times count back from.
  await page.clock.setFixedTime(now);
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], memory, ci_durations: DURATIONS, tasks });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}
// The board only: the CFO's Task tab shows the queue and its meter too.
const board = (page: Page) => page.locator(".task-board");
const card = (page: Page, id: string) => board(page).locator(".task-card-shell").filter({ has: page.getByRole("button", { name: "Stop " + id, exact: true }).or(page.getByRole("button", { name: "Remove " + id, exact: true })) });

for (const width of [1440, 390]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 2400 } });

    test("each paused card says why it waits and what resumes it, once, inside its card", async ({ page }) => {
      // Arrange
      await open(page, machine(4.6, { live: 2, limit: 2, configured: 8, slots: 0 }), [task("queued-one", "queued", { generation: "", brief: true }), ...WORKING, ...PAUSED]);
      const section = board(page).getByRole("region", { name: "Paused", exact: true });

      // Act
      const said = await section.locator(".card-status-text").allTextContents();
      const outside = await section.locator(".task-card-shell").evaluateAll((cards) => cards.filter((shell) => {
        const box = shell.getBoundingClientRect(), status = shell.querySelector(".plain-status")!.getBoundingClientRect();
        return status.left < box.left || status.right > box.right;
      }).length);

      // Assert
      expect(said).toEqual(["Memory: resumes at 5 GB free", "Waiting on PR #331 to merge", "Waiting for your answer", "Paused by you", "Waiting on CI, usually 13 min"]);
      expect(outside).toBe(0);
    });
  });
}

test.describe("on the wide board", () => {
  test.use({ viewport: { width: 1440, height: 2400 } });

  test("Next sits on the goblin paused for memory, not the queue, while memory is short", async ({ page }) => {
    await open(page, machine(4.6, { live: 2, limit: 2, configured: 8, slots: 0 }), [task("queued-one", "queued", { generation: "", brief: true }), ...WORKING, ...PAUSED]);
    await expect(board(page).locator(".next-chip")).toHaveCount(1);
    await expect(card(page, "paused-memory").locator(".next-chip")).toHaveText("Next up");
    await expect(card(page, "paused-memory").locator(".next-chip")).toHaveClass(/waiting/);
  });

  test("with only pauses that wait on him, a date or a pull request, Next stays on the queue", async ({ page }) => {
    const waiting = PAUSED.filter((item) => item.id !== "paused-memory");
    await open(page, machine(9, { live: 2, limit: 7, configured: 8, slots: 5 }), [task("queued-one", "queued", { generation: "", brief: true }), ...WORKING, ...waiting]);
    await expect(board(page).locator(".next-chip")).toHaveCount(1);
    await expect(card(page, "queued-one").locator(".next-chip")).toHaveText("Next up");
  });

  test("a reported production defect says it jumps the queue and takes Next from a paused goblin", async ({ page }) => {
    await open(page, machine(9, { live: 2, limit: 7, configured: 8, slots: 5 }), [task("queued-one", "queued", { generation: "", brief: true }), task("urgent", "queued", { generation: "", brief: true, priority: "production-defect" }), ...WORKING, ...PAUSED]);
    await expect(board(page).locator(".next-chip")).toHaveCount(1);
    await expect(card(page, "urgent").locator(".next-chip")).toHaveText("Production defect: jumps the queue");
  });

  test("the meter shows the goblins live against the cap, and the setting while memory lowers it", async ({ page }) => {
    await open(page, machine(9, { live: 3, limit: 8, configured: 8, slots: 5 }), [task("queued-one", "queued", { generation: "", brief: true })]);
    const meter = board(page).getByRole("group", { name: "Memory", exact: true });
    await expect(meter.locator(".memory-capacity")).toHaveText("Goblins live3 of 8");
    await expect(meter).not.toContainText("Memory allows");
  });

  test("with no free slot, Start and Resume say so before the click", async ({ page }) => {
    await open(page, machine(5.2, { live: 3, limit: 3, configured: 8, slots: 0 }), [task("queued-one", "queued", { generation: "", brief: true }), ...PAUSED]);
    await expect(board(page).getByRole("group", { name: "Memory", exact: true })).toContainText("Memory allows 3 of the 8 set");
    for (const button of [board(page).getByRole("button", { name: "Start queued-one" }), board(page).getByRole("button", { name: "Resume paused-pull" })]) {
      await expect(button).toHaveAttribute("aria-disabled", "true");
      await expect(button).toHaveAttribute("data-tip", "No free slot: 3 of 3 goblins live");
    }
  });

  test("only a live goblin past 20 minutes without progress says for how long", async ({ page }) => {
    await open(page, machine(9, { live: 2, limit: 7, configured: 8, slots: 5 }), [...WORKING, ...PAUSED]);
    await expect(board(page).locator(".card-stalled")).toHaveCount(1);
    await expect(card(page, "working-stalled").locator(".card-stalled")).toHaveText("No progress for 23m");
  });
});
