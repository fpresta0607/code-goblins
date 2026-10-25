import { expect, holdStream, openTaskList, type Page, test } from "./site";

// The Overlord, 2026-10-08, of his board: "paused is not truly paused these
// are waiting", "merge train failed and landed of the same merge
// train...should not duplicate", "how is there complete with conflicts??"
// and "why when i click on completed merge trains is there no panel??".
// Goblins paused until their pull requests merge sit in Paused with every
// other paused goblin (the Overlord, 2026-10-16: "paused does it means youre
// on the bench to be resumed"), their chips saying where their pull requests
// stand; a batch shows as one train card listing what it landed and, with
// what happens next, what it could not merge; and a train's card opens its
// panel. The supervisor is played by one held snapshot, its trains
// as it folds them.
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
// The train of 2026-10-10 as it should have gone: one check failed by
// chance, its failed jobs ran again and passed, and the train landed.
const CHANCE = { name: "go (conpty)", link: REPO + "/actions/runs/38033453853/job/114158952237" };
const SECOND_TRY = { ...LANDED, earlier: [], cars: LANDED.cars.slice(0, 2), history: [{ ...run(1, [517, 519], "landed", 45), failed_once: [CHANCE] }] };
const SINGLE = { ...RETRYING, earlier: [], cars: RETRYING.cars.slice(0, 1), history: [run(1, [517], "", 5)] };
const STOPPED = { ...SINGLE, state: "stopped", finished: ago(2), note: "Stopped by the operator while the release was reviewed", cars: SINGLE.cars.map((each) => ({ ...each, state: "returned" })), history: [run(1, [517], "stopped", 5)] };
const ZERO_RUN = { ...STOPPED, pr: "", runs: 0, history: [], note: "Stopped before a run was built: <release review>" };
const HELD = {
  id: "code-goblins-20261013-005606", repository: "fpresta0607/code-goblins", base: "main", pr: REPO + "/pull/734", state: "stopped", runs: 1,
  started: "2026-10-13T00:56:06.48349Z", finished: "2026-10-13T01:08:32.2162188Z", note: "no pull request left to test merged cleanly onto main", earlier: [],
  cars: [{ ...car(729, "cg-key-fetch-build", "Ned", "Build Boss", "returned", "it was labelled hold"), title: "feat(auth): broker isolated browser logins", head: "840af3c98c5c294a25bb8eefef3c55320f084e0e" }],
  history: [{ number: 1, riders: [729], base: "9b791908d5ae24e6e417fb5ffc918a8f9f493fde", head: "06bcfc16fc5ff34e8b09ae482a55b71a729b0ec0", pushed: "2026-10-13T00:56:09.5027002Z", result: "changed", pr: REPO + "/pull/734" }],
};
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
// On a phone an open panel covers the board, and Back returns to it.
async function backToBoard(page: Page) {
  const back = page.getByRole("button", { name: "Back to Board", exact: true });
  if (await back.count()) await back.click();
}
// Opening a train hands the keyboard to its panel on the next frame (App's
// select), so a test that types into the panel waits for that handoff first:
// one that lands between its focus and its key takes the key from under it.
const panelTakesKeyboard = (page: Page) => expect(page.getByRole("complementary", { name: "Task review" })).toBeFocused();
// Every colour drawn inside an element, its own and its children's.
const colours = (part: ReturnType<Page["locator"]>) => part.evaluate((element) => [element, ...element.querySelectorAll("*")].flatMap((each) => { const style = getComputedStyle(each); return [style.color, style.backgroundColor]; }));

