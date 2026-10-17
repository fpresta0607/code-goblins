import { expect, holdStream, test, type Page } from "./site";

// The Overlord, 2026-10-17, of cards carrying a "claude-opus-5-5 · high" line
// and an "explicit choice" line: "when you hover the icon, it should tell you
// the model that it's using. You don't have to have it displayed on it. It's
// just too redundant, too wordy." A goblin's card and its canvas node show no
// model or route line, the harness mark's tip names the model, and a paused
// card says only what it waits for. Where the card's time sits is
// minimal-card.spec.ts's.
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const task = (id: string, phase: string, fields: Record<string, unknown> = {}) => ({
  id, title: id, project: "code-goblins", phase, verified: false, generation: id + "-1", since: ago(95), session: "claude/" + id, harness: "claude",
  model: "claude-opus-5-5", effort: "xhigh", route_lane: "strong", route_reason: "explicit choice", ...fields,
});
const WORKING = task("working-one", "working", { pr: "https://github.com/o/r/pull/12" });
const PAUSED = task("paused-one", "paused", {
  at: ago(30),
  pending_engine: { harness: "claude", model: "claude-sonnet-5-5", effort: "high", when: "resume" },
  lifecycle: { phase: "paused", action: "pause", at: ago(30), kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "dependency", until: "task:working-one", at: ago(30) } },
});
const SESSIONS = [WORKING, PAUSED].map((goblin) => ({ id: goblin.session, native_id: goblin.id, harness: "claude", role: "goblin", task_id: goblin.id, generation: goblin.generation, phase: goblin.phase === "paused" ? "ended" : "active" }));

async function open(page: Page) {
  await page.clock.setFixedTime(now);
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, revision: 1, attention: [], tasks: [WORKING, PAUSED], sessions: SESSIONS });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}
const card = (page: Page, id: string) => page.locator(".task-board .task-card-shell").filter({ has: page.locator(".card-title").getByText(id, { exact: true }) });

test("a goblin's card and canvas node show no model or route line, and its harness mark's tip names the model", async ({ page }) => {
  // Arrange
  await open(page);
  const working = card(page, "working-one");

  // Act
  await working.locator(".card-harness [role=img]").hover();

  // Assert
  await expect(working).not.toContainText("claude-opus-5-5");
  await expect(working).not.toContainText("explicit choice");
  await expect(page.getByRole("tooltip")).toHaveText("Claude Code · claude-opus-5-5 · xhigh");
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  const node = page.locator(".flow-node").filter({ has: page.locator("strong").getByText("working-one", { exact: true }) });
  await expect(node).toBeVisible();
  await expect(node).not.toContainText("claude-opus-5-5");
  await expect(node).not.toContainText("explicit choice");
});

test("a paused card says only what it waits for, with no resume line", async ({ page }) => {
  // Arrange and act
  await open(page);
  const paused = card(page, "paused-one");

  // Assert
  await expect(paused.locator(".card-status-text")).toHaveText("Paused until working-one delivers");
  await expect(paused).not.toContainText(/resume/i);
  await expect(paused).not.toContainText("claude-sonnet-5-5");
});
