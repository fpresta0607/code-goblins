import { expect, holdStream, openTaskList, type Page, test } from "./site";

// The Overlord, 2026-10-17, of Frank's terminal header (his task's long
// description, Waiting on the CFO, and VS Code and folder on a row of their
// own) beside Wanda's working one: "keep the same terminal header structure
// that we have for the CFO, even for goblins: the goblin image, then the name
// and title, and then the status of what it's doing. I do like the way the
// working status looks for in-progress goblins." A goblin's Terminal view
// heads with its picture, its name and title, its task id and one status
// line, in every state, with no description and no buttons but the panel's
// Chat and Task switch, which sits in the header's row (the Overlord,
// 2026-10-28: "chat and task doesn't need to be full width, it can fit inline
// with cfo and afk and just be the icons"): the rest are in its Task view.
// Since 2026-10-29 the header is one short row ("Minimize height and maximize
// the space"), its picture, name and task id beside the switch, and its
// status line is the first thing in its Task view: the working line in the
// conversation says what it does. The supervisor is played by one held
// snapshot.
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
  test(`a goblin ${state} heads its panel with its picture, name and task id on one row, and says its status atop its Task view`, async ({ page }, testInfo) => {
    // Arrange
    const header = await openTerminal(page, { ...FRANK, ...fields });

    // Act
    await header.screenshot({ path: testInfo.outputPath(`terminal-header-${state.replaceAll(" ", "-")}.png`) });
    const parts = await header.evaluate((element) => [".goblin-avatar", "#panel-title", ".panel-goblin-task"].map((selector) => {
      const box = element.querySelector<HTMLElement>(selector)!.getBoundingClientRect();
      return { top: box.top, bottom: box.bottom, middle: box.top + box.height / 2 };
    }));
    const details = await openTaskList(page);

    // Assert: the row holds no status, no description and no buttons but
    // the switch and the panel's own controls.
    await expect(header.locator(".goblin-avatar")).toBeVisible();
    await expect(header.locator("#panel-title")).toHaveText("Frank - Chat Captain");
    await expect(header.locator(".panel-goblin-task")).toHaveText(FRANK.id);
    await expect(header.locator(".panel-status")).toHaveCount(0);
    await expect(details.locator(".panel-status")).toHaveText(status);
    await expect(details.locator(".panel-status")).toHaveClass(new RegExp("phase-" + phase));
    await expect(header).not.toContainText(FRANK.title);
    await expect(header.locator(".panel-actions, .panel-activity, .project-line, a")).toHaveCount(0);
    await expect(header.locator(".panel-identity").getByRole("button")).toHaveCount(0);
    await expect(header.getByRole("group", { name: "Panel view" }).getByRole("button")).toHaveCount(2);
    for (const part of parts.slice(1)) {
      expect(part.middle, "on the picture's row").toBeGreaterThan(parts[0].top);
      expect(part.middle, "on the picture's row").toBeLessThan(parts[0].bottom);
    }
  });
}

test("the task list keeps the compact task id and holds VS Code, the folder, the ticket and the pull request", async ({ page }) => {
  // Arrange
  await openTerminal(page, FRANK);

  // Act
  const header = await openTaskList(page);

  // Assert
  await expect(page.locator(".panel-header .panel-goblin-task")).toHaveText(FRANK.id);
  await expect(header.locator(".panel-actions").getByRole("button", { name: "Open in VS Code" })).toBeVisible();
  await expect(header.locator(".panel-actions").getByRole("button", { name: "Open folder" })).toBeVisible();
  await expect(header.locator(".panel-actions a.ticket-link, .panel-actions a[href$='/issues/88']")).toHaveCount(1);
  await expect(header.locator(".panel-actions a[href='" + REPO + "/pull/91']")).toHaveCount(1);
});

for (const width of [390, 1000]) {
  test(`the task list at ${width} px links to the goblin being awaited`, async ({ page }) => {
    await page.setViewportSize({ width: width === 390 ? 390 : 1707, height: 1067 });
    await page.addInitScript((width) => localStorage.setItem("cfo-pane-width", String(width)), width);
    await openTerminal(page, { ...FRANK, phase: "waiting", waiting_on: WANDA.id });

    const header = await openTaskList(page);
    await header.getByRole("button", { name: "Open Wanda - Map Maker, which this goblin is waiting on" }).click();

    await expect(page.locator("#panel-title")).toHaveText("Wanda - Map Maker");
    await expect(page.locator(".panel-goblin-task")).toHaveText(WANDA.id);
  });
}
