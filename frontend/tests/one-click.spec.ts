import { expect, test, type Page, type Route } from "./site";

// The Overlord, 2026-10-08: "resume goblin is glitchy should be smooth ... it
// took multiple clicks with an error message ... i hate yellow text line
// display". One click starts or resumes a goblin: its card says Starting or
// Resuming in the frame he clicks, a second click while it is on its way
// sends nothing more, memory under the mark holds no button, and a click the
// supervisor refuses puts the card back and goes to the CFO, never to a line
// on the board.
const since = "2026-10-08T11:00:00Z";
const GB = 2 ** 30;
const memoryWith = (available: number) => ({ next: 5 * GB, floor: 4 * GB, total: 32 * GB, available: available * GB, commit_limit: 48 * GB, commit_available: 40 * GB, paged_pool: GB / 2, nonpaged_pool: GB / 4 });
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const QUEUED = task("queued-one", "queued", { generation: "", brief: true, queue_revision: "q1" });
const PAUSED = task("paused-one", "paused", { lifecycle: { phase: "paused", action: "pause", at: since, kept: ["worktree"], stopped: [], problems: [], handoff_saved: true, validation_restarts: false } });

const events = (available: number) => `event: snapshot\ndata: ${JSON.stringify({ healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], memory: memoryWith(available), tasks: [QUEUED, PAUSED] })}\n\n`;

interface Posted { path: string; body: Record<string, unknown> }

// open shows the board with available GB free; answer is how the supervisor
// answers a Start or a lifecycle click, and posted collects every change the
// board asked for.
async function open(page: Page, available: number, answer: (route: Route) => Promise<void> | void, posted: Posted[]) {
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") {
      posted.push({ path, body: route.request().postDataJSON() });
      if (path === "/api/tasks/start" || path === "/api/tasks/lifecycle") return answer(route);
      await route.fulfill({ status: 202, json: { reported: true } });
    } else if (path === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: events(available) });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
}

const card = (page: Page, id: string) => page.locator(".task-board .task-card-shell").filter({ has: page.locator(".card-title").getByText(id, { exact: true }) });
const neverAnswered = () => { /* the click stays on its way */ };

for (const [name, id, button, status, path] of [
  ["Resume", "paused-one", "Resume paused-one", "Resuming", "/api/tasks/lifecycle"],
  ["Start", "queued-one", "Start queued-one", "Starting", "/api/tasks/start"],
] as const) {
  test(`one click on ${name} says ${status} at once, and a second click sends nothing more`, async ({ page }) => {
    // Arrange
    const posted: Posted[] = [];
    await open(page, 9, neverAnswered, posted);
    const control = card(page, id).getByRole("button", { name: button });

    // Act
    await control.dblclick();

    // Assert
    await expect(card(page, id).locator(".card-status-text")).toHaveText(status);
    await expect.poll(() => posted.filter((request) => request.path === path)).toHaveLength(1);
  });

  test(`${name} under the memory mark is one click too`, async ({ page }) => {
    // Arrange
    const posted: Posted[] = [];
    await open(page, 4.2, neverAnswered, posted);
    const control = card(page, id).getByRole("button", { name: button });

    await expect(control).not.toHaveAttribute("aria-disabled", "true");

    // Act
    await control.click();

    // Assert
    await expect.poll(() => posted.filter((request) => request.path === path)).toHaveLength(1);
    await expect(card(page, id).locator(".card-status-text")).toHaveText(status);
  });

  test(`a ${name} the supervisor refuses puts the card back and goes to the CFO, with no line on the board`, async ({ page }) => {
    // Arrange
    const posted: Posted[] = [];
    await open(page, 9, (route) => route.fulfill({ status: 409, json: { error: "The task session changed; refresh its card" } }), posted);

    // Act
    await card(page, id).getByRole("button", { name: button }).click();

    // Assert
    await expect.poll(() => posted.filter((request) => request.path === "/api/cfo/report").map((request) => request.body.text)).toEqual(["The task session changed; refresh its card"]);
    await expect(card(page, id).locator(".card-status-text")).not.toHaveText(status);
    await expect(page.locator(".click-feedback, .task-notes, [role=alert]")).toHaveCount(0);
  });
}
