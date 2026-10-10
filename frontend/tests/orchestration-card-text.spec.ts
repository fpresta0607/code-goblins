import { expect, test, type Locator, type Page } from "./site";

// A part of a card's text and the lines it takes: all its lines, those a clip
// leaves at least some of, and those it leaves whole.
interface Part { part: string; text: string; lines: number; shown: number; whole: number }
interface Fit {
  card: { width: number; height: number };
  parts: Part[];
  // What the card paints beyond its border box, and each element that holds
  // more than it shows.
  spills: string[];
  truncated: string[];
}

// What a canvas card paints, measured from the rendered page: each line of its
// text, its avatar and its status dot as the box each covers once the boxes
// that clip them have cut it. Anything painted beyond the card's border box
// spills, and a line a clip cuts is a cut or an ellipsis to the Overlord.
function fit(card: Element): Fit {
  const frame = card.getBoundingClientRect();
  const name = (element: Element) => element.tagName.toLowerCase() + (element.classList.length ? "." + [...element.classList].join(".") : "");
  const painted = (box: DOMRect, from: Element) => {
    let { left, top, right, bottom } = box;
    for (let at: Element | null = from; at && at !== card; at = at.parentElement) {
      if (getComputedStyle(at).overflow === "visible") continue;
      const clip = at.getBoundingClientRect();
      left = Math.max(left, clip.left); top = Math.max(top, clip.top); right = Math.min(right, clip.right); bottom = Math.min(bottom, clip.bottom);
    }
    return { left, top, right, bottom, isSeen: right > left && bottom > top };
  };
  const spills: string[] = [], truncated: string[] = [];
  const isBeyond = (box: { left: number; top: number; right: number; bottom: number }) =>
    box.left < frame.left - 1 || box.right > frame.right + 1 || box.top < frame.top - 1 || box.bottom > frame.bottom + 1;
  const main = card.querySelector(".flow-node-main")!;
  for (const element of main.querySelectorAll(".card-copy, .goblin-avatar, .status-dot")) {
    const box = painted(element.getBoundingClientRect(), element.parentElement!);
    if (box.isSeen && isBeyond(box)) spills.push(name(element));
  }
  for (const element of [main, ...main.querySelectorAll("*")]) {
    if (getComputedStyle(element).overflow !== "visible" && (element.scrollHeight > element.clientHeight || element.scrollWidth > element.clientWidth)) truncated.push(name(element));
  }
  const kinds: [string, string][] = [["strong", "title"], [".browser-indicator", "browser"], [".project-label", "project"], [".plain-status", "status"], ["small", "relation"]];
  const parts: Part[] = [];
  const walker = document.createTreeWalker(main, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent!.replace(/\s+/g, " ").trim();
    if (!text) continue;
    const host = node.parentElement!;
    const kind = kinds.find(([selector]) => host.closest(selector))?.[1] ?? name(host);
    const range = document.createRange();
    range.selectNodeContents(node);
    // A line's box is as tall as its font's ascent and descent, a little more
    // than the line height its slot in the text has, so a line is whole when
    // its slot is, and shown when half of its slot is.
    const slot = parseFloat(getComputedStyle(host).lineHeight);
    const lines = new Map<number, { isShown: boolean; isWhole: boolean }>();
    for (const line of range.getClientRects()) {
      const box = painted(line, host);
      const height = Math.min(line.height, slot), at = Math.round(line.top / 2), same = lines.get(at);
      const isWhole = box.isSeen && box.right - box.left > line.width - .5 && box.bottom - box.top > height - .5;
      lines.set(at, { isShown: !!same?.isShown || (box.isSeen && box.bottom - box.top >= height / 2), isWhole: (same?.isWhole ?? true) && isWhole });
      if (box.isSeen && isBeyond(box)) spills.push(kind + " '" + text.slice(0, 24) + "'");
    }
    const shown = [...lines.values()].filter((line) => line.isShown).length, whole = [...lines.values()].filter((line) => line.isWhole).length;
    const earlier = parts.find((part) => part.part === kind);
    if (earlier) { earlier.text += " " + text; earlier.lines += lines.size; earlier.shown += shown; earlier.whole += whole; }
    else parts.push({ part: kind, text, lines: lines.size, shown, whole });
  }
  const scale = frame.width / (card as HTMLElement).offsetWidth;
  return { card: { width: Math.round(frame.width / scale), height: Math.round(frame.height / scale) }, parts, spills, truncated };
}

// The screenshot's status carries the time of day in the viewer's zone.
test.use({ timezoneId: "America/Los_Angeles" });

async function open(page: Page) {
  await page.goto("/tests/fixtures/orchestration-card-text.html");
  await expect(page.locator(".flow-node")).toHaveCount(5);
  await page.evaluate(() => document.fonts.ready);
  // The pointer rests on empty canvas, where no card lifts under it.
  await page.mouse.move(2, 2);
}
const card = (page: Page, title: string): Locator => page.locator(".flow-node").filter({ has: page.locator("strong", { hasText: title }) });

// The Overlord's screenshot of Woody's card: the first line of its title stood
// above the card's top border and the last of its status, "4:05 AM", hung
// below the bottom one. A card holds all of what it says.
test("a goblin's card holds its whole title, project and status, as in the Overlord's screenshot", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const seen = await card(page, "Woody").evaluate(fit);

  // Assert
  expect(seen.parts.map((part) => part.part + ": " + part.text)).toEqual(["title: Woody - Tour Guide", "project: code-goblins", "status: Allowance: resumes Oct 14, 4:05 AM"]);
  expect(seen.spills).toEqual([]);
  expect(seen.truncated).toEqual([]);
  expect(seen.parts.filter((part) => part.whole < part.lines).map((part) => part.part + " is cut")).toEqual([]);
  expect(seen.card).toEqual({ width: 292, height: 132 });
});

// Text a card cannot hold may end in an ellipsis, but none of it is painted
// beyond the card, whatever it says, and no part of it goes unseen.
for (const [title, parts] of [
  ["Wilhelmina", ["title", "browser", "project", "status", "relation"]],
  ["Cornelius", ["title", "browser", "project", "status"]],
  ["Bartholomew", ["title", "project", "status"]],
  ["CFO", ["title", "status"]],
] as const) {
  test(`${title}'s card paints none of its text beyond its border`, async ({ page }) => {
    // Arrange
    await open(page);

    // Act
    const seen = await card(page, title).evaluate(fit);

    // Assert: the card holds the parts this case is meant to crowd in.
    expect(seen.parts.map((part) => part.part)).toEqual(parts);
    expect(seen.spills).toEqual([]);
    expect(seen.parts.filter((part) => !part.shown).map((part) => part.part + " is hidden")).toEqual([]);
    expect(seen.card).toEqual({ width: 292, height: 132 });
  });
}

// A browser line is a fourth part of a card's text, and it leaves a status of
// two lines whole.
test("a card whose browser is active still holds its whole status", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const seen = await card(page, "Cornelius").evaluate(fit);

  // Assert
  expect(seen.parts.find((part) => part.part === "status")).toMatchObject({ text: "Allowance: resumes Oct 14, 4:05 AM", lines: 2, whole: 2 });
});
