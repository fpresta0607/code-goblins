import { expect, test, type Locator, type Page } from "./site";

declare global {
  interface Window { tipOutlived?: Promise<number> }
}

interface Crowding { cards: number; overlaps: string[]; outside: string[]; tallTitles: string[]; smallText: string[] }

// Everything a card draws, each as the boxes it covers: every line of text, and
// every chip, button, avatar, dot and icon as one box. Two parts of one card
// that cover the same pixels overprint; a part outside its card spills.
function crowding(): Crowding {
  const atomic = ".goblin-avatar, .rank, .status-dot, svg, a, button:not(.task-card), .next-chip";
  const report: Crowding = { cards: 0, overlaps: [], outside: [], tallTitles: [], smallText: [] };
  const name = (element: Element) => element.tagName.toLowerCase() + "." + [...element.classList].join(".");
  for (const card of document.querySelectorAll(".task-card-shell")) {
    const frame = (card.closest(".ranked-item") || card).getBoundingClientRect();
    report.cards++;
    const boxes: { owner: Element; label: string; rect: DOMRect }[] = [];
    const scope = card.closest(".ranked-item") || card;
    for (const element of scope.querySelectorAll(atomic)) {
      if (element.parentElement?.closest(atomic) || element.closest(".sr-only, dialog")) continue;
      const rect = element.getBoundingClientRect();
      if (rect.width && rect.height && getComputedStyle(element).visibility !== "hidden") boxes.push({ owner: element, label: name(element), rect });
    }
    const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const host = node.parentElement!;
      if (!node.textContent?.trim() || host.closest(atomic) || host.closest(".sr-only, dialog") || getComputedStyle(host).visibility === "hidden") continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      // Lines a clamped title hides are cut off by its clipping box, not drawn.
      const clips = [];
      for (let at: Element | null = host; at && at !== scope; at = at.parentElement) if (getComputedStyle(at).overflow !== "visible") clips.push(at.getBoundingClientRect());
      for (const line of range.getClientRects()) {
        let left = line.left, top = line.top, right = line.right, bottom = line.bottom;
        for (const clip of clips) { left = Math.max(left, clip.left); top = Math.max(top, clip.top); right = Math.min(right, clip.right); bottom = Math.min(bottom, clip.bottom); }
        if (right - left > 0 && bottom - top > 0) boxes.push({ owner: host, label: name(host) + " '" + node.textContent.trim().slice(0, 24) + "'", rect: new DOMRect(left, top, right - left, bottom - top) });
      }
      if (parseFloat(getComputedStyle(host).fontSize) < 16) report.smallText.push(name(host) + " " + getComputedStyle(host).fontSize);
    }
    for (const box of boxes) {
      if (box.rect.left < frame.left - 1 || box.rect.right > frame.right + 1 || box.rect.top < frame.top - 1 || box.rect.bottom > frame.bottom + 1) report.outside.push(box.label);
    }
    for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i], b = boxes[j];
      if (a.owner === b.owner || a.owner.contains(b.owner) || b.owner.contains(a.owner)) continue;
      const width = Math.min(a.rect.right, b.rect.right) - Math.max(a.rect.left, b.rect.left);
      const height = Math.min(a.rect.bottom, b.rect.bottom) - Math.max(a.rect.top, b.rect.top);
      if (width > 1 && height > 1) report.overlaps.push(a.label + " / " + b.label);
    }
    for (const title of card.querySelectorAll<HTMLElement>(".card-title")) {
      const lines = title.getBoundingClientRect().height / parseFloat(getComputedStyle(title).lineHeight);
      if (lines > 3.1) report.tallTitles.push(title.textContent!.slice(0, 30) + " " + lines.toFixed(1) + " lines");
    }
  }
  return report;
}

interface ShownTip { text: string; part: string; covers: string[]; onCard: boolean; side: string; offScreen: boolean; width: number; fontSize: number }

