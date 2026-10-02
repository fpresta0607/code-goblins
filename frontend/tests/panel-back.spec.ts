import { expect, holdStream, test, type Page } from "./site";

// The panel of anything but the CFO has Back where Close was: a button with
// its arrow and its name, which returns the panel to the CFO's panel on the
// view that panel last showed. The CFO's own panel keeps Close. Escape does
// what the button in the corner does.
const since = "2026-10-02T09:00:00Z";
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since, ...fields });
const BOARD = [
  task("queued-one", "queued", { generation: "", brief: true, queue_revision: "q1" }),
  task("working-one", "working", { harness: "codex" }),
  task("paused-one", "paused", { at: since }),
  task("finished:done-one", "done", { title: "done-one", archived: true, merged: true, verified: true, generation: "", branch: "fix/done-one", at: since }),
];

async function open(page: Page) {
  const snapshot = { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], tasks: BOARD };
  await holdStream(page, snapshot);
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/workspace") await route.fulfill({ json: { repository: "code-goblins", root: "C:/work/code-goblins", harness: "codex", notes: [] } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
}

// A card on the board: the CFO's panel lists the queued tasks too.
const card = (page: Page, id: string) => page.locator(".task-board .task-card-shell").filter({ has: page.locator(".card-title").getByText(id, { exact: true }) }).locator(".task-card");
const title = (page: Page) => page.locator("#panel-title");
const back = (page: Page) => page.getByRole("button", { name: "Back to the CFO", exact: true });
const close = (page: Page) => page.getByRole("button", { name: "Close panel", exact: true });
const pill = (page: Page, view: "Task" | "Terminal") => page.locator(".panel-pill").getByRole("button", { name: view, exact: true });

test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

for (const id of ["queued-one", "working-one", "paused-one", "done-one"]) {
  test(`the panel of ${id} has Back in place of Close, and Back shows the CFO's panel`, async ({ page }) => {
    await open(page);
    await card(page, id).click();
    await expect(title(page)).toHaveText(id);
    await expect(back(page)).toHaveText("Back");
    await expect(back(page).locator("svg")).toBeVisible();
    await expect(close(page)).toHaveCount(0);
    await back(page).click();
    await expect(title(page)).toHaveText("CFO");
    await expect(close(page)).toBeVisible();
    await expect(back(page)).toHaveCount(0);
    // The keyboard is back on the card the panel was opened from.
    await expect(card(page, id)).toBeFocused();
  });
}

test("Escape goes back from a task's panel, and closes the CFO's", async ({ page }) => {
  await open(page);
  await card(page, "working-one").click();
  await expect(title(page)).toHaveText("working-one");
  await page.keyboard.press("Escape");
  await expect(title(page)).toHaveText("CFO");
  await expect(page.locator(".context-pane")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".context-pane")).toBeHidden();
});

test("Back returns to the view the CFO's panel last showed, and a maximized panel stays maximized", async ({ page }) => {
  await open(page);
  // The CFO's panel opens on its terminal; its Task view shows beside the board.
  await page.keyboard.press("Control+Alt+1");
  await expect(title(page)).toHaveText("CFO");
  await pill(page, "Task").click();
  await card(page, "working-one").click();
  await expect(title(page)).toHaveText("working-one");
  await back(page).click();
  await expect(title(page)).toHaveText("CFO");
  await expect(pill(page, "Task")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".canvas-region")).toBeVisible();

  // Now the CFO's panel last showed its terminal, which covers the board.
  await pill(page, "Terminal").click();
  await expect(page.locator(".canvas-region")).toBeHidden();
  await page.keyboard.press("Control+Alt+2");
  await expect(title(page)).toHaveText("working-one");
  await expect(page.locator(".canvas-region")).toBeHidden();
  await back(page).click();
  await expect(title(page)).toHaveText("CFO");
  await expect(pill(page, "Terminal")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".canvas-region")).toBeHidden();
  await expect(close(page)).toBeVisible();
});

test("on Orchestration a goblin's panel has Back, and the CFO's has Close", async ({ page }) => {
  await open(page);
  await card(page, "working-one").click();
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await expect(title(page)).toHaveText("working-one");
  await expect(close(page)).toHaveCount(0);
  await back(page).click();
  await expect(title(page)).toHaveText("CFO");
  await expect(close(page)).toBeVisible();
  await expect(back(page)).toHaveCount(0);
});

test("the panel with nothing chosen keeps Close", async ({ page }) => {
  await open(page);
  await expect(page.getByRole("heading", { name: "Review the work" })).toBeVisible();
  await expect(close(page)).toBeVisible();
  await expect(back(page)).toHaveCount(0);
});
