import { CEO_DOOR, expect, test, type Locator, type Page } from "./site";

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
  [".tree-child-model", "model"],
  [".card-status-text, .card-copy > .plain-status", "status"], [".tree-child-runs", "runs"], [".state-chip", "state"], [".tree-memory", "memory"],
  [".card-clock", "clock"], [".card-pr", "pull request"], [".subscription-dial", "dial"], ["strong", "title"], [".browser-indicator", "browser"],
  [".project-label, .card-repo", "project"], ["small", "relation"], [".node-role", "role"], [".node-task", "task"], [".node-status", "status"], [".flow-session-details", "session"],
  [".tree-tally", "tally"], [".tree-count-words", "count"], [".tree-count-memory", "memory"], [".afk-toggle-label", "AFK"], [".afk-header > .muted", "AFK words"],
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

for (const size of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`the CEO's subscription dials and spend never overlap at fit zoom at ${size.width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize(size);
    const isPhone = size.width === 390;
    await page.goto("/tests/fixtures/orchestration-card-text.html?crowded&view=" + (isPhone ? "lineage" : "canvas"));
    await page.evaluate(() => document.fonts.ready);
    const ceo = page.locator(isPhone ? ".workflow-node.role-ceo" : ".flow-node").filter({ has: page.locator("strong", { hasText: /^CEO$/ }) });
    await expect(ceo.locator(".subscription-dial")).toHaveCount(3);

    // Act
    const overlaps = await ceo.evaluate((node) => {
      const frame = node.getBoundingClientRect();
      const parts = [...node.querySelectorAll(".subscription-ring, .subscription-percent, .key-spend-spent, .key-spend-budget, .card-harness, .icon-button")]
        .filter((element) => element.getClientRects().length)
        .map((element) => ({ name: element.classList.value, box: element.getBoundingClientRect() }));
      return parts.flatMap((one, index) => [
        ...parts.slice(index + 1).filter((other) => one.box.left < other.box.right - .5 && other.box.left < one.box.right - .5 && one.box.top < other.box.bottom - .5 && other.box.top < one.box.bottom - .5).map((other) => one.name + " overlaps " + other.name),
        ...(one.box.left < frame.left - .5 || one.box.right > frame.right + .5 || one.box.top < frame.top - .5 || one.box.bottom > frame.bottom + .5 ? [one.name + " leaves the card"] : []),
      ]);
    });

    // Assert
    expect(overlaps).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath(`ceo-dials-${size.width}.png`) });
  });
}

// The Overlord's screenshot of Woody's card: the first line of its title stood
// above the card's top border and the last of its status, "4:05 AM", hung
// below the bottom one. A card holds all of what it says.
test("a goblin's card holds its whole title, project and status, as in the Overlord's screenshot", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const seen = await card(page, "Woody").evaluate(fit, PARTS);

  // Assert
  expect(describe(seen)).toEqual(["title: Woody - Tour Guide", "status: Waiting on Jules", "project: code-goblins", "clock: 1h 35m", "session: Session details unavailable"]);
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
  expect(describe(seen)).toEqual(["title: Jules IX - Watcher Reviewer", "runs: Review the queue watcher", "model: Model not recorded", "state: Working 30m", "memory: 4 MB"]);
  expect(seen.spills).toEqual([]);
  expect(seen.cut).toEqual([]);
  expect(seen.truncated).toEqual([]);
  expect(seen.parts.filter((part) => part.whole < part.lines).map((part) => part.part + " is cut")).toEqual([]);
});

// Text a card cannot hold may end in an ellipsis, but none of it is painted
// beyond the card, whatever it says, and no part of it goes unseen.
for (const [title, parts] of [
  ["Wilhelmina", ["title", "status", "project", "clock", "browser"]],
  ["Cornelius", ["title", "status", "project", "clock", "browser", "session"]],
  ["Bartholomew", ["title", "status", "project", "clock", "session"]],
  ["CEO", ["title", "status", "dial", "AFK"]],
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
    expect(seen.parts.map((part) => part.part)).toEqual(["title", "runs", "model", "state", "memory"].filter((part) => part !== "memory" || name !== "Bartholomew II"));
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
    return [fact, !element ? "" : fact === "harness" ? element.getAttribute("aria-label") || "" : fact === "pull request" ? element.getAttribute("href") || "" : text(element)];
  }));
}

// A goblin whose task names no session says beside its card on the canvas
// whether its native terminal is running, and never calls an unknown terminal
// running. A phone's lineage list prints no words on its branches
// (phone-branch-text.spec.ts).
for (const [state, text] of [["busy", "Native terminal running, session details unavailable"], ["parked", "Native terminal running, session details unavailable"], ["unknown", "Session details unavailable"]]) {
  test(`canvas at 1440px says a ${state} terminal's session details are unavailable`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width: 1440, height: 900 });

    // Act
    await page.goto("/tests/fixtures/orchestration-card-text.html?terminal=" + state);

    // Assert
    const task = page.locator(".flow-node").filter({ has: page.locator(".task-card", { hasText: "Olive - Billing Baron" }) });
    await expect(task.locator(".flow-session-details")).toHaveText(text);
  });
}

