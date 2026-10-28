import { expect, test, type Page } from "./site";
import { GOBLIN, open, openGoblin, PAGE, snapshot } from "./session-board";

// The Overlord, 2026-10-26: "just have a streaming ands stats line in bottom
// cornern liek this ... where teh time stamp is plus a banner that you can
// open for usage stats on teh session". The conversation's bottom corner
// carries the stats, and a banner above the composer opens the session's
// usage in place. Since 2026-10-27 the working animation sits at the end of
// the reply in progress and the time facts under his message
// (tests/reply-end.spec.ts).

const USAGE = { input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000, cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5" };
const PAGE_WITH_USAGE = { ...PAGE, usage: USAGE };
const MEMORY = { ...GOBLIN, tree: { task_id: GOBLIN.id, generation: GOBLIN.generation, harness: GOBLIN.harness, memory: 208 * 2 ** 20, own_memory: 208 * 2 ** 20, conversation_at: "", children: [], unread: [], source_updated_at: "", fetched_at: "" } };
const footer = (page: Page) => page.locator(".session-footer");

test("the conversation's bottom line carries the stats, not a wall of numbers, and the time facts sit under his message", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { ...MEMORY, phase: "settled" }), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);

  // Assert
  const line = footer(page);
  await expect(line).toBeVisible();
  await expect(line).not.toContainText(/Sent|Replied|Working/);
  await expect(page.locator('[data-message="m0"] .message-receipt')).toHaveText(/^Sent [^·]+$/);
  await expect(page.locator(".reply-working")).toHaveCount(0);
  // The fuller numbers live on in the banner, not on this line.
  await expect(line).not.toContainText("$3.209");
  await expect(line).not.toContainText("59.3%");
  await page.getByRole("button", { name: "Context: 59.3% used", exact: true }).click();
  await expect(page.getByRole("tooltip", { name: "Session statistics", exact: true })).toContainText("1.4M");
});

test("the reply in progress ends with the harness's working animation while the session works", async ({ page }, testInfo) => {
  // Arrange
  await open(page, snapshot({}, { ...MEMORY, runtime: { state: "working" } }), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);

  // Assert
  const working = page.locator(".reply-working .working-indicator");
  await expect(working).toBeVisible();
  await expect(footer(page)).not.toContainText("Working");
  // The indicator takes Claude Code's own ink from the board's mark.
  await expect(working).toHaveCSS("color", "rgb(217, 119, 87)");
  await expect(page.locator(".context-pane .working-indicator:visible")).toHaveCount(1);
  await testInfo.attach("footer-working.png", { body: await page.screenshot(), contentType: "image/png" });
});

// The Overlord, 2026-10-28, on a panel at rest that ran 32 animations and was
// drawn again four times a second: "32 animations??? remvoe like all but the
// streaming one". A working session's conversation runs one animation, of
// opacity alone, which the compositor draws, and its working spot is never
// drawn again while nothing happens.
test("a working session's conversation runs one opacity animation and redraws nothing at rest", async ({ page }) => {
  // Arrange
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  await open(page, snapshot({}, { ...MEMORY, runtime: { state: "working" } }), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);
  await expect(page.locator(".reply-working .working-indicator")).toBeVisible();
  await page.evaluate(() => {
    const seen = { mutations: 0 };
    new MutationObserver((records) => { seen.mutations += records.length; })
      .observe(document.querySelector(".reply-working")!, { subtree: true, childList: true, attributes: true, characterData: true });
    Object.assign(window, { seen });
  });

  // Act
  await page.clock.runFor(20_000);

  // Assert
  const mutations = await page.evaluate(() => (window as unknown as { seen: { mutations: number } }).seen.mutations);
  const animations = await page.evaluate(() => document.getAnimations()
    .filter((animation) => animation.playState === "running" && animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".context-pane"))
    .map((animation) => ({
      name: (animation as CSSAnimation).animationName,
      properties: [...new Set((animation.effect as KeyframeEffect).getKeyframes().flatMap((frame) => Object.keys(frame)))].filter((key) => !["offset", "computedOffset", "easing", "composite"].includes(key)),
    })));
  expect(mutations).toBe(0);
  expect(animations).toEqual([{ name: "working-breath", properties: ["opacity"] }]);
});

