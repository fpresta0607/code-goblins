import { expect, test, type Locator, type Page } from "./site";

// A task's panel has one action row under its header: the card's Start and
// Remove for a task that has not started, and Pause or Resume and Stop for
// one that has. A queued task is removed, never stopped, on its card as in
// its panel, and is edited in place on its task line in the header.
const since = "2026-10-02T09:00:00Z";
const memory = { next: 5368709120, floor: 4294967296, total: 34359738368, available: 9442450944, commit_limit: 51539607552, commit_available: 21474836480, paged_pool: 536870912, nonpaged_pool: 322122547 };
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const QUEUED = task("queued-one", "queued", { generation: "", brief: true, queue_revision: "q1", detail: "Its detail." });
const BLOCKED = task("queued-blocked", "queued", { generation: "", brief: true, queue_revision: "q1", waits: [{ kind: "task", target: "queued-one" }], reason: "after queued-one" });
const WORKING = task("working-one", "working", { harness: "codex" });
const PAUSED = task("paused-one", "paused", { lifecycle: { phase: "paused", action: "pause", at: since, kept: ["worktree", "session"], stopped: [], problems: [], handoff_saved: true, validation_restarts: false } });
const REMOVED = task("finished:queued-one", "stopped", { title: "queued-one", generation: "", archived: true, at: since, lifecycle: { phase: "stopped", action: "stop", at: since, kept: ["task brief"], stopped: [], problems: [], handoff_saved: false, validation_restarts: false } });
const BOARD = [QUEUED, BLOCKED, WORKING, PAUSED];

const events = (revision: number, tasks: Record<string, unknown>[]) => `event: snapshot\ndata: ${JSON.stringify({ healthy: true, instance: "fixture", cfo_runs: true, revision, attention: [], memory, tasks })}\n\n`;

interface Posted { path: string; body: Record<string, unknown> }

