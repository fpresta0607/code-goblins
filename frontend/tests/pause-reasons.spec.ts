import { expect, holdStream, openTaskList, type Page, test } from "./site";

// The board's part of automatic resume, as approved on its Scrawl page: each
// paused card says Paused and why, Next sits on the card the order for a free
// slot takes first, the memory meter says nothing of slots or a cap, a Start
// or Resume in a panel goes by memory alone, a reported production defect
// says it jumps the queue, and a live goblin past 20 minutes without progress
// says for how long.
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
const machine = (available: number) => ({ available: available * GB, total: 32 * GB, commit_available: 20 * GB, commit_limit: 48 * GB, paged_pool: 0.5 * GB, nonpaged_pool: 0.3 * GB, floor: 4 * GB, next: 5 * GB, holders: [] });
const USUAL = [{ repository: "o/r", kind: "ci", seconds: 13 * 60 }];

async function open(page: Page, memory: object, tasks: object[]) {
  // The board reads the clock the fixture's times count back from.
  await page.clock.setFixedTime(now);
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], memory, ci_usual: USUAL, tasks });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}
// The board only: the CFO's Task tab shows the queue and its meter too.
const board = (page: Page) => page.locator(".task-board");
const card = (page: Page, id: string) => board(page).locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText(id, { exact: true }) });
const panel = (page: Page) => page.locator(".goblin-panel");

// Opens a goblin's panel from its card, on its Task view.
async function select(page: Page, id: string) {
  await card(page, id).locator(".task-card").click();
  await expect(page.locator("#panel-title")).toHaveText(id);
  await openTaskList(page);
}

for (const width of [1440, 390]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 2400 } });

    test("each paused card says Paused and why, once, inside its card", async ({ page }) => {
      // Arrange
      await open(page, machine(4.6), [task("queued-one", "queued", { generation: "", brief: true }), ...WORKING, ...PAUSED]);
      const cards = board(page).locator(".task-card-shell").filter({ has: page.locator(".phase-paused") });

      // Act
      const said = await Promise.all(PAUSED.map((paused) => card(page, paused.id).locator(".card-status-text").textContent()));
      const outside = await cards.evaluateAll((shells) => shells.filter((shell) => {
        const box = shell.getBoundingClientRect(), status = shell.querySelector(".plain-status")!.getBoundingClientRect();
        return status.left < box.left || status.right > box.right;
      }).length);

      // Assert
      expect(said).toEqual(["Paused for memory", "Paused until PR #331 merges", "Paused until you answer", "Paused by you", "Paused until CI finishes, usually 13 min"]);
      expect(outside).toBe(0);
    });

    // The Overlord, 2026-10-16: "paused does it means youre on the bench to
    // be resumed". Every paused goblin's terminal is stopped, whatever it
    // waits for, so Paused holds them all and none reads Waiting.
    test("Paused holds every paused goblin, whatever its pause waits for", async ({ page }) => {
      await open(page, machine(4.6), [...WORKING, ...PAUSED]);
      const section = board(page).getByRole("region", { name: "Paused", exact: true });

      await expect(section.locator(".card-status-text")).toHaveText(["Paused for memory", "Paused until PR #331 merges", "Paused until you answer", "Paused by you", "Paused until CI finishes, usually 13 min"]);
      await expect(section.locator(".card-title")).toHaveText(PAUSED.map((paused) => paused.id));
      await expect(board(page).locator(".card-status-text").filter({ hasText: /^Waiting/ })).toHaveCount(0);
    });
  });
}

