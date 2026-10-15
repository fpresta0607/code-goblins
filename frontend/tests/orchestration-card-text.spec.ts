import { expect, test, type Locator, type Page } from "./site";

// A part of a card's text and the lines it takes: all its lines, those a clip
// leaves at least some of, and those it leaves whole.
interface Part { part: string; text: string; lines: number; shown: number; whole: number }
interface Fit {
  card: { width: number; height: number };
  parts: Part[];
  // What the card paints beyond its border box, each picture a clip cuts, and
  // each element that holds more than it shows.
  spills: string[];
  cut: string[];
  truncated: string[];
}

// The parts of the cards on the canvas, by the element each sits in, the
// first that holds it naming it.
const PARTS: [string, string][] = [
  [".card-status-text, .card-copy > .plain-status", "status"], [".tree-child-runs", "runs"], [".state-chip", "state"], [".tree-memory", "memory"],
  [".card-clock", "clock"], [".card-pr", "pull request"], [".subscription-dial", "dial"], ["strong", "title"], [".browser-indicator", "browser"],
  [".project-label", "project"], ["small", "relation"], [".node-role", "role"], [".node-task", "task"], [".node-status", "status"],
  [".tree-tally", "tally"], [".tree-count-words", "count"], [".tree-count-memory", "memory"],
];

// What a card paints, measured from the rendered page: each line of its text
// and each picture in it (avatar, baby goblin, status dot, mark, icon, dial)
// as the box each covers, a picture's ring or shadow included, once the
// boxes that clip them have cut it. Anything painted beyond the card's border
// box spills, a picture a clip cuts is cut, and a line a clip cuts is a cut
// or an ellipsis to the Overlord. The chevron that hangs half below a
// goblin's card, its glow and text only a screen reader reads are not on the
// card's face.
function fit(card: Element, kinds: [string, string][]): Fit {
  const frame = card.getBoundingClientRect(), scale = frame.width / (card as HTMLElement).offsetWidth;
  const name = (element: Element) => element.tagName.toLowerCase() + (element.classList.length ? "." + [...element.classList].join(".") : "");
  const isOffFace = (element: Element) => !!element.closest(".node-disclosure, .activity-glow, .sr-only");
  const painted = (box: { left: number; top: number; right: number; bottom: number }, from: Element) => {
    let { left, top, right, bottom } = box;
    for (let at: Element | null = from; at && at !== card.parentElement; at = at.parentElement) {
      const style = getComputedStyle(at);
      if (style.overflow === "visible") continue;
      const clip = at.getBoundingClientRect(), margin = style.overflow === "clip" ? (parseFloat(style.overflowClipMargin) || 0) * scale : 0;
      left = Math.max(left, clip.left - margin); top = Math.max(top, clip.top - margin); right = Math.min(right, clip.right + margin); bottom = Math.min(bottom, clip.bottom + margin);
    }
    return { left, top, right, bottom, isSeen: right > left && bottom > top };
  };
  // How far a picture's first outer shadow, such as a status dot's ring,
  // reaches past its box.
  const reach = (element: Element) => {
    const shadow = getComputedStyle(element).boxShadow;
    if (shadow === "none" || shadow.includes("inset")) return 0;
    const [x = 0, y = 0, blur = 0, spread = 0] = (shadow.split(/,(?![^(]*\))/)[0].match(/-?[\d.]+px/g) || []).map(parseFloat);
    return (Math.max(Math.abs(x), Math.abs(y)) + blur + spread) * scale;
  };
  const spills: string[] = [], cut: string[] = [], truncated: string[] = [];
  const isBeyond = (box: { left: number; top: number; right: number; bottom: number }) =>
    box.left < frame.left - 1 || box.right > frame.right + 1 || box.top < frame.top - 1 || box.bottom > frame.bottom + 1;
  for (const element of card.querySelectorAll(".goblin-avatar, .baby-goblin, .status-dot, .mark, .icon, .subscription-ring, svg")) {
    if (isOffFace(element)) continue;
    const own = element.getBoundingClientRect(), out = reach(element);
    const whole = { left: own.left - out, top: own.top - out, right: own.right + out, bottom: own.bottom + out };
    const box = painted(whole, element.parentElement!);
    if (box.isSeen && isBeyond(box)) spills.push(name(element));
    if (box.left > whole.left + .5 || box.top > whole.top + .5 || box.right < whole.right - .5 || box.bottom < whole.bottom - .5) cut.push(name(element));
  }
  for (const element of [card, ...card.querySelectorAll("*")]) {
    if (isOffFace(element) || element instanceof SVGElement) continue;
    if (getComputedStyle(element).overflow !== "visible" && (element.scrollHeight > element.clientHeight + 1 || element.scrollWidth > element.clientWidth + 1)) truncated.push(name(element));
  }
  const parts: Part[] = [];
  const walker = document.createTreeWalker(card, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent!.replace(/\s+/g, " ").trim();
    const host = node.parentElement!;
    if (!text || isOffFace(host)) continue;
    const kind = kinds.find(([selector]) => host.closest(selector))?.[1] ?? name(host);
    const range = document.createRange();
    range.selectNodeContents(node);
    // A line's box is as tall as its font's ascent and descent, a little more
    // than the line height its slot in the text has, so a line is whole when
    // its slot is, and shown when half of its slot is, at the canvas's zoom.
    const slot = (parseFloat(getComputedStyle(host).lineHeight) || parseFloat(getComputedStyle(host).fontSize) * 1.2) * scale;
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
  return { card: { width: Math.round(frame.width / scale), height: Math.round(frame.height / scale) }, parts, spills, cut, truncated };
}

// The screenshot's status carries the time of day in the viewer's zone.
test.use({ timezoneId: "America/Los_Angeles", locale: "en-US" });

async function open(page: Page, view = "canvas") {
  await page.goto("/tests/fixtures/orchestration-card-text.html" + (view === "canvas" ? "" : "?view=" + view));
  await expect(page.locator(view === "canvas" ? ".flow-node" : view === "lineage" ? ".workflow-node" : ".task-card-shell").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  // The pointer rests on empty canvas, where no card lifts under it.
  await page.mouse.move(2, 2);
}
const card = (page: Page, title: string): Locator => page.locator(".flow-node").filter({ has: page.locator("strong", { hasText: title }) });
const baby = (page: Page, name: string): Locator => page.locator(".tree-child").filter({ has: page.locator("strong", { hasText: name }) });
const measure = async (cards: Locator) => Promise.all((await cards.all()).map((one) => one.evaluate(fit, PARTS)));
const describe = (seen: Fit) => seen.parts.map((part) => part.part + ": " + part.text);

// The Overlord's screenshot of Woody's card: the first line of its title stood
// above the card's top border and the last of its status, "4:05 AM", hung
// below the bottom one. A card holds all of what it says.
test("a goblin's card holds its whole title, project and status, as in the Overlord's screenshot", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const seen = await card(page, "Woody").evaluate(fit, PARTS);

  // Assert
  expect(describe(seen)).toEqual(["title: Woody - Tour Guide", "project: code-goblins", "status: Allowance: resumes Oct 14, 4:05 AM"]);
  expect(seen.spills).toEqual([]);
  expect(seen.cut).toEqual([]);
  expect(seen.truncated).toEqual([]);
  expect(seen.parts.filter((part) => part.whole < part.lines).map((part) => part.part + " is cut")).toEqual([]);
});

// The Overlord's screenshot of a baby goblin under Jules: its memory, "4 MB",
// hung over the card's bottom border. A baby goblin's card holds all of what
// it says, and says what it runs.
test("a baby goblin's card holds its whole name, what it runs, its state and its memory, as in the Overlord's screenshot", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const seen = await baby(page, "Jules IX").evaluate(fit, PARTS);

  // Assert
  expect(describe(seen)).toEqual(["title: Jules IX - Watcher Reviewer", "runs: Review the queue watcher", "state: Working 30m", "memory: 4 MB"]);
  expect(seen.spills).toEqual([]);
  expect(seen.cut).toEqual([]);
  expect(seen.truncated).toEqual([]);
  expect(seen.parts.filter((part) => part.whole < part.lines).map((part) => part.part + " is cut")).toEqual([]);
});

