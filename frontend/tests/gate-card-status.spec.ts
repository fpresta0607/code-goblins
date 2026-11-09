import { expect, test } from "./site";
import { open, snapshot } from "./session-board";

for (const width of [390, 1440]) {
  test(`gate decisions and worker activity stay distinct across cards at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const task = { goblin_name: "Hugo", goblin_title: "Build Tamer", phase: "blocked", gate_step: "rebase", reason: "Pipeline decision required at rebase; use cfo pipeline respond", report: "working", runtime: { state: "busy" } };
    await open(page, snapshot({}, task));
    const board = page.locator(".task-board .task-card-shell").filter({ hasText: "Hugo - Build Tamer" });
    await expect(board.locator(".card-status-text")).toHaveText("Waiting on the CFO: rebase gate");
    await expect(board.locator(".card-status-text")).toHaveAttribute("data-tip", task.reason);
    await expect(board.locator(".card-activity")).toHaveText("Terminal active · Last report: working");

    await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    const orchestration = page.locator(width > 640 ? ".flow-node .task-card-shell" : ".workflow-node .task-card-shell").filter({ hasText: "Hugo - Build Tamer" });
    await expect(orchestration.locator(".card-status-text")).toHaveText("Waiting on the CFO: rebase gate");
    await expect(orchestration.locator(".card-activity")).toHaveText("Terminal active · Last report: working");

    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2 }, { ...task, phase: "review", gate_step: "test", reason: "Pipeline evidence is incomplete or belongs to a different HEAD", runtime: { state: "parked" } }));

    await expect(orchestration.locator(".card-status-text")).toHaveText("In review gate: tests");
    await expect(orchestration.locator(".card-activity")).toHaveText("Terminal awaiting input · Last report: working");
    await page.getByRole("button", { name: "Board", exact: true }).click();
    await expect(board.locator(".card-status-text")).toHaveText("In review gate: tests");
    await expect(board.locator(".card-activity")).toHaveText("Terminal awaiting input · Last report: working");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}