for (const [view, width] of [["canvas", 1440], ["lineage", 320]] as const) {
  test(`${view} reuses the board task card and its controls at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const selectors = { title: ".card-title", status: ".card-status-text", clock: ".card-clock", repository: ".card-repo", "pull request": ".card-pr", harness: ".card-harness .mark" };
    await open(page, "board");
    const board = await page.locator(".task-card-shell").filter({ hasText: "Olive - Billing Baron" }).evaluate(facts, selectors);

    await open(page, view);

    const task = page.locator(view === "canvas" ? ".flow-node" : ".workflow-node").filter({ hasText: "Olive - Billing Baron" });
    await expect(task.locator(".task-card-shell")).toHaveCount(1);
    expect(await task.evaluate(facts, selectors)).toEqual(board);
    await expect(task.locator("button button, button a, a button")).toHaveCount(0);
    await task.locator(".task-card").focus();
    await expect(task.locator(".task-card")).toBeFocused();
    expect(await task.locator(".goblin-avatar").evaluate((face) => (face as HTMLElement).offsetWidth)).toBe(44);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}

// The Overlord, 2026-10-12: a card on the canvas shows the same facts as
// that goblin's card on the board, its name and title, its status, how long
// it has run, its pull request and its harness mark. The board's card is one
// row since 2026-10-17, so its project is in its panel, and its pull request
// is an icon: what both open is compared.
for (const title of ["Olive - Billing Baron", "Woody - Tour Guide", "Bartholomew - Distinguished Infrastructure Archivist", "Jules - Process Keeper"]) {
  test(`${title}'s canvas card says what its board card says`, async ({ page }) => {
    // Arrange
    await open(page, "board");
    const board = await page.locator(".task-card-shell").filter({ has: page.locator(".card-title", { hasText: title }) })
      .evaluate(facts, { title: ".card-title", status: ".card-status-text", clock: ".card-clock", "pull request": ".card-pr", harness: ".card-harness .mark" });
    await open(page);

    // Act
    const canvas = await card(page, title).evaluate(facts, { title: "strong", status: ".card-status-text", clock: ".card-clock", "pull request": ".card-pr", harness: ".card-harness .mark" });

    // Assert
    expect(board.title).toBe(title);
    expect(canvas).toEqual(board);
  });
}

// The CEO's card on the canvas opens its terminal and shows the weekly
// allowance of his account, as the board's CEO bar does.
// The switch is reachable in Orchestration too, where the bar is not, right
// beside the button that opens the CEO (the Overlord, 2026-11-05).
for (const view of ["canvas", "lineage"]) {
  test(`the CEO's card on the ${view} keeps its AFK switch right beside the button that opens it`, async ({ page }) => {
    // Act
    await open(page, view);

    // Assert
    const end = page.locator(".ceo-pin-end");
    const afk = (await end.getByRole("switch", { name: "AFK mode" }).boundingBox())!;
    const opens = (await end.getByRole("button", { name: CEO_DOOR }).boundingBox())!;
    expect(Math.abs(afk.y + afk.height / 2 - (opens.y + opens.height / 2))).toBeLessThanOrEqual(1);
    expect(afk.x + afk.width).toBeLessThanOrEqual(opens.x);
    expect(opens.x - (afk.x + afk.width)).toBeLessThanOrEqual(16);
  });
}

// A switch that cannot be read says so in words, whatever room the CEO's card
// has for them: its foot keeps them inside the border, wholly shown and clear
// of everything else the card draws, with one dial and with all it can hold.
for (const { view, width, height } of [
  { view: "canvas", width: 1440, height: 1200 },
  { view: "lineage", width: 390, height: 844 },
  { view: "lineage", width: 320, height: 700 },
]) {
  for (const isCrowded of [false, true]) {
    test(`the CEO's card on the ${view} at ${width}px${isCrowded ? ", crowded," : ""} holds an unreadable switch's words inside its border`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height });

      // Act
      await page.goto(`/tests/fixtures/orchestration-card-text.html?unreadable&view=${view}${isCrowded ? "&crowded" : ""}`);
      await page.evaluate(() => document.fonts.ready);
      await page.mouse.move(2, 2);

      // Assert
      const ceo = page.locator(view === "canvas" ? ".flow-node" : ".workflow-node.role-ceo").filter({ has: page.locator("strong", { hasText: /^CEO$/ }) });
      const seen = await ceo.evaluate(fit, PARTS);
      expect(seen.spills).toEqual([]);
      expect(seen.cut).toEqual([]);
      const words = seen.parts.find((part) => part.part === "AFK words");
      expect(words?.text).toBe("Unreadable: a press resets it");
      expect(words?.whole).toBe(words?.lines);
      const overlaps = await ceo.evaluate((node) => {
        const said = node.querySelector(".afk-header > .muted")!.getBoundingClientRect();
        return [...node.querySelectorAll("strong, .plain-status, .card-status-text, .subscription-dial, .afk-toggle, .icon-button, .card-harness")]
          .filter((part) => { const box = part.getBoundingClientRect(); return box.width > 0 && said.left < box.right - .5 && box.left < said.right - .5 && said.top < box.bottom - .5 && box.top < said.bottom - .5; })
          .map((part) => part.className || part.tagName);
      });
      expect(overlaps).toEqual([]);
    });
  }
}

test("the CEO's canvas card opens its terminal and shows the usage dial", async ({ page }) => {
  // Arrange
  await open(page);
  const ceo = card(page, "CEO");

  // Act
  await ceo.getByRole("button", { name: CEO_DOOR }).click();

  // Assert
  await expect(ceo.getByRole("progressbar", { name: /Claude 62% weekly remaining/ })).toBeVisible();
  await expect(ceo.getByRole("img", { name: "Claude Code" })).toBeVisible();
  expect(await page.evaluate(() => (window as Window & { ceoOpened?: number }).ceoOpened)).toBe(1);
});
