import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord, 2026-10-08, with the Tasks list and Zane's panel: his card
// read "Not started" with Start while the supervisor had already started
// him, and his panel read "Not started" with Remove alone. "in queue should
// say 'Queued' ... why no run button when it says play in panel ... when its
// starting... it displays where the status dot is in the board panel as well
// status dots need to be better synched across pane and panels and boards".
// A task reads one status, the same words and the same dot, on its card, its
// panel, its terminal pane and its canvas node, from one snapshot. The
// supervisor is played by one held snapshot per state.
const since = "2026-10-08T21:30:00Z";
// The snapshot's time: half a minute after the goblin's session started.
const AT = "2026-10-08T21:30:30Z";
const GB = 2 ** 30;
const memory = { next: 5 * GB, floor: 4 * GB, total: 32 * GB, available: 9 * GB, commit_limit: 48 * GB, commit_available: 40 * GB, paged_pool: GB / 2, nonpaged_pool: GB / 4 };
const ZANE = "Zane - Train Tamer";
const zane = (fields: Record<string, unknown>) => ({
  id: "cg-train-history", title: "Train history", goblin_name: "Zane", goblin_title: "Train Tamer", project: "code-goblins", verified: false,
  harness: "claude", model: "claude-opus-5-5", effort: "xhigh", since, brief: true, queue_revision: "q1", generation: "", ...fields,
});
const WORKING_OTHER = { id: "cg-other", title: "Other work", goblin_name: "Ned", goblin_title: "Docs Scribe", project: "code-goblins", phase: "working", verified: false, generation: "cg-other-1", harness: "claude", since };

const STATES = [
  { name: "queued", task: zane({ phase: "queued" }), text: "Queued", phase: "queued", column: "Tasks" },
  { name: "started by the scheduler", task: zane({ phase: "queued", starting: true }), text: "Starting", phase: "started", column: "In progress" },
  { name: "starting with its session recorded", task: zane({ phase: "unknown", starting: true, generation: "s1", backend: "native" }), text: "Starting", phase: "started", column: "In progress" },
  { name: "started, with no evidence of it yet", task: zane({ phase: "unknown", generation: "s1", backend: "native" }), text: "Starting", phase: "started", column: "In progress" },
  { name: "working", task: zane({ phase: "working", generation: "s1", backend: "native" }), text: "Working", phase: "working", column: "In progress" },
  { name: "a failed start", task: zane({ phase: "queued", start_error: "cfo spawn: the brief names no project" }), text: "Start failed", phase: "failed", column: "Tasks" },
] as const;

interface Posted { path: string; body: Record<string, unknown> }

async function open(page: Page, task: Record<string, unknown>, posted: Posted[] = []) {
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, at: AT, attention: [], memory, tasks: [task, WORKING_OTHER] });
  await page.route("**/api/**", async (route) => {
    if (route.request().method() === "POST") {
      posted.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() });
      return route.fulfill({ status: 202, json: { revision: 2 } });
    }
    return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

const board = (page: Page) => page.locator(".task-board");
const card = (page: Page) => board(page).locator(".task-card-shell").filter({ has: page.locator(".card-title").getByText(ZANE, { exact: true }) });
const header = (page: Page) => page.locator(".goblin-panel .panel-header");
const pill = (page: Page, view: "Task" | "Terminal") => page.locator(".panel-pill").getByRole("button", { name: view, exact: true });

// A status as a surface draws it: its words and the phase its dot is drawn in.
async function read(status: Locator) {
  return status.evaluate((element) => ({
    text: element.textContent?.trim() || "",
    phase: [...element.classList].find((name) => name.startsWith("phase-"))?.slice("phase-".length) || "",
    dot: getComputedStyle(element.querySelector(".status-dot")!).backgroundColor,
  }));
}

const YELLOWS = ["rgb(243, 197, 120)", "rgb(217, 183, 106)"];

