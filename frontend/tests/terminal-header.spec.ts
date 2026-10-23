import { expect, holdStream, openTaskList, type Page, test } from "./site";

// The Overlord, 2026-10-17, of Frank's terminal header (his task's long
// description, Waiting on the CFO, and VS Code and folder on a row of their
// own) beside Wanda's working one: "keep the same terminal header structure
// that we have for the CFO, even for goblins: the goblin image, then the name
// and title, and then the status of what it's doing. I do like the way the
// working status looks for in-progress goblins." A goblin's Terminal view
// heads with its picture, its name and title, its task id and one status
// line, in every state, with no description and no buttons: those are in its
// Task view. The supervisor is played by one held snapshot.
const REPO = "https://github.com/fpresta0607/household-assistant";
const at = new Date(Date.now() - 20 * 60_000).toISOString();
const FRANK = {
  id: "ha-steward-reply-scroll", title: "In Steward a reply appears whole with its top in view, a sent message lands smoothly, and short replies stay put", project: "household-assistant",
  goblin_name: "Frank", goblin_title: "Chat Captain", harness: "claude", model: "claude-opus-5-5", phase: "working", verified: false, generation: "s-ha-steward-reply-scroll", since: at,
  ticket: { number: 88, url: REPO + "/issues/88", state: "in progress" }, pr: REPO + "/pull/91",
};
const WANDA = { id: "ha-pantry-map", title: "The pantry map shows each shelf", project: "household-assistant", goblin_name: "Wanda", goblin_title: "Map Maker", harness: "claude", phase: "working", verified: false, generation: "s-ha-pantry-map", since: at };
const pause = { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "overlord", at } };
const STATES: [string, Record<string, unknown>, string, string][] = [
  ["working", {}, "Working", "working"],
  ["waiting on CI", { phase: "waiting", waiting_on: "ci" }, "Waiting on CI", "waiting"],
  ["waiting on the CFO", { phase: "waiting", waiting_on: "overlord" }, "Waiting on the CFO", "waiting"],
  ["waiting on another goblin", { phase: "waiting", waiting_on: WANDA.id }, "Waiting on Wanda - Map Maker", "waiting"],
  ["paused", { phase: "paused", at, lifecycle: pause }, "Paused by you", "paused"],
  ["resuming", { phase: "resuming" }, "Resuming", "resuming"],
  ["done", { phase: "done", verified: true }, "Delivered", "done"],
];

async function openTerminal(page: Page, goblin: Record<string, unknown>) {
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], tasks: [goblin, WANDA] });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await page.locator(".task-card").filter({ hasText: "Frank" }).click();
  await page.evaluate(() => document.fonts.ready);
  return page.locator(".panel-header");
}

test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

for (const [state, fields, status, phase] of STATES) {
  test(`a goblin ${state} heads its terminal with its picture, name, task id and one status line, and no buttons`, async ({ page }, testInfo) => {
    // Arrange
    const header = await openTerminal(page, { ...FRANK, ...fields });

    // Act
    await header.screenshot({ path: testInfo.outputPath(`terminal-header-${state.replaceAll(" ", "-")}.png`) });
    const rows = await header.evaluate((element) => ["#panel-title", ".panel-goblin-task", ".panel-status"].map((selector) => {
      const part = element.querySelector<HTMLElement>(selector)!, box = part.getBoundingClientRect();
      return { top: Math.round(box.top), bottom: Math.round(box.bottom), lines: Math.round(box.height / parseFloat(getComputedStyle(part).lineHeight)) };
    }));

    // Assert
    await expect(header.locator(".goblin-avatar")).toBeVisible();
    await expect(header.locator("#panel-title")).toHaveText("Frank - Chat Captain");
    await expect(header.locator(".panel-goblin-task")).toHaveText(FRANK.id);
    await expect(header.locator(".panel-status")).toHaveText(status);
    await expect(header.locator(".panel-status")).toHaveClass(new RegExp("phase-" + phase));
    await expect(header).not.toContainText(FRANK.title);
    await expect(header.locator(".panel-actions, .panel-activity, .project-line, button, a")).toHaveCount(0);
    expect(rows.map((row) => row.lines)).toEqual([1, 1, 1]);
    expect(rows[1].top).toBeGreaterThanOrEqual(rows[0].bottom - 1);
    expect(rows[2].top).toBeGreaterThanOrEqual(rows[1].bottom - 1);
  });
}

test("the Task view still names the task and holds VS Code, the folder, the ticket and the pull request", async ({ page }) => {
  // Arrange
  await openTerminal(page, FRANK);

  // Act
  await openTaskList(page);
  const header = page.locator(".panel-header");

  // Assert
  await expect(header.locator(".panel-goblin-task")).toHaveText(FRANK.title);
  await expect(header.locator(".panel-actions").getByRole("button", { name: "Open in VS Code" })).toBeVisible();
  await expect(header.locator(".panel-actions").getByRole("button", { name: "Open folder" })).toBeVisible();
  await expect(header.locator(".panel-actions a.ticket-link, .panel-actions a[href$='/issues/88']")).toHaveCount(1);
  await expect(header.locator(".panel-actions a[href='" + REPO + "/pull/91']")).toHaveCount(1);
});

test("the Task view of a goblin waiting on another goblin still links to that goblin from its status", async ({ page }) => {
  // Arrange
  await openTerminal(page, { ...FRANK, phase: "waiting", waiting_on: WANDA.id });

  // Act
  await openTaskList(page);

  // Assert
  await expect(page.locator(".panel-header .panel-status").getByRole("button", { name: "Open Wanda - Map Maker, which this goblin is waiting on" })).toBeVisible();
});
