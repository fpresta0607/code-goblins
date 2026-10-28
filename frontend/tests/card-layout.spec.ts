import { expect, holdStream, test, type Locator, type Page } from "./site";

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

interface ShownTip { text: string; part: string; side: string; offScreen: boolean; width: number; fontSize: number }

// The tips shown while the pointer or focus is on a part of a task card, each
// with the side of the part it touches, none when its edge is not the 8 px
// its arrow spans from the part or it lies beside the part without facing
// it, and whether it leaves the screen. A tip is the board's floating tip or
// the part's own CSS tip, measured from its pseudo-element.
function shownTips(part: Element): ShownTip[] {
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
  const own = part.getBoundingClientRect();
  return tips.map(({ text, box, fontSize }) => {
    const gaps = { above: own.top - box.bottom, below: box.top - own.bottom, right: box.left - own.right, left: own.left - box.right };
    const across = (side: string) => side === "above" || side === "below" ? Math.min(box.right, own.right) - Math.max(box.left, own.left) : Math.min(box.bottom, own.bottom) - Math.max(box.top, own.top);
    return {
      text, part: part.getAttribute("data-tip") || "", fontSize, width: Math.round(box.width),
      side: (["above", "below", "right", "left"] as const).find((side) => Math.abs(gaps[side] - 8) < 1 && across(side) > 0) ?? "",
      offScreen: box.left < 0 || box.top < 0 || box.right > innerWidth || box.bottom > innerHeight,
    };
  });
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

// What is wrong with the tips a part shows: a tip away from its part,
// leaving the screen or too small to read, or a tip of some other part.
function tipProblems(tips: ShownTip[]) {
  return tips.flatMap((tip) => [
    ...(tip.text !== tip.part ? ["'" + tip.part + "' shows '" + tip.text + "'"] : []),
    ...(!tip.side ? ["'" + tip.text + "' does not touch its part"] : []),
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

// In a list as narrow as the CFO panel's queue on a phone, a card's clock and
// icons, its harness mark last, take a line under its name and status, which
// keep the card's whole width, inside the list and with nothing drawn over
// anything else.
test("at 390 px a card in the CFO panel's queue puts its clock and icons under its text", async ({ page }) => {
  await board(page, 390, 0, "?queue");
  const shell = page.locator(".cfo-queue .task-card-shell").first();
  const list = (await page.locator(".cfo-queue .task-cards").first().boundingBox())!;
  const card = (await shell.locator(".task-card").boundingBox())!, mark = (await shell.locator(".card-harness").boundingBox())!;
  expect(list.width).toBeLessThan(420);
  expect(mark.y).toBeGreaterThanOrEqual(card.y + card.height);
  expect(mark.x + mark.width).toBeLessThanOrEqual(list.x + list.width);
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

// The Overlord, 2026-10-09: a tip on a card landed away from what he
// hovered. Every tip on a card touches its own part and stays on the screen,
// at a size that reads. Each layout shows a known number of tips, so a walk
// that stops finding them fails.
for (const [width, region, tipCount] of [[1400, 280, 9], [390, 0, 8], [1000, 0, 6]]) {
  test(`at ${region || width} px every tip on a task card touches its part and stays on the screen`, async ({ page }) => {
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
    expect(shown).toBeGreaterThanOrEqual(tipCount);
    expect(problems).toEqual([]);
  });
}

test("a long title's tip is wide enough to read", async ({ page }) => {
  await board(page, 390);
  const [tip] = await tipsOf(page.getByRole("button", { name: /^The board's terminal/ }).first());
  expect(tip.width).toBeGreaterThanOrEqual(280);
});

// A tip on a card opens against its own part, above it where there is room,
// never against the whole card.
test("a tip on a card opens above its own part, not the card", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") });
  const finished = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/198']") });
  for (const part of [finished.locator(".card-pr"), shell.locator(".card-harness [role=img]")]) {
    const tips = await tipsOf(part);
    expect({ problems: tipProblems(tips), sides: tips.map((tip) => tip.side) }).toEqual({ problems: [], sides: ["above"] });
    await page.mouse.move(1, 1);
    await expect(page.getByRole("tooltip")).toHaveCount(0);
  }
});

test("a card's tip is gone as soon as the pointer leaves the part it names", async ({ page }) => {
  await board(page, 1000);
  const mark = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") }).locator(".card-harness [role=img]");
  expect(await tipsOf(mark)).toHaveLength(1);
  await watchTipGo(mark, "pointerout");
  await page.mouse.move(1, 1);
  expect(await page.evaluate(() => window.tipOutlived)).toBeLessThanOrEqual(2);
});

test("a card's tip shows on keyboard focus, touching its part, and goes with the focus", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/198']") });
  // A finished card's pull request comes after the card itself, and its tip
  // names it and its branch.
  await shell.locator(".task-card").focus();
  await page.keyboard.press("Tab");
  const chip = shell.locator(".card-pr");
  await expect(chip).toBeFocused();
  let tips: ShownTip[] = [];
  await expect.poll(async () => (tips = await chip.evaluate(shownTips)).length).toBe(1);
  expect(tips[0].text).toBe("code-goblins #198 · fix/orchestration-pulse-end");
  expect(tipProblems(tips)).toEqual([]);
  await watchTipGo(chip, "blur");
  await chip.evaluate((element) => (element as HTMLElement).blur());
  expect(await page.evaluate(() => window.tipOutlived)).toBeLessThanOrEqual(2);
});

// Focus stays on a part while the page scrolls under it, so its tip moves with
// its part instead of staying where the part was.
test("a keyboard-focused part's tip follows its part when the page scrolls", async ({ page }) => {
  await board(page, 1000);
  const shell = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/198']") });
  // The screen is shorter than the board, so the page has room to scroll,
  // and the finished card is the board's last, so focus scrolls the page
  // down to it and leaves room to scroll back up.
  await page.setViewportSize({ width: 1000, height: 700 });
  await shell.locator(".task-card").focus();
  await page.keyboard.press("Tab");
  const chip = shell.locator(".card-pr");
  await expect(chip).toBeFocused();
  await expect.poll(async () => (await chip.evaluate(shownTips)).length).toBe(1);
  const top = Math.round(await shell.evaluate((card) => card.getBoundingClientRect().top)) + 60;
  // The page scrolls only once it has taken the new screen's height, so the
  // scroll is repeated until the card is 60 px lower.
  await expect.poll(() => shell.evaluate((card, top) => {
    scrollBy(0, card.getBoundingClientRect().top - top);
    return Math.round(card.getBoundingClientRect().top);
  }, top)).toBe(top);
  let tips: ShownTip[] = [];
  await expect.poll(async () => tipProblems(tips = await chip.evaluate(shownTips))).toEqual([]);
  expect(tips.map((tip) => tip.side)).toEqual(["above"]);
});

// A part's tip can change while the pointer rests on it, as a harness mark's
// does when its goblin moves to another effort.
test("a tip shows what its part says now", async ({ page }) => {
  await board(page, 1000);
  const mark = page.locator(".task-card-shell").filter({ has: page.locator(".card-pr[href$='/205']") }).locator(".card-harness [role=img]");
  expect((await tipsOf(mark)).map((tip) => tip.text)).toEqual(["Codex · GPT-6 Astra · xhigh"]);
  await mark.evaluate((part) => part.setAttribute("data-tip", "Codex · GPT-6 Astra · high"));
  await expect.poll(async () => (await mark.evaluate(shownTips)).map((tip) => tip.text)).toEqual(["Codex · GPT-6 Astra · high"]);
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
  ["its bottom corner", async (shell: Locator) => { const box = (await shell.boundingBox())!; return { x: box.width - 8, y: box.height - 8 }; }],
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
  await expect(mark).toHaveAttribute("aria-label", "Codex · GPT-6 Astra · xhigh");
  await expect(mark).toHaveAttribute("data-tip", "Codex · GPT-6 Astra · xhigh");
  await expect(mark.locator("svg path")).toHaveCount(1);
});

// The Overlord, 2026-10-07: "format and indent can fill whole box why so
// uneven". In his window, beside the CFO's panel, a live card's status
// starts where its name starts, as a row in a conversation list does since
// 2026-10-17. The Overlord, 2026-10-16: a card
// carries no action buttons, so a goblin with a harness update waiting shows
// no Update, Pause or Stop on its card; they are in its panel.
test.describe("in his window, beside the CFO's panel", () => {
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  test("a live card's status lines up under its name, and it carries no controls", async ({ page }) => {
    // Arrange
    const live = { id: "cg-board-trim", title: "Trim the board's header, meters and cards", project: "code-goblins", phase: "working", verified: false, generation: "g1", since: "2026-10-07T20:00:00Z", harness: "claude", harness_update: { harness: "claude", installed: "2026-10-07T20:30:00Z" } };
    await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], tasks: [live] });
    await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
    await page.goto("/");
    await page.keyboard.press("Control+Alt+1");
    const shell = page.locator(".task-card-shell").filter({ hasText: "Trim the board's" });
    await expect(shell).toBeVisible();

    // Act
    const layout = await shell.evaluate((card) => ({
      controls: card.querySelectorAll(".task-controls").length,
      statusUnderTheName: Math.round(card.querySelector(".card-status-text")!.getBoundingClientRect().left - card.querySelector(".card-title")!.getBoundingClientRect().left) <= 18,
    }));

    // Assert: no controls, and the status, after its dot, starts under the
    // name.
    expect(layout).toEqual({ controls: 0, statusUnderTheName: true });
    await expect(shell.getByRole("button", { name: /^(Update|Pause|Stop) / })).toHaveCount(0);
  });
});

// The Overlord, 2026-10-17, of Murray's card, its date running into a ticket,
// a pull request, two checks and its harness mark: "We don't need to have the
// full October 14, we could just put 10/14 to keep it smaller, and the checks
// that you have here, we don't need to have the test checks as an icon. That
// can be in the task panel. But for now, we just want to know the pull
// request and the issue." A card's bottom row holds its time, a date as its
// month and day and a time only for today, then its ticket, its pull request
// and its harness mark, none over another at the board's narrowest.
test.describe("in en-US at UTC", () => {
  test.use({ locale: "en-US", timezoneId: "UTC" });

  test("at 280 px a card's bottom row shows a short date and only its ticket and pull request, none over another", async ({ page }, testInfo) => {
    // Arrange
    await board(page, 1400, 280);
    const murray = page.locator(".task-card-shell").filter({ has: page.locator(".card-title", { hasText: "Murray - API Artisan" }) });
    const finished = page.locator(".task-card-shell").filter({ hasText: "Orchestration pulses end smoothly" });

    // Act
    await murray.screenshot({ path: testInfo.outputPath("murray.png") });
    const row = await murray.evaluate((shell) => {
      const parts = [...shell.querySelectorAll(":scope > .card-repo, :scope > .card-clock, :scope > .card-icons > *")];
      const kinds = parts.map((part) => ["card-repo", "card-clock", "card-ticket", "card-pr", "card-harness"].find((kind) => part.classList.contains(kind)) || part.className);
      const boxes = parts.map((part) => part.getBoundingClientRect()), overlaps: string[] = [];
      for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i], b = boxes[j];
        if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > 0 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 0) overlaps.push(kinds[i] + " / " + kinds[j]);
      }
      return { kinds, overlaps };
    });

    // Assert
    // The Overlord, 2026-10-17: "why do resume dates need to be so long and
    // typed out". Two days back reads as its weekday, the whole date in its tip.
    await expect(murray.locator(".card-clock time")).toHaveText("Mon");
    await expect(murray.locator(".card-clock time")).toHaveAttribute("data-tip", "Mon, Sep 28, 2026, 7:22 PM");
    await expect(finished.locator(".card-clock time")).toHaveText("9:15 AM");
    expect(row.kinds).toEqual(["card-repo", "card-clock", "card-ticket", "card-pr", "card-harness"]);
    expect(row.overlaps).toEqual([]);
  });
});
