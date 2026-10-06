import { expect, test, type Page } from "./site";

const catalog = { harnesses: [
  { id: "codex", name: "Codex", models: [
    { id: "current-model", name: "Current model", efforts: ["low", "high", "xhigh"], default_effort: "high" },
    { id: "next-model", name: "Next model", efforts: ["low", "high"], default_effort: "low" },
  ] },
  { id: "pi", name: "pi", reason: "Sign in to pi", models: [] },
] };
const task = (phase: string) => ({ id: "engine-task", title: "Choose the task engine", project: "code-goblins", harness: "codex", model: "current-model", effort: "xhigh", phase, verified: false, backend: "native", generation: phase === "queued" ? "" : "s1", queue_revision: "q1", brief: true, archived: phase === "done" });

async function open(page: Page, phase: string, posts: unknown[], error = "", values = {}) {
  const snapshot = { instance: "fixture", cfo_runs: true, healthy: true, tasks: [{ ...task(phase), ...values }], revision: 1 };
  await page.addInitScript((initial) => {
    class EngineStream extends EventTarget {
      onerror: (() => void) | null = null;
      constructor() {
        super();
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(initial) })));
        window.addEventListener("fixture-snapshot", (event) => { if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) })); });
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: EngineStream });
  }, snapshot);
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/engines") return route.fulfill({ json: catalog });
    if (path === "/api/tasks/engine") {
      posts.push(route.request().postDataJSON());
      return route.fulfill({ status: error ? 409 : 200, json: error ? { error } : { saved: true, revision: "q2" } });
    }
    if (path === "/api/workspace") return route.fulfill({ json: { repository: "code-goblins", harness: "codex", model: "current-model", root: "C:/work", notes: [] } });
    return route.fulfill({ json: {} });
  });
  await page.goto("/");
  await page.locator(".task-board .task-card").filter({ hasText: "Choose the task engine" }).click();
  // A task with a terminal opens on it; the selectors are on its Task view.
  await expect(page.locator("#panel-title")).toHaveText("Choose the task engine");
  const pill = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await pill.count()) await pill.click();
  const connections = page.locator("details.connections > summary");
  if (await connections.count()) await connections.click();
}

for (const width of [1440, 390]) {
  test.describe(`engine selector at ${width}`, () => {
    test.use({ viewport: { width, height: 1000 } });
    for (const phase of ["queued", "paused"]) {
      test(`${phase} saves the selected engine for its next start`, async ({ page }) => {
        const posts: unknown[] = [];
        await open(page, phase, posts);
        const engine = page.getByRole("group", { name: "Task engine", exact: true });
        await expect(engine.getByRole("combobox", { name: "Harness", exact: true })).toHaveValue("codex");
        await engine.getByRole("combobox", { name: "Model", exact: true }).selectOption("next-model");
        await expect(engine.getByRole("combobox", { name: "Effort", exact: true }).locator("option[value=xhigh]")).toHaveCount(0);
        await engine.getByRole("combobox", { name: "Effort", exact: true }).selectOption("high");
        await engine.getByRole("button", { name: phase === "queued" ? "Save" : "Save for Resume", exact: true }).click();
        await expect.poll(() => posts).toEqual([{ task: "engine-task", generation: phase === "queued" ? "" : "s1", revision: "q1", harness: "codex", model: "next-model", effort: "high", when: "" }]);
        await expect(engine).toContainText(phase === "queued" ? "Saved for Start" : "Saved for Resume");
        const box = await engine.boundingBox();
        expect(box!.x + box!.width).toBeLessThanOrEqual(width);
        if (width === 390) {
          const harnessBox = (await engine.getByRole("combobox", { name: "Harness", exact: true }).boundingBox())!;
          const modelBox = (await engine.getByRole("combobox", { name: "Model", exact: true }).boundingBox())!;
          const effortBox = (await engine.getByRole("combobox", { name: "Effort", exact: true }).boundingBox())!;
          expect(harnessBox.width).toBeGreaterThan(modelBox.width);
          expect(harnessBox.y).toBeLessThan(modelBox.y);
          expect(effortBox.y).toBe(modelBox.y);
        }
      });
    }

    test("running choice defaults to turn end and keeps live values until the new session", async ({ page }) => {
      const posts: unknown[] = [];
      await open(page, "working", posts);
      const engine = page.getByRole("group", { name: "Task engine", exact: true });
      await engine.getByRole("combobox", { name: "Model", exact: true }).selectOption("next-model");
      await engine.getByRole("button", { name: "Apply", exact: true }).click();
      const dialog = page.getByRole("dialog", { name: "Switch this task's engine?" });
      await expect(dialog).toContainText("Uncommitted work stays");
      await expect(dialog.getByRole("button", { name: "Switch when its turn ends" })).toBeFocused();
      expect(posts).toEqual([]);
      await dialog.getByRole("button", { name: "Switch when its turn ends" }).click();
      await expect.poll(() => posts.length).toBe(1);
      expect(posts[0]).toMatchObject({ when: "turn-end", model: "next-model" });
      await expect(engine.getByRole("combobox", { name: "Model", exact: true })).toHaveValue("current-model");
      await page.evaluate((pending) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: { instance: "fixture", cfo_runs: true, healthy: true, revision: 2, tasks: [pending] } })), { ...task("working"), pending_engine: { harness: "codex", model: "next-model", effort: "low", when: "turn-end" } });
      await expect(page.locator(".task-card")).toContainText("Pending: next-model low");
      await page.evaluate((live) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: { instance: "fixture", cfo_runs: true, healthy: true, revision: 3, tasks: [live] } })), { ...task("working"), generation: "s2", model: "next-model", effort: "low" });
      await expect(page.locator(".task-card")).not.toContainText("Pending:");
      await page.locator("details.connections > summary").click();
      await expect(engine.getByRole("combobox", { name: "Model", exact: true })).toHaveValue("next-model");
    });

    test("completed engine is read only and includes its effort", async ({ page }) => {
      const posts: unknown[] = [];
      await open(page, "done", posts);
      const engine = page.getByRole("group", { name: "Task engine", exact: true });
      await expect(engine).toContainText("current-model xhigh");
      await expect(engine.locator("select")).toHaveCount(0);
      expect(posts).toEqual([]);
    });
  });
}

