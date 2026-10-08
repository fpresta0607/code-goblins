import { expect, test, type Page } from "./site";

// The Overlord's canvas: 836 by 956 at his window size, 1707 wide at 1.5x,
// with the CFO's panel open beside it.
const CANVAS = { width: 836, height: 956 };

const zoom = async (page: Page) => Number((await page.getByRole("status", { name: "Zoom" }).textContent())!.replace("%", ""));
const card = (page: Page, name: string) => page.locator(".flow-node").filter({ has: page.getByText(name, { exact: true }) });
async function boxes(page: Page) {
  return page.locator(".flow-node").evaluateAll((nodes) => nodes.map((node) => {
    const box = node.getBoundingClientRect();
    return { name: node.querySelector("strong")!.textContent!, left: box.left, top: box.top, right: box.right, bottom: box.bottom };
  }));
}
// The cards any part of which lies outside the canvas.
async function outside(page: Page) {
  const frame = (await page.locator(".flow-canvas").boundingBox())!;
  return (await boxes(page)).filter((box) => box.left < frame.x || box.top < frame.y || box.right > frame.x + frame.width || box.bottom > frame.y + frame.height).map((box) => box.name);
}

test.beforeEach(async ({ page }) => {
  await page.setViewportSize(CANVAS);
});

test("the wheel zooms the canvas in and out where the pointer is", async ({ page }) => {
  await page.goto("/tests/fixtures/canvas-controls.html");
  await expect(page.locator(".flow-node")).toHaveCount(6);
  const start = await zoom(page);
  const before = (await card(page, "Goblin 2").boundingBox())!;
  const pointer = { x: before.x + before.width * .25, y: before.y + before.height * .75 };
  await page.mouse.move(pointer.x, pointer.y);
  await page.mouse.wheel(0, -300);
  await expect.poll(() => zoom(page)).toBeGreaterThan(start);
  const after = (await card(page, "Goblin 2").boundingBox())!;
  expect(after.width, "the card grew").toBeGreaterThan(before.width);
  expect(Math.abs((pointer.x - after.x) / after.width - .25), "the same point of the card stays under the pointer").toBeLessThan(.01);
  expect(Math.abs((pointer.y - after.y) / after.height - .75)).toBeLessThan(.01);
  await page.mouse.wheel(0, 600);
  await expect.poll(() => zoom(page)).toBeLessThan(start);
});

test("dragging the canvas moves every card with it, and Fit frames them all where they were", async ({ page }) => {
  await page.goto("/tests/fixtures/canvas-controls.html");
  await expect(page.locator(".flow-node")).toHaveCount(6);
  const fitted = await boxes(page);
  await page.mouse.move(12, 12);
  await page.mouse.down();
  await page.mouse.move(162, 102, { steps: 6 });
  await page.mouse.up();
  const moved = await boxes(page);
  for (const [i, box] of moved.entries()) {
    expect(box.left - fitted[i].left, box.name + " moved across").toBeCloseTo(150, 0);
    expect(box.top - fitted[i].top, box.name + " moved down").toBeCloseTo(90, 0);
  }
  await page.getByRole("button", { name: "Fit canvas" }).click();
  await expect.poll(async () => (await boxes(page)).map((box) => Math.round(box.left) + "," + Math.round(box.top))).toEqual(fitted.map((box) => Math.round(box.left) + "," + Math.round(box.top)));
});

test("a tree too big for the canvas fits it whole, when it opens, at Fit and at Arrange", async ({ page }) => {
  await page.goto("/tests/fixtures/canvas-controls.html?goblins=30");
  await expect(page.locator(".flow-node")).toHaveCount(31);
  expect(await outside(page)).toEqual([]);
  expect(await zoom(page), "below today's 35 percent floor").toBeLessThan(35);
  await page.getByRole("button", { name: "Zoom in" }).click();
  await page.getByRole("button", { name: "Zoom in" }).click();
  expect(await outside(page)).not.toEqual([]);
  await page.getByRole("button", { name: "Fit canvas" }).click();
  await expect.poll(() => outside(page)).toEqual([]);
  await page.getByRole("button", { name: "Zoom in" }).click();
  await page.getByRole("button", { name: "Arrange" }).click();
  await expect.poll(() => outside(page)).toEqual([]);
});
