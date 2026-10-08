import { expect, holdStream, test, type Page } from "./site";

// The Overlord, 2026-10-08, of his board: "paused is not truly paused these
// are waiting", "merge train failed and landed of the same merge
// train...should not duplicate", "how is there complete with conflicts??"
// and "why when i click on completed merge trains is there no panel??".
// Goblins waiting on their pull requests stay in In progress and Paused holds
// only goblins stopped for memory; a batch shows as one train card listing
// only what it landed; a pull request it could not merge shows on its
// goblin's card; and a train's card opens its panel. The supervisor is played
// by one held snapshot, its trains as it folds them.
const REPO = "https://github.com/fpresta0607/code-goblins";
const FIRST_PR = REPO + "/pull/522";
const TRAIN_PR = REPO + "/pull/526";
const AMBER = "rgb(243, 197, 120)";
const HEAD = "a".repeat(40);
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const goblin = (id: string, name: string, title: string, task: string, fields: Record<string, unknown> = {}) => ({
  id, title: task, goblin_name: name, goblin_title: title, project: "code-goblins", phase: "idle", report: "done", verified: false,
  generation: id + "-1", harness: "claude", model: "claude-opus-5-5", since: ago(95), ...fields,
});
const pause = (reason: string, until: string) => ({ phase: "paused", action: "pause", at: ago(20), kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason, until, at: ago(20) } });
const untilMerged = (id: string, name: string, title: string, task: string, number: number) => goblin(id, name, title, task, { phase: "paused", at: ago(20), pr: REPO + "/pull/" + number, lifecycle: pause("dependency", "pr:" + REPO + "/pull/" + number), hosted_checks: { head: HEAD, state: "passed", checks: 9 } });
const car = (number: number, task: string, name: string, title: string, state: string, note = "") => ({ number, url: REPO + "/pull/" + number, title, task, goblin: name, goblin_title: title, head: HEAD, state, note });
const run = (number: number, riders: number[], result: string, minutes: number) => ({ number, riders, base: "b".repeat(40), head: "c".repeat(40), pushed: ago(minutes), result });
const FAILED = {
  id: "code-goblins-20261008-190000", repository: "fpresta0607/code-goblins", base: "main", pr: FIRST_PR, state: "failed", runs: 3, started: ago(140), finished: ago(60),
  note: "main moved during 3 runs in a row, so nothing they tested could land: hold other merges to main, then start the train again",
  cars: [car(517, "cg-rosie", "Rosie", "Inline Editor", "returned"), car(519, "cg-wes", "Wes", "Train Conductor", "returned")],
  history: [run(1, [517, 519], "moved", 130), run(2, [517, 519], "moved", 100), run(3, [517, 519], "moved", 70)],
};
const LANDED = {
  id: "code-goblins-20261008-203000", repository: "fpresta0607/code-goblins", base: "main", pr: TRAIN_PR, state: "landed", runs: 1, started: ago(50), finished: ago(30), note: "",
  cars: [car(517, "cg-rosie", "Rosie", "Inline Editor", "landed"), car(519, "cg-wes", "Wes", "Train Conductor", "landed"), car(520, "cg-dolly", "Dolly", "Rank Keeper", "landed"), car(524, "cg-pearl", "Pearl", "Screen Model", "landed"), car(523, "cg-abe", "Abe", "Screen Painter", "conflict", "it conflicts in frontend/src/workflow.ts")],
  history: [run(1, [517, 519, 520, 524], "landed", 45)],
  earlier: [FAILED],
};
const RETRYING = { ...LANDED, state: "testing", finished: "", cars: LANDED.cars.slice(0, 2).map((each) => ({ ...each, state: "riding" })), history: [run(1, [517, 519], "", 5)] };
const TASKS = [
  untilMerged("cg-rosie", "Rosie", "Inline Editor", "Edit a task inline", 517),
  untilMerged("cg-wes", "Wes", "Train Conductor", "One testing state", 519),
  untilMerged("cg-dolly", "Dolly", "Rank Keeper", "Rank number stays", 520),
  untilMerged("cg-pearl", "Pearl", "Screen Model", "Host screen model", 524),
  goblin("cg-abe", "Abe", "Screen Painter", "Resume smoothly", { phase: "working", report: "working", pr: REPO + "/pull/523", hosted_checks: { head: HEAD, state: "passed", checks: 9 } }),
  goblin("cg-otis", "Otis", "Ledger Clerk", "Sync the ledger", { phase: "paused", report: "working", at: ago(10), lifecycle: pause("memory", "") }),
];
const snapshot = (trains: object[]) => ({
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }], tasks: TASKS, merge_trains: trains,
});