test("unavailable values and failed saves show their reasons", async ({ page }) => {
  const posts: unknown[] = [];
  await open(page, "queued", posts, "The task was edited; reload its card");
  const engine = page.getByRole("group", { name: "Task engine", exact: true });
  await expect(engine.getByRole("combobox", { name: "Harness", exact: true }).locator("option[value=pi]")).toHaveJSProperty("disabled", true);
  await engine.getByRole("combobox", { name: "Model", exact: true }).selectOption("next-model");
  await engine.getByRole("button", { name: "Save", exact: true }).click();
  await expect(engine.getByRole("alert")).toHaveText("The task was edited. Reload its card.");
});

test("switch now needs confirmation and a cancelled dialog sends nothing", async ({ page }) => {
  const posts: unknown[] = [];
  await open(page, "working", posts);
  const engine = page.getByRole("group", { name: "Task engine", exact: true });
  await engine.getByRole("combobox", { name: "Model", exact: true }).selectOption("next-model");
  await engine.getByRole("button", { name: "Apply", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Switch this task's engine?" });
  await expect(dialog).toContainText("Switch now interrupts its turn and any running gate step");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(posts).toEqual([]);
  await expect(engine.getByRole("button", { name: "Apply", exact: true })).toBeFocused();
  await engine.getByRole("button", { name: "Apply", exact: true }).click();
  await dialog.getByRole("button", { name: "Switch now", exact: true }).click();
  await expect.poll(() => posts).toEqual([{ task: "engine-task", generation: "s1", revision: "q1", harness: "codex", model: "next-model", effort: "low", when: "now" }]);
  await expect(engine.getByRole("combobox", { name: "Model", exact: true })).toHaveValue("current-model");
});

test("pending turn-end choice can be cancelled", async ({ page }) => {
  const posts: unknown[] = [];
  await open(page, "working", posts, "", { pending_engine: { harness: "codex", model: "next-model", effort: "low", when: "turn-end" } });
  const engine = page.getByRole("group", { name: "Task engine", exact: true });
  await engine.getByRole("button", { name: "Cancel pending", exact: true }).click();
  await expect.poll(() => posts).toEqual([{ task: "engine-task", generation: "s1", revision: "q1", harness: "codex", model: "current-model", effort: "xhigh", when: "cancel" }]);
  await page.evaluate((live) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: { instance: "fixture", cfo_runs: true, healthy: true, revision: 2, tasks: [live] } })), task("working"));
  await expect(engine).not.toContainText("Pending:");
  await expect(engine.getByRole("combobox", { name: "Model", exact: true })).toHaveValue("current-model");
});

test("current unavailable model is preserved until a valid choice is made", async ({ page }) => {
  const posts: unknown[] = [];
  await open(page, "paused", posts, "", { model: "missing-model" });
  const engine = page.getByRole("group", { name: "Task engine", exact: true });
  await expect(engine.getByRole("combobox", { name: "Model", exact: true })).toHaveValue("missing-model");
  await expect(engine).toContainText("Model missing-model is unavailable");
  await expect(engine.getByRole("combobox", { name: "Effort", exact: true })).toBeDisabled();
  expect(posts).toEqual([]);
  await engine.getByRole("combobox", { name: "Model", exact: true }).selectOption("current-model");
  await expect(engine.getByRole("button", { name: "Save for Resume", exact: true })).toBeEnabled();
});

test("older completed tasks report their missing engine without fetching options", async ({ page }) => {
  const posts: unknown[] = [];
  const engines: string[] = [];
  page.on("request", (request) => { if (request.url().endsWith("/api/engines")) engines.push(request.url()); });
  await open(page, "done", posts, "", { harness: "", model: "", effort: "" });
  const engine = page.getByRole("group", { name: "Task engine", exact: true });
  await expect(engine).toHaveText("Engine not recorded");
  expect(engines).toEqual([]);
  expect(posts).toEqual([]);
});
