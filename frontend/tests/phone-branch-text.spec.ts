import { expect, type Page, test } from "./site";

// The Overlord, 2026-11-11, of the Orchestration tab on his phone: "Where it
// says dispatched and spawned in the orchestration tab on mobile ... It
// shouldn't have that. Just remove that text on the branch on mobile." The
// branch lines and the cards stay, and the desktop canvas is as it was. The
// supervisor is played by a held snapshot: a goblin the CEO dispatched, one
// a session reports it spawned, one whose task names no session, and a
// helper its goblin spawned.
const since = new Date(Date.now() - 42 * 60_000).toISOString();
const task = (id: string, name: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, goblin_name: name, goblin_title: title, project: "northwind-api", phase: "working", verified: false, generation: "g1", harness: "claude", since, ...fields });
const session = (id: string, role: string, parent: string, relation: string, taskID = "") => ({ id, native_id: id, harness: "claude", role, task_id: taskID, generation: taskID ? "g1" : "", parent, relation, phase: "working", updated_at: since });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [
    session("s-cfo", "cfo", "", ""),
    session("s-sync", "goblin", "", "", "nw-sync"),
    session("s-export", "goblin", "s-cfo", "spawned", "nw-export"),
    session("s-fixtures", "goblin", "s-export", "spawned", "nw-fixtures"),
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

// openOrchestration holds the stream open on SNAPSHOT and returns what hands
// the board a later snapshot on it.
async function openOrchestration(page: Page) {
  await page.addInitScript((first) => {
    class HeldStream extends EventTarget {
      onerror: (() => void) | null = null;
      constructor() {
        super();
        const publish = (data: string) => this.dispatchEvent(new MessageEvent("snapshot", { data }));
        window.addEventListener("fixture-snapshot", (event) => { if (event instanceof CustomEvent) publish(event.detail); });
        setTimeout(() => publish(first));
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: HeldStream });
  }, JSON.stringify(SNAPSHOT));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  return (next: object) => page.evaluate((data) => { window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: data })); }, JSON.stringify(next));
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

  test("each child's rail reaches its turn, the last child's rail and activity end there, and the turn runs clear of the parent's disclosure button", async ({ page }) => {
    // Arrange: Mabel spawns Pip, her only child, and writes to it, which
    // lights Pip's branch. It is measured once Pip's card has come in.
    const publish = await openOrchestration(page);
    const list = page.getByRole("region", { name: "Reported workflow" });
    await expect(list.locator(".workflow-node")).toHaveCount(5);
    const at = new Date(Date.now() - 1000).toISOString();
    const receipt = (id: string, kind: string) => ({ id, kind, task_id: "nw-fixtures", generation: "g1", source: "s-export", target: "s-fixtures", state: "accepted", at });
    await publish({ ...SNAPSHOT, revision: 2, activity: [receipt("pip-born", "created"), receipt("pip-told", "message")] });
    const effects = list.locator(".workflow-children > .workflow-branch:last-child > :is(.compact-creation-highlight, .compact-communication-pulse)");
    await expect(effects).toHaveCount(2);
    await list.locator(".node-enter").evaluate((card) => Promise.all(card.getAnimations().map((animation) => animation.finished)));

    // Act
    const misses = await list.locator(".workflow-children > .workflow-branch").evaluateAll((branches) => branches.flatMap((branch) => {
      const node = branch.querySelector(":scope > .node-group > .workflow-node")!;
      const name = node.querySelector(".card-title")?.textContent;
      const turn = getComputedStyle(node, "::after"), rail = getComputedStyle(branch, "::before");
      const turnTop = node.getBoundingClientRect().top + node.clientTop + parseFloat(turn.top);
      const railEnd = branch.getBoundingClientRect().top + parseFloat(rail.top) + parseFloat(rail.height);
      const disclosure = branch.parentElement!.closest(".workflow-branch")!.querySelector(":scope > .node-group .node-disclosure")!.getBoundingClientRect();
      const isFirst = !branch.previousElementSibling, isLast = !branch.nextElementSibling;
      const effectsPastTurn = [...branch.querySelectorAll(":scope > .compact-creation-highlight, :scope > .compact-communication-pulse")]
        .filter((effect) => isLast && effect.getBoundingClientRect().bottom > turnTop + 1.5);
      return [
        ...(rail.content === "none" || railEnd < turnTop ? [`${name}: its rail stops above its turn`] : []),
        ...(isLast && railEnd > turnTop + 1.5 ? [`${name}: its rail runs on past its turn`] : []),
        ...(isFirst && turnTop <= disclosure.bottom ? [`${name}: its turn crosses the disclosure button above it`] : []),
        ...effectsPastTurn.map((effect) => `${name}: its ${effect.className} runs on past its turn`),
      ];
    }));

    // Assert
    expect(misses).toEqual([]);
    await expect(effects, "Pip's branch is still lit after it was measured").toHaveCount(2);
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