async function open(page: Page, trains: object[] = [LANDED]) {
  await page.clock.setFixedTime(now);
  await holdStream(page, snapshot(trains));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const board = (page: Page) => page.locator(".task-board");
const column = (page: Page, name: string) => board(page).getByRole("region", { name, exact: true });
const pausedSection = (page: Page) => column(page, "Paused");
const card = (page: Page, name: string) => board(page).locator(".task-card-shell").filter({ has: page.locator(".card-title").filter({ hasText: name }) });
const panel = (page: Page) => page.locator(".train-panel");
// Every colour drawn inside an element, its own and its children's.
const colours = (part: ReturnType<Page["locator"]>) => part.evaluate((element) => [element, ...element.querySelectorAll("*")].flatMap((each) => { const style = getComputedStyle(each); return [style.color, style.backgroundColor]; }));

for (const viewport of [{ name: "in his window", width: 1707, height: 1067 }, { name: "on a phone", width: 390, height: 844 }]) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test("goblins waiting on their pull requests stay in In progress and Paused holds only the goblin stopped for memory", async ({ page }) => {
      await open(page);

      for (const name of ["Rosie - Inline Editor", "Wes - Train Conductor", "Dolly - Rank Keeper", "Pearl - Screen Model"]) {
        await expect(column(page, "In progress").locator(".task-card-shell").filter({ hasText: name })).toHaveCount(1);
        await expect(pausedSection(page).locator(".task-card-shell").filter({ hasText: name })).toHaveCount(0);
        await expect(card(page, name).locator(".card-status-text")).toHaveText("Landed");
      }
      await expect(pausedSection(page).locator(".task-card-shell")).toHaveText([/Otis - Ledger Clerk.*Memory: resumes at 5 GB free/]);
    });

    test("one card for the batch lists only what it landed, and no card for the train it took on", async ({ page }) => {
      await open(page);

      const trains = column(page, "Completed").locator(".train-card");
      await expect(trains).toHaveCount(1);
      await expect(trains.locator(".train-status")).toHaveText("Landed on run 4");
      await expect(trains.locator(".train-status")).toHaveClass(/train-passed/);
      await expect(trains.locator(".train-car")).toHaveText([/#517.*Rosie - Inline Editor.*Landed/, /#519.*Wes - Train Conductor.*Landed/, /#520.*Dolly - Rank Keeper.*Landed/, /#524.*Pearl - Screen Model.*Landed/]);
      await expect(trains).not.toContainText(/#523|Train failed|Next train|Conflicts/);
      await expect(trains.locator("p")).toHaveCount(0);
      await expect(column(page, "Completed")).not.toContainText("Nothing finished yet");
      expect(await colours(trains)).not.toContain(AMBER);
    });

    test("a pull request the train could not merge shows on its goblin's card with what happens next, never in yellow", async ({ page }) => {
      await open(page);

      const abe = column(page, "In progress").locator(".task-card-shell").filter({ hasText: "Abe - Screen Painter" });
      const chip = abe.locator("a.card-checks");
      await expect(chip).toHaveText("Merging main to fix a conflict");
      await expect(chip).toHaveAttribute("href", TRAIN_PR);
      expect(await colours(abe)).not.toContain(AMBER);
    });

    test("a click on a finished train's card opens its panel: every pull request and every run, one tap each", async ({ page }) => {
      // Arrange
      await open(page);
      const trainCard = column(page, "Completed").locator(".train-card");

      // Act
      await trainCard.locator(".train-title").click();

      // Assert
      await expect(page.locator("#panel-title")).toHaveText("Merge train");
      await expect(trainCard).toHaveClass(/selected/);
      await expect(panel(page).locator(".panel-status")).toHaveText("Landed on run 4");
      await expect(panel(page).getByRole("link", { name: "Open the train's pull request" })).toHaveAttribute("href", TRAIN_PR);
      await expect(panel(page).getByRole("group", { name: "Panel view" })).toHaveCount(0);
      const pulls = panel(page).getByRole("region", { name: "Pull requests" }).locator("a.train-panel-row");
      await expect(pulls).toHaveText([/#517.*Rosie - Inline Editor.*Landed/, /#519.*Wes - Train Conductor.*Landed/, /#520.*Dolly - Rank Keeper.*Landed/, /#524.*Pearl - Screen Model.*Landed/, /#523.*Abe - Screen Painter.*Merging main to fix a conflict/]);
      expect(await pulls.evaluateAll((links) => links.map((link) => link.getAttribute("href")))).toEqual([517, 519, 520, 524, 523].map((number) => REPO + "/pull/" + number));
      const runs = panel(page).getByRole("region", { name: "Runs" }).locator(".train-panel-run");
      await expect(runs).toHaveText([/Run 1.*#517.*#519.*Main moved/, /Run 2.*#517.*#519.*Main moved/, /Run 3.*#517.*#519.*Main moved/, /Run 4.*#517.*#519.*#520.*#524.*Landed/]);
      expect(await runs.locator(".train-panel-open").evaluateAll((links) => links.map((link) => link.getAttribute("href")))).toEqual([FIRST_PR, FIRST_PR, FIRST_PR, TRAIN_PR]);
      await expect(runs.last().locator("a.train-panel-rider").first()).toHaveAttribute("href", REPO + "/pull/517");
      await expect(panel(page).locator(".train-panel-clock")).toBeVisible();
      await expect(panel(page).locator(".panel-activity, .panel-details-row")).toHaveCount(0);
      expect(await colours(panel(page))).not.toContain(AMBER);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    });

    test("a click on a running train's card opens its panel with the runs so far", async ({ page }) => {
      // Arrange: the train that retries what the failed one carried.
      await open(page, [RETRYING]);
      const trainCard = column(page, "In progress").locator(".train-card");
      await expect(trainCard.locator(".train-status")).toHaveText("CI tests #517, #519, run 4");

      // Act
      await trainCard.locator(".train-title").click();

      // Assert
      await expect(panel(page).locator(".panel-status")).toHaveText("CI tests #517, #519, run 4");
      await expect(panel(page).getByRole("region", { name: "Runs" }).locator(".train-panel-run")).toHaveText([/Run 1.*Main moved/, /Run 2.*Main moved/, /Run 3.*Main moved/, /Run 4.*#517.*#519.*Testing/]);
      await expect(card(page, "Rosie - Inline Editor").locator(".card-status-text")).toHaveText("Testing");
      await expect(column(page, "Completed").locator(".train-card")).toHaveCount(0);
    });
  });
}

test.describe("in his window", () => {
  test.use({ viewport: { width: 1707, height: 1067 } });

  test("Back from a train's panel returns to the CFO, and a goblin's card opens the goblin again", async ({ page }) => {
    await open(page);
    await column(page, "Completed").locator(".train-card .train-title").click();
    await expect(page.locator("#panel-title")).toHaveText("Merge train");

    await page.getByRole("button", { name: "Back to the CFO" }).click();
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await expect(column(page, "Completed").locator(".train-card")).not.toHaveClass(/selected/);

    await card(page, "Wes - Train Conductor").locator(".task-card").click();
    await expect(page.locator("#panel-title")).toHaveText("Wes - Train Conductor");
  });
});