test("a session whose usage cannot be read leaves its ring unmeasured and says why", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);

  // Assert: the footer says it once, in one sentence on the notice, and the
  // disclosure says it too.
  await expect(footer(page).locator(".notice")).toHaveText("Claude usage could not be read.");
  await expect(page.getByRole("region", { name: "Session usage", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Context usage unavailable", exact: true }).click();
  const stats = page.getByRole("tooltip", { name: "Session statistics", exact: true });
  await expect(stats).toContainText("Claude usage could not be read");
  await expect(stats).toContainText("its token usage and spend");
});

test("a session with a readable provider and no recorded usage says Usage unavailable", async ({ page }) => {
  // Arrange
  const subscriptions = [{ provider: "claude", status: "available", percent_remaining: 12, read_at: "", resets_at: "2026-10-28T09:00:00Z", source: "oauth", floor_percent: 5 }];
  await open(page, snapshot({ subscriptions }, MEMORY));
  await openGoblin(page);

  // Assert
  await expect(footer(page).locator(".notice")).toHaveText("Usage unavailable");
});

test("the context ring opens available session stats and allowance without covering the composer", async ({ page }) => {
  // Arrange
  const subscriptions = [{ provider: "claude", status: "available", percent_remaining: 12, read_at: "", resets_at: "2026-10-28T09:00:00Z", source: "oauth", floor_percent: 5 }];
  await open(page, snapshot({ subscriptions }, MEMORY), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);
  const ring = page.getByRole("button", { name: "Context: 59.3% used", exact: true });
  await expect(ring).toBeVisible();
  await ring.click();
  const banner = page.getByRole("tooltip", { name: "Session statistics", exact: true });
  await expect(banner).toContainText("Approaching weekly usage limit");
  await expect(banner).toContainText("Resets ");

  // Assert: the full stats open in place, and the box he writes in stays visible.
  const details = banner.locator(".usage-stats");
  await expect(details).toBeVisible();
  await expect(details).toContainText("Input tokens");
  await expect(details).toContainText("1.4M");
  await expect(details).toContainText("Context");
  await expect(details).toContainText("593k of 1.0M");
  await expect(page.getByRole("region", { name: "Messages to " + "Zeke - Touch Wrangler" })).toBeInViewport();
  await page.getByRole("region", { name: "Messages to Zeke - Touch Wrangler" }).getByRole("textbox").fill("still reachable");
});

test("the conversation draws no speaker name and no harness icon above a message", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);
  const log = page.getByRole("log", { name: "Messages of Zeke - Touch Wrangler" });

  // Assert: neither the sender's name nor a harness mark heads a message.
  await expect(log.locator(".message-from")).toHaveCount(0);
  await expect(log.locator(".message header")).toHaveCount(0);
  await expect(log).not.toContainText("You");
  await expect(log).not.toContainText("CFO");
  // The sides and bubbles still say who spoke, and the words still read.
  await expect(log.locator(".message-prompt .message-bubble")).toContainText("Read the brief");
  await expect(log.locator(".message-reply")).toContainText("On it.");
});

test("a selection he settles copies itself, with the quiet Copied, and a cleared selection copies nothing", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async (text: string) => { (window as unknown as { __copied?: string[] }).__copied = ((window as unknown as { __copied?: string[] }).__copied ?? []).concat(text); } } });
  });
  await open(page);
  await openGoblin(page);

  // Act: a selection of a prompt's words settles.
  await page.evaluate(() => {
    const bubble = document.querySelector(".message-prompt .message-bubble");
    const conversation = document.querySelector(".conversation");
    if (!bubble || !conversation) throw new Error("no conversation");
    const range = document.createRange();
    range.selectNodeContents(bubble);
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    window.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
  });

  // Assert
  await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __copied?: string[] }).__copied?.join("") ?? "")).toContain("Read the brief");

  // Act: a cleared selection settles.
  await page.evaluate(() => {
    window.getSelection()?.removeAllRanges();
    document.querySelector(".conversation")!.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
  });

  // Assert: nothing new copied.
  await expect.poll(() => page.evaluate(() => (window as unknown as { __copied?: string[] }).__copied?.length ?? 0)).toBe(1);
});

for (const runtime of [undefined, { state: "idle" }]) {
  test(`Codex keeps the board's working indicator at the reply's end when runtime is ${runtime ? "idle" : "unavailable"}`, async ({ page }) => {
    await open(page, snapshot({}, { harness: "codex", phase: "working", runtime }), "", PAGE_WITH_USAGE);
    await openGoblin(page);
    await expect(page.locator(".reply-working")).toBeVisible();
    await expect(page.locator(".panel-header .working-indicator")).toHaveCount(0);
    await expect(page.locator(".context-pane .working-indicator:visible")).toHaveCount(1);
  });
}

test("thinking folds to its first line and opens to its paragraphs, lists and code, never wider than the column", async ({ page }) => {
  const thought = "First **read** the change.\n\n1. Check `input`.\n2. Keep the draft.\n\n```sh\ngit status --short\n```\n\n" + "https://example.test/" + "long".repeat(80);
  await open(page, snapshot(), "", { messages: [{ id: "thought", seq: 0, at: "2026-10-26T10:00:00Z", kind: "reply", blocks: [{ kind: "thinking", text: thought }] }], more: false });
  await openGoblin(page);
  const thinking = page.locator(".message-thinking");
  // Its first line reads as words, its markdown drawn, as Codex's reasoning
  // summaries open with a bold header.
  await expect(thinking.locator(".thinking-preview")).toHaveText("First read the change.");
  await expect(thinking.locator(".thinking-preview strong")).toHaveText("read");
  await expect(thinking.locator("li"), "folded thinking mounts nothing under its line").toHaveCount(0);
  await thinking.locator("summary").click();
  // The opened thought's own words, not the folded line, which keeps its
  // own bold "read" in the summary.
  await expect(thinking.locator(".message-words strong")).toHaveText("read");
  await expect(thinking.locator("li")).toHaveCount(2);
  await expect(thinking.locator("pre code")).toHaveText("git status --short");
  await expect.poll(() => thinking.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
});
