import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-17: "if there's something that's turned or finished,
// why is it still being shown inside the orchestration? ... it should just be
// for in-progress goblins to begin with". The fixture holds beside the
// goblins in progress the records that read Turn finished, Working and
// Session ended under the CFO that day: a retired goblin's session, an ended
// session, a live goblin's earlier generation, a sub-agent record with no
// terminal of its own, paused goblins and a queued brief.
const IN_PROGRESS = ["CFO", "Kip - Chart Captain", "Pip - Chart Helper", "Wanda - API Ace", "Wendell - Gate Whisperer"];

// titles names each card the selector finds by its title, in order.
const titles = (page: Page, cards: string) => page.locator(cards).evaluateAll((found) => found.map((card) => card.querySelector("strong")!.textContent!).sort());

test("the canvas draws only the goblins in progress and their live helpers", async ({ page }) => {
  // Act
  await page.goto("/tests/fixtures/canvas-live-only.html");

  // Assert
  await expect(page.locator(".flow-node")).toHaveCount(IN_PROGRESS.length);
  expect(await titles(page, ".flow-node")).toEqual(IN_PROGRESS);
});

test("the lineage list draws the same goblins at phone width", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });

  // Act
  await page.goto("/tests/fixtures/canvas-live-only.html?list");

  // Assert
  await expect(page.locator(".workflow-node")).toHaveCount(IN_PROGRESS.length);
  expect(await titles(page, ".workflow-node")).toEqual(IN_PROGRESS);
});