test.describe("on the wide board", () => {
  test.use({ viewport: { width: 1440, height: 2400 } });

  test("Next sits on the goblin paused for memory, not the queue, while memory is short", async ({ page }) => {
    await open(page, machine(4.6), [task("queued-one", "queued", { generation: "", brief: true }), ...WORKING, ...PAUSED]);
    await expect(board(page).locator(".next-chip")).toHaveCount(1);
    await expect(card(page, "paused-memory").locator(".next-chip")).toHaveText("Next up");
    await expect(card(page, "paused-memory").locator(".next-chip")).toHaveClass(/waiting/);
  });

  test("with only pauses that wait on him, a date or a pull request, Next stays on the queue", async ({ page }) => {
    const waiting = PAUSED.filter((item) => item.id !== "paused-memory");
    await open(page, machine(9), [task("queued-one", "queued", { generation: "", brief: true }), ...WORKING, ...waiting]);
    await expect(board(page).locator(".next-chip")).toHaveCount(1);
    await expect(card(page, "queued-one").locator(".next-chip")).toHaveText("Next up");
  });

  test("a reported production defect says it jumps the queue and takes Next from a paused goblin", async ({ page }) => {
    await open(page, machine(9), [task("queued-one", "queued", { generation: "", brief: true }), task("urgent", "queued", { generation: "", brief: true, priority: "production-defect" }), ...WORKING, ...PAUSED]);
    await expect(board(page).locator(".next-chip")).toHaveCount(1);
    await expect(card(page, "urgent").locator(".next-chip")).toHaveText("Production defect: jumps the queue");
  });

  test("the meter says nothing of slots or a cap", async ({ page }) => {
    await open(page, machine(9), [task("queued-one", "queued", { generation: "", brief: true })]);
    const meter = board(page).getByRole("group", { name: "Memory", exact: true });
    await expect(meter).toContainText("Memory free");
    await expect(meter).not.toContainText("Goblins live");
    await expect(meter).not.toContainText("Memory allows");
  });

  // On 2026-10-07 the board refused a Start with "No free slot: 8 of 8
  // goblins live" while 9 GB was free: no count of goblins holds one back.
  // Start and Resume are in the task's panel, never on its card.
  test("with eight goblins live and 9 GB free, Start and Resume are not held back", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
    await open(page, machine(9), [task("queued-one", "queued", { generation: "", brief: true }), ...PAUSED]);
    for (const [id, name] of [["queued-one", "Start queued-one"], ["paused-pull", "Resume paused-pull"]]) {
      await select(page, id);
      const button = panel(page).getByRole("button", { name, exact: true });
      await expect(button).toBeVisible();
      await expect(button).not.toHaveAttribute("aria-disabled", "true");
    }
  });

  // The Overlord, 2026-10-16: a paused goblin is on the bench to be resumed,
  // and a waiting one is live, on standby. One paused until another goblin
  // delivers reads so in Paused, and its panel resumes or stops it; one
  // waiting on that goblin reads Waiting in In progress, and its panel pauses
  // or stops it, with nothing to resume.
  test("a goblin paused on another reads Paused and sits in Paused, and one waiting on it reads Waiting in In progress", async ({ page }) => {
    // Arrange
    const awaited = task("cg-merge", "working", { goblin_name: "Bernie", goblin_title: "Merge Maestro" });
    const benched = paused("paused-on-task", "dependency", "task:cg-merge", 15);
    const standing = task("waiting-on-task", "waiting", { waiting_on: "cg-merge" });
    await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
    await open(page, machine(9), [awaited, standing, benched]);
    const section = board(page).getByRole("region", { name: "Paused", exact: true });
    const progress = board(page).getByRole("region", { name: "In progress", exact: true });

    // Assert: the paused goblin, on its card and in its panel.
    await expect(card(page, "paused-on-task").locator(".card-status-text")).toHaveText("Paused until Bernie delivers");
    await expect(section.locator(".card-title")).toHaveText(["paused-on-task"]);
    await select(page, "paused-on-task");
    const benchedControls = panel(page).getByRole("group", { name: "Controls for paused-on-task" });
    await expect(benchedControls.getByRole("button")).toHaveText(["Resume", "Stop"]);
    await expect(panel(page).getByRole("button", { name: "Pause paused-on-task", exact: true })).toHaveCount(0);

    // Assert: the waiting goblin, on its card and in its panel.
    await expect(card(page, "waiting-on-task").locator(".card-status-text")).toHaveText("Waiting on Bernie - Merge Maestro");
    await expect(progress.locator(".card-title").getByText("waiting-on-task", { exact: true })).toHaveCount(1);
    await expect(section.locator(".card-title").getByText("waiting-on-task", { exact: true })).toHaveCount(0);
    await select(page, "waiting-on-task");
    const standingControls = panel(page).getByRole("group", { name: "Controls for waiting-on-task" });
    await expect(standingControls.getByRole("button")).toHaveText(["Pause", "Stop"]);
    await expect(panel(page).getByRole("button", { name: "Resume waiting-on-task", exact: true })).toHaveCount(0);
  });

  // The Overlord, 2026-10-08: "everything error wise goes to cfo". A goblin
  // without progress is the CFO's to hear, not a warning on its card.
  test("a live goblin past 20 minutes without progress has no warning on its card", async ({ page }) => {
    await open(page, machine(9), [...WORKING, ...PAUSED]);
    await expect(card(page, "working-stalled")).toBeVisible();
    await expect(board(page).getByText(/No progress for/)).toHaveCount(0);
  });
});
