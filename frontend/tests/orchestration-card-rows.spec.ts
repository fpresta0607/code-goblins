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

// What a card's marks take: the lines its people stand on, each mark its
// border leaves outside it, the card and the line under it where they leave
// the room their place gives them, and whether the card holds more than it
// shows.
function marksOf(card: Element): { lines: number; spills: string[]; isScrolling: boolean } {
  const frame = card.querySelector<HTMLElement>(".task-card-shell")!;
  const outside = (parts: Element[], within: Element) => {
    const outer = within.getBoundingClientRect();
    return parts.filter((part) => { const box = part.getBoundingClientRect(); return box.left < outer.left - 1 || box.right > outer.right + 1 || box.top < outer.top - 1 || box.bottom > outer.bottom + 1; })
      .map((part) => part.getAttribute("class") || part.tagName);
  };
  const people = [...card.querySelectorAll(".same-area-avatars > *")];
  return {
    lines: new Set(people.map((person) => Math.round(person.getBoundingClientRect().top))).size,
    spills: [...outside([...card.querySelectorAll(".card-icons > *"), ...people], frame), ...outside([frame, ...card.querySelectorAll(".flow-session-details")], card)],
    isScrolling: frame.scrollHeight > frame.clientHeight + 1 || frame.scrollWidth > frame.clientWidth + 1,
  };
}

// The lines a part of a baby goblin's card needs and the lines it shows.
// What it needs is measured on a copy of the part that nothing limits, at
// the part's own width and in its own font.
function linesOf(card: Element, selector: string): { needs: number; shows: number } {
  const part = card.querySelector<HTMLElement>(selector)!, line = parseFloat(getComputedStyle(part).lineHeight);
  const free = part.cloneNode(true) as HTMLElement;
  free.style.cssText = "display: block; -webkit-line-clamp: none; white-space: normal; overflow: visible";
  part.after(free);
  const needs = Math.round(free.offsetHeight / line);
  free.remove();
  return { needs, shows: Math.round(part.offsetHeight / line) };
}

async function open(page: Page, view = "canvas", hasEveryMark = false) {
  await page.goto("/tests/fixtures/orchestration-card-text.html?view=" + view + (hasEveryMark ? "&marks" : ""));
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

// A goblin with every mark a card wears, a ticket, a pull request, four
// people in the same area and its harness mark, has more marks than fit
// beside a clock as long as 2h 15m, so its people take a second line and the
// row grows. The clock stays right under the project at the text's edge,
// never in the middle of the taller row, and the card still holds it all.
for (const { view, width, height } of [{ view: "canvas", width: 1440, height: 900 }, { view: "lineage", width: 390, height: 844 }]) {
  test(`a goblin's clock stays right under its project when its marks take a second line, on the ${view}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height });
    await open(page, view, true);
    const card = page.locator(view === "canvas" ? ".flow-node.has-task" : ".workflow-node.has-task").filter({ has: page.locator(".card-title", { hasText: "Bartholomew" }) });
    await expect(card.locator(".card-clock")).toContainText("2h 15m");
    for (const [mark, count] of [[".card-ticket", 1], [".card-pr", 1], [".same-area-person", 3], [".same-area-more", 1], [".card-harness", 1]] as const) await expect(card.locator(mark), mark).toHaveCount(count);

    // Act
    const seen = await card.evaluate(measure, GOBLIN);
    const marks = await card.evaluate(marksOf);

    // Assert: the marks need a second line, the case this test is for.
    expect(marks.lines, "the lines the people in the same area stand on").toBeGreaterThan(1);
    expectOneGroup(seen, "the card with every mark", view === "canvas");
    expect(marks.spills, "marks outside the card, or the card outside its place").toEqual([]);
    expect(marks.isScrolling, "the card holds more than it shows").toBe(false);
  });
}

// A baby goblin's name takes up to three lines and what it runs up to two,
// each ending in an ellipsis past them. At the card's width Bartholomew II -
// Observability Investigator needs three lines for its name, the helper that
// reconciles the exports needs more, and what Bartholomew II runs needs at
// least two.
for (const { title, baby, part, least, most, shows } of [
  { title: "a baby goblin's name that needs three lines shows all three", baby: "Bartholomew II", part: ".tree-child-copy strong", least: 3, most: 3, shows: 3 },
  { title: "a baby goblin's name that needs more than three lines shows three and is cut short", baby: "Reconcile the nightly", part: ".tree-child-copy strong", least: 4, most: Infinity, shows: 3 },
  { title: "what a baby goblin runs shows on two lines when it needs them", baby: "Bartholomew II", part: ".tree-child-runs", least: 2, most: Infinity, shows: 2 },
]) {
  test(title, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width: 1440, height: 900 });
    await open(page);
    const card = page.locator(".tree-branches .tree-child").filter({ has: page.locator("strong", { hasText: baby }) });

    // Act
    const seen = await card.evaluate(linesOf, part);

    // Assert: the part needs the lines this case is for, and shows these.
    expect(seen.needs, "the lines the part needs").toBeGreaterThanOrEqual(least);
    expect(seen.needs, "the lines the part needs").toBeLessThanOrEqual(most);
    expect(seen.shows, "the lines the part shows").toBe(shows);
  });
}
