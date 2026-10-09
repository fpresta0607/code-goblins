import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-08: "branches should connect to the top of their baby
// goblins". Every line of the family tree, from a goblin to a goblin under it
// and from a goblin to each of its baby goblins, ends at the top center of
// the child it leads to, starts at the bottom of its parent, and runs
// through no card on its way.

interface Point { x: number; y: number }
interface Line { start: Point; end: Point; points: Point[] }
interface Box { name: string; left: number; top: number; right: number; bottom: number; isCard: boolean }

// Every line on the canvas, where it starts and ends on the page and a point
// every few pixels along it.
const lines = (page: Page): Promise<Line[]> => page.locator(".branch-lines path, .connection-line > path").evaluateAll((paths) => paths.map((element) => {
  const path = element as SVGPathElement, matrix = path.getScreenCTM()!, length = path.getTotalLength();
  const at = (along: number) => {
    const point = path.getPointAtLength(along);
    return { x: matrix.a * point.x + matrix.c * point.y + matrix.e, y: matrix.b * point.x + matrix.d * point.y + matrix.f };
  };
  return { start: at(0), end: at(length), points: Array.from({ length: Math.ceil(length / 3) + 1 }, (_, i) => at(Math.min(length, i * 3))) };
}));

// Every card and every baby goblin on the canvas.
const nodes = (page: Page): Promise<Box[]> => page.locator(".flow-node, .tree-branches > li").evaluateAll((elements) => elements.map((element) => {
  const box = element.getBoundingClientRect();
  return { name: element.querySelector("strong")?.textContent || "", left: box.left, top: box.top, right: box.right, bottom: box.bottom, isCard: element.matches(".flow-node") };
}));

const near = (one: Point, other: Point) => Math.abs(one.x - other.x) <= 1.5 && Math.abs(one.y - other.y) <= 1.5;
const topCenter = (box: Box) => ({ x: (box.left + box.right) / 2, y: box.top });
// Each line leaves its parent's bottom on its own, beside its siblings', so
// it starts on the bottom of a card clear of its rounded corners.
const isOnBottom = (point: Point, box: Box) => Math.abs(point.y - box.bottom) <= 1.5 && point.x > box.left + (box.right - box.left) / 10 && point.x < box.right - (box.right - box.left) / 10;

// What is wrong with the canvas's lines as it shows now, by name: a line
// that ends anywhere but the top center of a child or starts anywhere but the
// bottom of a card, a child no line reaches, and, where every card is
// arranged, a line that runs through a card.
async function wrongLines(page: Page, isArranged: boolean): Promise<string[]> {
  const [drawn, boxes] = [await lines(page), await nodes(page)];
  const wrong: string[] = [];
  for (const line of drawn) {
    const child = boxes.find((box) => near(line.end, topCenter(box)));
    const parent = boxes.find((box) => box.isCard && isOnBottom(line.start, box));
    if (!child) wrong.push(`a line ends at ${Math.round(line.end.x)},${Math.round(line.end.y)}, the top center of no card or baby goblin`);
    if (!parent) wrong.push(`a line to ${child?.name || "nothing"} starts at ${Math.round(line.start.x)},${Math.round(line.start.y)}, the bottom of no card`);
    if (!isArranged) continue;
    for (const box of boxes) {
      if (line.points.some((point) => point.x > box.left + 2 && point.x < box.right - 2 && point.y > box.top + 2 && point.y < box.bottom - 2)) wrong.push(`the line to ${child?.name} runs through ${box.name}`);
    }
  }
  for (const box of boxes.filter((box) => box.name !== "CFO")) {
    const reaching = drawn.filter((line) => near(line.end, topCenter(box))).length;
    if (reaching !== 1) wrong.push(`${reaching} lines reach the top of ${box.name}`);
  }
  return wrong;
}

const CHILDREN = 13;

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/tests/fixtures/tree-branches.html");
  await expect(page.locator(".tree-branches > li")).toHaveCount(9);
  await expect(page.locator(".flow-node")).toHaveCount(5);
});

test("every branch ends at the top center of its goblin or baby goblin, at every zoom", async ({ page }) => {
  expect(await lines(page), "a line to each card under the CFO and to each baby goblin").toHaveLength(CHILDREN);
  expect(await wrongLines(page, true), "fitted").toEqual([]);
  for (let i = 0; i < 3; i++) await page.getByRole("button", { name: "Zoom in" }).click();
  expect(await wrongLines(page, true), "zoomed in").toEqual([]);
  for (let i = 0; i < 6; i++) await page.getByRole("button", { name: "Zoom out" }).click();
  expect(await wrongLines(page, true), "zoomed out").toEqual([]);
  await page.mouse.move(700, 500);
  await page.mouse.wheel(0, -500);
  await expect.poll(async () => Number((await page.getByRole("status", { name: "Zoom" }).textContent())!.replace("%", ""))).toBeGreaterThan(80);
  expect(await wrongLines(page, true), "zoomed by the wheel").toEqual([]);
});

test("every branch ends at the top center of its child after a drag and after Arrange", async ({ page }) => {
  const drag = async (name: string, by: Point) => {
    const box = (await page.locator(".flow-node").filter({ has: page.getByText(name, { exact: true }) }).locator(".flow-node-main").boundingBox())!;
    await page.mouse.move(box.x + 40, box.y + 20);
    await page.mouse.down();
    await page.mouse.move(box.x + 40 + by.x, box.y + 20 + by.y, { steps: 8 });
    await page.mouse.up();
    // The pointer leaves the card, which lifts under it while it rests there.
    await page.mouse.move(2, 2);
  };
  await drag("Grub - Tree Surgeon", { x: -60, y: 40 });
  await expect.poll(() => wrongLines(page, false), "after dragging a goblin with baby goblins").toEqual([]);
  await drag("Draft the release notes", { x: 220, y: 30 });
  await expect.poll(() => wrongLines(page, false), "after dragging the goblin under it").toEqual([]);
  await page.getByRole("button", { name: "Arrange" }).click();
  expect(await wrongLines(page, true), "after Arrange").toEqual([]);
});

// At phone width the canvas is the lineage list: each child, goblin or baby
// goblin, hangs from the rail of its parent on a line that turns into the top
// center of its card.
test("at phone width every line from a rail ends at the top center of its child", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/tree-branches.html?view=lineage");
  const counts = page.getByRole("button", { name: /^Show what runs under/ });
  while (await counts.count()) await counts.first().click();
  const children = page.locator(".workflow-children > li > .node-group > .workflow-node, .tree-rail > li");
  await expect(children, "four goblins and nine baby goblins").toHaveCount(13);
  const misses = await children.evaluateAll((elements) => elements.flatMap((element) => {
    const box = element.getBoundingClientRect(), line = getComputedStyle(element, "::after");
    const end = { x: box.left + element.clientLeft + parseFloat(line.left) + parseFloat(line.width) - parseFloat(line.borderRightWidth) / 2, y: box.top + element.clientTop + parseFloat(line.top) + parseFloat(line.height) };
    const name = element.querySelector("strong")?.textContent;
    return Math.abs(end.x - (box.left + box.width / 2)) <= 1.5 && Math.abs(end.y - box.top) <= 1.5 ? [] : [`${name}: its line ends at ${end.x},${end.y}`];
  }));
  expect(misses).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
