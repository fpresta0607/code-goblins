import { expect, holdStream, test, type Page } from "./site";
import { parseSnapshot } from "../src/types";
import { treeFleet } from "./fixtures/tree-branches-fleet";

test("Arrange keeps a mixed fleet and its subagents visible beside the CFO panel", async ({ page }, testInfo) => {
  // Arrange
  const snapshot = parseSnapshot(treeFleet(Date.now()));
  snapshot.sessions = [];
  snapshot.tasks[3].parent = "fleet";
  for (let index = 0; index < 13; index++) snapshot.tasks.push({ ...snapshot.tasks[3], id: "extra-" + index, title: "Review task " + (index + 1), parent: "", tree: undefined });
  snapshot.tasks[0].tree!.children.push({ ...snapshot.tasks[0].tree!.children[0], id: "shell:wait", kind: "shell", label: "Wait for checks", task: "sleep 2400" });
  await page.setViewportSize({ width: 1707, height: 1067 });
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await holdStream(page, snapshot);
  await page.route("**/api/**", route => route.fulfill({ status: 404, json: { error: "Fixture resource" } }));
  await page.goto("/");
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await page.evaluate(() => document.fonts.ready);
  await page.mouse.move(2, 2);
  await page.screenshot({ path: testInfo.outputPath("tree.png") });

  // Act
  await page.getByRole("button", { name: "Arrange", exact: true }).click();
  await page.mouse.move(2, 2);
  await page.screenshot({ path: testInfo.outputPath("grid.png") });

  // Assert
  await expect(page.locator(".flow-node")).toHaveCount(18);
  await expect(page.getByRole("list", { name: "What runs under Grub - Tree Surgeon" }).locator(".tree-child")).toHaveCount(3);
  await expect(page.locator(".tree-child").filter({ hasText: "Wait for checks" })).toHaveCount(0);
  expect(Number((await page.getByRole("status", { name: "Zoom" }).textContent())!.replace("%", ""))).toBeGreaterThan(25);
  for (const element of await page.locator(".flow-node, .tree-child").all()) await expect(element).toBeInViewport();
});

test("the tree and Arrange grid show three subagents and never a background shell baby", async ({ page }, testInfo) => {
  // Arrange
  await page.setViewportSize({ width: 836, height: 956 });
  await page.goto("/tests/fixtures/fleet-tree.html?view=regression");
  await page.screenshot({ path: testInfo.outputPath("tree.png") });
  const children = page.getByRole("list", { name: "What runs under Build the fleet tree" }).locator(".tree-child");

  // Act
  await page.getByRole("button", { name: "Arrange", exact: true }).click();
  await page.screenshot({ path: testInfo.outputPath("grid.png") });

  // Assert
  await expect(page.locator(".flow-node")).toHaveCount(4);
  await expect(children).toHaveCount(3);
  await expect(children.filter({ hasText: "Wait for checks" })).toHaveCount(0);
  await expect(children.filter({ hasText: "Review layout" })).toContainText("Silent 12m");
  await expect(page.getByRole("list", { name: "What runs under Stream the billing CSV export" })).toHaveCount(0);
  await expect(page.getByRole("list", { name: "What runs under Fix the flaky checkout test" })).toHaveCount(0);
  for (const element of await page.locator(".flow-node, .tree-child").all()) await expect(element).toBeInViewport();
});

// Every card, every baby goblin and every folded count on the canvas, as
// boxes.
async function boxes(page: Page) {
  return page.locator(".flow-node, .tree-branch, .tree-branches > li").evaluateAll((nodes) => nodes.map((node) => {
    const box = node.getBoundingClientRect();
    return { name: (node.querySelector("strong")?.textContent || "") + (node.matches(".tree-branch") ? " children" : ""), left: box.left, top: box.top, right: box.right, bottom: box.bottom };
  }));
}
// The pairs of which one covers any part of the other.
const covered = async (page: Page) => (await boxes(page)).flatMap((one, i, all) => all.slice(i + 1)
  .filter((other) => one.left < other.right && other.left < one.right && one.top < other.bottom && other.top < one.bottom)
  .map((other) => one.name + " and " + other.name));

