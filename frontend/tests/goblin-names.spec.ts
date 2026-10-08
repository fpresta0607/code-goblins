import { expect, holdStream, test, type Page } from "./site";

// Every goblin goes by its fun name and title, with its avatar, on its card,
// on the merge train, on the Orchestration canvas and at the head of its
// panel, and its task sits in the tip its card or canvas card shows after the
// pointer rests on it and under its name in the panel. The supervisor is
// played by one held snapshot.
const REPO = "https://github.com/northwind/northwind-api";
const since = new Date(Date.now() - 42 * 60_000).toISOString();
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: [
    { id: "nw-sync", title: "Say why a billing sync fails", goblin_name: "Jerry", goblin_title: "Code Designer", project: "northwind-api", phase: "working", verified: false, generation: "nw-sync-1", harness: "claude", since },
    { id: "nw-export", title: "Export the audit trail", goblin_name: "Mabel", goblin_title: "Bug Hunter", project: "northwind-api", phase: "working", verified: false, generation: "nw-export-1", harness: "codex", pr: REPO + "/pull/55", since },
  ],
  merge_trains: [{
    id: "northwind-api-20261008-020000", repository: "northwind/northwind-api", base: "main", pr: REPO + "/pull/900", state: "testing", runs: 1,
    started: new Date(Date.now() - 10 * 60_000).toISOString(), note: "",
    cars: [
      { number: 55, url: REPO + "/pull/55", title: "feat: export the audit trail", task: "nw-export", goblin: "Mabel", goblin_title: "Bug Hunter", state: "riding", note: "" },
      { number: 56, url: REPO + "/pull/56", title: "fix: retry the webhook", task: "nw-webhook", goblin: "Otis", goblin_title: "Train Conductor", state: "riding", note: "" },
    ],
  }],
};

async function open(page: Page) {
  await holdStream(page, SNAPSHOT);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

// Jerry's card, the live one with no pull request, found without its name.
const jerry = (page: Page) => page.locator(".task-card-shell").filter({ hasNot: page.locator(".card-pr") }).filter({ hasText: "northwind-api" });

for (const viewport of [{ name: "in his window", width: 1707, height: 1067 }, { name: "on a phone", width: 390, height: 844 }]) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test("a goblin's card names it with its avatar and shows its task in its tip", async ({ page }) => {
      await open(page);
      await expect(jerry(page).locator(".card-title")).toHaveText("Jerry - Code Designer");
      await expect(jerry(page).locator(".goblin-avatar")).toBeVisible();
      await jerry(page).locator(".card-title").hover();
      await expect(page.getByRole("tooltip")).toHaveText("Say why a billing sync fails");
    });

    test("the merge train names each pull request's goblin with its avatar, and its task in the tip", async ({ page }) => {
      await open(page);
      const cars = page.locator(".train-car");
      await expect(cars.nth(0)).toContainText("Mabel - Bug Hunter");
      await expect(cars.nth(1)).toContainText("Otis - Train Conductor");
      await expect(cars.nth(0).locator(".goblin-avatar")).toBeVisible();
      await expect(cars.nth(1).locator(".goblin-avatar")).toBeVisible();
      await cars.nth(0).locator(".train-car-goblin").hover();
      await expect(page.getByRole("tooltip")).toHaveText("Export the audit trail");
      await cars.nth(1).locator(".train-car-goblin").hover();
      await expect(page.getByRole("tooltip")).toHaveText("fix: retry the webhook");
    });

    test("a goblin's panel opens on its name and title, then its task", async ({ page }) => {
      await open(page);
      await jerry(page).locator(".card-title").click();
      await expect(page.locator("#panel-title")).toHaveText("Jerry - Code Designer");
      await expect(page.locator(".panel-goblin-task")).toHaveText("Say why a billing sync fails");
    });

    test("the canvas names each goblin with its avatar, and its task in its tip or under its name", async ({ page }) => {
      await open(page);
      await page.getByRole("button", { name: "Orchestration", exact: true }).click();
      if (viewport.width > 640) {
        const node = page.locator(".flow-node").filter({ hasText: "Jerry - Code Designer" });
        await expect(node).toHaveCount(1);
        await expect(node.locator(".goblin-avatar")).toBeVisible();
        await node.locator("strong").hover();
        await expect(page.getByRole("tooltip")).toHaveText("Say why a billing sync fails");
      } else {
        // A phone shows the canvas as its list, which says the task under
        // the name.
        const row = page.locator(".workflow-node").filter({ hasText: "Jerry - Code Designer" });
        await expect(row).toHaveCount(1);
        await expect(row.locator(".goblin-avatar")).toBeVisible();
        await expect(row.locator(".node-task")).toHaveText("Task: Say why a billing sync fails");
      }
    });
  });
}