// The tips shown while the pointer or focus is on a part of a task card, each
// with the card's controls and links it covers, whether it lies on the card at
// all, which side of the card it is on and whether it leaves the screen. A tip is the board's floating tip or
// the part's own CSS tip, measured from its pseudo-element.
function shownTips(part: Element): ShownTip[] {
  const card = part.closest(".task-card-shell")!;
  const tips: { text: string; box: DOMRect; fontSize: number }[] = [];
  for (const tip of document.querySelectorAll<HTMLElement>("[role=tooltip]")) {
    if (getComputedStyle(tip).visibility !== "hidden") tips.push({ text: tip.textContent!, box: tip.getBoundingClientRect(), fontSize: parseFloat(getComputedStyle(tip).fontSize) });
  }
  const after = getComputedStyle(part, "::after");
  if (after.content !== "none" && after.content !== "normal" && parseFloat(after.opacity) > .99) {
    let block = part;
    while (getComputedStyle(block).position === "static") block = block.parentElement!;
    const frame = block.getBoundingClientRect(), shift = new DOMMatrixReadOnly(after.transform === "none" ? undefined : after.transform);
    const size = (length: string, edges: string[]) => parseFloat(length) + (after.boxSizing === "border-box" ? 0 : edges.reduce((sum, edge) => sum + parseFloat(after.getPropertyValue(edge)), 0));
    const width = size(after.width, ["padding-left", "padding-right", "border-left-width", "border-right-width"]);
    const height = size(after.height, ["padding-top", "padding-bottom", "border-top-width", "border-bottom-width"]);
    tips.push({ text: part.getAttribute("data-tip")!, box: new DOMRect(frame.left + block.clientLeft + parseFloat(after.left) + shift.e, frame.top + block.clientTop + parseFloat(after.top) + shift.f, width, height), fontSize: parseFloat(after.fontSize) });
  }
  const meets = (one: DOMRect, other: DOMRect) => Math.min(one.right, other.right) - Math.max(one.left, other.left) > 1 && Math.min(one.bottom, other.bottom) - Math.max(one.top, other.top) > 1;
  const shell = card.getBoundingClientRect();
  const controls = [...card.querySelectorAll("a, button:not(.task-card), [role=img]")].filter((control) => !control.contains(part) && !part.contains(control));
  return tips.map(({ text, box, fontSize }) => ({
    text, part: part.getAttribute("data-tip") || "", fontSize, width: Math.round(box.width),
    covers: controls.filter((control) => meets(box, control.getBoundingClientRect())).map((control) => control.getAttribute("aria-label") || control.textContent!.trim()),
    onCard: meets(box, shell),
    side: box.bottom <= shell.top ? "over" : box.top >= shell.bottom ? "under" : "neither",
    offScreen: box.left < 0 || box.top < 0 || box.right > innerWidth || box.bottom > innerHeight,
  }));
}

// Waits for the tip a part shows once the pointer is on it. A hover scrolls
// the part into view; point puts the pointer on it some other way.
async function tipsOf(part: Locator, point = () => part.hover()) {
  await point();
  let tips: ShownTip[] = [];
  await expect.poll(async () => (tips = await part.evaluate(shownTips)).length).toBeGreaterThan(0);
  return tips;
}

// Starts counting, from the next time the pointer or the focus leaves a part,
// the page's frames its tip is still drawn in. A tip goes as soon as its part
// is left however slow the machine is, so its going is counted in the page's
// own frames, not timed from the test.
function watchTipGo(part: Locator, leave: "pointerout" | "blur") {
  return part.evaluate((element, leave) => {
    window.tipOutlived = new Promise((resolve) => element.addEventListener(leave, () => {
      let frames = 0;
      const look = () => {
        if (!document.querySelector("[role=tooltip]")) resolve(frames);
        else { frames++; requestAnimationFrame(look); }
      };
      look();
    }, { once: true }));
  }, leave);
}

