import { CEO_DOOR, expect, openTaskList, taskList, type Page, test } from "./site";

// The Overlord, 2026-10-13: every harness control belongs in a category of
// its own named Harness, for queued tasks, active goblins and the CEO.
// Nothing stands below the selectors, and an active goblin gets the CEO's
// Restart with its harness.
const catalog = { harnesses: [
  { id: "codex", name: "Codex", models: [
    { id: "current-model", name: "Current model", efforts: ["low", "high", "xhigh"], default_effort: "high" },
    { id: "next-model", name: "Next model", efforts: ["low", "high"], default_effort: "low" },
  ] },
  { id: "claude", name: "Claude Code", models: [
    { id: "claude-model", name: "Claude model", efforts: ["high", "xhigh"], default_effort: "xhigh" },
  ] },
] };
const task = (phase: string) => ({ id: "engine-task", title: "Choose the task engine", project: "code-goblins", harness: "codex", model: "current-model", effort: "xhigh", phase, verified: false, backend: "native", generation: phase === "queued" ? "" : "s1", queue_revision: "q1", brief: true });
const CEO = { ceo_runs: true, ceo_terminal: "ceo", ceo_terminal_since: "2026-10-13T09:00:00Z", ceo_harness: "codex", sessions: [{ id: "ceo-1", harness: "codex", role: "ceo", model: "gpt-6-astra" }] };

async function open(page: Page, tasks: object[], overrides: object = {}) {
  const posts: { path: string; body: unknown }[] = [];
  await page.addInitScript((initial) => {
    class Stream extends EventTarget {
      onerror: (() => void) | null = null;
      constructor() {
        super();
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(initial) })));
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: Stream });
  }, { instance: "fixture", healthy: true, revision: 1, tasks, ...CEO, ...overrides });
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/engines") return route.fulfill({ json: catalog });
    if (path === "/api/tasks/engine" || path === "/api/ceo/restart" || path === "/api/ceo/engine") {
      posts.push({ path, body: route.request().postDataJSON() });
      return route.fulfill({ status: 202, json: { accepted: true } });
    }
    if (path === "/api/workspace") return route.fulfill({ json: { repository: "code-goblins", harness: "codex", root: "C:/work", branch: "main", notes: [] } });
    return route.fulfill({ json: {} });
  });
  await page.goto("/");
  return posts;
}

// A queued task with no goblin named yet is headed by its id, since its title
// is its task line under the heading (#1600), and a started one by its title.
async function openTask(page: Page) {
  await page.locator(".task-card").filter({ hasText: "Choose the task engine" }).first().click();
  await expect(page.locator("#panel-title")).toHaveText(/^(?:engine-task|Choose the task engine)$/);
  await openTaskList(page);
}

