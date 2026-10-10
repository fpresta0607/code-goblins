import { expect, holdStream, type Locator, type Page } from "./site";

// The caret a summary draws as a person sees it once it has finished turning:
// its shape, its size, its color, its weight and how far it is turned.
export const caret = (summary: Locator) => summary.evaluate(async (element) => {
  const icon = element.querySelector(":scope > svg");
  if (!icon) return null;
  await Promise.all(icon.getAnimations().map((animation) => animation.finished));
  const style = getComputedStyle(icon);
  return { shape: icon.innerHTML, width: style.width, height: style.height, color: style.color, weight: style.strokeWidth, turn: style.transform };
});

// The summaries under a part of the board that draw the browser's own
// triangle, by what they say.
export const nativeMarkers = (scope: Locator) => scope.locator("summary").evaluateAll((summaries) => summaries.filter((element) => {
  const style = getComputedStyle(element);
  return style.display === "list-item" && style.listStyleType !== "none";
}).map((element) => element.textContent));

// The caret before Workspace in a goblin's task panel, closed and open: the
// one every other part of the board that opens and closes is held to.
export async function panelCaret(page: Page) {
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
    tasks: [{ id: "caret-one", title: "caret-one", project: "code-goblins", phase: "working", verified: false, generation: "caret-one-1", since: "2026-10-09T09:00:00Z" }] });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await page.locator(".task-card").filter({ hasText: "caret-one" }).first().click();
  const pill = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
  if (await pill.count()) await pill.click();
  const workspace = page.locator(".goblin-panel .panel-content > details").filter({ has: page.locator("> summary", { hasText: "Workspace" }) }).locator("> summary");
  const closed = await caret(workspace);
  await workspace.click();
  const opened = await caret(workspace);
  expect(closed).not.toBeNull();
  expect(opened?.turn).not.toBe(closed?.turn);
  return { closed, opened };
}
