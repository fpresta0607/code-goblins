import { expect, test, type Locator, type Page } from "@playwright/test";

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