for (const viewport of [{ name: "in his window", width: 1707, height: 1067 }, { name: "on a phone", width: 390, height: 844 }]) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test("one pull request and one run share a primary row with the current run link", async ({ page }) => {
      await open(page, [SINGLE]);

      await column(page, "In progress").locator(".train-card .train-title").click();
      await panelTakesKeyboard(page);

      const rows = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
      await expect(rows).toHaveText([/#517.*Rosie - Inline Editor.*Testing/]);
      await expect(rows.getByRole("link", { name: /#517.*Rosie/ })).toHaveAttribute("href", REPO + "/pull/517");
      await expect(rows.getByRole("link", { name: "Testing", exact: true })).toHaveAttribute("href", TRAIN_PR);
      await rows.getByRole("link", { name: /#517.*Rosie/ }).focus();
      await page.keyboard.press("Tab");
      await expect(rows.getByRole("link", { name: "Testing", exact: true })).toBeFocused();
      await expect(panel(page).getByRole("heading", { name: /^Runs/ })).toHaveCount(0);
      await expect(panel(page).locator("details")).toHaveCount(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: test.info().outputPath("single-pr.png"), fullPage: true });
    });

    test("a retry keeps the latest state in the primary row and folds its failed attempt", async ({ page }) => {
      const retry = { ...SINGLE, runs: 2, history: [{ ...run(1, [517], "failed", 10), link: CHANCE.link }, run(2, [517], "", 5)] };
      await open(page, [retry]);

      await column(page, "In progress").locator(".train-card .train-title").click();
      await panelTakesKeyboard(page);

      const primary = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
      await expect(primary.getByRole("link", { name: "Testing", exact: true })).toHaveAttribute("href", TRAIN_PR);
      const history = panel(page).locator("details");
      await expect(history).not.toHaveAttribute("open", "");
      await expect(panel(page).locator(".train-panel-run")).toHaveCount(0);
      await history.locator("summary").focus();
      await page.keyboard.press("Enter");
      const attempts = history.locator(".train-panel-run");
      await expect(attempts).toHaveText([/Run 1.*#517.*Failed/, /Run 2.*#517.*Testing/]);
      await expect(attempts.first().getByRole("link", { name: "Open run 1" })).toHaveAttribute("href", CHANCE.link);
      await expect(attempts.last().getByRole("link", { name: "Open run 2" })).toHaveAttribute("href", TRAIN_PR);
    });

    test("multiple pull requests keep their own states and the actual rider groups in history", async ({ page }) => {
      const split = { ...RETRYING, earlier: [], runs: 2, cars: [RETRYING.cars[0], { ...RETRYING.cars[1], state: "waiting" }, LANDED.cars[4]], history: [{ ...run(1, [517, 519, 523], "failed", 10), link: CHANCE.link }, run(2, [517], "", 5)] };
      await open(page, [split]);

      await column(page, "In progress").locator(".train-card .train-title").click();

      const rows = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
      await expect(rows).toHaveText([/#517.*Rosie.*Testing/, /#519.*Wes.*Waits its turn/, /#523.*Abe.*Merging main to fix a conflict/]);
      await expect(rows.nth(1).locator("a.train-panel-state")).toHaveCount(0);
      await expect(rows.last().locator("a.train-panel-state")).toHaveCount(0);
      await expect(panel(page).locator(".train-panel-run")).toHaveCount(0);
      await panel(page).locator("details > summary").click();
      const attempts = panel(page).locator(".train-panel-run");
      await expect(attempts.first().locator(".train-panel-rider")).toHaveText(["#517", "#519", "#523"]);
      await expect(attempts.last().locator(".train-panel-rider")).toHaveText(["#517"]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      expect(await panel(page).locator(".panel-content").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    });

    test("a riding pull request keeps the failed run link while its next build is pending", async ({ page }) => {
      const rebuilding = { ...RETRYING, earlier: [], cars: [RETRYING.cars[0], { ...RETRYING.cars[1], state: "waiting" }], history: [{ ...run(1, [517, 519], "failed", 5), link: CHANCE.link }] };
      await open(page, [rebuilding]);

      await column(page, "In progress").locator(".train-card .train-title").click();

      const rows = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
      await expect(rows.first().getByRole("link", { name: "Failed", exact: true })).toHaveAttribute("href", CHANCE.link);
      await expect(rows.last().locator(".train-panel-state")).toHaveText("Waits its turn");
      await expect(rows.last().locator("a.train-panel-state")).toHaveCount(0);
    });

    test("one failed attempt keeps its failure and failed-once links in history", async ({ page }) => {
      const failed = { ...SINGLE, state: "stopped", finished: ago(2), cars: SINGLE.cars.map((each) => ({ ...each, state: "culprit" })), history: [{ ...run(1, [517], "failed", 5), link: CHANCE.link, failed_once: [CHANCE] }] };
      await open(page, [failed]);

      const history = board(page).getByRole("region", { name: "Train history", exact: true });
      await history.locator("summary").focus();
      await page.keyboard.press("Enter");
      await history.getByRole("button", { name: /Open merge train/ }).focus();
      await page.keyboard.press("Enter");

      await panelTakesKeyboard(page);
      await expect(panel(page).getByRole("region", { name: "Pull requests" }).getByRole("link", { name: "Failed twice" })).toHaveAttribute("href", CHANCE.link);
      await panel(page).locator("details > summary").focus();
      await page.keyboard.press("Space");
      await expect(panel(page).getByRole("link", { name: "Open run 1" })).toHaveAttribute("href", CHANCE.link);
      await expect(panel(page).getByRole("link", { name: CHANCE.name, exact: true })).toHaveAttribute("href", CHANCE.link);
    });

    for (const [name, attempt, status] of [
      ["stopped", STOPPED, "Stopped"],
      ["failed", FAILED, "Train failed, main kept moving"],
      ["zero-run", ZERO_RUN, "Stopped"],
    ] as const) {
      test(`a ${name} zero-merge train stays out of Completed and opens from history by keyboard`, async ({ page }) => {
        await open(page, [attempt]);

        await expect(column(page, "Completed").locator(".train-card")).toHaveCount(0);
        await expect(column(page, "Completed")).toContainText("Nothing finished yet");
        const history = board(page).getByRole("region", { name: "Train history", exact: true });
        await expect(history.locator("details")).not.toHaveAttribute("open", "");
        await expect(history.locator(".train-card")).toHaveCount(0);
        await history.locator("summary").focus();
        await page.keyboard.press("Enter");

        await expect(history.locator(".train-status")).toHaveText(status);
        await expect(history.locator(".train-car-state")).toHaveText(attempt.cars.map(() => "Rides the next train"));
        await expect(history).toContainText(attempt.note);
        await expect(column(page, "Completed").locator(".train-card")).toHaveCount(0);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await history.screenshot({ path: test.info().outputPath("board-history.png") });
        await history.getByRole("button", { name: /Open merge train/ }).focus();
        await page.keyboard.press("Enter");
        await expect(panel(page).locator(".panel-status")).toHaveText(status);
        await expect(panel(page)).toContainText(attempt.note);
        await expect(panel(page).locator(".train-panel-note")).toHaveText(attempt.note);
        await expect(panel(page).locator(".train-panel-note > *")).toHaveCount(0);
        if (attempt.runs === 0) {
          await expect(panel(page).getByRole("link", { name: "Open the train's pull request" })).toHaveCount(0);
          await expect(panel(page).locator(".train-panel-run")).toHaveCount(0);
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.screenshot({ path: test.info().outputPath("stopped-detail.png"), fullPage: true });
      });
    }

    for (const state of ["stopped", "failed"]) {
      test(`a partially landed ${state} train stays in Completed without blaming the landed pull request`, async ({ page }) => {
        const partial = { ...RETRYING, state, finished: ago(2), earlier: [], note: "Stopped after the first half landed", cars: [{ ...RETRYING.cars[0], state: "landed" }, { ...RETRYING.cars[1], state: "returned" }], history: [{ ...run(1, [517, 519], "failed", 5), link: CHANCE.link }] };
        await open(page, [partial]);

        const completed = column(page, "Completed").locator(".train-card");
        await expect(completed).toHaveCount(1);
        await expect(completed.locator(".train-status")).toHaveText("Landed");
        await expect(completed.locator(".train-car")).toHaveText([/#517.*Rosie - Inline Editor$/, /#519.*Wes - Train Conductor.*Rides the next train$/]);
        await expect(board(page).getByRole("region", { name: "Train history", exact: true })).toHaveCount(0);
        await completed.locator(".train-title").click();

        const rows = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
        await expect(rows.first().locator(".train-panel-state")).toHaveText("Landed");
        await expect(rows.first().locator(".train-panel-state")).toHaveClass(/train-passed/);
        await expect(rows.last().locator(".train-panel-state")).toHaveText("Rides the next train");
        await expect(rows.locator("a.train-panel-state")).toHaveCount(0);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      });
    }

    test("a train that broke CI names its culprit once, and its other riders say what happens next", async ({ page }) => {
      // Arrange
      const broken = { ...STOPPED, note: "#517 breaks CI on main", cars: [{ ...SINGLE.cars[0], state: "culprit" }, { ...RETRYING.cars[1], state: "returned" }], history: [run(1, [517, 519], "failed", 5)] };
      await open(page, [broken]);
      const history = board(page).getByRole("region", { name: "Train history", exact: true });

      // Act
      await history.locator("summary").click();

      // Assert
      await expect(history.locator(".train-status")).toHaveText("#517 breaks CI");
      await expect(history.locator(".train-car")).toHaveText([/#517.*Rosie - Inline Editor$/, /#519.*Wes - Train Conductor.*Rides the next train$/]);
    });

    test("a stopped zero-merge train keeps PR729's hold reason as plain text in its primary row", async ({ page }) => {
      await open(page, [HELD]);

      await expect(column(page, "Completed").locator(".train-card")).toHaveCount(0);
      await expect(column(page, "Completed")).toContainText("Nothing finished yet");
      const history = board(page).getByRole("region", { name: "Train history", exact: true });
      await history.locator("summary").focus();
      await page.keyboard.press("Enter");
      await history.getByRole("button", { name: /Open merge train/ }).focus();
      await page.keyboard.press("Enter");
      await panelTakesKeyboard(page);

      const primary = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
      await expect(primary).toContainText("it was labelled hold");
      await expect(primary.locator(".train-panel-car-note")).toHaveText("it was labelled hold");
      await expect(primary.getByRole("link")).toHaveAttribute("href", REPO + "/pull/729");
      await expect(primary.locator(".train-panel-state")).toHaveText("Rides the next train");
      await expect(panel(page)).toContainText(HELD.note);
      await panel(page).locator("details > summary").focus();
      await page.keyboard.press("Enter");
      await expect(panel(page).locator(".train-panel-run")).toHaveText(/Run 1.*#729.*A pull request changed/);
      await expect(panel(page).getByRole("link", { name: "Open run 1" })).toHaveAttribute("href", HELD.pr);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      expect(await panel(page).locator(".panel-content").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.screenshot({ path: test.info().outputPath("held-pr729.png"), fullPage: true });
    });

    test("an active train that already landed a half stays in progress", async ({ page }) => {
      const partial = { ...RETRYING, earlier: [], runs: 2, cars: [{ ...RETRYING.cars[0], state: "landed" }, RETRYING.cars[1]], history: [{ ...run(1, [517], "landed", 10), pr: FIRST_PR }, run(2, [519], "", 5)] };
      await open(page, [partial]);

      await expect(column(page, "In progress").locator(".train-card")).toHaveCount(1);
      await expect(column(page, "Completed").locator(".train-card")).toHaveCount(0);
      await expect(board(page).getByRole("region", { name: "Train history", exact: true })).toHaveCount(0);
      await column(page, "In progress").locator(".train-card .train-title").click();

      const rows = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
      await expect(rows.first().getByRole("link", { name: "Landed", exact: true })).toHaveAttribute("href", FIRST_PR);
      await expect(rows.last().getByRole("link", { name: "Testing", exact: true })).toHaveAttribute("href", TRAIN_PR);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    });

    test("folded failed and zero-run attempts keep their reasons beside the runs they own", async ({ page }) => {
      const retried = { ...LANDED, earlier: [{ ...ZERO_RUN, id: "before-any-run" }, FAILED, HELD], cars: [...LANDED.cars, { ...HELD.cars[0], state: "landed", note: "" }], history: [run(1, [517, 519, 520, 524, 729], "landed", 1)] };
      await open(page, [retried]);

      await expect(column(page, "Completed").locator(".train-card")).toHaveCount(1);
      await expect(board(page).getByRole("region", { name: "Train history", exact: true })).toHaveCount(0);
      await column(page, "Completed").locator(".train-card .train-title").click();
      await panelTakesKeyboard(page);
      await panel(page).locator("details > summary").focus();
      await page.keyboard.press("Enter");

      const attempts = panel(page).locator(".train-panel-attempt");
      await expect(attempts).toHaveCount(4);
      await expect(attempts.nth(0)).toContainText(ZERO_RUN.note);
      await expect(attempts.nth(0).locator(".train-panel-run")).toHaveCount(0);
      await expect(attempts.nth(0)).toContainText("No CI run");
      await expect(attempts.nth(1)).toContainText(FAILED.note);
      await expect(attempts.nth(1).locator(".train-panel-run")).toHaveText([/Run 1.*Main moved/, /Run 2.*Main moved/, /Run 3.*Main moved/]);
      await expect(attempts.nth(2)).toContainText("#729: it was labelled hold");
      await expect(attempts.nth(2).locator(".train-panel-run")).toHaveText([/Run 4.*#729.*A pull request changed/]);
      await expect(attempts.nth(3).locator(".train-panel-run")).toHaveText([/Run 5.*Landed/]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      expect(await panel(page).locator(".panel-content").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.screenshot({ path: test.info().outputPath("folded-history.png"), fullPage: true });
    });

    test("goblins paused until their pull requests merge sit in Paused with their pause, and their panels say their pull requests landed", async ({ page }) => {
      await open(page);

      for (const [name, number] of [["Rosie - Inline Editor", 517], ["Wes - Train Conductor", 519], ["Dolly - Rank Keeper", 520], ["Pearl - Screen Model", 524]] as const) {
        await backToBoard(page);
        await expect(pausedSection(page).locator(".task-card-shell").filter({ hasText: name })).toHaveCount(1);
        await expect(card(page, name).locator(".card-status-text")).toHaveText(`Paused until PR #${number} merges`);
        await expect(card(page, name).locator(".hosted-checks")).toHaveCount(0);
        await card(page, name).locator(".task-card").click();
        const header = await openTaskList(page);
        await expect(header.locator(".hosted-checks")).toHaveAttribute("aria-label", /^Landed: /);
      }
      await backToBoard(page);
      await expect(pausedSection(page).locator(".card-title")).toHaveText(["Otis - Ledger Clerk", "Rosie - Inline Editor", "Wes - Train Conductor", "Dolly - Rank Keeper", "Pearl - Screen Model"]);
      await expect(card(page, "Otis - Ledger Clerk").locator(".card-status-text")).toHaveText("Paused for memory");
    });

    test("one card for the batch lists what it landed and what it could not merge, and no card for the train it took on", async ({ page }) => {
      await open(page);

      const trains = column(page, "Completed").locator(".train-card");
      await expect(trains).toHaveCount(1);
      await expect(trains.locator(".train-status")).toHaveText("Landed on run 4");
      await expect(trains.locator(".train-status")).toHaveClass(/train-passed/);
      // The train's status says Landed, so its landed rows do not say it again.
      await expect(trains.locator(".train-car")).toHaveText([/#517.*Rosie - Inline Editor$/, /#519.*Wes - Train Conductor$/, /#520.*Dolly - Rank Keeper$/, /#524.*Pearl - Screen Model$/, /#523.*Abe - Screen Painter.*Merging main to fix a conflict$/]);
      await expect(trains).not.toContainText(/Train failed|Next train|Conflicts/);
      await expect(trains.locator("p")).toHaveCount(0);
      await expect(column(page, "Completed")).not.toContainText("Nothing finished yet");
      expect(await colours(trains)).not.toContain(AMBER);
    });

    test("a pull request the train could not merge shows in its goblin's panel with what happens next, never in yellow", async ({ page }) => {
      await open(page);

      const abe = column(page, "In progress").locator(".task-card-shell").filter({ hasText: "Abe - Screen Painter" });
      await expect(abe.locator(".hosted-checks")).toHaveCount(0);
      await abe.locator(".task-card").click();
      const header = await openTaskList(page);
      const chip = header.locator("a.hosted-checks");
      await expect(chip).toHaveAttribute("aria-label", /^Merging main to fix a conflict: /);
      await expect(chip).toHaveAttribute("href", TRAIN_PR);
      await backToBoard(page);
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
      await expect(panel(page).locator(".panel-status")).toHaveText("Landed on run 4");
      await expect(panel(page).getByRole("link", { name: "Open the train's pull request" })).toHaveAttribute("href", TRAIN_PR);
      await expect(panel(page).getByRole("group", { name: "Panel view" })).toHaveCount(0);
      const pulls = panel(page).getByRole("region", { name: "Pull requests" }).getByRole("listitem");
      await expect(pulls).toHaveText([/#517.*Rosie - Inline Editor.*Landed/, /#519.*Wes - Train Conductor.*Landed/, /#520.*Dolly - Rank Keeper.*Landed/, /#524.*Pearl - Screen Model.*Landed/, /#523.*Abe - Screen Painter.*Merging main to fix a conflict/]);
      expect(await pulls.locator(".train-panel-pull").evaluateAll((links) => links.map((link) => link.getAttribute("href")))).toEqual([517, 519, 520, 524, 523].map((number) => REPO + "/pull/" + number));
      await panel(page).locator("details > summary").click();
      const runs = panel(page).locator(".train-panel-run");
      await expect(runs).toHaveText([/Run 1.*#517.*#519.*Main moved/, /Run 2.*#517.*#519.*Main moved/, /Run 3.*#517.*#519.*Main moved/, /Run 4.*#517.*#519.*#520.*#524.*Landed/]);
      expect(await runs.locator(".train-panel-open").evaluateAll((links) => links.map((link) => link.getAttribute("href")))).toEqual([FIRST_PR, FIRST_PR, FIRST_PR, TRAIN_PR]);
      await expect(runs.last().locator("a.train-panel-rider").first()).toHaveAttribute("href", REPO + "/pull/517");
      await expect(panel(page).locator(".train-panel-clock")).toBeVisible();
      await expect(panel(page).locator(".panel-activity, .panel-details-row")).toHaveCount(0);
      expect(await colours(panel(page))).not.toContain(AMBER);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await backToBoard(page);
      await expect(trainCard).toHaveClass(/selected/);
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
      await panel(page).locator("details > summary").click();
      await expect(panel(page).locator(".train-panel-run")).toHaveText([/Run 1.*Main moved/, /Run 2.*Main moved/, /Run 3.*Main moved/, /Run 4.*#517.*#519.*Testing/]);
      await backToBoard(page);
      await expect(column(page, "Completed")).toBeVisible();
      await expect(column(page, "Completed").locator(".train-card")).toHaveCount(0);
    });

    test("a run that landed on its second try says so in the train's panel, the check that failed once one tap away", async ({ page }) => {
      // Arrange
      await open(page, [SECOND_TRY]);

      // Act
      await column(page, "Completed").locator(".train-card .train-title").click();

      // Assert
      await expect(panel(page).locator(".panel-status")).toHaveText("Landed");
      await panel(page).locator("details > summary").click();
      const runs = panel(page).locator(".train-panel-run");
      await expect(runs).toHaveText([/Run 1.*#517.*#519.*Landed on the second try.*Failed once.*go \(conpty\)/]);
      await expect(runs.locator(".train-panel-state")).toHaveClass(/train-passed/);
      await expect(runs.locator("a.train-panel-check")).toHaveAttribute("href", CHANCE.link);
      const chip = await runs.locator("a.train-panel-check").boundingBox(), row = await runs.boundingBox();
      expect(chip && row && chip.x >= row.x && chip.x + chip.width <= row.x + row.width && chip.height >= 32).toBe(true);
      expect(await colours(panel(page))).not.toContain(AMBER);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
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
