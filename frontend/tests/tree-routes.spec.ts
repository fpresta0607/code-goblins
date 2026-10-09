import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-08 about 22:20Z: "child goblin branches are not
// individually distinguishable and appear to be taking weird routes when
// spacing lines can be cleaner with less overlap". On a goblin with seven
// baby goblins and a goblin under them, beside two goblins with three and
// four baby goblins, every line leaves its parent's bottom on its own, ends
// at the top center of its child, runs through no card or baby goblin on its
// way and keeps a line's gap from every other line, and resting on a baby
// goblin or a card, or the keyboard on it, lights its own line end to end.

interface Point { x: number; y: number }
interface Line { points: Point[]; isLit: boolean }
interface Box { name: string; left: number; top: number; right: number; bottom: number; isCard: boolean }

// The gap the canvas keeps between two lines side by side, as fleet-tree.ts
// lays them out.
const LINE_GAP = 8;

// Every line on the canvas as points on the page a pixel or so apart, and
// the canvas's zoom.
const lines = (page: Page): Promise<{ lines: Line[]; scale: number }> => page.locator(".branch-lines path, .connection-line > path").evaluateAll((paths) => {
  const drawn = paths.map((element) => {
    const path = element as SVGPathElement, matrix = path.getScreenCTM()!, length = path.getTotalLength();
    const at = (along: number) => {
      const point = path.getPointAtLength(along);
      return { x: matrix.a * point.x + matrix.c * point.y + matrix.e, y: matrix.b * point.x + matrix.d * point.y + matrix.f };
    };
    return { points: Array.from({ length: Math.ceil(length) + 1 }, (_, i) => at(Math.min(length, i))), isLit: element.closest(".lit") !== null };
  });
  return { lines: drawn, scale: (paths[0] as SVGPathElement).getScreenCTM()!.a };
});

// Every card and every baby goblin on the canvas.
const nodes = (page: Page): Promise<Box[]> => page.locator(".flow-node, .tree-branches > li").evaluateAll((elements) => elements.map((element) => {
  const box = element.getBoundingClientRect();
  return { name: element.querySelector("strong")?.textContent || "", left: box.left, top: box.top, right: box.right, bottom: box.bottom, isCard: element.matches(".flow-node") };
}));

// Whether two lines' bounds come within a distance of each other.
const isNear = (one: Line, other: Line, distance: number) => {
  const [a, b] = [one, other].map((line) => ({ left: Math.min(...line.points.map((point) => point.x)), right: Math.max(...line.points.map((point) => point.x)), top: Math.min(...line.points.map((point) => point.y)), bottom: Math.max(...line.points.map((point) => point.y)) }));
  return a.left - distance < b.right && b.left - distance < a.right && a.top - distance < b.bottom && b.top - distance < a.bottom;
};
const near = (one: Point, other: Point) => Math.abs(one.x - other.x) <= 1.5 && Math.abs(one.y - other.y) <= 1.5;
const topCenter = (box: Box) => ({ x: (box.left + box.right) / 2, y: box.top });
const childAt = (boxes: Box[], line: Line) => boxes.find((box) => near(line.points[line.points.length - 1], topCenter(box)));

// What is wrong with the canvas's lines, by name: a line that leaves no
// card's bottom or ends anywhere but the top center of a child, a line that
// runs through a card or a baby goblin, and two lines nearer than a line's
// gap.
async function wrongLines(page: Page): Promise<string[]> {
  const [{ lines: drawn, scale }, boxes] = [await lines(page), await nodes(page)];
  const wrong: string[] = [];
  const named = drawn.map((line) => childAt(boxes, line)?.name || "nothing");
  for (const [i, line] of drawn.entries()) {
    const start = line.points[0], end = line.points[line.points.length - 1];
    if (!childAt(boxes, line)) wrong.push(`a line ends at ${Math.round(end.x)},${Math.round(end.y)}, the top center of no card or baby goblin`);
    if (!boxes.some((box) => box.isCard && Math.abs(start.y - box.bottom) <= 1.5 && start.x > box.left + 16 * scale && start.x < box.right - 16 * scale)) wrong.push(`the line to ${named[i]} leaves no card's bottom`);
    for (const box of boxes) {
      if (line.points.some((point) => point.x > box.left + 2 && point.x < box.right - 2 && point.y > box.top + 2 && point.y < box.bottom - 2)) wrong.push(`the line to ${named[i]} runs through ${box.name}`);
    }
    for (const [j, other] of drawn.entries()) {
      if (j <= i || !isNear(line, other, LINE_GAP * scale)) continue;
      let nearest = Infinity;
      for (const one of line.points) for (const two of other.points) nearest = Math.min(nearest, Math.hypot(one.x - two.x, one.y - two.y));
      if (nearest < (LINE_GAP - 1.5) * scale) wrong.push(`the lines to ${named[i]} and ${named[j]} come ${(nearest / scale).toFixed(1)} apart`);
    }
  }
  return wrong;
}

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.goto("/tests/fixtures/tree-routes.html");
  await expect(page.locator(".tree-branches > li")).toHaveCount(14);
  await expect(page.locator(".flow-node")).toHaveCount(5);
});

test("every line leaves its parent's bottom on its own, ends at its child's top, crosses no card and keeps a line's gap from every other", async ({ page }) => {
  expect((await lines(page)).lines, "a line to each of the four goblins and to each of the fourteen baby goblins").toHaveLength(18);
  expect(await wrongLines(page), "fitted").toEqual([]);
  for (let i = 0; i < 3; i++) await page.getByRole("button", { name: "Zoom in" }).click();
  expect(await wrongLines(page), "zoomed in").toEqual([]);
});

test("on a canvas as narrow as the one beside an open panel, where the goblins take two rows, no line passes behind a card or a baby goblin", async ({ page }) => {
  await page.goto("/tests/fixtures/tree-routes.html?view=narrow");
  await expect(page.locator(".tree-branches > li")).toHaveCount(14);
  const rows = await page.locator(".flow-node").evaluateAll((cards) => new Set(cards.map((card) => Math.round(card.getBoundingClientRect().top))).size);
  expect(rows, "the CFO above two rows of goblins").toBeGreaterThan(2);
  expect(await wrongLines(page)).toEqual([]);
});

test("resting on a baby goblin or a card, or the keyboard on it, lights its own line end to end and no other", async ({ page }) => {
  const lit = async () => {
    const [{ lines: drawn }, boxes] = [await lines(page), await nodes(page)];
    return drawn.filter((line) => line.isLit).map((line) => childAt(boxes, line)?.name);
  };
  await page.mouse.move(2, 2);
  expect(await lit(), "nothing rests on a node").toEqual([]);
  const baby = page.locator(".tree-branches > li").filter({ hasText: "Grub VII" });
  await baby.hover();
  await expect.poll(lit, "resting on a baby goblin").toEqual(["Grub VII - Branch Measurer"]);
  await page.mouse.move(2, 2);
  await expect.poll(lit, "the pointer gone").toEqual([]);
  await page.locator(".tree-branches > li").filter({ hasText: "Grub V -" }).getByRole("button").focus();
  await expect.poll(lit, "the keyboard on a baby goblin").toEqual(["Grub V - Export Tracer"]);
  await page.locator(".flow-node").filter({ hasText: "Kip - Echo Chaser" }).getByRole("button").first().focus();
  await expect.poll(lit, "the keyboard on a goblin's card").toEqual(["Kip - Echo Chaser"]);
  await page.locator(".flow-node").filter({ hasText: "Draft the release notes" }).hover();
  await expect.poll(lit, "resting on the goblin under the baby goblins").toEqual(["Draft the release notes"]);
});
