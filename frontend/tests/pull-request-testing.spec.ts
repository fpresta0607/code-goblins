import { expect, holdStream, test, type Page } from "./site";

// The Overlord, 2026-10-08, of Sid's pull request #512: the merge train's
// card read "CI tests #512" and "#512 Sid - Memory Keeper Testing" while
// Sid's panel read Paused in yellow, "It resumes by itself when PR #512
// merges." and "Checks passed". A goblin whose pull request is under test
// reads one state with one link on its card, its panel, its canvas node and
// the train's card, and stays in In progress, its session live or paused
// until the pull request merges. A real pause stays in the Paused section,
// never in yellow. The supervisor is played by one held snapshot.
const REPO = "https://github.com/northwind/northwind-api";
const TRAIN_PR = REPO + "/pull/900";
const RUN = REPO + "/actions/runs/77/job/770";
const HEAD = "a".repeat(40);
const AMBER = "rgb(243, 197, 120)";
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const goblin = (id: string, name: string, title: string, task: string, fields: Record<string, unknown> = {}) => ({
  id, title: task, goblin_name: name, goblin_title: title, project: "northwind-api", phase: "idle", report: "done", verified: false,
  generation: id + "-1", harness: "claude", model: "claude-opus-5-5", since: ago(95), ...fields,
});
const pause = (reason: string, until: string) => ({ phase: "paused", action: "pause", at: ago(20), kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason, until, at: ago(20) } });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: [
    goblin("nw-sid", "Sid", "Memory Keeper", "Keep the memory floor", { phase: "paused", at: ago(20), pr: REPO + "/pull/512", lifecycle: pause("dependency", "pr:" + REPO + "/pull/512"),
      hosted_checks: { head: HEAD, state: "passed", checks: 9, at: ago(16) } }),
    goblin("nw-mae", "Mae", "Bug Hunter", "Retry the webhook", { pr: REPO + "/pull/513", hosted_checks: { head: "b".repeat(40), state: "pending", checks: 9, link: RUN, at: ago(2) } }),
    goblin("nw-otis", "Otis", "Ledger Clerk", "Sync the ledger", { phase: "paused", report: "working", at: ago(10), lifecycle: pause("memory", "") }),
    goblin("nw-ned", "Ned", "Docs Scribe", "Fix the docs links", { phase: "working", report: "working" }),
  ],
  merge_trains: [{
    id: "northwind-api-20261008-182000", repository: "northwind/northwind-api", base: "main", pr: TRAIN_PR, state: "testing", runs: 1, started: ago(15), note: "",
    cars: [
      { number: 512, url: REPO + "/pull/512", title: "Keep the memory floor", task: "nw-sid", goblin: "Sid", goblin_title: "Memory Keeper", head: HEAD, state: "riding" },
      { number: 514, url: REPO + "/pull/514", title: "Cache the rates", task: "nw-done", goblin: "", goblin_title: "", head: "c".repeat(40), state: "riding" },
    ],
  }],
};

