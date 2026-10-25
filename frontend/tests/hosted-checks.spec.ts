import { expect, openTaskList, type Page, test } from "./site";

// A live goblin's hosted checks and its change's newest cfo gate test run
// beside its pull request in its panel, and never on its card (the Overlord,
// 2026-10-17: "we don't need to have the test checks as an icon. That can be
// in the task panel"). The supervisor is played by one held snapshot.
const REPO = "https://github.com/northwind/northwind-api";
const JOB = REPO + "/actions/runs/5/job/50";
const task = (id: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, project: "northwind-api", phase: "working", verified: false, generation: id + "-1", harness: "claude", since: new Date(Date.now() - 42 * 60_000).toISOString(), ...fields });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: [
    task("nw-sync", "Say why a billing sync fails", { pr: REPO + "/pull/55", ticket: { number: 52, url: REPO + "/issues/52", state: "pr open" }, hosted_checks: { head: "3d7072f8aa", state: "failed", checks: 9, failed: ["go (rest)", "frontend"], link: JOB, at: new Date().toISOString() }, local_checks: { commit: "3d7072f8aa", level: "affected", required_level: "affected", status: "failed", duration_seconds: 1080, queue_seconds: 300, failed: ["internal/supervisor"], at: new Date().toISOString() } }),
    task("nw-export", "Export the audit trail", { pr: REPO + "/pull/56", hosted_checks: { head: "5f52309400", state: "passed", checks: 9, approved: true, at: new Date().toISOString() }, local_checks: { commit: "5f52309400", level: "affected", required_level: "affected", status: "passed", duration_seconds: 600, failed: [], at: new Date().toISOString() } }),
    task("nw-retry", "Retry the webhook", { pr: REPO + "/pull/57", hosted_checks: { head: "445bd85000", state: "pending", checks: 9, at: new Date().toISOString() } }),
    task("nw-docs", "Fix the docs links", { pr: REPO + "/pull/58", deployment: { commit: "aaa111bbb222", state: "deploying", workflows: ["Deploy"], link: REPO + "/actions/runs/81", at: new Date().toISOString() } }),
    task("finished:nw-ship", "Ship the export", { phase: "done", verified: true, archived: true, merged: true, generation: "", harness: "", pr: REPO + "/pull/59", at: new Date().toISOString(), deployment: { commit: "ccc333ddd444", state: "failed", workflows: ["Deploy API", "Deploy worker"], link: REPO + "/actions/runs/82", at: new Date().toISOString() } }),
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
async function openPanel(page: Page, title: string) {
  await card(page, title).locator(".task-card").click();
  return openTaskList(page);
}

// The parts of a card's pull request row, as boxes: none covers another and
// none leaves its card.
const crowded = (page: Page, title: string) => card(page, title).evaluate((shell) => {
  const frame = shell.getBoundingClientRect();
  const parts = [...shell.querySelectorAll(".card-pr, .card-ticket, .hosted-checks, .local-checks, .deployment")].map((part) => part.getBoundingClientRect());
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

    test("a card shows no checks, and its panel says where its pull request's checks stand and opens the failed check's page", async ({ page }) => {
      await open(page);

      await expect(page.locator(".task-card-shell").locator(".hosted-checks, .local-checks, .deployment")).toHaveCount(0);
      expect(await crowded(page, "Say why a billing sync fails")).toEqual({ parts: 2, outside: 0, overlaps: 0 });
      const header = await openPanel(page, "Say why a billing sync fails");
      const failed = header.locator("a.hosted-checks");
      await expect(failed).toHaveAttribute("aria-label", /^Checks failed: /);
      await expect(failed).toHaveAttribute("href", JOB);
      await expect(failed).toHaveAttribute("data-tip", "Failed: go (rest), frontend");
      await expect(failed).toHaveClass(/hosted-failed/);
      const local = header.locator("a.local-checks");
      await expect(local).toHaveAttribute("aria-label", /^Local tests failed: /);
      await expect(local).toHaveAttribute("href", "/api/tasks/nw-sync/checks");
      await expect(local).toHaveAttribute("data-tip", "Failed: internal/supervisor. Affected level, 18m, 5m of it waiting for its turn");
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    });
  });
}

// A merged task's deploy shows beside its pull request in its panel, in the
// checks' look and apart from them: a finished task's in Completed, and a
// live one's.
test("a merged task's panel says how its deploy stands and opens the run", async ({ page }) => {
  await open(page);

  const finished = await openPanel(page, "Ship the export");
  const failed = finished.locator("a.deployment");
  await expect(failed).toHaveAttribute("aria-label", /^Deploy failed: /);
  await expect(failed).toHaveAttribute("href", REPO + "/actions/runs/82");
  await expect(failed).toHaveAttribute("data-tip", "Deploy API, Deploy worker at ccc333d");
  await expect(failed).toHaveClass(/hosted-failed/);
  const live = await openPanel(page, "Fix the docs links");
  await expect(live.locator("a.deployment")).toHaveAttribute("aria-label", /^Deploying: /);
  const unshipped = await openPanel(page, "Say why a billing sync fails");
  await expect(unshipped.locator(".deployment")).toHaveCount(0);
});

test("the panel shows the checks beside its pull request", async ({ page }) => {
  await open(page);

  await card(page, "Export the audit trail").locator(".task-card").click();
  const header = await openTaskList(page);

  await expect(header.locator(".hosted-checks")).toHaveText(["Checks passed", "Approved"]);
  await expect(header.locator("a.local-checks")).toHaveText("Local tests passed");
  await expect(header.locator("a.hosted-checks")).toHaveAttribute("href", REPO + "/pull/56/checks");
});
