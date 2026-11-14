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

// What a card's marks take: the lines they stand on, whether the first of
// them starts under the clock, each mark drawn over the clock and by how
// much, each mark its border leaves outside it, the card and the line under
// it where they leave the room their place gives them, and whether the card
// holds more than it shows. A mark is counted alone, the people in the same
// area one by one: a mark no line can break is drawn past the edge of the
// room the marks share, which that room's own box does not show.
function marksOf(card: Element): { lines: number; isUnderClock: boolean; overClock: string[]; spills: string[]; isScrolling: boolean } {
  const frame = card.querySelector<HTMLElement>(".task-card-shell")!;
  const named = (part: Element) => part.getAttribute("class") || part.tagName;
  const outside = (parts: Element[], within: Element) => {
    const outer = within.getBoundingClientRect();
    return parts.filter((part) => { const box = part.getBoundingClientRect(); return box.left < outer.left - 1 || box.right > outer.right + 1 || box.top < outer.top - 1 || box.bottom > outer.bottom + 1; }).map(named);
  };
  const people = [...card.querySelectorAll(".same-area-avatars > *")];
  const marks = [...card.querySelectorAll(".card-icons > :not(.same-area-avatars)"), ...people];
  const boxes = marks.map((mark) => mark.getBoundingClientRect()).sort((one, other) => one.top - other.top);
  // Marks on one line share its height, so a line starts where a mark's top
  // is at or under every mark above it.
  let lines = 0, floor = -Infinity;
  for (const box of boxes) { if (box.top >= floor) lines++; floor = Math.max(floor, box.bottom); }
  const clock = card.querySelector(".card-clock")!.getBoundingClientRect();
  return {
    lines, isUnderClock: boxes[0].top >= clock.bottom,
    overClock: marks.flatMap((mark) => {
      const box = mark.getBoundingClientRect(), over = Math.min(box.right, clock.right) - Math.max(box.left, clock.left);
      return over > 0 && box.top < clock.bottom && clock.top < box.bottom ? [named(mark) + " by " + Math.round(over) + "px"] : [];
    }),
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

// variants names what the fixture adds to Bartholomew, such as "&marks".
async function open(page: Page, view = "canvas", variants = "") {
  await page.goto("/tests/fixtures/orchestration-card-text.html?view=" + view + variants);
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

const EVERY_MARK = [[".card-ticket", 1], [".card-pr", 1], [".same-area-person", 3], [".same-area-more", 1], [".card-harness", 1]] as const;
const bartholomew = (page: Page, view: string) => page.locator(view === "canvas" ? ".flow-node.has-task" : ".workflow-node.has-task").filter({ has: page.locator(".card-title", { hasText: "Bartholomew" }) });

// A goblin with every mark a card wears, a ticket, a pull request, four
// people in the same area and its harness mark, has more marks than fit
// beside a clock as long as 2h 15m, so they take a second line and the row
// grows. The clock stays right under the project at the text's edge, never
// in the middle of the taller row, and the card still holds it all.
for (const { view, width, height } of [{ view: "canvas", width: 1440, height: 900 }, { view: "lineage", width: 390, height: 844 }]) {
  test(`a goblin's clock stays right under its project when its marks take a second line, on the ${view}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height });
    await open(page, view, "&marks");
    const card = bartholomew(page, view);
    await expect(card.locator(".card-clock")).toContainText("2h 15m");
    for (const [mark, count] of EVERY_MARK) await expect(card.locator(mark), mark).toHaveCount(count);

    // Act
    const seen = await card.evaluate(measure, GOBLIN);
    const marks = await card.evaluate(marksOf);

    // Assert: the marks need a second line, the case this test is for.
    expect(marks.lines, "the lines the marks stand on").toBeGreaterThan(1);
    expect(marks.isUnderClock, "the marks start under the clock").toBe(false);
    expectOneGroup(seen, "the card with every mark", view === "canvas");
    expect(marks.overClock, "marks drawn over the clock").toEqual([]);
    expect(marks.spills, "marks outside the card, or the card outside its place").toEqual([]);
    expect(marks.isScrolling, "the card holds more than it shows").toBe(false);
  });
}

// A card of the phone's list is as wide as the phone leaves it, and a card
// under another is narrower still. Browser active is one mark no line can
// break, so beside a clock that starts at the text's edge a narrow card had
// less room for it than it is wide, and it was drawn over the clock. A card's
// marks end its clock's row while the widest of them has room beside the
// clock, and take the row under it when it has none: what the card holds
// decides, never its width alone. A card 304px wide holds Browser active
// beside a clock that says 2h 15m and under the longest clock a goblin in
// progress has, "just started". A phone 320px wide leaves his card 246px and
// one 390px wide leaves it 316px.
for (const { wide, clock, isUnderClock } of [
  { wide: 246, clock: "2h 15m", isUnderClock: true },
  { wide: 246, clock: "just started", isUnderClock: true },
  { wide: 304, clock: "2h 15m", isUnderClock: false },
  { wide: 304, clock: "just started", isUnderClock: true },
  { wide: 316, clock: "2h 15m", isUnderClock: false },
  { wide: 336, clock: "just started", isUnderClock: false },
]) {
  test(`no mark covers the clock of a list card ${wide}px wide that says ${clock}`, async ({ page }) => {
    // Arrange: a phone as wide as leaves his card the width of this case.
    await page.setViewportSize({ width: 390, height: 844 });
    await open(page, "lineage", "&marks&browser" + (clock === "just started" ? "&fresh" : ""));
    const card = bartholomew(page, "lineage");
    const around = 390 - await card.evaluate((node: HTMLElement) => node.offsetWidth);
    await page.setViewportSize({ width: wide + around, height: 844 });
    await expect(card.locator(".card-clock")).toContainText(clock);
    await expect(card.locator(".card-icons .screen-open")).toHaveText("Browser active");
    for (const [mark, count] of EVERY_MARK) await expect(card.locator(mark), mark).toHaveCount(count);

    // Act
    const width = await card.evaluate((node: HTMLElement) => node.offsetWidth);
    const seen = await card.evaluate(measure, GOBLIN);
    const marks = await card.evaluate(marksOf);

    // Assert: the card is as wide as this case says.
    expect(width, "the card's width").toBe(wide);
    expect(marks.overClock, "marks drawn over the clock").toEqual([]);
    expect(marks.isUnderClock, "the marks start under the clock").toBe(isUnderClock);
    expectOneGroup(seen, "the card with every mark", false);
    expect(marks.spills, "marks outside the card").toEqual([]);
    expect(marks.isScrolling, "the card holds more than it shows").toBe(false);
  });
}

// The Overlord, 2026-11-12: a narrow card's marks move under its clock only
// when they would overlap it. On a phone 320px wide every card is narrow, and
// a card with a pull request and its harness mark, or its harness mark alone,
// has room for them beside its clock, so it keeps the row it had. Wilhelmina
// and Cornelius present a page, so their marks begin with Browser active,
// which has no room beside the clock, and take the row under it.
test("a narrow list card whose marks fit beside its clock keeps them on the clock's row", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 320, height: 844 });
  await open(page, "lineage", "&marks&browser");
  const others = page.locator(".workflow-node.has-task").filter({ hasNot: page.locator(".card-title", { hasText: "Bartholomew" }) });
  const isPresenting = (title: string) => title.startsWith("Wilhelmina") || title.startsWith("Cornelius");

  // Act
  const widths = await others.evaluateAll((nodes: HTMLElement[]) => nodes.map((node) => node.offsetWidth));
  const marks = await Promise.all((await others.all()).map((card) => card.evaluate(marksOf)));
  const goblins = await measureAll(others, GOBLIN);
  const titles = await others.locator(".card-title").allInnerTexts();

  // Assert: every other goblin was measured, each in a narrow card.
  expect(marks).toHaveLength(6);
  expect(Math.max(...widths), "the widest of the cards").toBeLessThanOrEqual(260);
  expect(titles.filter(isPresenting), "the cards that present a page").toHaveLength(2);
  expect(marks.map((one) => one.isUnderClock), "cards whose marks start under the clock").toEqual(titles.map(isPresenting));
  expect(marks.flatMap((one) => one.overClock), "marks drawn over a clock").toEqual([]);
  goblins.forEach((seen, i) => expectOneGroup(seen, "goblin card " + (i + 1), false));
});

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

// A baby goblin's state is longest in its first minute, and longest of all
// for a state the board does not know. Beside its head the card showed
// neither whole, each ending in an ellipsis, where the card with its head on
// top had room for both.
for (const state of ["Working just started", "Unknown just started"]) {
  test(`a baby goblin's card shows its state whole in its first minute: ${state}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width: 1440, height: 900 });
    await open(page, "canvas", "&fresh");
    const card = page.locator(".tree-branches .tree-child").filter({ has: page.locator(".state-chip-text", { hasText: state }) });
    await expect(card).toHaveCount(1);

    // Act
    const text = await card.locator(".state-chip-text").evaluate((chip: HTMLElement) => ({ needs: chip.scrollWidth, shows: chip.clientWidth }));
    const seen = await card.evaluate(measure, BABY);

    // Assert
    expect(text.shows, "the width of the state the card shows, of the " + text.needs + "px it needs").toBeGreaterThanOrEqual(text.needs);
    expectOneGroup(seen, "the baby goblin's card", true);
  });
}