for (const state of STATES) {
  test(`${state.name} reads ${state.text} at the same dot on its card, its panel, its terminal pane and its canvas node`, async ({ page }) => {
    // Arrange
    await open(page, state.task);

    // Act
    const onCard = await read(card(page).locator(".plain-status"));
    await card(page).locator(".task-card").click();
    await expect(page.locator("#panel-title")).toHaveText(ZANE);
    if (await pill(page, "Task").count()) await pill(page, "Task").click();
    const onPanel = await read(header(page).locator(".panel-status"));
    let onPane = onPanel;
    if (await pill(page, "Terminal").count()) {
      await pill(page, "Terminal").click();
      onPane = await read(header(page).locator(".panel-status"));
    }
    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    const node = page.locator(".flow-node").filter({ has: page.locator("strong", { hasText: ZANE }) });
    const onCanvas = state.column === "Tasks" ? null : await read(node.locator(".plain-status"));

    // Assert
    const want = { text: state.text, phase: state.phase };
    expect({ text: onCard.text, phase: onCard.phase }, "card").toEqual(want);
    expect({ text: onPanel.text, phase: onPanel.phase }, "panel").toEqual(want);
    expect({ text: onPane.text, phase: onPane.phase }, "terminal pane").toEqual(want);
    expect(onPanel.dot, "the panel's dot is the card's").toBe(onCard.dot);
    expect(onPane.dot, "the terminal pane's dot is the card's").toBe(onCard.dot);
    expect(YELLOWS, "never a yellow dot").not.toContain(onCard.dot);
    if (onCanvas) {
      expect({ text: onCanvas.text, phase: onCanvas.phase }, "canvas node").toEqual(want);
      expect(onCanvas.dot, "the canvas node's dot is the card's").toBe(onCard.dot);
    } else await expect(node).toHaveCount(0);
  });

  test(`${state.name} sits in ${state.column}${state.column === "Tasks" ? "" : ", with no Start"}`, async ({ page }) => {
    await open(page, state.task);
    const column = board(page).getByRole("region", { name: state.column, exact: true });
    await expect(column.locator(".card-title").getByText(ZANE, { exact: true })).toHaveCount(1);
    await expect(board(page).locator(".card-title").getByText(ZANE, { exact: true })).toHaveCount(1);
    if (state.column !== "Tasks") await expect(card(page).getByRole("button", { name: /^(Start|Adjust) / })).toHaveCount(0);
  });
}

for (const [when, task] of [["before", zane({ phase: "queued", starting: true })], ["once", zane({ phase: "unknown", starting: true, generation: "s1", backend: "native" })]] as const) {
  test(`a task the supervisor starts heads In progress ${when} its session is recorded`, async ({ page }) => {
    await open(page, task);
    const progress = board(page).getByRole("region", { name: "In progress", exact: true });
    await expect(progress.locator(".card-title")).toHaveText([ZANE, "Ned - Docs Scribe"]);
  });
}

test("a queued task's panel has the same Start as its card, and it starts the task the same way", async ({ page }) => {
  // Arrange
  const posted: Posted[] = [];
  await open(page, zane({ phase: "queued" }), posted);
  await card(page).getByRole("button", { name: "Start Train history" }).click();
  await expect.poll(() => posted).toEqual([{ path: "/api/tasks/start", body: { task: "cg-train-history" } }]);
  await page.reload();
  await expect(page.locator(".board-column").first()).toBeVisible();
  posted.length = 0;
  await card(page).locator(".task-card").click();
  const controls = page.locator(".goblin-panel").getByRole("group", { name: "Controls for Train history" });

  // Act
  const shown = await controls.getByRole("button").allTextContents();
  await controls.getByRole("button", { name: "Start Train history" }).click();

  // Assert
  expect(shown).toEqual(["Start", "Remove"]);
  await expect.poll(() => posted).toEqual([{ path: "/api/tasks/start", body: { task: "cg-train-history" } }]);
  await expect(header(page).locator(".panel-status")).toHaveText("Starting");
  await expect(card(page).locator(".card-status-text")).toHaveText("Starting");
});

test("a task with no terminal yet says its status where its terminal will be", async ({ page }) => {
  await open(page, zane({ phase: "queued", starting: true }));
  await card(page).locator(".task-card").click();
  await pill(page, "Terminal").click();
  await expect(page.locator(".deck-slot:not([hidden]) .terminal-empty")).toHaveText("Starting");
});
