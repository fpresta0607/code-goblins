import { expect, test, type Page } from "./site";

// Every card and every goblin's children on the canvas, as boxes.
async function boxes(page: Page) {
  return page.locator(".flow-node, .tree-branch").evaluateAll((nodes) => nodes.map((node) => {
    const box = node.getBoundingClientRect();
    return { name: (node.querySelector("strong")?.textContent || "") + (node.matches(".tree-branch") ? " children" : ""), left: box.left, top: box.top, right: box.right, bottom: box.bottom };
  }));
}
// The pairs of which one covers any part of the other.
const covered = async (page: Page) => (await boxes(page)).flatMap((one, i, all) => all.slice(i + 1)
  .filter((other) => one.left < other.right && other.left < one.right && one.top < other.bottom && other.top < one.bottom)
  .map((other) => one.name + " and " + other.name));

test("each goblin's children show as a count under its card, collapsed, and none for a goblin running nothing", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html");
  await expect(page.locator(".tree-count")).toHaveCount(2);
  await expect(page.locator(".tree-child")).toHaveCount(0);
  const builder = page.getByRole("button", { name: /under Build the fleet tree/ });
  await expect(builder).toHaveAttribute("aria-label", "Show what runs under Build the fleet tree: 1 working, 412 MB");
  await expect(builder.getByRole("img", { name: "Sub-agent" })).toHaveCount(1);
  await expect(builder.getByRole("img", { name: "Dev server" })).toHaveCount(1);
  await expect(page.getByRole("button", { name: /under Stream the billing CSV export: 1 working, 1\.1 GB/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /under Fix the flaky checkout test/ })).toHaveCount(0);
  expect(await covered(page)).toEqual([]);
});

test("a count opens its goblin's children as baby goblins with their states, idle and finished ones dimmed, and the canvas makes room", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html");
  await page.getByRole("button", { name: /under Build the fleet tree/ }).click();
  const children = page.getByRole("list", { name: "What runs under Build the fleet tree" }).locator(".tree-child");
  await expect(children).toHaveCount(3);
  await expect(children.nth(0)).toContainText("Map harness plumbing");
  await expect(children.nth(0)).toContainText("Working 4m · Explore");
  await expect(children.nth(0)).not.toHaveClass(/dim/);
  await expect(children.nth(1)).toContainText("Dev server :5173");
  await expect(children.nth(1)).toContainText("Idle 38m");
  await expect(children.nth(1)).toContainText("412 MB");
  await expect(children.nth(1)).toHaveClass(/dim/);
  await expect(children.nth(2)).toContainText("Research MCP OAuth");
  await expect(children.nth(2)).toContainText("Done 12m ago");
  await expect(children.nth(2)).toHaveClass(/dim/);
  expect(await children.getByRole("img").evaluateAll((heads) => heads.map((head) => head.getAttribute("data-tip")))).toEqual(["Sub-agent", "Dev server", "Sub-agent"]);
  await expect.poll(() => covered(page)).toEqual([]);
  await page.getByRole("button", { name: /Hide what runs under Build the fleet tree/ }).click();
  await expect(page.locator(".tree-child")).toHaveCount(0);
});

test("the goblin panel lists what is working with state, time and memory, a silent child's last line, and the finished folded away", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html?view=panel");
  await expect(page.locator(".working-summary")).toHaveText(/2 working.*1 silent.*1 idle.*2\.6 GB in all/);
  const rows = page.getByRole("list", { name: "Running" }).locator(".working-row");
  await expect(rows).toHaveCount(4);
  const silent = rows.filter({ hasText: "Run the affected Go tests" });
  await expect(silent.locator(".working-last-line")).toHaveText("ok   internal/monitor 41.2s");
  await expect(silent.locator(".plain-status")).toHaveText("Silent");
  await expect(silent.locator(".working-for")).toHaveText("14m");
  await expect(rows.filter({ hasText: "Test run" }).locator(".working-memory")).toHaveText("1.2 GB");
  await expect(page.getByRole("list", { name: "Finished" })).toBeHidden();
  await page.getByText("2 finished").click();
  await expect(page.getByRole("list", { name: "Finished" }).locator(".working-row")).toHaveCount(2);
  await expect(page.locator(".working-freshness")).toContainText("The goblin itself holds 1.0 GB.");
});

test("a goblin's card counts what runs under it and names a child gone silent, with its last line", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html?view=cards");
  const card = page.locator(".task-card-shell").filter({ hasText: "Build the fleet tree" });
  expect(await card.locator(".card-tree").getByRole("img").evaluateAll((heads) => heads.map((head) => head.getAttribute("aria-label")))).toEqual(["Sub-agent", "Background shell", "Dev server", "Test run"]);
  await expect(card.locator(".card-silent b")).toHaveText("Silent 14m: Run the affected Go tests");
  await expect(card.locator(".card-silent code")).toHaveText("ok   internal/monitor 41.2s");
  await expect(page.locator(".task-card-shell").filter({ hasText: "Fix the flaky checkout test" }).locator(".card-tree, .card-silent")).toHaveCount(0);
});

test("at phone width the lineage list opens a goblin's children on a rail, with nothing wider than the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/fleet-tree.html?view=lineage");
  await page.getByRole("button", { name: /under Build the fleet tree/ }).click();
  await expect(page.getByRole("list", { name: "What runs under Build the fleet tree" }).locator(".tree-child")).toHaveCount(3);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
