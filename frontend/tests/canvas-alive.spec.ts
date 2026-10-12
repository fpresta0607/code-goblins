import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-12: the Orchestration canvas comes alive, a working
// goblin glowing, a baby goblin's birth and exit animating, and a pulse on
// every real exchange, lightweight. Each motion starts with its event and
// ends with it, and nothing runs while nothing changes.
type Step = "birth" | "report" | "finish" | "goblin";

async function open(page: Page) {
  await page.goto("/tests/fixtures/canvas-alive.html");
  await expect(page.locator(".tree-child")).toHaveCount(4);
  await page.evaluate(() => document.fonts.ready);
  await page.mouse.move(2, 2);
}
const step = (page: Page, name: Step, goblin: string) => page.evaluate(([name, goblin]) => (window as Window & { canvasStep?: (name: string, goblin: string) => void }).canvasStep!(name, goblin), [name, goblin]);
// The animations running anywhere on the page.
const running = (page: Page) => page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running").length);
const settled = async (page: Page, timeout = 3000) => expect.poll(() => running(page), { timeout }).toBe(0);
// The properties the running animations on the page move.
const animated = (page: Page) => page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running")
  .flatMap((animation) => animation.effect instanceof KeyframeEffect ? animation.effect.getKeyframes().flatMap((frame) => Object.keys(frame)) : [])
  .filter((key) => !["offset", "computedOffset", "easing", "composite"].includes(key)));

test("a working goblin glows and a waiting one does not", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const glowing = await page.locator(".flow-node.is-working strong").allTextContents();

  // Assert
  expect(glowing.sort()).toEqual(["Mabel - Email Elf", "Pip - Test Tamer"]);
});

test("the canvas runs nothing while nothing changes", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  await page.waitForTimeout(1500);

  // Assert
  expect(await running(page)).toBe(0);
  await expect(page.locator(".branch-pulse, .tree-branches > li.born, .tree-branches > li.leaving")).toHaveCount(0);
});

test("a baby goblin born grows in on its branch with a pulse from its goblin, and nothing runs once it has", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  await step(page, "birth", "cg-tests");

  // Assert
  await expect(page.locator(".tree-child")).toHaveCount(5);
  await expect(page.locator(".tree-branches > li.born")).toHaveCount(1);
  await expect(page.locator(".branch-pulse")).toHaveCount(1);
  expect(await running(page)).toBeGreaterThan(0);
  await settled(page);
  await expect(page.locator(".branch-pulse, .tree-branches > li.born")).toHaveCount(0);
});

test("a baby goblin that reports sends a pulse up its branch, which ends with it", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  await step(page, "report", "pd-email");

  // Assert
  const pulse = page.locator(".branch-pulse");
  await expect(pulse).toHaveCount(1);
  expect(await pulse.evaluate((element) => element.getAnimations().map((animation) => Object.keys(animation.effect instanceof KeyframeEffect ? animation.effect.getKeyframes()[0] : {}).filter((key) => key === "transform" || key === "opacity")).flat().sort())).toEqual(["opacity", "transform"]);
  await settled(page);
  await expect(pulse).toHaveCount(0);
});

test("a baby goblin that finishes reports back, fades where it hung and goes", async ({ page }) => {
  // Arrange
  await open(page);
  const finished = await page.locator(".tree-branches[aria-label='What runs under Pip - Test Tamer'] .tree-child strong").first().textContent();

  // Act
  await step(page, "finish", "cg-tests");

  // Assert: it stays, leaving, with a pulse up its branch, then goes.
  await expect(page.locator(".tree-branches > li.leaving")).toHaveCount(1);
  await expect(page.locator(".tree-branches > li.leaving strong")).toHaveText(finished!);
  await expect(page.locator(".branch-pulse")).toHaveCount(1);
  await settled(page);
  await expect(page.locator(".tree-child")).toHaveCount(3);
  await expect(page.locator(".tree-branches > li.leaving, .branch-pulse")).toHaveCount(0);
});

test("a goblin's report pulses its connector from the CFO, and nothing runs once the pulse ends", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  await step(page, "goblin", "cg-tests");

  // Assert
  await expect(page.locator(".connector-effect.communicating")).toHaveCount(1);
  await expect(page.locator(".connector-effect .communication-pulse").first()).toBeAttached();
  expect(await running(page)).toBeGreaterThan(0);
  await settled(page, 9000);
  await expect(page.locator(".connector-effect")).toHaveCount(0);
});

test("every motion on the canvas moves only transforms and opacity", async ({ page }) => {
  // Arrange
  await open(page);
  const moved = new Set<string>();

  // Act: each exchange the canvas shows, read while it plays.
  for (const [name, goblin] of [["goblin", "cg-tests"], ["birth", "pd-email"], ["report", "cg-tests"], ["finish", "pd-email"]] as [Step, string][]) {
    await step(page, name, goblin);
    for (const property of await animated(page)) moved.add(property);
  }

  // Assert
  expect([...moved].sort()).toEqual(["opacity", "transform"]);
});

test("with reduced motion a baby goblin appears and goes at once, with no pulse", async ({ page }) => {
  // Arrange
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page);

  // Act
  await step(page, "birth", "cg-tests");
  await step(page, "report", "pd-email");
  await step(page, "finish", "pd-email");
  await step(page, "goblin", "cg-tests");

  // Assert
  await expect(page.locator(".tree-child")).toHaveCount(4);
  await expect(page.locator(".branch-pulse, .tree-branches > li.born, .tree-branches > li.leaving")).toHaveCount(0);
  expect(await running(page)).toBe(0);
});
