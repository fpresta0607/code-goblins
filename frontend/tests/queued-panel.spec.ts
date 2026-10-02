import { expect, test, type Locator, type Page } from "./site";

// A task's panel has one action row under its header: Remove for a task that
// has not started, whose Start stays on its card, and Pause or Resume and Stop
// for one that has. A queued task is removed, never stopped, on its card as in
// its panel, and the form that adjusts it carries its own Save changes under
// its text box.
const since = "2026-10-02T09:00:00Z";
const memory = { next: 5368709120, floor: 4294967296, total: 34359738368, available: 9442450944, commit_limit: 51539607552, commit_available: 21474836480, paged_pool: 536870912, nonpaged_pool: 322122547 };
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const QUEUED = task("queued-one", "queued", { generation: "", brief: true, queue_revision: "q1", detail: "Its detail." });
const BLOCKED = task("queued-blocked", "queued", { generation: "", brief: true, queue_revision: "q1", dependencies: ["queued-one"], reason: "Waiting on queued-one" });
const WORKING = task("working-one", "working", { harness: "codex" });
const PAUSED = task("paused-one", "paused", { lifecycle: { phase: "paused", action: "pause", at: since, kept: ["worktree", "session"], stopped: [], problems: [], handoff_saved: true, validation_restarts: false } });
const REMOVED = task("finished:queued-one", "stopped", { title: "queued-one", generation: "", archived: true, at: since, lifecycle: { phase: "stopped", action: "stop", at: since, kept: ["task brief"], stopped: [], problems: [], handoff_saved: false, validation_restarts: false } });
const BOARD = [QUEUED, BLOCKED, WORKING, PAUSED];

const events = (revision: number, tasks: Record<string, unknown>[]) => `event: snapshot\ndata: ${JSON.stringify({ healthy: true, instance: "fixture", cfo_runs: true, revision, attention: [], memory, tasks })}\n\n`;

interface Posted { path: string; body: Record<string, unknown> }

// posted collects what the board asked the supervisor to change.
async function open(page: Page, posted: Posted[] = []) {
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") {
      posted.push({ path, body: route.request().postDataJSON() });
      await route.fulfill({ json: { revision: 2 } });
    } else if (path === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: events(1, BOARD) });
    else if (path === "/api/workspace") await route.fulfill({ json: { repository: "code-goblins", root: "C:/work/code-goblins", harness: "codex", notes: [] } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const panel = (page: Page) => page.locator(".goblin-panel");
const card = (page: Page, id: string) => page.locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText(id, { exact: true }) });

async function select(page: Page, id: string) {
  await card(page, id).locator(".task-card").click();
  await expect(page.locator("#panel-title")).toHaveText(id);
  const pill = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await pill.count()) await pill.click();
}

// The names of a row's buttons, in the order they are drawn.
const names = (row: Locator) => row.getByRole("button").evaluateAll((buttons) => buttons.map((button) => button.textContent));

async function inOneRow(row: Locator) {
  const tops = await row.getByRole("button").evaluateAll((buttons) => buttons.map((button) => Math.round(button.getBoundingClientRect().top)));
  expect(new Set(tops).size, "every button of the row starts on the same line").toBe(1);
}

// Every pair of controls in the panel that touch or overlap, and every one
// that reaches outside the panel; controls are measured by their boxes.
function crowded(): string[] {
  const GAP = 8;
  const region = document.querySelector(".goblin-panel")!;
  const frame = region.getBoundingClientRect();
  const controls = [...region.querySelectorAll<HTMLElement>(".panel-header :is(button, a), .panel-task :is(button, a, textarea)")].filter((element) => element.getClientRects().length && !element.closest("dialog"));
  const name = (element: HTMLElement) => element.getAttribute("aria-label") || element.textContent || element.tagName;
  const problems: string[] = [];
  controls.forEach((first, at) => {
    const a = first.getBoundingClientRect();
    if (a.left < frame.left || a.right > frame.right) problems.push(name(first) + " reaches outside the panel");
    for (const second of controls.slice(at + 1)) {
      if (first.contains(second) || second.contains(first)) continue;
      const b = second.getBoundingClientRect();
      const across = Math.max(a.left, b.left) - Math.min(a.right, b.right), down = Math.max(a.top, b.top) - Math.min(a.bottom, b.bottom);
      if (across < GAP && down < GAP) problems.push(name(first) + " / " + name(second));
    }
  });
  return problems;
}

// How far the action row keeps from the line under the header.
async function clearance(page: Page) {
  const header = (await panel(page).locator(".panel-header").boundingBox())!;
  const row = (await panel(page).getByRole("group", { name: /^Controls for / }).boundingBox())!;
  return row.y - (header.y + header.height);
}

test("a queued task's panel has Remove alone, and its Start stays on its card", async ({ page }) => {
  const posted: Posted[] = [];
  await open(page, posted);
  for (const id of ["queued-one", "queued-blocked"]) {
    await select(page, id);
    const row = panel(page).getByRole("group", { name: "Controls for " + id });
    expect(await names(row), id).toEqual(["Remove"]);
    // A labelled button needs no tip to say its name.
    await expect(row.getByRole("button")).not.toHaveAttribute("data-tip");
    await expect(panel(page).getByRole("button", { name: /^(Start|Stop|Adjust) / })).toHaveCount(0);
    expect(await clearance(page), id).toBeGreaterThanOrEqual(16);
  }
  const controls = card(page, "queued-one").getByRole("group", { name: "Controls for queued-one" }).getByRole("button");
  expect(await controls.evaluateAll((buttons) => buttons.map((button) => button.getAttribute("aria-label")))).toEqual(["Start queued-one", "Adjust queued-one", "Remove queued-one"]);
  await controls.first().click();
  await expect.poll(() => posted).toEqual([{ path: "/api/tasks/start", body: { task: "queued-one" } }]);
});

