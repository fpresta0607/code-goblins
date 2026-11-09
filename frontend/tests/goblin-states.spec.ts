import { expect, holdStream, type Locator, openTaskList, type Page, test } from "./site";

// The Overlord, 2026-11-02: "How is a goblin waiting on memory yet it's
// still in progress? Goblin states in the board can use some polishing." A
// board with one goblin in each state: each card reads one short state that
// says whether the goblin runs, what it waits for and what brings it back,
// its panel reads the same words, a paused goblin sits only in Paused and a
// live one never reads Paused (#1924).
const REPO = "https://github.com/northwind/northwind-api";
const HEAD = "a".repeat(40);
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const pause = (reason: string, until = "") => ({ phase: "paused", at: ago(20),
  lifecycle: { phase: "paused", action: "pause", at: ago(20), kept: [], stopped: [], problems: [], handoff_saved: true, validation_kept: false, pause: { reason, until, at: ago(20) } } });
const landing = (number: number, head: string) => ({ phase: "idle", report: "done", pr: REPO + "/pull/" + number, hosted_checks: { head, state: "passed", checks: 9, at: ago(6) } });

interface GoblinState { id: string; name: string; fields: Record<string, unknown>; text: string; isPaused: boolean }

const STATES: GoblinState[] = [
  { id: "nw-wren", name: "Wren - Webhook Wrangler", fields: { phase: "working", report: "working" }, text: "Working", isPaused: false },
  { id: "nw-lars", name: "Lars - Load Tester", fields: { phase: "waiting", waiting_on: "memory",
    tree: { task_id: "nw-lars", generation: "nw-lars-1", harness: "claude", memory: 0, own_memory: 0, children: [], line_place: 2 } }, text: "2nd in line for the machine", isPaused: false },
  { id: "nw-vera", name: "Vera - Vault Keeper", fields: { phase: "waiting", waiting_on: "overlord" }, text: "Waiting on you", isPaused: false },
  { id: "nw-cole", name: "Cole - Config Clerk", fields: { phase: "blocked", report: "blocked", reason: "Which port should the API listen on?" }, text: "Waiting on the CFO", isPaused: false },
  { id: "nw-cid", name: "Cid - Check Chaser", fields: { phase: "waiting", waiting_on: "ci" }, text: "Waiting on CI", isPaused: false },
  { id: "nw-dot", name: "Dot - Deploy Driver", fields: { phase: "waiting", waiting_on: "deploy" }, text: "Waiting on its deploy", isPaused: false },
  { id: "nw-nell", name: "Nell - Schema Sculptor", fields: { phase: "waiting", waiting_on: "nw-wren" }, text: "Waiting on Wren", isPaused: false },
  { id: "nw-tess", name: "Tess - Train Rider", fields: landing(512, HEAD), text: "Landing on a train", isPaused: false },
  { id: "nw-mae", name: "Mae - Bug Hunter", fields: landing(513, "b".repeat(40)), text: "Landing, waits to merge", isPaused: false },
  { id: "nw-otis", name: "Otis - Ledger Clerk", fields: pause("memory"), text: "Paused until memory frees", isPaused: true },
  { id: "nw-sid", name: "Sid - Memory Keeper", fields: pause("dependency", "pr:" + REPO + "/pull/514"), text: "Paused until PR #514 merges", isPaused: true },
  { id: "nw-bea", name: "Bea - Bench Warmer", fields: pause("overlord"), text: "Paused until you resume it", isPaused: true },
];

const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: STATES.map(({ id, name, fields }) => {
    const [goblinName, goblinTitle] = name.split(" - ");
    return { id, title: id, goblin_name: goblinName, goblin_title: goblinTitle, project: "northwind-api", verified: false,
      generation: id + "-1", harness: "claude", model: "claude-opus-5-5", since: ago(95), ...fields };
  }),
  merge_trains: [{
    id: "northwind-api-20261102-200000", repository: "northwind/northwind-api", base: "main", pr: REPO + "/pull/900", state: "testing", runs: 1, started: ago(15), note: "",
    cars: [{ number: 512, url: REPO + "/pull/512", title: "nw-tess", task: "nw-tess", goblin: "Tess", goblin_title: "Train Rider", head: HEAD, state: "riding" }],
  }],
  reviews: [{ id: "waiting-nw-vera-1", identity: "v".repeat(64), task: "nw-vera", title: "Waiting on you: sign in to the package registry", state: "open", created_at: ago(5), updated_at: ago(5) }],
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
const cardIn = (part: Locator, name: string) => part.locator(".task-card-shell").filter({ has: part.page().locator(".card-title").getByText(name, { exact: true }) });

for (const width of [1707, 390]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 2400 } });

    test("each goblin reads one state on its card and its panel, and only a stopped goblin reads Paused, in Paused", async ({ page }, testInfo) => {
      test.slow();
      // Arrange
      await open(page);
      if (width === 1707) await page.getByRole("button", { name: "Close panel" }).click();

      // Act
      await board(page).screenshot({ path: testInfo.outputPath(`goblin-states-${width}.png`) });

      // Assert
      for (const { name, text, isPaused } of STATES) {
        const card = cardIn(board(page), name);
        await expect(card.locator(".card-status-text"), name).toHaveText(text);
        await expect(cardIn(isPaused ? pausedSection(page) : progress(page), name), name).toHaveCount(1);
        if (!isPaused) await expect(cardIn(pausedSection(page), name), name + " is live, so never in Paused").toHaveCount(0);
        await card.locator(".task-card").click();
        await expect(page.locator("#panel-title")).toHaveText(name);
        const header = await openTaskList(page);
        await expect(header.locator(".panel-status"), name + "'s panel").toHaveText(text);
        if (width === 390) await page.getByRole("button", { name: "Back to Board", exact: true }).click();
      }
      await expect(board(page).locator(".card-status-text").filter({ hasText: /^Paused/ })).toHaveCount(STATES.filter((state) => state.isPaused).length);
    });

    test("the goblin waiting on you reads what its Command Center list is called, and is listed there", async ({ page }) => {
      // Arrange
      await open(page);

      // Act
      await page.locator(".command-center-menu > summary").click();
      const waiting = page.getByRole("region", { name: "Waiting on you", exact: true });

      // Assert
      await expect(waiting.getByRole("heading", { level: 3 })).toHaveText(/^Waiting on you\s*1$/);
      await expect(waiting).toContainText("sign in to the package registry");
      await expect(cardIn(board(page), "Vera - Vault Keeper").locator(".card-status-text")).toHaveText("Waiting on you");
    });
  });
}
