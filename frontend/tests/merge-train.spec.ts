import { expect, test, type Page } from "./site";

// A merge train shows as one card with its pull requests: a running one at
// the top of In progress, a finished one at the top of Completed. The
// supervisor is played by one held snapshot.
const REPO = "https://github.com/northwind/northwind-api";
const car = (number: number, state: string, title: string, note = "", goblin = "", goblin_title = "") => ({ number, url: REPO + "/pull/" + number, title, task: "nw-" + number, state, note, goblin, goblin_title });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: [
    { id: "nw-sync", title: "Say why a billing sync fails", project: "northwind-api", phase: "working", verified: false, generation: "nw-sync-1", harness: "claude", since: new Date(Date.now() - 42 * 60_000).toISOString() },
  ],
  merge_trains: [
    {
      id: "northwind-api-20261007-160000", repository: "northwind/northwind-api", base: "main", pr: REPO + "/pull/900", state: "testing", runs: 2,
      started: new Date(Date.now() - 20 * 60_000).toISOString(), note: "CI failed on #55, #56, #57, #58 (test), so its first half rides alone next: #55, #56",
      cars: [car(55, "riding", "Retry the webhook"), car(56, "riding", "Export the audit trail with every field the auditors asked for in their last review"), car(57, "waiting", "Fix the docs links"), car(58, "waiting", "Rename the sync job")],
    },
    {
      id: "northwind-api-20261007-120000", repository: "northwind/northwind-api", base: "main", pr: REPO + "/pull/899", state: "stopped", runs: 3,
      started: new Date(Date.now() - 5 * 3600_000).toISOString(), finished: new Date(Date.now() - 4 * 3600_000).toISOString(), note: "#53 breaks CI on main: test (" + REPO + "/actions/runs/7)",
      cars: [car(51, "landed", "Ship the export", "", "Ingrid", "Clock Icon Restorer of the Minimal Cards"), car(52, "landed", "Cache the rates"), car(53, "culprit", "Speed up the importer", "failed: test (" + REPO + "/actions/runs/7)"), car(54, "returned", "Tidy the logs")],
    },
  ],
};

async function open(page: Page) {
  await page.addInitScript((data) => {
    class HeldStream extends EventTarget {
      onerror: unknown = null;
      constructor() { super(); setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data }))); }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: HeldStream });
  }, JSON.stringify(SNAPSHOT));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const column = (page: Page, name: string) => page.locator(".board-column").filter({ has: page.getByRole("heading", { name: new RegExp("^" + name) }) });

for (const viewport of [{ name: "in his window", width: 1707, height: 1067 }, { name: "on a phone", width: 390, height: 844 }]) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test("a running train heads In progress as one card with its pull requests", async ({ page }) => {
      await open(page);

      const running = column(page, "In progress").locator(".train-card");
      await expect(running).toHaveCount(1);
      await expect(running).toContainText("northwind/northwind-api into main");
      const status = running.locator("a.train-status");
      await expect(status).toHaveText("CI tests #55, #56, run 2");
      await expect(status).toHaveAttribute("href", REPO + "/pull/900");
      await expect(status).toHaveClass(/train-pending/);
      // What happened last is in the status's tip, never a line on the card.
      await expect(status).toHaveAttribute("data-tip", /first half rides alone next: #55, #56$/);
      await expect(running.locator("p")).toHaveCount(0);
      // A rider says where it stands only when that differs from the train.
      await expect(running.locator(".train-car")).toHaveText([/#55.*Retry the webhook$/, /#56.*Export the audit trail.*review$/, /#57.*Fix the docs links.*Waits its turn/, /#58.*Rename the sync job.*Waits its turn/]);
      await expect(running.locator(".train-car").first().locator("a")).toHaveAttribute("href", REPO + "/pull/55");
      // The train's card comes before the goblins' cards.
      const first = await column(page, "In progress").locator(".train-card, .task-card-shell").first().getAttribute("class");
      expect(first).toContain("train-card");
    });

    test("a finished train heads Completed and lists what it landed and what it could not merge", async ({ page }) => {
      await open(page);

      const finished = column(page, "Completed").locator(".train-card");
      await expect(finished).toHaveCount(1);
      await expect(finished.locator(".train-status")).toHaveText("Landed on run 3");
      await expect(finished.locator(".train-status")).toHaveClass(/train-passed/);
      // The train's status says Landed, so its landed riders do not say it
      // again; one it could not merge says what happened to it.
      await expect(finished.locator(".train-car")).toHaveText(["#51Ingrid - Clock Icon Restorer of the Minimal Cards", "#52Cache the rates", "#53Speed up the importerBreaks CI", "#54Tidy the logsRides the next train"]);
      await expect(finished.locator(".train-car-state")).toHaveText(["Breaks CI", "Rides the next train"]);
      await expect(finished.locator(".train-car").nth(2)).toHaveClass(/train-failed/);
      await expect(column(page, "In progress").locator(".train-card")).toHaveCount(1);
    });

    test("a train's card stays inside its column and a long title is cut, not wrapped over its state", async ({ page }) => {
      await open(page);

      const card = column(page, "In progress").locator(".train-card");
      const fits = await card.evaluate((element) => {
        const frame = element.getBoundingClientRect();
        const rows = [...element.querySelectorAll(".train-car")].map((row) => row.getBoundingClientRect());
        const states = [...element.querySelectorAll(".train-car-state")].map((state) => state.getBoundingClientRect());
        return {
          inside: [...element.children].every((child) => { const box = child.getBoundingClientRect(); return box.left >= frame.left - 1 && box.right <= frame.right + 1; }),
          oneLine: rows.every((row) => row.height < 40),
          statesInside: states.every((state) => state.right <= frame.right + 1),
        };
      });
      expect(fits).toEqual({ inside: true, oneLine: true, statesInside: true });
    });

    // The Overlord, 2026-10-17: "don't need to have landed in #1073 lines in
    // completed, instead allow goblin name and title to flex fit that row".
    test("a landed rider's goblin name and title take the row and wrap whole", async ({ page }, testInfo) => {
      // Arrange
      await open(page);
      const finished = column(page, "Completed").locator(".train-card");
      const title = finished.locator(".train-car").first().locator(".train-car-title");

      // Act
      await finished.screenshot({ path: testInfo.outputPath("landed-train.png") });
      const fit = await title.evaluate((element) => {
        const row = element.closest(".train-car")!.getBoundingClientRect(), goblin = element.closest(".train-car-goblin")!.getBoundingClientRect();
        return { isWhole: element.scrollHeight <= element.clientHeight && element.scrollWidth <= element.clientWidth, reachesTheEnd: row.right - goblin.right < 12 };
      });

      // Assert
      await expect(title).toHaveText("Ingrid - Clock Icon Restorer of the Minimal Cards");
      expect(fit).toEqual({ isWhole: true, reachesTheEnd: true });
    });
  });
}