// posted collects what the board asked the supervisor to change.
async function open(page: Page, posted: Posted[] = [], tasks: Record<string, unknown>[] = BOARD) {
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") {
      posted.push({ path, body: route.request().postDataJSON() });
      await route.fulfill({ json: { revision: 2 } });
    } else if (path === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: events(1, tasks) });
    else if (path === "/api/workspace") await route.fulfill({ json: { repository: "code-goblins", root: "C:/work/code-goblins", harness: "codex", notes: [] } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const panel = (page: Page) => page.locator(".goblin-panel");
const card = (page: Page, id: string) => page.locator(".task-board .task-card-shell").filter({ has: page.locator(".card-title").getByText(id, { exact: true }) });

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
  const controls = [...region.querySelectorAll<HTMLElement>(":is(.panel-header, .panel-task) :is(button, a, textarea)")].filter((element) => element.getClientRects().length && !element.closest("dialog"));
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

// The Overlord, 2026-10-08, of a queued panel with Remove alone: "why no run
// button when it says play in panel". A task held by what it waits on has no
// Start, on its card as in its panel.
test("a queued task's panel has the card's Start and Remove, and one held by what it waits on has Remove alone", async ({ page }) => {
  const posted: Posted[] = [];
  await open(page, posted);
  for (const [id, shown] of [["queued-one", ["Start", "Remove"]], ["queued-blocked", ["Remove"]]] as const) {
    await select(page, id);
    const row = panel(page).getByRole("group", { name: "Controls for " + id });
    expect(await names(row), id).toEqual(shown);
    // A labelled button needs no tip to say its name.
    for (const button of await row.getByRole("button").all()) await expect(button).not.toHaveAttribute("data-tip");
    await expect(panel(page).getByRole("button", { name: /^(Stop|Adjust) / })).toHaveCount(0);
    await inOneRow(row);
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
  // The supervisor shows the task out of the queue only once it has the
  // request: the board asks for a snapshot twice a second, and one that
  // arrived before the press would take the confirmation away with its task.
  await page.route("**/api/tasks/lifecycle", async (route) => {
    posted.push({ path: "/api/tasks/lifecycle", body: route.request().postDataJSON() });
    await page.route("**/api/events", (stream) => stream.fulfill({ contentType: "text/event-stream", body: events(2, [BLOCKED, WORKING, PAUSED, REMOVED]) }));
    await route.fulfill({ json: { revision: 2 } });
  });
  await dialog.getByRole("button", { name: "Remove from queue" }).click();
  await expect.poll(() => posted.map((request) => request.path)).toEqual(["/api/tasks/lifecycle"]);
  expect(posted[0].body).toMatchObject({ task: "queued-one", generation: "", revision: "q1", action: "stop" });
  const tasks = page.getByRole("region", { name: "Tasks", exact: true });
  await expect(tasks.locator(".task-card-shell")).toHaveCount(1);
  await expect(card(page, "queued-one").and(tasks.locator(".task-card-shell"))).toHaveCount(0);
  await expect(panel(page).locator(".lifecycle-details li")).toHaveText(["Task brief"]);
});

test("removing a queued task says Removing while it goes", async ({ page }) => {
  await open(page);
  await page.route("**/api/tasks/lifecycle", () => { /* never answered: the removal stays under way */ });
  await select(page, "queued-one");
  await panel(page).getByRole("button", { name: "Remove queued-one" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Remove from queue" }).click();
  await expect(panel(page).locator(".panel-status")).toHaveText("Removing");
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
      if (!id.startsWith("queued-")) continue;
      // A queued task's line opens its edit box in the header.
      await panel(page).locator(".panel-header").getByRole("button", { name: /^Edit the task/ }).click();
      await expect(panel(page).locator(".panel-header").getByRole("textbox")).toBeVisible();
      expect(await page.evaluate(crowded), id + " editing").toEqual([]);
      expect(await clearance(page), id + " editing").toBeGreaterThanOrEqual(16);
    }
    // The measure sees a row whose buttons touch, so an empty report above
    // means they are apart, not that nothing was measured.
    await page.addStyleTag({ content: ".goblin-panel .task-controls { gap: 0 !important; }" });
    expect(await page.evaluate(crowded)).toEqual(["Resume paused-one / Stop paused-one"]);
  });
}

// The Overlord, 2026-10-08: "the caret icon should open up an edit field box
// right in the header of task panel under the goblin name ie where it says the
// task", with "no extra section that we see below it". A named queued task's
// line under its name opens a box holding its title and detail.
const NAMED = task("queued-named", "queued", { generation: "", brief: true, queue_revision: "q1", title: "Fix the billing sync", detail: "Say why it fails.", goblin_name: "Jerry", goblin_title: "Sync Fixer" });
const WRITTEN = "Fix the billing sync\n\nSay why it fails.";

async function openNamed(page: Page, posted: Posted[] = []) {
  await open(page, posted, [NAMED, WORKING]);
  await page.locator(".task-board .task-card-shell").filter({ hasText: "Jerry - Sync Fixer" }).locator(".task-card").click();
  await expect(page.locator("#panel-title")).toHaveText("Jerry - Sync Fixer");
}

const header = (page: Page) => panel(page).locator(".panel-header");
const taskLine = (page: Page) => header(page).getByRole("button", { name: /^Edit the task/ });
const editor = (page: Page) => header(page).getByRole("group", { name: "Edit the task" });
const box = (page: Page) => editor(page).getByRole("textbox", { name: "Task title and detail" });

// answerSaves answers each save with the next revision and keeps what it was
// asked.
async function answerSaves(page: Page, posted: Posted[]) {
  await page.route("**/api/tasks/adjust", async (route) => {
    posted.push({ path: "/api/tasks/adjust", body: route.request().postDataJSON() });
    await route.fulfill({ json: { saved: true, revision: "q" + (posted.length + 1) } });
  });
}

// Whether a CSS color reads as yellow or amber: a hue from orange to
// yellow-green that is not a grey.
function isYellow(color: string): boolean {
  const [r, g, b] = color.match(/[\d.]+/g)!.slice(0, 3).map((value) => Number(value) / 255);
  const max = Math.max(r, g, b), spread = max - Math.min(r, g, b);
  if (spread < 0.08) return false;
  const hue = max === r ? 60 * (((g - b) / spread + 6) % 6) : max === g ? 60 * ((b - r) / spread + 2) : 60 * ((r - g) / spread + 4);
  return hue >= 30 && hue <= 75;
}

test("a queued task's panel has no section to adjust it, and its box opens in the header", async ({ page }) => {
  // Arrange
  await openNamed(page);
  const below = panel(page).locator(".panel-task");

  // Act
  await taskLine(page).click();

  // Assert
  await expect(box(page)).toBeVisible();
  await expect(panel(page).getByText("Adjust this task")).toHaveCount(0);
  await expect(below.getByRole("textbox")).toHaveCount(0);
  await expect(below.getByRole("button", { name: /Save/ })).toHaveCount(0);
  await expect(below.getByRole("button")).toHaveText(["Start", "Remove"]);
});

for (const where of ["caret", "line"] as const) {
  test(`a click on the task line's ${where} turns it into an edit box in place, under the goblin's name`, async ({ page }) => {
    // Arrange
    await openNamed(page);
    await expect(taskLine(page)).toHaveText("Fix the billing sync");
    await expect(taskLine(page).locator(".icon")).toBeVisible();

    // Act
    await (where === "caret" ? taskLine(page).locator(".icon") : taskLine(page).getByText("Fix the billing sync")).click();

    // Assert
    await expect(box(page)).toBeFocused();
    await expect(box(page)).toHaveValue(WRITTEN);
    await expect(taskLine(page)).toHaveCount(0);
    const name = (await page.locator("#panel-title").boundingBox())!;
    const project = (await header(page).locator(".project-label").boundingBox())!;
    const field = (await editor(page).boundingBox())!;
    expect(field.y, "under the goblin's name").toBeGreaterThanOrEqual(name.y + name.height);
    expect(field.y + field.height, "above its project").toBeLessThanOrEqual(project.y);
  });
}

test("the edit box grows with its text", async ({ page }) => {
  // Arrange
  await openNamed(page);
  await taskLine(page).click();
  const height = async () => (await box(page).boundingBox())!.height;
  await box(page).fill("One line");
  const short = await height();

  // Act
  await box(page).fill(["One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight"].join("\n"));

  // Assert
  expect(await height()).toBeGreaterThan(short * 3);
  expect(await box(page).evaluate((element) => element.scrollHeight <= element.clientHeight + 1), "it shows every line without scrolling").toBe(true);
});

for (const how of ["Enter", "the check"] as const) {
  test(`${how} saves the box with the task's revision, and Shift+Enter adds a line`, async ({ page }) => {
    // Arrange
    const posted: Posted[] = [];
    await openNamed(page);
    await answerSaves(page, posted);
    await taskLine(page).click();
    await box(page).fill("A better title");
    await box(page).press("Shift+Enter");
    await box(page).pressSequentially("Its new detail.");
    await expect(box(page)).toHaveValue("A better title\nIts new detail.");

    // Act
    if (how === "Enter") await box(page).press("Enter");
    else await editor(page).getByRole("button", { name: "Save task" }).click();

    // Assert
    await expect.poll(() => posted).toEqual([{ path: "/api/tasks/adjust", body: { task: "queued-named", revision: "q1", text: "A better title\nIts new detail.", action: "save", operation: expect.any(String) } }]);
    await expect(editor(page)).toHaveCount(0);
    await expect(taskLine(page)).toHaveText("A better title");
    await expect(taskLine(page)).toBeFocused();
  });
}

test("a second edit before the board hears of the first saves on the revision the first returned", async ({ page }) => {
  // Arrange
  const posted: Posted[] = [];
  await openNamed(page);
  await answerSaves(page, posted);
  await taskLine(page).click();
  await box(page).fill("A better title");
  await box(page).press("Enter");
  await expect(taskLine(page)).toHaveText("A better title");

  // Act
  await taskLine(page).click();
  await expect(box(page)).toHaveValue("A better title");
  await box(page).fill("The best title");
  await box(page).press("Enter");

  // Assert
  await expect.poll(() => posted.map((request) => request.body.revision)).toEqual(["q1", "q2"]);
  await expect(taskLine(page)).toHaveText("The best title");
});

for (const how of ["Escape", "the cross"] as const) {
  test(`${how} puts the task line back unchanged and keeps the panel open`, async ({ page }) => {
    // Arrange
    const posted: Posted[] = [];
    await openNamed(page, posted);
    await taskLine(page).click();
    await box(page).fill("Something else");

    // Act
    if (how === "Escape") await box(page).press("Escape");
    else await editor(page).getByRole("button", { name: "Cancel edit" }).click();

    // Assert
    await expect(editor(page)).toHaveCount(0);
    await expect(taskLine(page)).toHaveText("Fix the billing sync");
    await expect(taskLine(page)).toBeFocused();
    await expect(page.locator("#panel-title")).toHaveText("Jerry - Sync Fixer");
    await taskLine(page).click();
    await expect(box(page)).toHaveValue(WRITTEN);
    expect(posted).toEqual([]);
  });
}

test("while it saves, the box shows it is busy", async ({ page }) => {
  // Arrange
  await openNamed(page);
  await page.route("**/api/tasks/adjust", () => { /* never answered: the save stays under way */ });
  await taskLine(page).click();

  // Act
  await box(page).press("Enter");

  // Assert
  await expect(editor(page)).toHaveAttribute("aria-busy", "true");
  await expect(box(page)).not.toBeEditable();
  await expect(editor(page).getByRole("button", { name: "Save task" })).toBeDisabled();
});

test("a refused save keeps the text and says why beside the box for a moment, never in yellow", async ({ page }) => {
  // Arrange
  await page.clock.install();
  await openNamed(page);
  await page.route("**/api/tasks/adjust", (route) => route.fulfill({ status: 409, json: { error: "The queued task changed; reopen its card" } }));
  await taskLine(page).click();
  await box(page).fill("A better title");

  // Act
  await box(page).press("Enter");

  // Assert
  const why = header(page).getByRole("status").filter({ hasText: "The queued task changed. Reopen its card." });
  await expect(why).toBeVisible();
  await expect(box(page)).toHaveValue("A better title");
  await expect(box(page)).toBeEditable();
  const color = await why.evaluate((element) => getComputedStyle(element).color);
  expect(isYellow(color), color).toBe(false);
  expect(isYellow("rgb(231, 211, 163)"), "the measure sees the waiting amber").toBe(true);
  await expect(page.getByRole("alert")).toHaveCount(0);
  const field = (await editor(page).boundingBox())!, words = (await why.boundingBox())!;
  expect(words.y - (field.y + field.height), "beside the box").toBeLessThan(16);
  await page.clock.runFor(6500);
  await expect(why).toHaveCount(0);
  await expect(box(page)).toHaveValue("A better title");
});

// The Overlord, 2026-10-07, on an amber box at the head of Tasks saying a
// start needs more memory: "any alerts that are critical go through cfo to me
// as needed". A start the supervisor tried by itself and could not make shows
// nothing on the card: the CFO is told, and the meter shows memory.
test("a queued card whose supervisor start failed shows no failure", async ({ page }) => {
  // Arrange
  const failed = { ...QUEUED, start_error: "memory 4.6 GB free; a start needs 5 GB of memory and commit to keep the 4 GB floor" };
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/events") await route.fulfill({ contentType: "text/event-stream", body: events(1, [failed, WORKING]) });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });

  // Act
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();

  // Assert
  const queued = card(page, "queued-one");
  await expect(queued).toBeVisible();
  await expect(queued.getByRole("alert")).toHaveCount(0);
  await expect(page.getByText("keep the 4 GB floor")).toHaveCount(0);
});
