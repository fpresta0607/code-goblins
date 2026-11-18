import { expect, holdStream, test, type Locator, type Page } from "./site";

// The Overlord, 2026-11-18, of the board on a tablet while something waited
// on him: "On tablet sized screen board ceo commmand center header when
// command center has a notification text stretches into a single" and
// "Column simple flex fit please" (#2670). The CEO's lantern box held its
// actions at their whole width beside the line, so the line shrank to a word
// or two per row. Its line now takes the free width, and its actions go to a
// row of their own when they do not fit beside it.
const NOW = "2026-10-15T12:00:00Z";
const usage = (provider: string, percent: number) => ({ provider, status: "available", percent_remaining: percent, read_at: NOW, resets_at: "2026-10-20T22:27:42Z", source: "oauth", floor_percent: 5 });
const task = (id: string, title: string) => ({ id, title, project: "moss-garden", phase: "working", generation: id + "-1", verified: false });
const question = (id: string, forTask: string, createdAt: string) => ({ id, identity: "a".repeat(64), task: forTask, status: "pending", created_at: createdAt, text: "Which path stones should I lay?", options: ["Slate", "Gravel"], recommended: "Slate" });
const LINE = "1 question waits for the CEO (14 min). 3 goblins at work.";
const SNAPSHOT = {
  instance: "ceo-bar-fit", revision: 1, healthy: true, example: true, at: NOW, ceo_runs: true, ceo_terminal: "ceo", ceo_harness: "claude",
  subscriptions: [usage("claude", 62), usage("codex", 76)],
  actions_spend: { status: "available", month: "2026-10", total: { minutes: 700, used_usd: 12, billed_usd: 8.5 },
    repositories: [{ name: "moss", minutes: 700, used_usd: 12, billed_usd: 8.5 }], read_at: NOW, checked_at: NOW, detail: "" },
  tasks: [task("mg-fern", "Fern beds"), task("mg-pond", "Pond liner"), task("mg-shed", "Shed roof")],
  // One question for the Overlord, and one a goblin asked that the CEO has
  // left for 14 minutes, which the line names first, so it is the bar's
  // longer line.
  questions: [question("mg-asks", "", NOW), question("mg-fern-asks", "mg-fern", "2026-10-15T11:46:00Z")],
  attention: [], reviews: [], runs: [], credentials: [], actions: [],
};

// open shows the board at a width, with the CEO's panel beside it as the
// board opens or closed, and returns the CEO's lantern box.
async function open(page: Page, width: number, isPanelOpen: boolean) {
  await page.setViewportSize({ width, height: 900 });
  await page.clock.install({ time: new Date(NOW) });
  await holdStream(page, SNAPSHOT);
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  const close = page.getByRole("button", { name: "Close panel" });
  if (isPanelOpen) await expect(close).toBeVisible();
  else if (await close.isVisible()) await close.click();
  await expect(close).toBeVisible({ visible: isPanelOpen });
  const box = page.locator(".ceo-pin .dialogue-box");
  await expect(box.locator(".dialogue-text p")).toHaveText(LINE);
  await page.evaluate(() => document.fonts.ready);
  return box;
}

// fit reads the lantern box: how many rows its line wraps to and how wide
// they are, whether its actions sit beside the line or on a row under it,
// and each action's box against the bar's.
const fit = (box: Locator) => box.evaluate((element) => {
  const line = element.querySelector(".dialogue-text p")!;
  const range = document.createRange();
  range.selectNodeContents(line);
  const rows = new Set([...range.getClientRects()].map((rect) => Math.round(rect.top))).size;
  const bar = element.getBoundingClientRect();
  const text = line.getBoundingClientRect();
  const actions = element.querySelector(".dialogue-actions")!.getBoundingClientRect();
  const controls = [...element.querySelectorAll<HTMLElement>(".dialogue-actions button, .dialogue-actions [role=progressbar], .dialogue-actions [role=img]")];
  const isInside = controls.every((control) => {
    const part = control.getBoundingClientRect();
    return part.width > 0 && part.left >= bar.left && part.right <= bar.right && part.top >= bar.top && part.bottom <= bar.bottom;
  });
  return { rows, lineWidth: Math.round(text.width), isBeside: actions.top < text.bottom && actions.left >= text.right, isInside, controls: controls.length };
});

// A row of the line at 200 px holds several words of it at 16 px, never one
// or two.
const READABLE = 200;

for (const width of [768, 820, 1024, 1180]) {
  for (const isPanelOpen of [true, false]) {
    test(`at ${width}px with the CEO's panel ${isPanelOpen ? "open" : "closed"} and something waiting the CEO's line reads in whole lines and every action stays in the bar`, async ({ page }, testInfo) => {
      // Arrange
      const box = await open(page, width, isPanelOpen);

      // Act
      const layout = await fit(box);
      await box.screenshot({ path: testInfo.outputPath(`ceo-bar-${width}-${isPanelOpen ? "panel" : "alone"}.png`) });

      // Assert
      expect(layout.lineWidth, "the width of a row of the CEO's line").toBeGreaterThanOrEqual(READABLE);
      expect(layout.isInside, "every action inside the bar").toBe(true);
      expect(layout.controls).toBe(6);
      await expect(box.getByRole("button", { name: "Open Command Center: 2 waiting on you" })).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "no sideways scroll").toBe(true);
    });
  }
}

test("at a desktop width where everything fits the lantern box stays one row", async ({ page }) => {
  // Arrange
  const box = await open(page, 1707, false);

  // Act
  const layout = await fit(box);

  // Assert
  expect(layout).toMatchObject({ rows: 1, isBeside: true, isInside: true, controls: 6 });
});