test("each goblin's running children hang on branches under its card as baby goblins, finished ones and a goblin whose children all finished left off", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html");
  const branches = page.getByRole("list", { name: "What runs under Build the fleet tree" });
  const children = branches.locator(".tree-child");
  await expect(children).toHaveCount(1);
  await expect(children.nth(0)).toContainText("Plumbing Mapper");
  await expect(children.nth(0)).toContainText("Working 4m");
  await expect(children.nth(0)).not.toHaveClass(/dim/);
  await expect(children.filter({ hasText: "Server Keeper" })).toHaveCount(0);
  await expect(children.filter({ hasText: "OAuth Researcher" })).toHaveCount(0);
  expect(await children.getByRole("img").evaluateAll((heads) => heads.map((head) => head.getAttribute("aria-label")))).toEqual(["Sub-agent"]);
  await expect(branches.locator(".branch-lines path"), "a branch to each child").toHaveCount(1);
  await expect(page.getByRole("list", { name: "What runs under Stream the billing CSV export" })).toHaveCount(0);
  await expect(page.getByRole("list", { name: "What runs under Fix the flaky checkout test" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /descendants of Fix the flaky checkout test/ })).toHaveCount(0);
  await expect(page.locator(".tree-count")).toHaveCount(0);
  const card = (await page.locator(".flow-node").filter({ hasText: "Build the fleet tree" }).boundingBox())!;
  for (const child of await children.all()) expect((await child.boundingBox())!.y, "under the goblin's card").toBeGreaterThan(card.y + card.height);
  expect(await covered(page)).toEqual([]);
});

test("the chevron folds a goblin's branches to their count, the same rounded rectangle as the baby goblins, and the count opens them again", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html");
  const corner = (selector: string) => page.locator(selector).first().evaluate((node) => getComputedStyle(node).borderTopLeftRadius);
  const babyCorner = await corner(".tree-child");
  await page.getByRole("button", { name: "Collapse descendants of Build the fleet tree" }).click();
  await expect(page.getByRole("list", { name: "What runs under Build the fleet tree" })).toHaveCount(0);
  const count = page.getByRole("button", { name: /under Build the fleet tree/ });
  await expect(count).toHaveAttribute("aria-label", "Show what runs under Build the fleet tree: 1 working");
  await expect(count.getByRole("img", { name: "Sub-agent" })).toHaveCount(1);
  await expect(count.getByRole("img", { name: "Dev server" })).toHaveCount(0);
  expect(await corner(".tree-count"), "the count is the same rounded rectangle as the baby goblins").toBe(babyCorner);
  await expect.poll(() => covered(page)).toEqual([]);
  await count.click();
  await expect(page.getByRole("list", { name: "What runs under Build the fleet tree" }).locator(".tree-child")).toHaveCount(1);
  await expect(page.locator(".tree-count")).toHaveCount(0);
});

test("the goblin panel lists working children and folds finished children away", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html?view=panel");
  await expect(page.locator(".working-summary")).toHaveText(/2 working.*1 silent.*1 idle.*2\.6 GB in all/);
  const rows = page.getByRole("list", { name: "Running" }).locator(".working-row");
  await expect(rows).toHaveCount(2);
  const silent = rows.filter({ hasText: "Run the affected Go tests" });
  await expect(silent).toHaveCount(0);
  await expect(rows.filter({ hasText: "Dev server" })).toHaveCount(0);
  await expect(rows.filter({ hasText: "Test run" }).locator(".working-memory")).toHaveText("1.2 GB");
  await expect(page.getByRole("list", { name: "Finished" })).toBeHidden();
  await page.getByText("2 finished").click();
  await expect(page.getByRole("list", { name: "Finished" }).locator(".working-row")).toHaveCount(2);
  // No line says how fresh the reading is (the Overlord, 2026-10-08: "i dont
  // need descriptive text everywhere like this"), and the goblin's own memory
  // follows the total in a few muted words.
  await expect(page.locator(".working-freshness")).toHaveCount(0);
  await expect(page.locator(".working-summary .muted")).toHaveText(", 1.0 GB its own");
});

// The Overlord, 2026-10-07, on the yellow "Silent 6h 41m: ..." banner a
// goblin's card carried: "yellow banner absolutely hate it as well". A child
// gone silent is named in the goblin's panel (the test above), never on its
// card, and since the card became one row on 2026-10-17 nothing that runs
// under the goblin is on it: its panel's What's working and the canvas show
// them.
test("a goblin's card shows nothing that runs under it, a child gone silent included", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html?view=cards");
  const card = page.locator(".task-card-shell").filter({ hasText: "Build the fleet tree" });
  await expect(card).toHaveCount(1);
  await expect(card.getByRole("img", { name: "Sub-agent" })).toHaveCount(0);
  await expect(card).not.toContainText("Silent");
  await expect(card).not.toContainText("Run the affected Go tests");
});

test("at phone width the lineage list opens a goblin's running children on a rail, finished ones left off, with nothing wider than the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/fleet-tree.html?view=lineage");
  await expect(page.getByRole("button", { name: /under Fix the flaky checkout test/ })).toHaveCount(0);
  await page.getByRole("button", { name: /under Build the fleet tree/ }).click();
  const children = page.getByRole("list", { name: "What runs under Build the fleet tree" }).locator(".tree-child");
  await expect(children).toHaveCount(1);
  await expect(children.filter({ hasText: "OAuth Researcher" })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