// What is wrong with the tips a part shows: a tip covering the card's
// controls or links, lying on the card, leaving the screen or too small to
// read, or a tip of some other part.
function tipProblems(tips: ShownTip[]) {
  return tips.flatMap((tip) => [
    ...(tip.text !== tip.part ? ["'" + tip.part + "' shows '" + tip.text + "'"] : []),
    ...(tip.covers.length ? ["'" + tip.text + "' covers " + tip.covers.join(", ")] : []),
    ...(tip.onCard ? ["'" + tip.text + "' lies on its card"] : []),
    ...(tip.offScreen ? ["'" + tip.text + "' leaves the screen"] : []),
    ...(tip.fontSize < 15 ? ["'" + tip.text + "' is " + tip.fontSize + " px"] : []),
  ]);
}

async function board(page: Page, width: number, region = 0, query = region ? "?board=" + region : "") {
  await page.setViewportSize({ width, height: 1400 });
  await page.goto("/tests/fixtures/card-layout.html" + query);
  await page.evaluate(() => document.fonts.ready);
  await expect(page.locator(".task-card-shell").first()).toBeVisible();
}

// A 280 px board region is the narrowest the board gets beside an open panel,
// 390 px is a phone, and 1000 px a board with room to spare.
for (const [width, region] of [[1400, 280], [390, 0], [1000, 0]]) {
  test(`at ${region || width} px no part of a task card overprints another, spills or shrinks`, async ({ page }) => {
    await board(page, width, region);
    const seen = await page.evaluate(crowding);
    expect(seen.cards).toBeGreaterThanOrEqual(5);
    expect({ overlaps: seen.overlaps, outside: seen.outside, tallTitles: seen.tallTitles, smallText: [...new Set(seen.smallText)] })
      .toEqual({ overlaps: [], outside: [], tallTitles: [], smallText: [] });
  });
}

// A card collapses to one column by the width of the list it sits in, so in
// the CFO panel's queue on a phone its controls go under its text, as they do
// in a board column that narrow, and leave the text the card's whole width.
test("at 390 px a card in the CFO panel's queue collapses to one column", async ({ page }) => {
  await board(page, 390, 0, "?queue");
  const shell = page.locator(".cfo-queue .task-card-shell").first();
  const list = (await page.locator(".cfo-queue .task-cards").first().boundingBox())!;
  const card = (await shell.locator(".task-card").boundingBox())!, controls = (await shell.locator(".task-controls").boundingBox())!;
  expect(list.width).toBeLessThan(420);
  expect(controls.y).toBeGreaterThanOrEqual(card.y + card.height);
  const seen = await page.evaluate(crowding);
  expect({ overlaps: seen.overlaps, outside: seen.outside, tallTitles: seen.tallTitles, smallText: [...new Set(seen.smallText)] })
    .toEqual({ overlaps: [], outside: [], tallTitles: [], smallText: [] });
});