async function openCeo(page: Page) {
  await page.locator(".ceo-pin").getByRole("button", { name: CEO_DOOR }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CEO");
  await openTaskList(page);
}

const harness = (page: Page) => page.locator("details.harness");

for (const who of ["goblin", "CEO"] as const) {
  for (const transition of ["breakpoint", "maximize", "disclosure"] as const) {
    test(`${who} keeps its unsaved harness choice through ${transition}`, async ({ page }) => {
      // Arrange
      await page.addInitScript(() => localStorage.setItem("ceo-pane-width", "800"));
      const posts = await open(page, [task("paused")]);
      if (who === "goblin") await openTask(page);
      else await openCeo(page);
      await harness(page).locator("> summary").click();
      const model = harness(page).getByRole("combobox", { name: "Model", exact: true });
      if (who === "goblin") await model.selectOption("next-model");
      else {
        await harness(page).getByRole("button", { name: "Harness: Codex" }).click();
        await page.getByRole("menuitemradio", { name: "Claude Code", exact: true }).click();
      }

      // Act
      if (transition === "breakpoint") {
        await page.locator(".context-pane").evaluate((pane) => { pane.style.width = "1000px"; });
        await expect(page.locator(".panel-view-switch")).toBeVisible();
        await page.locator(".context-pane").evaluate((pane) => { pane.style.width = "800px"; });
      } else if (transition === "maximize") {
        await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();
        await expect(page.locator(".panel-view-switch")).toHaveCount(0);
        await page.getByRole("button", { name: "Restore the panel", exact: true }).click();
      } else {
        await page.locator(".panel-view-switch").getByRole("button", { name: "Chat", exact: true }).click();
        await expect(harness(page)).toBeHidden();
      }
      await openTaskList(page);

      // Assert
      await expect(harness(page)).toHaveAttribute("open", "");
      if (who === "goblin") {
        await expect(model).toHaveValue("next-model");
        await harness(page).getByRole("button", { name: "Save for Resume", exact: true }).click();
        await expect.poll(() => posts).toEqual([{ path: "/api/tasks/engine", body: { task: "engine-task", generation: "s1", revision: "q1", harness: "codex", model: "next-model", effort: "low", when: "" } }]);
      } else {
        await expect(harness(page).getByRole("button", { name: "Harness: Claude Code" })).toBeVisible();
        await harness(page).getByRole("button", { name: "Restart the CEO" }).click();
        await expect.poll(() => posts).toEqual([{ path: "/api/ceo/restart", body: { harness: "claude" } }]);
      }
    });
  }
}

for (const phase of ["queued", "paused"]) {
  test(`a ${phase} task's selectors are under Harness, with nothing under them`, async ({ page }) => {
    // Arrange
    await open(page, [task(phase)]);
    await openTask(page);

    // Act
    await harness(page).locator("> summary").click();

    // Assert
    const engine = harness(page).getByRole("group", { name: "Task engine", exact: true });
    await expect(engine.getByRole("combobox", { name: "Harness", exact: true })).toHaveValue("codex");
    await expect(engine.getByRole("combobox", { name: "Model", exact: true })).toHaveValue("current-model");
    await expect(engine.getByRole("combobox", { name: "Effort", exact: true })).toHaveValue("xhigh");
    await expect(engine.getByRole("button")).toHaveCount(0);
    await expect(taskList(page)).not.toContainText("No changes yet");
  });
}

// The Overlord, 2026-11-24: "also in task paenl harness selector page you
// shouldnt be able to select the saved default model just ahrenss type and
// restart" (#2846). The model's button in the chat box sets a running
// session's model and effort, so a running goblin's Harness and the CEO's
// hold the harness and Restart alone; a queued or paused task, which no
// chat runs yet, keeps its selectors.
test("an active goblin's Harness holds its harness and Restart and no model or effort: unchanged it restarts the goblin, on another harness it switches at once", async ({ page }) => {
  // Arrange
  const posts = await open(page, [task("working")]);
  await openTask(page);
  await harness(page).locator("> summary").click();
  const engine = harness(page).getByRole("group", { name: "Task engine", exact: true });
  const restart = engine.getByRole("button", { name: /^Restart/ });

  // Act
  await restart.click();

  // Assert
  await expect(engine.getByRole("combobox", { name: "Harness", exact: true })).toHaveValue("codex");
  await expect(engine.getByRole("combobox", { name: "Model", exact: true })).toHaveCount(0);
  await expect(engine.getByRole("combobox", { name: "Effort", exact: true })).toHaveCount(0);
  await expect.poll(() => posts).toEqual([{ path: "/api/tasks/engine", body: { task: "engine-task", generation: "s1", revision: "q1", harness: "codex", model: "current-model", effort: "xhigh", when: "restart" } }]);

  // Act
  await engine.getByRole("combobox", { name: "Harness", exact: true }).selectOption("claude");
  await restart.click();

  // Assert: no dialog and no words, the switch goes at once, onto the
  // harness's first model at its own effort.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect.poll(() => posts.length).toBe(2);
  expect(posts[1]).toEqual({ path: "/api/tasks/engine", body: { task: "engine-task", generation: "s1", revision: "q1", harness: "claude", model: "claude-model", effort: "xhigh", when: "now" } });
});

test("the CEO's Harness holds Restart and its harness, and no model or effort of its own", async ({ page }) => {
  // Arrange: a CEO whose engine record names its model and effort.
  const posts = await open(page, [], { ceo_engine: { harness: "codex", model: "next-model", effort: "low", pending: false, problem: "", waiting: "" } });
  await openCeo(page);

  // Act
  await harness(page).locator("> summary").click();
  await harness(page).getByRole("button", { name: "Restart the CEO" }).click();

  // Assert
  await expect(harness(page).getByRole("button", { name: "Harness: Codex" })).toBeVisible();
  await expect(harness(page).getByRole("combobox", { name: "Model" })).toHaveCount(0);
  await expect(harness(page).getByRole("combobox", { name: "Effort" })).toHaveCount(0);
  await expect.poll(() => posts).toEqual([{ path: "/api/ceo/restart", body: { harness: "codex" } }]);
});

test("the CEO's Restart and harness are under Harness in its panel, not in its header", async ({ page }) => {
  // Arrange
  const posts = await open(page, []);
  await openCeo(page);

  // Assert
  await expect(page.locator(".panel-header").getByRole("button", { name: "Restart the CEO" })).toHaveCount(0);

  // Act
  await harness(page).locator("> summary").click();
  await harness(page).getByRole("button", { name: "Restart the CEO" }).click();

  // Assert
  await expect(harness(page).getByRole("button", { name: "Harness: Codex" })).toBeVisible();
  await expect.poll(() => posts).toEqual([{ path: "/api/ceo/restart", body: { harness: "codex" } }]);
});
