import { expect, test, type Locator, type Page } from "./site";

// The Overlord, 2026-11-11, of the Orchestration view: the cards carry every
// part of the board's task card, but "the content sits off-center in the card
// and there is a lot of empty space". The canvas gave a goblin's card its
// fixed size and the card spread its rows over it, so its project sat alone
// between two empty bands with its clock at the card's edge, under the
// portrait, and a baby goblin's card was another shape, its text centred
// under its head. What a card says is one group: its rows beside its portrait
// on one left edge, evenly apart, the group in the middle of the card.
interface Box { left: number; top: number; right: number; bottom: number }
interface Rows {
  // The portrait, each row of text beside it from the top down, the marks at
  // the card's corner and the room the card leaves above and below them all.
  face: Box; rows: { part: string; box: Box }[]; marks: Box | null; above: number; below: number;
}

// The rows of a goblin's card and of a baby goblin's, by the element each is.
const GOBLIN = { frame: ".task-card-shell", face: ".goblin-avatar", rows: [".card-head", ".card-copy > .plain-status", ".card-activity", ".card-repo", ".card-clock"], marks: ".card-icons" };
const BABY = { frame: "", face: ".baby-goblin", rows: [".tree-child-copy strong", ".tree-child-runs", ".tree-child-model", ".state-chip", ".tree-memory"], marks: "" };

// What a card draws, in the card's own pixels whatever the canvas's zoom.
function measure(card: Element, shape: typeof GOBLIN): Rows {
  const frame = (shape.frame ? card.querySelector(shape.frame)! : card) as HTMLElement;
  const outer = frame.getBoundingClientRect(), scale = outer.width / frame.offsetWidth;
  const box = (element: Element): Box => {
    const own = element.getBoundingClientRect();
    return { left: (own.left - outer.left) / scale, top: (own.top - outer.top) / scale, right: (own.right - outer.left) / scale, bottom: (own.bottom - outer.top) / scale };
  };
  const rows = shape.rows.flatMap((selector) => { const element = card.querySelector(selector); return element ? [{ part: selector, box: box(element) }] : []; });
  const marks = shape.marks ? card.querySelector(shape.marks) : null, face = box(card.querySelector(shape.face)!);
  const all = [face, ...rows.map((row) => row.box), ...(marks ? [box(marks)] : [])];
  return { face, rows, marks: marks && box(marks), above: Math.min(...all.map((one) => one.top)), below: outer.height / scale - Math.max(...all.map((one) => one.bottom)) };
}

// A card whose rows are one group: each starts where the first does, past the
// portrait, none is further than a line's gap under the one above it, none
// runs under the marks, and the group sits in the middle of the card.
function expectOneGroup(seen: Rows, name: string, isCentred: boolean) {
  const edge = seen.rows[0].box.left;
  expect(seen.face.right, name + ": the portrait is beside the text").toBeLessThanOrEqual(edge);
  expect(seen.rows.filter((row) => Math.abs(row.box.left - edge) > 1).map((row) => row.part), name + ": rows off the text's left edge").toEqual([]);
  const bands = seen.rows.slice(1).flatMap((row, i) => row.box.top - seen.rows[i].box.bottom > 6 ? [seen.rows[i].part + " to " + row.part + ": " + Math.round(row.box.top - seen.rows[i].box.bottom) + "px"] : []);
  expect(bands, name + ": empty bands between rows").toEqual([]);
  const last = seen.rows[seen.rows.length - 1].box;
  if (seen.marks && seen.marks.top < last.bottom && last.top < seen.marks.bottom) expect(last.right, name + ": the last row ends before the marks").toBeLessThanOrEqual(seen.marks.left);
  if (isCentred) expect(Math.abs(seen.above - seen.below), name + ": as much room above the group as below it").toBeLessThanOrEqual(2);
}

const measureAll = async (cards: Locator, shape: typeof GOBLIN) => Promise.all((await cards.all()).map((card) => card.evaluate(measure, shape)));

async function open(page: Page, view = "canvas") {
  await page.goto("/tests/fixtures/orchestration-card-text.html" + (view === "canvas" ? "" : "?view=" + view));
  await expect(page.locator(view === "canvas" ? ".flow-node" : ".workflow-node").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  // The pointer rests on empty canvas, where no card lifts under it.
  await page.mouse.move(2, 2);
}

// At the zoom that fits the canvas in his window and zoomed in past full size.
for (const zoom of ["fitted", "zoomed in"]) {
  test(`a goblin's canvas card and its baby goblins' each hold their rows as one group, ${zoom}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width: 1440, height: 900 });
    await open(page);
    if (zoom === "zoomed in") {
      for (let i = 0; i < 8; i++) await page.getByRole("button", { name: "Zoom in" }).click();
      await page.mouse.move(2, 2);
    }

    // Act
    const goblins = await measureAll(page.locator(".flow-node.has-task"), GOBLIN);
    const babies = await measureAll(page.locator(".tree-branches .tree-child"), BABY);

    // Assert: every goblin in progress and every baby goblin was measured.
    expect(goblins).toHaveLength(7);
    expect(babies).toHaveLength(5);
    goblins.forEach((seen, i) => expectOneGroup(seen, "goblin card " + (i + 1), true));
    babies.forEach((seen, i) => expectOneGroup(seen, "baby goblin card " + (i + 1), true));
  });
}

// The list the canvas becomes at phone width draws the same card, which
// grows with what it says there.
test("a goblin's card on the phone's list holds its rows as one group", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, "lineage");

  // Act
  const goblins = await measureAll(page.locator(".workflow-node.has-task"), GOBLIN);

  // Assert
  expect(goblins).toHaveLength(7);
  goblins.forEach((seen, i) => expectOneGroup(seen, "goblin card " + (i + 1), false));
});