async function open(page: Page) {
  await page.clock.setFixedTime(now);
  await holdStream(page, SNAPSHOT);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const board = (page: Page) => page.locator(".task-board");
const progress = (page: Page) => board(page).getByRole("region", { name: "In progress", exact: true });
const pausedSection = (page: Page) => board(page).getByRole("region", { name: "Paused", exact: true });
const card = (page: Page, name: string) => board(page).locator(".task-card-shell").filter({ has: page.locator(".card-title").filter({ hasText: name }) });
const header = (page: Page) => page.locator(".goblin-panel .panel-header");
// What the panel shows in place of a terminal whose session is not running.
const ended = (page: Page) => page.locator(".deck-slot:not([hidden]) .ended-session");

async function select(page: Page, name: string) {
  await card(page, name).locator(".task-card").click();
  await expect(page.locator("#panel-title")).toHaveText(name);
}

// What a part says and opens, and the colour it is drawn in.
const read = (part: ReturnType<Page["locator"]>) => part.evaluate((element) => ({
  text: element.textContent, href: element.getAttribute("href"), color: getComputedStyle(element).color,
}));

for (const width of [1440, 390]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 1400 } });

    test("a goblin paused until its pull request merges reads the train's Testing and opens the train's pull request, in In progress", async ({ page }) => {
      // Arrange
      await open(page);
      const sid = card(page, "Sid - Memory Keeper");
      const train = progress(page).locator(".train-card");

      // Act
      const status = await read(sid.locator(".plain-status"));
      const chip = await read(sid.locator("a.card-checks"));
      const trainStatus = await read(train.locator("a.train-status"));
      const row = train.locator(".train-car").filter({ hasText: "#512" });

      // Assert
      await expect(progress(page).locator(".task-card-shell").filter({ hasText: "Sid - Memory Keeper" })).toHaveCount(1);
      await expect(pausedSection(page).locator(".task-card-shell").filter({ hasText: "Sid - Memory Keeper" })).toHaveCount(0);
      expect(status.text).toBe("Testing");
      expect(status.color).not.toBe(AMBER);
      expect(chip).toMatchObject({ text: "Testing", href: TRAIN_PR });
      expect(trainStatus.href).toBe(TRAIN_PR);
      await expect(row).toContainText("Sid - Memory Keeper");
      await expect(row.locator(".train-car-state")).toHaveText("Testing");
      await expect(sid).not.toContainText(/Paused|resumes/);
      await expect(sid.getByRole("button", { name: "Resume Keep the memory floor", exact: true })).toBeVisible();
    });

    test("a goblin waiting on its own CI reads Testing and opens that run", async ({ page }) => {
      await open(page);
      const mae = card(page, "Mae - Bug Hunter");

      await expect(mae.locator(".card-status-text")).toHaveText("Testing");
      await expect(mae.locator("a.card-checks")).toHaveText("Testing");
      await expect(mae.locator("a.card-checks")).toHaveAttribute("href", RUN);
    });

    test("a real pause stays in the Paused section with what resumes it, never in yellow", async ({ page }) => {
      await open(page);
      const otis = pausedSection(page).locator(".task-card-shell").filter({ hasText: "Otis - Ledger Clerk" });

      const status = await read(otis.locator(".plain-status"));
      const dot = await otis.locator(".status-dot").evaluate((element) => getComputedStyle(element).backgroundColor);

      expect(status.text).toBe("Memory: resumes at 5 GB free");
      expect(status.color).not.toBe(AMBER);
      expect(dot).not.toBe(AMBER);
      await expect(pausedSection(page).locator(".task-card-shell")).toHaveCount(1);
    });
  });
}

test.describe("in his window", () => {
  test.use({ viewport: { width: 1707, height: 1067 } });

  test("the panel and the canvas node read the card's state and open the same page", async ({ page }) => {
    // Arrange
    await open(page);
    const cardTip = await card(page, "Sid - Memory Keeper").locator(".task-card").getAttribute("data-tip");

    // Act
    await select(page, "Sid - Memory Keeper");
    const panelStatus = await read(header(page).locator(".panel-status"));
    const panelChip = await read(header(page).locator("a.hosted-checks"));
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    const node = page.locator(".flow-node").filter({ has: page.locator("strong", { hasText: "Sid - Memory Keeper" }) });
    const nodeStatus = await read(node.locator(".plain-status"));

    // Assert
    expect(panelStatus.text).toBe("Testing");
    expect(panelStatus.color).not.toBe(AMBER);
    expect(panelChip).toMatchObject({ text: "Testing", href: TRAIN_PR });
    await expect(header(page).locator(".panel-activity")).toHaveCount(0);
    await expect(header(page)).not.toContainText(/Paused|resumes/);
    await expect(ended(page).locator("h2")).toHaveText("Testing");
    await expect(ended(page)).not.toContainText("Paused");
    expect(nodeStatus.text).toBe("Testing");
    await expect(node.locator(".flow-node-main")).toHaveAttribute("data-tip", cardTip!);
    await expect(node.locator(".flow-node-main")).toHaveAttribute("aria-label", /^Sid - Memory Keeper\. Testing\./);
  });

  test("a real pause's panel still says its session paused", async ({ page }) => {
    await open(page);

    await select(page, "Otis - Ledger Clerk");

    await expect(header(page).locator(".panel-status")).toHaveText("Paused");
    await expect(ended(page).locator("h2")).toHaveText("Session paused");
  });

  test("the panel of a goblin waiting on its own CI opens that run", async ({ page }) => {
    await open(page);

    await select(page, "Mae - Bug Hunter");

    await expect(header(page).locator(".panel-status")).toHaveText("Testing");
    await expect(header(page).locator("a.hosted-checks")).toHaveAttribute("href", RUN);
  });
});