// Text a card cannot hold may end in an ellipsis, but none of it is painted
// beyond the card, whatever it says, and no part of it goes unseen.
for (const [title, parts] of [
  ["Wilhelmina", ["title", "browser", "project", "status", "relation"]],
  ["Cornelius", ["title", "browser", "project", "status"]],
  ["Bartholomew", ["title", "project", "status", "clock", "pull request"]],
  ["CFO", ["title", "status", "dial"]],
] as const) {
  test(`${title}'s card paints none of its text beyond its border`, async ({ page }) => {
    // Arrange
    await open(page);

    // Act
    const seen = await card(page, title).evaluate(fit, PARTS);

    // Assert: the card holds the parts this case is meant to crowd in.
    expect(seen.parts.map((part) => part.part)).toEqual(parts);
    expect(seen.spills).toEqual([]);
    expect(seen.cut).toEqual([]);
    expect(seen.parts.filter((part) => !part.shown).map((part) => part.part + " is hidden")).toEqual([]);
  });
}

// Every card on the canvas, a goblin's, a baby goblin's and a folded
// goblin's count, at his window, and every card of the list the canvas
// becomes at phone width.
for (const { view, width, height } of [
  { view: "canvas", width: 1440, height: 1200 },
  { view: "canvas", width: 1920, height: 1080 },
  { view: "lineage", width: 390, height: 844 },
  { view: "lineage", width: 320, height: 700 },
]) {
  test(`no card's text leaves its border, baby goblins included, on the ${view} at ${width}px`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height });
    await open(page, view);
    if (view === "canvas") await page.getByRole("button", { name: "Collapse descendants of Jules - Process Keeper" }).click();
    else for (const count of await page.locator(".tree-count").all()) await count.click();
    const selector = view === "canvas" ? ".flow-node, .tree-branches .tree-child, .tree-branch .tree-count" : ".workflow-node, .tree-rail .tree-child, .tree-lineage .tree-count";

    // Act
    const seen = await measure(page.locator(selector));

    // Assert: every kind of card was measured, and none spills or hides a part.
    expect(seen.length).toBeGreaterThanOrEqual(view === "canvas" ? 11 : 13);
    expect(seen.flatMap((one) => one.spills.map((spill) => describe(one)[0] + ": " + spill))).toEqual([]);
    expect(seen.flatMap((one) => one.cut.map((part) => describe(one)[0] + ": " + part + " is cut"))).toEqual([]);
    // A card on the phone's rail grows, so what a baby goblin runs and its
    // state wrap there whole.
    const isRail = view === "lineage";
    expect(seen.flatMap((one) => one.truncated.filter((element) => isRail && /state-chip|tree-child-runs/.test(element)).map((element) => describe(one)[0] + ": " + element))).toEqual([]);
    expect(seen.flatMap((one) => one.parts.filter((part) => !part.shown).map((part) => describe(one)[0] + ": " + part.part + " is hidden"))).toEqual([]);
  });
}

