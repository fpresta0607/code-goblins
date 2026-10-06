import { expect, test, type Page } from "./site";

// Tickets and people on the board: a task's ticket and the avatars of
// teammates whose work meets its branch on its card, and the people of its
// project in its goblin's panel. The supervisor is played by one held
// snapshot, and GitHub's avatar host by a route that draws each avatar.
const REPO = "https://github.com/northwind/northwind-api";
const avatar = (id: number) => "https://avatars.githubusercontent.com/u/" + id;
const ana = { login: "ana-teammate", avatar_url: avatar(7) };
const ben = { login: "ben-teammate", avatar_url: avatar(8) };
const cy = { login: "cy-teammate", avatar_url: avatar(404) };
const task = (id: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, project: "northwind-api", phase: "working", verified: false, generation: id + "-1", harness: "claude", since: new Date(Date.now() - 42 * 60_000).toISOString(), ...fields });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  projects: [{ name: "northwind-api", repository: "northwind/northwind-api", contributors: [ben, ana, cy] }],
  tasks: [
    task("nw-sync", "Say why a billing sync fails", { pr: REPO + "/pull/55", ticket: { number: 52, url: REPO + "/issues/52", state: "pr open" }, overlaps: [{ ...ana, what: "PR #46", url: REPO + "/pull/46" }, { ...ana, what: "issue #49", url: REPO + "/issues/49" }] }),
    task("nw-export", "Export the audit trail", { phase: "queued", generation: "", harness: "", brief: true, ticket: { number: 54, url: REPO + "/issues/54", state: "queued" } }),
    task("solo-tidy", "Tidy the solo tool", { project: "solo-tool" }),
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
  // GitHub's avatar host: each avatar a colored square, and one that fails.
  await page.route("https://avatars.githubusercontent.com/**", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").pop();
    if (id === "404") return route.fulfill({ status: 404, body: "" });
    return route.fulfill({ contentType: "image/svg+xml", body: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40"><rect width="40" height="40" fill="#${id === "7" ? "2f7d5b" : "8a4fbf"}"/></svg>` });
  });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const card = (page: Page, title: string) => page.locator(".task-card-shell").filter({ hasText: title });
async function openPanel(page: Page, title: string) {
  await card(page, title).locator(".task-card").click();
  const taskView = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await taskView.count()) await taskView.click();
  return page.locator(".panel-header");
}
const fits = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);

// Every part of a card that a ticket or an avatar adds, as boxes: none covers
// another and none leaves its card.
const crowded = (page: Page, title: string) => card(page, title).evaluate((shell) => {
  const frame = shell.getBoundingClientRect();
  const parts = [...shell.querySelectorAll(".card-pr, .card-ticket, .same-area-person")].map((part) => part.getBoundingClientRect());
  const outside = parts.filter((box) => box.left < frame.left - 1 || box.right > frame.right + 1 || box.top < frame.top - 1 || box.bottom > frame.bottom + 1).length;
  let overlaps = 0;
  for (let i = 0; i < parts.length; i++) for (let j = i + 1; j < parts.length; j++) {
    const a = parts[i], b = parts[j];
    if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1) overlaps++;
  }
  return { parts: parts.length, outside, overlaps };
});

// His window: maximized on a 2560 by 1600 screen at 150 percent.
test.describe("in his window", () => {
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  test("a card shows its ticket and who else works in its area, and the panel names the project's people", async ({ page }) => {
    await open(page);

    const sync = card(page, "Say why a billing sync fails");
    const ticket = sync.locator("a.card-ticket");
    await expect(ticket).toHaveAttribute("href", REPO + "/issues/52");
    await expect(ticket).toHaveAttribute("aria-label", "Open Ticket #52: pr open");
    await expect(ticket).toHaveText("#52");
    const teammate = sync.locator("a.same-area-person");
    await expect(teammate).toHaveCount(1);
    await expect(teammate).toHaveAttribute("href", REPO + "/pull/46");
    await expect(teammate).toHaveAttribute("data-tip", "In the same area: ana-teammate: PR #46, issue #49");
    await expect(teammate.locator("img")).toHaveAttribute("src", avatar(7));
    expect(await crowded(page, "Say why a billing sync fails")).toEqual({ parts: 3, outside: 0, overlaps: 0 });
    // Queued work shows the ticket it was given at once, here and in the CFO's queue.
    await expect(card(page, "Export the audit trail").locator("a.card-ticket")).toHaveText(["#54", "#54"]);

    const header = await openPanel(page, "Say why a billing sync fails");
    const people = header.locator(".people-row .person");
    const names = header.locator(".people-row .person-name");
    await expect(names).toHaveText(["ana-teammate", "ben-teammate", "cy-teammate"]);
    await expect(people.first()).toHaveClass(/same-area/);
    await expect(people.nth(1)).not.toHaveClass(/same-area/);
    await expect(people.first().locator("a")).toHaveAttribute("href", "https://github.com/ana-teammate");
    await expect(header.locator("a.ticket-link")).toHaveAttribute("href", REPO + "/issues/52");
    // The project's people share its line, beside its name, in his window.
    const [project, row] = await Promise.all([header.locator(".project-label").boundingBox(), header.locator(".people-row").boundingBox()]);
    expect(row!.x).toBeGreaterThan(project!.x + project!.width);
    expect(row!.y).toBeLessThan(project!.y + project!.height);
    // An avatar GitHub does not serve shows the person's initial instead.
    await expect(people.nth(2).locator(".person-avatar.initial")).toHaveText("C");
    expect(await fits(page)).toBe(true);
  });

  test("a project only the Overlord works in shows no ticket and no people", async ({ page }) => {
    await open(page);
    const solo = card(page, "Tidy the solo tool");
    await expect(solo.locator(".card-ticket, .same-area-person")).toHaveCount(0);
    await expect(solo.locator(".card-links")).toHaveCount(0);
    const header = await openPanel(page, "Tidy the solo tool");
    await expect(header.locator(".project-label")).toHaveText("solo-tool");
    await expect(header.locator(".people-row, .ticket-link")).toHaveCount(0);
  });
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2 });

  test("the card and the panel keep their ticket and people inside the page", async ({ page }) => {
    await open(page);
    expect(await crowded(page, "Say why a billing sync fails")).toEqual({ parts: 3, outside: 0, overlaps: 0 });
    const header = await openPanel(page, "Say why a billing sync fails");
    await expect(header.locator(".people-row .person")).toHaveCount(3);
    const [panel, row] = await Promise.all([header.boundingBox(), header.locator(".people-row").boundingBox()]);
    expect(row!.x + row!.width).toBeLessThanOrEqual(panel!.x + panel!.width + 1);
    expect(await fits(page)).toBe(true);
  });
});
