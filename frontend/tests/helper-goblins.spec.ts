import { expect, test } from "./site";

test("a helper hangs under its parent as a helper goblin, not as a card of its own", async ({ page }) => {
  await page.goto("/tests/fixtures/helper-goblins.html");
  await expect(page.locator(".flow-node").filter({ hasText: "Accounts migration" })).toHaveCount(0);
  const helper = page.getByRole("list", { name: "What runs under Let goblins start helpers" }).locator(".tree-child");
  await expect(helper).toHaveCount(1);
  await expect(helper.getByRole("img", { name: "Helper goblin" })).toHaveCount(1);
  await expect(helper).toContainText("Accounts migration");
  await expect(helper).toContainText("Working 4m");
  await expect(helper).toContainText("512 MB");
  // A helper whose parent is paused, with no tree to hold it, keeps its card.
  await expect(page.locator(".flow-node").filter({ hasText: "Ledger fixtures" })).toHaveCount(1);
});

test("a helper's card on the board names its parent", async ({ page }) => {
  await page.goto("/tests/fixtures/helper-goblins.html?view=cards");
  await expect(page.locator(".task-card-shell").filter({ hasText: "Accounts migration" }).locator(".card-secondary")).toHaveText("Helper of Let goblins start helpers");
  await expect(page.locator(".task-card-shell").filter({ hasText: "Ledger fixtures" }).locator(".card-secondary")).toHaveText("Helper of Sync the ledger");
  const parent = page.locator(".task-card-shell").filter({ has: page.locator(".card-title", { hasText: /^Let goblins start helpers$/ }) });
  await expect(parent).toHaveCount(1);
  await expect(parent.getByText(/^Helper of/)).toHaveCount(0);
});

test("at phone width the lineage list shows a held helper under its parent only", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/helper-goblins.html?view=lineage");
  await expect(page.locator(".node-select").filter({ hasText: "Accounts migration" })).toHaveCount(0);
  await page.getByRole("button", { name: /under Let goblins start helpers/ }).click();
  await expect(page.getByRole("list", { name: "What runs under Let goblins start helpers" }).locator(".tree-child")).toContainText("Accounts migration");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test("a helper no tree holds hangs under its parent's card, on the canvas and in the lineage list", async ({ page }) => {
  await page.goto("/tests/fixtures/helper-goblins.html");
  const parent = page.locator(".flow-node").filter({ hasText: "Sync the ledger" });
  const helper = page.locator(".flow-node").filter({ hasText: "Ledger fixtures" });
  await expect(helper).toHaveCount(1);
  await expect(page.locator(".connection-line.relation-Helper")).toHaveCount(1);
  const [above, below] = [await parent.boundingBox(), await helper.boundingBox()];
  expect(below!.y).toBeGreaterThan(above!.y + above!.height);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/helper-goblins.html?view=lineage");
  const under = page.getByRole("list", { name: "Children of Sync the ledger" });
  await expect(under.locator(".node-select").filter({ hasText: "Ledger fixtures" })).toHaveCount(1);
  await expect(under.locator(".node-relation")).toHaveText("Helper goblin");
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