test("a queued card has Remove, with its tip, and no Stop", async ({ page }) => {
  await open(page);
  const remove = card(page, "queued-one").getByRole("button", { name: "Remove queued-one" });
  await expect(remove).toHaveAttribute("data-tip", "Remove");
  await expect(card(page, "queued-one").getByRole("button", { name: /^Stop / })).toHaveCount(0);
  await expect(card(page, "working-one").getByRole("button", { name: "Stop working-one" })).toHaveAttribute("data-tip", "Stop");
  await expect(card(page, "paused-one").getByRole("button", { name: "Stop paused-one" })).toBeVisible();
});

test("Remove asks first, then takes the task out of the queue and keeps its brief", async ({ page }) => {
  const posted: Posted[] = [];
  await open(page, posted);
  await select(page, "queued-one");
  const remove = panel(page).getByRole("button", { name: "Remove queued-one" });
  await remove.click();
  const dialog = page.getByRole("dialog", { name: "Remove this task?" });
  await expect(dialog).toContainText("It leaves the queue and will not start.");
  await expect(dialog).toContainText("Its brief is kept.");
  await expect(dialog.getByRole("button")).toHaveText(["Remove from queue", "Cancel"]);
  await expect(dialog).not.toContainText("Stop");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  expect(posted).toEqual([]);

  await remove.click();
  // The supervisor's next snapshot shows the task out of the queue.
  await page.route("**/api/events", (route) => route.fulfill({ contentType: "text/event-stream", body: events(2, [BLOCKED, WORKING, PAUSED, REMOVED]) }));
  await dialog.getByRole("button", { name: "Remove from queue" }).click();
  await expect.poll(() => posted.map((request) => request.path)).toEqual(["/api/tasks/lifecycle"]);
  expect(posted[0].body).toMatchObject({ task: "queued-one", generation: "", revision: "q1", action: "stop" });
  const tasks = page.getByRole("region", { name: "Tasks", exact: true });
  await expect(tasks.locator(".task-card-shell")).toHaveCount(1);
  await expect(card(page, "queued-one").and(tasks.locator(".task-card-shell"))).toHaveCount(0);
  await expect(panel(page).locator(".lifecycle-details li")).toHaveText(["task brief"]);
});

test("removing a queued task says Removing while it goes", async ({ page }) => {
  await open(page);
  await page.route("**/api/tasks/lifecycle", () => { /* never answered: the removal stays under way */ });
  await select(page, "queued-one");
  await panel(page).getByRole("button", { name: "Remove queued-one" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Remove from queue" }).click();
  await expect(panel(page).getByRole("status").filter({ hasText: "Removing..." })).toBeVisible();
});

test("Adjust this task has Save changes, labelled, under its text box, and no Send to CFO", async ({ page }) => {
  const posted: Posted[] = [];
  await open(page, posted);
  await select(page, "queued-one");
  const form = panel(page).getByRole("region", { name: "Adjust this task" });
  const row = form.locator(".task-controls");
  expect(await names(row)).toEqual(["Save changes"]);
  await expect(panel(page).getByText("Send", { exact: false })).toHaveCount(0);
  const box = (await form.getByRole("textbox").boundingBox())!;
  expect((await row.boundingBox())!.y).toBeGreaterThan(box.y + box.height);
  await expect(form.locator("p.muted").last()).toHaveText("Save updates the task and its brief.");
  await form.getByRole("textbox").fill("A better title\n\nIts new detail.");
  await form.getByRole("button", { name: "Save changes" }).click();
  await expect.poll(() => posted).toEqual([{ path: "/api/tasks/adjust", body: expect.objectContaining({ task: "queued-one", revision: "q1", text: "A better title\n\nIts new detail.", action: "save" }) }]);
});

test("a running task keeps Pause and Stop, and a paused one Resume and Stop, in the same row", async ({ page }) => {
  await open(page);
  await select(page, "working-one");
  const working = panel(page).getByRole("group", { name: "Controls for working-one" });
  expect(await names(working)).toEqual(["Pause", "Stop"]);
  await inOneRow(working);
  expect(await clearance(page)).toBeGreaterThanOrEqual(16);
  await working.getByRole("button", { name: "Stop working-one" }).click();
  const dialog = page.getByRole("dialog", { name: "Stop this task?" });
  await expect(dialog.getByRole("button")).toHaveText(["Pause instead (Recommended)", "Stop and delete", "Cancel"]);
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await select(page, "paused-one");
  const paused = panel(page).getByRole("group", { name: "Controls for paused-one" });
  expect(await names(paused)).toEqual(["Resume", "Stop"]);
  await inOneRow(paused);
  expect(await clearance(page)).toBeGreaterThanOrEqual(16);
});

for (const [size, viewport] of [["wide", { width: 1440, height: 1200 }], ["phone", { width: 390, height: 844 }]] as const) {
  test(`at ${size} width no control of a panel touches another, the line above it, or the panel's edge`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await open(page);
    for (const id of ["queued-one", "queued-blocked", "working-one", "paused-one"]) {
      await select(page, id);
      await expect(panel(page).getByRole("group", { name: "Controls for " + id })).toBeVisible();
      expect(await page.evaluate(crowded), id).toEqual([]);
      expect(await clearance(page), id).toBeGreaterThanOrEqual(16);
    }
    // The measure sees a row whose buttons touch, so an empty report above
    // means they are apart, not that nothing was measured.
    await page.addStyleTag({ content: ".goblin-panel .task-controls { gap: 0 !important; }" });
    expect(await page.evaluate(crowded)).toEqual(["Resume paused-one / Stop paused-one"]);
  });
}
