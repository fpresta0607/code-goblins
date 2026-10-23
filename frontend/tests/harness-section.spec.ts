import { expect, openTaskList, taskList, type Page, test } from "./site";

// The Overlord, 2026-10-13: every harness control belongs in a category of
// its own named Harness, for queued tasks, active goblins and the CFO.
// Connections lists no harness, nothing stands below the selectors, and an
// active goblin gets the CFO's Restart with its harness, model and effort.
const catalog = { harnesses: [
  { id: "codex", name: "Codex", models: [
    { id: "current-model", name: "Current model", efforts: ["low", "high", "xhigh"], default_effort: "high" },
    { id: "next-model", name: "Next model", efforts: ["low", "high"], default_effort: "low" },
  ] },
] };
const task = (phase: string) => ({ id: "engine-task", title: "Choose the task engine", project: "code-goblins", harness: "codex", model: "current-model", effort: "xhigh", phase, verified: false, backend: "native", generation: phase === "queued" ? "" : "s1", queue_revision: "q1", brief: true });
const CFO = { cfo_runs: true, cfo_terminal: "cfo", cfo_terminal_since: "2026-10-13T09:00:00Z", cfo_harness: "codex", sessions: [{ id: "cfo-1", harness: "codex", role: "cfo", model: "gpt-6-astra" }] };

async function open(page: Page, tasks: object[]) {
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
  }, { instance: "fixture", healthy: true, revision: 1, tasks, ...CFO });
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/engines") return route.fulfill({ json: catalog });
    if (path === "/api/tasks/engine" || path === "/api/cfo/restart") {
      posts.push({ path, body: route.request().postDataJSON() });
      return route.fulfill({ status: 202, json: { accepted: true } });
    }
    if (path === "/api/workspace") return route.fulfill({ json: { repository: "code-goblins", harness: "codex", model: "Reported: gpt-6-astra", root: "C:/work", branch: "main", notes: [] } });
    if (path === "/api/connections") return route.fulfill({ json: { connections: [] } });
    return route.fulfill({ json: {} });
  });
  await page.goto("/");
  return posts;
}

async function openTask(page: Page) {
  await page.locator(".task-card").filter({ hasText: "Choose the task engine" }).first().click();
  await expect(page.locator("#panel-title")).toHaveText("Choose the task engine");
  await openTaskList(page);
}

async function openCfo(page: Page) {
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await openTaskList(page);
}

const harness = (page: Page) => page.locator("details.harness");
const connections = (page: Page) => page.locator("details.connections");

for (const who of ["goblin", "CFO"] as const) {
  for (const transition of ["breakpoint", "maximize", "disclosure"] as const) {
    test(`${who} keeps its unsaved harness choice through ${transition}`, async ({ page }) => {
      // Arrange
      await page.addInitScript(() => localStorage.setItem("cfo-pane-width", "800"));
      const posts = await open(page, [task("paused")]);
      if (who === "goblin") await openTask(page);
      else await openCfo(page);
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
        await expect(page.locator(".panel-side-task")).toBeVisible();
        await page.locator(".context-pane").evaluate((pane) => { pane.style.width = "800px"; });
      } else if (transition === "maximize") {
        await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();
        await expect(page.locator(".panel-side-task")).toBeVisible();
        await page.getByRole("button", { name: "Restore the panel", exact: true }).click();
      } else {
        await page.locator(".panel-task-fold > details > summary").click();
        await expect(harness(page)).toBeHidden();
      }
      await expect(page.locator(".panel-task-fold")).toBeVisible();
      await openTaskList(page);

      // Assert
      await expect(harness(page)).toHaveAttribute("open", "");
      if (who === "goblin") {
        await expect(model).toHaveValue("next-model");
        await harness(page).getByRole("button", { name: "Save for Resume", exact: true }).click();
        await expect.poll(() => posts).toEqual([{ path: "/api/tasks/engine", body: { task: "engine-task", generation: "s1", revision: "q1", harness: "codex", model: "next-model", effort: "low", when: "" } }]);
      } else {
        await expect(harness(page).getByRole("button", { name: "Harness: Claude Code" })).toBeVisible();
        await harness(page).getByRole("button", { name: "Restart the CFO" }).click();
        await expect.poll(() => posts).toEqual([{ path: "/api/cfo/restart", body: { harness: "claude" } }]);
      }
    });
  }
}

for (const phase of ["queued", "paused"]) {
  test(`a ${phase} task's selectors are under Harness, with nothing under them and no harness in Connections`, async ({ page }) => {
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
    if (await connections(page).count()) {
      await connections(page).locator("> summary").click();
      await expect(connections(page)).not.toContainText("Codex");
      await expect(connections(page).getByRole("combobox")).toHaveCount(0);
    }
  });
}

test("an active goblin's Harness has Restart beside its selectors: unchanged it restarts the goblin, changed it switches at once", async ({ page }) => {
  // Arrange
  const posts = await open(page, [task("working")]);
  await openTask(page);
  await harness(page).locator("> summary").click();
  const engine = harness(page).getByRole("group", { name: "Task engine", exact: true });
  const restart = engine.getByRole("button", { name: /^Restart/ });

  // Act
  await restart.click();

  // Assert
  await expect.poll(() => posts).toEqual([{ path: "/api/tasks/engine", body: { task: "engine-task", generation: "s1", revision: "q1", harness: "codex", model: "current-model", effort: "xhigh", when: "restart" } }]);

  // Act
  await engine.getByRole("combobox", { name: "Model", exact: true }).selectOption("next-model");
  await restart.click();

  // Assert: no dialog and no words, the switch goes at once.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect.poll(() => posts.length).toBe(2);
  expect(posts[1]).toEqual({ path: "/api/tasks/engine", body: { task: "engine-task", generation: "s1", revision: "q1", harness: "codex", model: "next-model", effort: "low", when: "now" } });
  await connections(page).locator("> summary").click();
  await expect(connections(page)).not.toContainText("Codex");
  await expect(connections(page).getByRole("combobox")).toHaveCount(0);
});

test("the CFO's Restart and harness are under Harness in its panel, not in its header, and Connections names no harness", async ({ page }) => {
  // Arrange
  const posts = await open(page, []);
  await openCfo(page);

  // Assert
  await expect(page.locator(".panel-header").getByRole("button", { name: "Restart the CFO" })).toHaveCount(0);

  // Act
  await harness(page).locator("> summary").click();
  await harness(page).getByRole("button", { name: "Restart the CFO" }).click();

  // Assert
  await expect(harness(page).getByRole("button", { name: "Harness: Codex" })).toBeVisible();
  await expect.poll(() => posts).toEqual([{ path: "/api/cfo/restart", body: { harness: "codex" } }]);
  await expect(connections(page)).toHaveCount(0);
});
