import { expect, holdStream, test, type Page } from "./site";

// The panel of anything but the CFO has Back where Close was: an icon button
// with its arrow, named Back to the CFO, which returns the panel to the CFO's
// panel on the view that panel last showed. It is an icon alone since the
// header is one row (2026-10-29). The CFO's own panel keeps Close. Escape does
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
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
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

test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

for (const id of ["queued-one", "working-one", "paused-one", "done-one"]) {
  test(`the panel of ${id} has Back in place of Close, and Back shows the CFO's panel`, async ({ page }) => {
    await open(page);
    await card(page, id).click();
    await expect(title(page)).toHaveText(id);
    await expect(back(page)).toHaveText("");
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

// The Overlord, 2026-10-29: "You could even just click on the CFO in the top,
// the CFO avatar, and that should take us back to the home screen." A task's
// picture is only a picture.
test("the CFO's picture in its header goes back to the board, maximized or not, and a task's does nothing", async ({ page }) => {
  // Arrange
  await open(page);
  const home = page.locator(".goblin-panel > .panel-header").getByRole("button", { name: "Home", exact: true });
  for (const isMaximized of [false, true]) {
    await page.keyboard.press("Control+Alt+1");
    await expect(title(page)).toHaveText("CFO");
    if (isMaximized) await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();
    await expect(page.locator(".canvas-region")).toBeVisible({ visible: !isMaximized });

    // Act
    await home.locator(".goblin-avatar").click();

    // Assert
    await expect(page.locator(".context-pane")).toBeHidden();
    await expect(page.locator(".canvas-region")).toBeVisible();
  }
  await card(page, "working-one").click();
  await expect(title(page)).toHaveText("working-one");
  await expect(home).toHaveCount(0);
});

// The header's identity cuts what overflows it, so Home's focus ring, its
// outline grown out by its width and offset, must lie inside the identity.
test("the CFO's Home draws its whole focus ring inside the header's identity", async ({ page }) => {
  // Arrange
  await open(page);
  await page.keyboard.press("Control+Alt+1");
  await expect(title(page)).toHaveText("CFO");
  const home = page.locator(".goblin-panel > .panel-header").getByRole("button", { name: "Home", exact: true });

  // Act
  await home.focus();

  // Assert
  const ring = await home.evaluate((button) => {
    const style = getComputedStyle(button);
    const grown = parseFloat(style.outlineWidth) + parseFloat(style.outlineOffset);
    const box = button.getBoundingClientRect(), identity = button.closest(".panel-identity")!.getBoundingClientRect();
    return {
      isFocusVisible: button.matches(":focus-visible"), outline: style.outlineStyle,
      past: Math.max(identity.left - (box.left - grown), identity.top - (box.top - grown), box.right + grown - identity.right, box.bottom + grown - identity.bottom),
    };
  });
  expect(ring.isFocusVisible).toBe(true);
  expect(ring.outline).toBe("solid");
  expect(ring.past).toBeLessThanOrEqual(0);
});

for (const id of ["working-one", "paused-one"]) {
  test(`a card opens the panel of ${id} beside the board, one view with no pill`, async ({ page }) => {
    await open(page);
    await card(page, id).click();
    await expect(title(page)).toHaveText(id);
    await expect(page.locator(".panel-pill")).toHaveCount(0);
    await expect(page.locator(".canvas-region")).toBeVisible();
    await back(page).click();
    await expect(title(page)).toHaveText("CFO");
    await expect(page.locator(".panel-pill")).toHaveCount(0);
  });
}

test("Back returns to the CFO's panel, and a maximized panel stays maximized", async ({ page }) => {
  await open(page);
  await page.keyboard.press("Control+Alt+1");
  await expect(title(page)).toHaveText("CFO");
  await card(page, "working-one").click();
  await expect(title(page)).toHaveText("working-one");
  await back(page).click();
  await expect(title(page)).toHaveText("CFO");
  await expect(page.locator(".canvas-region")).toBeVisible();

  // Now the CFO's panel is maximized over the board.
  await page.getByRole("button", { name: "Maximize the panel", exact: true }).click();
  await expect(page.locator(".canvas-region")).toBeHidden();
  await page.keyboard.press("Control+Alt+2");
  await expect(title(page)).toHaveText("working-one");
  await expect(page.locator(".canvas-region")).toBeHidden();
  await back(page).click();
  await expect(title(page)).toHaveText("CFO");
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
