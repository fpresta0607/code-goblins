import { expect, test, type Page } from "./site";

// On 2026-10-01 pd-connect-quickstart's card covered cg-credential-requests',
// which the Overlord had placed by hand where pd-connect-quickstart's arranged
// place came to be, and cg-board-theme sat beside the goblin it waited on.
const LAYOUT_KEY = "cfo-orchestration-layout-v2";
const PLACED_BY_HAND = { "task:cg-credential-requests": { x: 686, y: 542 } };

async function cards(page: Page) {
  await expect(page.locator(".flow-node")).toHaveCount(9);
  return page.locator(".flow-node").evaluateAll((nodes) => nodes.map((node) => {
    const box = node.getBoundingClientRect();
    return { name: node.querySelector("strong")!.textContent!, left: box.left, top: box.top, right: box.right, bottom: box.bottom };
  }));
}
// The pairs of cards of which one covers any part of the other.
const covered = async (page: Page) => (await cards(page)).flatMap((one, i, all) => all.slice(i + 1)
  .filter((other) => one.left < other.right && other.left < one.right && one.top < other.bottom && other.top < one.bottom)
  .map((other) => one.name + " and " + other.name));

for (const size of [{ width: 1440, height: 900 }, { width: 900, height: 700 }, { width: 390, height: 844 }]) {
  test(`no card covers another and a waiting goblin sits under the one it waits on, at ${size.width} by ${size.height}`, async ({ page }) => {
    await page.setViewportSize(size);
    await page.addInitScript(([key, layout]) => localStorage.setItem(key, layout), [LAYOUT_KEY, JSON.stringify(PLACED_BY_HAND)]);
    await page.goto("/tests/fixtures/orchestration-layout.html");
    expect(await covered(page)).toEqual([]);
    const all = await cards(page);
    const card = (name: string) => all.find((one) => one.name === name)!;
    const waiting = card("cg-board-theme"), awaited = card("cg-cfo-wakes");
    expect(waiting.top, "under the goblin it waits on").toBeGreaterThan(awaited.bottom);
    expect(waiting.left < awaited.right && awaited.left < waiting.right, "beneath it, not off to one side").toBe(true);
    await expect(page.locator(".dependency-line")).toHaveCount(1);
  });
}

test("dragging a card saves only that card's place, and the card it lands on makes room", async ({ page }) => {
  await page.goto("/tests/fixtures/orchestration-layout.html");
  const center = async (name: string) => {
    const box = (await page.locator(".flow-node").filter({ hasText: name }).boundingBox())!;
    return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  };
  const from = await center("cg-native-desktop"), onto = await center("cg-hidden-windows");
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(onto.x, onto.y, { steps: 8 });
  await page.mouse.up();
  await expect.poll(() => page.evaluate((key) => Object.keys(JSON.parse(localStorage.getItem(key) || "{}")), LAYOUT_KEY)).toEqual(["task:cg-native-desktop"]);
  await expect.poll(() => covered(page)).toEqual([]);
});