// A baby goblin with the longest name, state and helper's title of the
// fixture still shows every part, ended in an ellipsis where it must.
for (const name of ["Bartholomew Jr.", "Bartholomew II", "Reconcile the nightly"]) {
  test(`${name}'s baby goblin card shows every part inside its border`, async ({ page }) => {
    // Arrange
    await open(page);

    // Act
    const seen = await baby(page, name).evaluate(fit, PARTS);

    // Assert
    expect(seen.parts.map((part) => part.part)).toEqual(["title", "runs", "state", "memory"].filter((part) => part !== "memory" || name !== "Bartholomew II"));
    expect(seen.spills).toEqual([]);
    expect(seen.cut).toEqual([]);
    expect(seen.parts.filter((part) => !part.shown).map((part) => part.part + " is hidden")).toEqual([]);
  });
}

// The Overlord's screenshot of Mabel's baby goblins cut "Detached, working 5"
// short. The longest state a baby goblin has shows whole on one line.
test("a long-running subagent's state shows whole", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const seen = await baby(page, "Bartholomew Jr.").evaluate(fit, PARTS);

  // Assert
  const state = seen.parts.find((part) => part.part === "state")!;
  expect(state.text).toBe("Working 12h 59m");
  expect(state.whole).toBe(state.lines);
  expect(seen.truncated.filter((element) => element.includes("state-chip"))).toEqual([]);
});

// What a card says, read from its rendered parts.
function facts(card: Element, selectors: Record<string, string>): Record<string, string> {
  const text = (element: Element) => [...element.childNodes].filter((node) => !(node instanceof Element && node.classList.contains("sr-only")))
    .map((node) => node.textContent).join("").replace(/\s+/g, " ").trim();
  return Object.fromEntries(Object.entries(selectors).map(([fact, selector]) => {
    const element = card.querySelector(selector);
    return [fact, !element ? "" : fact === "harness" ? element.getAttribute("aria-label") || "" : fact === "pull request" ? text(element) + " " + element.getAttribute("href") : text(element)];
  }));
}

// The Overlord, 2026-10-12: a card on the canvas shows the same facts as
// that goblin's card on the board, its name and title, project and status,
// how long it has run, its pull request and its harness mark.
for (const title of ["Olive - Billing Baron", "Woody - Tour Guide", "Bartholomew - Distinguished Infrastructure Archivist", "Jules - Process Keeper"]) {
  test(`${title}'s canvas card says what its board card says`, async ({ page }) => {
    // Arrange
    await open(page, "board");
    const board = await page.locator(".task-card-shell").filter({ has: page.locator(".card-title", { hasText: title }) })
      .evaluate(facts, { title: ".card-title", project: ".card-repo", status: ".card-status-text", clock: ".card-clock", "pull request": ".card-pr", harness: ".card-harness .mark" });
    await open(page);

    // Act
    const canvas = await card(page, title).evaluate(facts, { title: "strong", project: ".project-label", status: ".card-status-text", clock: ".card-clock", "pull request": ".card-pr", harness: ".card-harness .mark" });

    // Assert
    expect(board.title).toBe(title);
    expect(canvas).toEqual(board);
  });
}

// The CFO's card on the canvas opens its terminal and shows the weekly
// allowance of his account, as the board's CFO bar does.
test("the CFO's canvas card opens its terminal and shows the usage dial", async ({ page }) => {
  // Arrange
  await open(page);
  const cfo = card(page, "CFO");

  // Act
  await cfo.getByRole("button", { name: "Open the CFO's terminal" }).click();

  // Assert
  await expect(cfo.getByRole("progressbar", { name: /Claude 62% weekly remaining/ })).toBeVisible();
  await expect(cfo.getByRole("img", { name: "Claude Code" })).toBeVisible();
  expect(await page.evaluate(() => (window as Window & { cfoOpened?: number }).cfoOpened)).toBe(1);
});
