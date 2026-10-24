import { expect, test } from "./site"

test("goblin cards name their model only in their harness mark's tip", async ({ page }, testInfo) => {
  await page.goto("/tests/fixtures/helper-goblins.html?view=cards")
  await page.screenshot({ path: testInfo.outputPath("goblin-card.png"), fullPage: true })
  const card = page.locator(".task-card-shell").filter({ has: page.locator(".card-title", { hasText: /^Let goblins start helpers$/ }) })
  await expect(card).not.toContainText("claude-opus-5-5")
  await expect(card).not.toContainText("strong cross-harness fallback")
  await expect(card.locator(".card-harness [role=img]")).toHaveAttribute("aria-label", /claude-opus-5-5/)
  await expect(card.locator("[title]")).toHaveCount(0)
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
})

test("baby goblins show their own recorded model, under a parent that shows none", async ({ page }, testInfo) => {
  await page.goto("/tests/fixtures/helper-goblins.html")
  await page.screenshot({ path: testInfo.outputPath("baby-goblin.png"), fullPage: true })
  const baby = page.getByRole("list", { name: "What runs under Let goblins start helpers" }).locator(".tree-child")
  const parent = page.locator(".flow-node").filter({ has: page.locator("strong", { hasText: /^Let goblins start helpers$/ }) })
  await expect(parent).not.toContainText("claude-opus-5-5")
  await expect(parent).not.toContainText("strong cross-harness fallback")
  await expect(baby.locator(".tree-child-model")).toHaveText("gpt-6-luna")
  await expect(baby.locator("[title]")).toHaveCount(0)
  const card = await baby.boundingBox()
  const model = await baby.locator(".tree-child-model").boundingBox()
  expect(model!.y + model!.height).toBeLessThanOrEqual(card!.y + card!.height)
})
