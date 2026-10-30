import { expect, holdStream, test, type Page } from "./site";

const GB = 2 ** 30;
const TITLE = "Audit the Code Goblins board layout, task cards and machine meters on desktop and phone screens";
const SNAPSHOT = {
  healthy: true, instance: "card-budget", revision: 1, cfo_runs: true, attention: [],
  memory: { available: 9 * GB, total: 32 * GB, commit_available: 20 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, paged_pool: GB, nonpaged_pool: GB, pool_line: 7 * GB },
  disk: { drive: "C:", total: 953 * GB, free: 300 * GB, floor: 15 * GB, wake: 10 * GB },
  processors: { performance_cores: 6, efficiency_cores: 4, free: .55, efficiency_free: .5, next: .25, busiest: [] },
  gpu: { adapters: [{ name: "Fixture GPU", busy: .2 }] },
  tasks: [
    { id: "urgent", title: TITLE, phase: "queued", project: "example", brief: true, priority: "production-defect", generation: "", verified: false },
    ...["busy", "idle", "stale", "busy", "idle", "stale"].map((state, index) => ({
      id: `worker-${index}`, title: `Working task ${index}`, goblin_name: `Worker ${index}`, project: "example", phase: "working", generation: `worker-${index}-1`, verified: false,
      runtime: { state, reason: "fixture", at: "2026-10-30T12:00:00Z" },
    })),
  ],
};

async function open(page: Page) {
  await holdStream(page, SNAPSHOT);
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Tasks", exact: true })).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test.describe(`${viewport.width} px board`, () => {
    test.use({ viewport });

    test("six working cards stay still on and off screen, including idle and stale sessions", async ({ page }) => {
      // Arrange
      await open(page);
      const cards = page.getByRole("region", { name: "In progress", exact: true }).locator(".task-card-shell");
      await expect(cards).toHaveCount(6);
      const animations = () => page.evaluate(() => document.getAnimations().filter((animation) =>
        animation.playState === "running" && animation.effect instanceof KeyframeEffect && animation.effect.target instanceof Element && animation.effect.target.closest(".task-card-shell")
      ).length);

      // Act and assert
      for (const reducedMotion of ["no-preference", "reduce"] as const) {
        await page.emulateMedia({ reducedMotion });
        await page.evaluate(() => scrollTo(0, 0));
        expect(await animations()).toBe(0);
        for (const card of await cards.all()) {
          await card.scrollIntoViewIfNeeded();
          expect(await animations()).toBe(0);
        }
        await page.evaluate(() => scrollTo(0, document.body.scrollHeight));
        expect(await animations()).toBe(0);
      }
    });

    test("a priority badge leaves the queued title two full lines", async ({ page }, testInfo) => {
      // Arrange
      await open(page);
      const card = page.getByRole("region", { name: "Tasks", exact: true }).locator(".task-card-shell").first();

      // Act
      const layout = await card.evaluate((element) => {
        const title = element.querySelector(".card-title")!, badge = element.querySelector(".next-chip")!;
        const titleBox = title.getBoundingClientRect(), badgeBox = badge.getBoundingClientRect();
        return { lines: titleBox.height / parseFloat(getComputedStyle(title).lineHeight), titleWidth: titleBox.width, headWidth: title.parentElement!.getBoundingClientRect().width, titleTop: titleBox.top, badgeBottom: badgeBox.bottom };
      });
      await card.screenshot({ path: testInfo.outputPath("priority-card.png") });

      // Assert
      expect(layout.lines).toBeCloseTo(2, 1);
      expect(layout.titleWidth).toBeCloseTo(layout.headWidth, 0);
      expect(layout.titleTop).toBeGreaterThanOrEqual(layout.badgeBottom);
    });
  });
}

test("at 390 by 844 the first queued card appears before machine meters on the first screen", async ({ page }, testInfo) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  const column = page.getByRole("region", { name: "Tasks", exact: true });
  const card = column.locator(".task-card-shell").first();

  // Act
  const cardBox = (await card.boundingBox())!, meters = (await column.locator(".task-meters").boundingBox())!;
  await page.screenshot({ path: testInfo.outputPath("phone-first-screen.png") });

  // Assert
  expect(cardBox.y).toBeGreaterThanOrEqual(0);
  expect(cardBox.y + cardBox.height).toBeLessThanOrEqual(844);
  expect(meters.y).toBeGreaterThanOrEqual(cardBox.y + cardBox.height);
  for (const name of ["Memory", "Kernel pools", "Disk", "CPU", "GPU"]) await expect(column.getByRole("group", { name, exact: true })).toBeVisible();
});