test("a clamped title shows in full in the card's tip", async ({ page }) => {
  await board(page, 390);
  const card = page.getByRole("button", { name: /^The board's terminal/ }).first();
  await card.hover();
  await expect(card).toHaveAttribute("data-tip", /the agents' keys reach the program$/);
});

// On 2026-10-01 the tip of a waiting card's chip lay over the card's pull
// request and its controls. Every tip on a card opens beside, under or over the
// card, never on it, and stays on the screen, at a size that reads.
for (const [width, region] of [[1400, 280], [390, 0], [1000, 0]]) {
  test(`at ${region || width} px every tip on a task card opens clear of the card and on the screen`, async ({ page }) => {
    test.slow();
    await board(page, width, region);
    const problems: string[] = [];
    let shown = 0;
    for (const shell of await page.locator(".task-card-shell").all()) {
      // The card finds out that its title is clamped once the pointer is on it.
      const card = shell.locator(".task-card");
      await card.hover();
      if (await card.evaluate((button) => { const title = button.querySelector(".card-title")!; return title.scrollHeight > title.clientHeight + 1; })) await expect(card).toHaveAttribute("data-tip", /./);
      for (const part of await shell.locator("[data-tip]").all()) {
        const tips = await tipsOf(part);
        shown += tips.length;
        problems.push(...tipProblems(tips));
      }
    }
    expect(shown).toBeGreaterThanOrEqual(12);
    expect(problems).toEqual([]);
  });
}

test("a long title's tip is wide enough to read", async ({ page }) => {
  await board(page, 390);
  const [tip] = await tipsOf(page.getByRole("button", { name: /^The board's terminal/ }).first());
  expect(tip.width).toBeGreaterThanOrEqual(280);
});

// A tip opens over the card or under it, whichever edge its part is nearer.
test("a tip opens on the side of the card its part is nearer", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
  expect((await tipsOf(shell.getByRole("button", { name: /^Pause / }))).map((tip) => tip.side)).toEqual(["over"]);
  expect((await tipsOf(shell.locator(".card-harness [role=img]"))).map((tip) => tip.side)).toEqual(["under"]);
});

// A card at the bottom of the screen has no room under it, and one at the top
// has none over it, so a tip that would open there opens on the other side.
test("a tip with no room on its side of the card opens on the other side, clear of the card", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
  // The screen is 80 px taller than the card, which leaves room for a tip on
  // one side of it only.
  await page.setViewportSize({ width: 1000, height: Math.ceil((await shell.boundingBox())!.height) + 80 });
  // The page scrolls only once it has taken the new screen's height, so the
  // scroll is repeated until the card is where it was sent.
  const scrollCardTo = (top: number) => expect.poll(() => shell.evaluate((card, top) => {
    scrollBy(0, card.getBoundingClientRect().top - top);
    return Math.round(card.getBoundingClientRect().top);
  }, top)).toBe(top);
  // The pointer goes straight to the part, since a hover may scroll the page
  // to bring the part to the middle of the screen.
  const pointAt = (part: Locator) => async () => {
    const box = (await part.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  };
  await scrollCardTo(76);
  const mark = shell.locator(".card-harness [role=img]"), pause = shell.getByRole("button", { name: /^Pause / });
  const low = await tipsOf(mark, pointAt(mark));
  expect(tipProblems(low)).toEqual([]);
  expect(low.map((tip) => tip.side)).toEqual(["over"]);
  await page.mouse.move(1, 1);
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await scrollCardTo(4);
  const high = await tipsOf(pause, pointAt(pause));
  expect(tipProblems(high)).toEqual([]);
  expect(high.map((tip) => tip.side)).toEqual(["under"]);
});

test("a card's tip is gone as soon as the pointer leaves the part it names", async ({ page }) => {
  await board(page, 1000);
  const stop = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") }).getByRole("button", { name: /^Stop / });
  expect(await tipsOf(stop)).toHaveLength(1);
  await watchTipGo(stop, "pointerout");
  await page.mouse.move(1, 1);
  expect(await page.evaluate(() => window.tipOutlived)).toBeLessThanOrEqual(2);
});

test("a card's tip shows on keyboard focus, clear of the card, and goes with the focus", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
  await shell.locator(".card-pr").focus();
  await page.keyboard.press("Tab");
  const terminal = shell.getByRole("button", { name: /^Open the terminal of / });
  await expect(terminal).toBeFocused();
  let tips: ShownTip[] = [];
  await expect.poll(async () => (tips = await terminal.evaluate(shownTips)).length).toBe(1);
  expect(tips[0].text).toBe("Terminal");
  expect(tipProblems(tips)).toEqual([]);
  await watchTipGo(terminal, "blur");
  await terminal.evaluate((element) => (element as HTMLElement).blur());
  expect(await page.evaluate(() => window.tipOutlived)).toBeLessThanOrEqual(2);
});

// Focus stays on a part while the page scrolls under it, so its tip moves with
// its card instead of staying where the card was.
test("a keyboard-focused part's tip follows its card when the page scrolls", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
  // The screen is shorter than the board, so the page has room to scroll.
  await page.setViewportSize({ width: 1000, height: 1000 });
  await shell.locator(".card-pr").focus();
  await page.keyboard.press("Tab");
  const terminal = shell.getByRole("button", { name: /^Open the terminal of / });
  await expect(terminal).toBeFocused();
  await expect.poll(async () => (await terminal.evaluate(shownTips)).length).toBe(1);
  const top = Math.round(await shell.evaluate((card) => card.getBoundingClientRect().top)) - 60;
  // The page scrolls only once it has taken the new screen's height, so the
  // scroll is repeated until the card is 60 px higher.
  await expect.poll(() => shell.evaluate((card, top) => {
    scrollBy(0, card.getBoundingClientRect().top - top);
    return Math.round(card.getBoundingClientRect().top);
  }, top)).toBe(top);
  let tips: ShownTip[] = [];
  await expect.poll(async () => tipProblems(tips = await terminal.evaluate(shownTips))).toEqual([]);
  expect(tips.map((tip) => tip.side)).toEqual(["over"]);
});

// A part's tip can change while the pointer rests on it, as Start's and
// Resume's do with the memory that is free.
test("a tip shows what its part says now", async ({ page }) => {
  await board(page, 1000);
  const pause = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") }).getByRole("button", { name: /^Pause / });
  expect((await tipsOf(pause)).map((tip) => tip.text)).toEqual(["Pause"]);
  await pause.evaluate((button) => button.setAttribute("data-tip", "Pause needs 2 GB free"));
  await expect.poll(async () => (await pause.evaluate(shownTips)).map((tip) => tip.text)).toEqual(["Pause needs 2 GB free"]);
});

// A card's status line says which goblin it waits on, and the card does not
// say it again in a chip of its own.
test("a waiting card names the goblin it waits on once, in its status line", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
  await expect(shell.locator(".card-status-text")).toHaveText("Waiting on cg-board-kill-with-a-long-goblin-name");
  expect((await shell.innerText()).split("cg-board-kill-with-a-long-goblin-name")).toHaveLength(2);
  await expect(shell.getByRole("button", { name: /which this goblin is waiting on$/ })).toHaveCount(0);
});

// The whole card opens its panel: a click on its padding or beside its chips
// selects it, as a click on its text does.
for (const [spot, at] of [
  ["its corner under its controls", async (shell: Locator) => { const box = (await shell.boundingBox())!; return { x: box.width - 8, y: box.height - 8 }; }],
  ["the room right of its pull request", async (shell: Locator) => { const box = (await shell.boundingBox())!, chip = (await shell.locator(".card-pr").boundingBox())!; return { x: chip.x + chip.width + 12 - box.x, y: chip.y + chip.height / 2 - box.y }; }],
  ["its harness mark", async (shell: Locator) => { const box = (await shell.boundingBox())!, mark = (await shell.locator(".card-harness").boundingBox())!; return { x: mark.x + mark.width / 2 - box.x, y: mark.y + mark.height / 2 - box.y }; }],
] as const) {
  for (const width of [390, 1000]) {
    test(`at ${width} px a click on ${spot} selects the card`, async ({ page }) => {
      await board(page, width);
      const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
      const card = shell.locator(".task-card");
      await expect(card).toHaveAttribute("aria-pressed", "false");
      await shell.click({ position: await at(shell) });
      await expect(card).toHaveAttribute("aria-pressed", "true");
    });
  }
}

// A card chosen by its harness mark is the card focus returns to when its
// panel closes, as it is when chosen by any other part of it.
test("closing a card chosen by its harness mark returns focus to the card", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
  const card = shell.locator(".task-card");
  await shell.locator(".card-harness").click();
  await expect(card).toHaveAttribute("aria-pressed", "true");
  await page.keyboard.press("Escape");
  await expect(card).toHaveAttribute("aria-pressed", "false");
  await expect(card).toBeFocused();
});

// Each card carries the mark of the harness its goblin runs, and the mark's tip
// names the harness, the model and the effort.
test("a card shows its harness mark with the harness, model and effort in its tip", async ({ page }) => {
  await board(page, 1000);
  const mark = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") }).locator(".card-harness [role=img]");
  await expect(mark).toHaveAttribute("aria-label", "Codex · gpt-6-astra · xhigh");
  await expect(mark).toHaveAttribute("data-tip", "Codex · gpt-6-astra · xhigh");
  await expect(mark.locator("svg path")).toHaveCount(1);
});
