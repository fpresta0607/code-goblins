import { expect, test, type Page } from "./site";

// A live goblin's hosted checks beside its pull request, on its card and in
// its panel. The supervisor is played by one held snapshot.
const REPO = "https://github.com/northwind/northwind-api";
const JOB = REPO + "/actions/runs/5/job/50";
const task = (id: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, project: "northwind-api", phase: "working", verified: false, generation: id + "-1", harness: "claude", since: new Date(Date.now() - 42 * 60_000).toISOString(), ...fields });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: [
    task("nw-sync", "Say why a billing sync fails", { pr: REPO + "/pull/55", ticket: { number: 52, url: REPO + "/issues/52", state: "pr open" }, hosted_checks: { head: "3d7072f8aa", state: "failed", checks: 9, failed: ["go (rest)", "frontend"], link: JOB, at: new Date().toISOString() } }),
    task("nw-export", "Export the audit trail", { pr: REPO + "/pull/56", hosted_checks: { head: "5f52309400", state: "passed", checks: 9, approved: true, at: new Date().toISOString() } }),
    task("nw-retry", "Retry the webhook", { pr: REPO + "/pull/57", hosted_checks: { head: "445bd85000", state: "pending", checks: 9, at: new Date().toISOString() } }),
    task("nw-docs", "Fix the docs links", { pr: REPO + "/pull/58" }),
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

const card = (page: Page, title: string) => page.locator(".task-card-shell").filter({ hasText: title });

// The parts of a card's pull request row, as boxes: none covers another and
// none leaves its card.
const crowded = (page: Page, title: string) => card(page, title).evaluate((shell) => {
  const frame = shell.getBoundingClientRect();
  const parts = [...shell.querySelectorAll(".card-pr, .card-checks, .card-ticket")].map((part) => part.getBoundingClientRect());
  const outside = parts.filter((box) => box.left < frame.left - 1 || box.right > frame.right + 1 || box.top < frame.top - 1 || box.bottom > frame.bottom + 1).length;
  let overlaps = 0;
  for (let i = 0; i < parts.length; i++) for (let j = i + 1; j < parts.length; j++) {
    const a = parts[i], b = parts[j];
    if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1) overlaps++;
  }
  return { parts: parts.length, outside, overlaps };
});

for (const viewport of [{ name: "in his window", width: 1707, height: 1067 }, { name: "on a phone", width: 390, height: 844 }]) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test("a card says where its pull request's checks stand and opens the failed check's page", async ({ page }) => {
      await open(page);

      const failed = card(page, "Say why a billing sync fails").locator("a.card-checks");
      await expect(failed).toHaveText("Checks failed");
      await expect(failed).toHaveAttribute("href", JOB);
      await expect(failed).toHaveAttribute("data-tip", "Failed: go (rest), frontend");
      await expect(failed).toHaveClass(/hosted-failed/);
      const passed = card(page, "Export the audit trail").locator(".card-checks");
      await expect(passed).toHaveText(["Checks passed", "Approved"]);
      await expect(passed.first()).toHaveAttribute("href", REPO + "/pull/56/checks");
      await expect(card(page, "Retry the webhook").locator("a.card-checks")).toHaveText("Checks running");
      await expect(card(page, "Fix the docs links").locator(".card-checks")).toHaveCount(0);
      expect(await crowded(page, "Say why a billing sync fails")).toEqual({ parts: 3, outside: 0, overlaps: 0 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    });
  });
}

test("the panel shows the checks beside its pull request", async ({ page }) => {
  await open(page);

  await card(page, "Export the audit trail").locator(".task-card").click();
  const taskView = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await taskView.count()) await taskView.click();
  const header = page.locator(".panel-header");

  await expect(header.locator(".hosted-checks")).toHaveText(["Checks passed", "Approved"]);
  await expect(header.locator("a.hosted-checks")).toHaveAttribute("href", REPO + "/pull/56/checks");
});
