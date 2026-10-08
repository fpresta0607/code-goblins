import { expect, test, type Page } from "./site";

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
  await expect(children).toHaveCount(2);
  await expect(children.nth(0)).toContainText("Plumbing Mapper");
  await expect(children.nth(0)).toContainText("Working 4m");
  await expect(children.nth(0)).not.toHaveClass(/dim/);
  await expect(children.nth(1)).toContainText("Server Keeper");
  await expect(children.nth(1)).toContainText("Idle 38m");
  await expect(children.nth(1)).toContainText("412 MB");
  await expect(children.nth(1), "an idle one can be opened, so it is not greyed").not.toHaveClass(/dim/);
  await expect(children.filter({ hasText: "OAuth Researcher" })).toHaveCount(0);
  expect(await children.getByRole("img").evaluateAll((heads) => heads.map((head) => head.getAttribute("aria-label")))).toEqual(["Sub-agent", "Dev server"]);
  await expect(branches.locator(".branch-lines path"), "a branch to each child").toHaveCount(2);
  await expect(page.getByRole("list", { name: "What runs under Stream the billing CSV export" }).locator(".tree-child")).toHaveCount(1);
  await expect(page.getByRole("list", { name: "What runs under Fix the flaky checkout test" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /descendants of Fix the flaky checkout test/ })).toHaveCount(0);
  await expect(page.locator(".tree-count")).toHaveCount(0);
  const card = (await page.locator(".flow-node").filter({ hasText: "Build the fleet tree" }).boundingBox())!;
  for (const child of await children.all()) expect((await child.boundingBox())!.y, "under the goblin's card").toBeGreaterThan(card.y + card.height);
  expect(await covered(page)).toEqual([]);
});

test("the chevron folds a goblin's branches to their count, the same rounded rectangle as the baby goblins, and the count opens them again", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html");
  await page.getByRole("button", { name: "Collapse descendants of Build the fleet tree" }).click();
  await expect(page.getByRole("list", { name: "What runs under Build the fleet tree" })).toHaveCount(0);
  const count = page.getByRole("button", { name: /under Build the fleet tree/ });
  await expect(count).toHaveAttribute("aria-label", "Show what runs under Build the fleet tree: 1 working, 412 MB");
  await expect(count.getByRole("img", { name: "Sub-agent" })).toHaveCount(1);
  await expect(count.getByRole("img", { name: "Dev server" })).toHaveCount(1);
  const corner = (selector: string) => page.locator(selector).first().evaluate((node) => getComputedStyle(node).borderTopLeftRadius);
  expect(await corner(".tree-count"), "the count is the same rounded rectangle as the baby goblins").toBe(await corner(".tree-child"));
  await expect.poll(() => covered(page)).toEqual([]);
  await count.click();
  await expect(page.getByRole("list", { name: "What runs under Build the fleet tree" }).locator(".tree-child")).toHaveCount(2);
  await expect(page.locator(".tree-count")).toHaveCount(0);
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
  // No line says how fresh the reading is (the Overlord, 2026-10-08: "i dont
  // need descriptive text everywhere like this"), and the goblin's own memory
  // is in the total's tip.
  await expect(page.locator(".working-freshness")).toHaveCount(0);
  await expect(page.getByText(/ in all$/)).toHaveAttribute("data-tip", "The goblin itself holds 1.0 GB");
});

// The Overlord, 2026-10-07, on the yellow "Silent 6h 41m: ..." banner a
// goblin's card carried: "yellow banner absolutely hate it as well". A child
// gone silent is named in the goblin's panel (the test above), never on its
// card.
test("a goblin's card counts what runs under it and names no child gone silent", async ({ page }) => {
  await page.goto("/tests/fixtures/fleet-tree.html?view=cards");
  const card = page.locator(".task-card-shell").filter({ hasText: "Build the fleet tree" });
  expect(await card.locator(".card-tree").getByRole("img").evaluateAll((heads) => heads.map((head) => head.getAttribute("aria-label")))).toEqual(["Sub-agent", "Background shell", "Dev server", "Test run"]);
  await expect(card).not.toContainText("Silent");
  await expect(card).not.toContainText("Run the affected Go tests");
  await expect(page.locator(".task-card-shell").filter({ hasText: "Fix the flaky checkout test" }).locator(".card-tree")).toHaveCount(0);
});

test("at phone width the lineage list opens a goblin's running children on a rail, finished ones left off, with nothing wider than the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/fleet-tree.html?view=lineage");
  await expect(page.getByRole("button", { name: /under Fix the flaky checkout test/ })).toHaveCount(0);
  await page.getByRole("button", { name: /under Build the fleet tree/ }).click();
  const children = page.getByRole("list", { name: "What runs under Build the fleet tree" }).locator(".tree-child");
  await expect(children).toHaveCount(2);
  await expect(children.filter({ hasText: "OAuth Researcher" })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
