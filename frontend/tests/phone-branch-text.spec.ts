import { expect, holdStream, type Page, test } from "./site";

// The Overlord, 2026-11-11, of the Orchestration tab on his phone: "Where it
// says dispatched and spawned in the orchestration tab on mobile ... It
// shouldn't have that. Just remove that text on the branch on mobile." The
// branch lines and the cards stay, and the desktop canvas is as it was. The
// supervisor is played by one held snapshot: a goblin the CEO dispatched, one
// a session reports it spawned, one whose task names no session, and a
// helper.
const since = new Date(Date.now() - 42 * 60_000).toISOString();
const task = (id: string, name: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, goblin_name: name, goblin_title: title, project: "northwind-api", phase: "working", verified: false, generation: "g1", harness: "claude", since, ...fields });
const session = (id: string, role: string, parent: string, relation: string, taskID = "") => ({ id, native_id: id, harness: "claude", role, task_id: taskID, generation: taskID ? "g1" : "", parent, relation, phase: "working", updated_at: since });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [
    session("s-cfo", "cfo", "", ""),
    session("s-sync", "goblin", "", "", "nw-sync"),
    session("s-export", "goblin", "s-cfo", "spawned", "nw-export"),
    session("s-fixtures", "goblin", "s-cfo", "spawned", "nw-fixtures"),
  ],
  tasks: [
    task("nw-sync", "Jerry", "Code Designer", { session: "s-sync" }),
    task("nw-export", "Mabel", "Bug Hunter", { session: "s-export" }),
    task("nw-rates", "Otis", "Speed Tuner"),
    task("nw-fixtures", "Pip", "Rail Fitter", { session: "s-fixtures", parent: "nw-export" }),
  ],
};
const GOBLINS = ["Jerry - Code Designer", "Mabel - Bug Hunter", "Otis - Speed Tuner", "Pip - Rail Fitter"];
const BRANCH_WORDS = /Dispatched by|spawned|Helper goblin|Session details|Reported child|Parent unreported|Parent unknown/i;

async function openOrchestration(page: Page) {
  await holdStream(page, SNAPSHOT);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
}

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the lineage list prints no words on any branch and keeps every card", async ({ page }) => {
    // Arrange
    await openOrchestration(page);
    const list = page.getByRole("region", { name: "Reported workflow" });

    // Act
    await expect(list.locator(".workflow-node")).toHaveCount(5);

    // Assert
    for (const name of GOBLINS) await expect(list.locator(".workflow-node").filter({ hasText: name })).toHaveCount(1);
    await expect(list.locator(".node-group > :not(.workflow-node)")).toHaveCount(0);
    await expect(list.getByText(BRANCH_WORDS)).toHaveCount(0);
  });

  test("each child's rail reaches its turn, and the turn runs clear of the parent's disclosure button", async ({ page }) => {
    // Arrange
    await openOrchestration(page);
    const list = page.getByRole("region", { name: "Reported workflow" });
    await expect(list.locator(".workflow-node")).toHaveCount(5);

    // Act
    const misses = await list.locator(".workflow-children > .workflow-branch").evaluateAll((branches) => branches.flatMap((branch) => {
      const node = branch.querySelector(":scope > .node-group > .workflow-node")!;
      const name = node.querySelector(".card-title")?.textContent;
      const turn = getComputedStyle(node, "::after"), rail = getComputedStyle(branch, "::before");
      const turnTop = node.getBoundingClientRect().top + node.clientTop + parseFloat(turn.top);
      const railEnd = branch.getBoundingClientRect().top + parseFloat(rail.top) + parseFloat(rail.height);
      const disclosure = branch.parentElement!.closest(".workflow-branch")!.querySelector(":scope > .node-group .node-disclosure")!.getBoundingClientRect();
      const isFirst = !branch.previousElementSibling;
      return [
        ...(rail.content === "none" || railEnd < turnTop ? [`${name}: its rail stops above its turn`] : []),
        ...(isFirst && turnTop <= disclosure.bottom ? [`${name}: its turn crosses the disclosure button above it`] : []),
      ];
    }));

    // Assert
    expect(misses).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  });
});

test.describe("on a desktop", () => {
  test.use({ viewport: { width: 1440, height: 1000 } });

  test("the Orchestration view stays the canvas, with a line to every card", async ({ page }) => {
    // Arrange
    await openOrchestration(page);

    // Act
    const canvas = page.locator(".flow-node");

    // Assert
    await expect(canvas).toHaveCount(5);
    for (const name of GOBLINS) await expect(canvas.filter({ hasText: name })).toHaveCount(1);
    await expect(page.locator(".connection-line")).toHaveCount(4);
    await expect(page.getByRole("region", { name: "Reported workflow" })).toHaveCount(0);
  });
});
