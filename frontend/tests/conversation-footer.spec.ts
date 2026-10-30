import { expect, test } from "./site";
import { GOBLIN, open, openGoblin, PAGE, snapshot } from "./session-board";

// The conversation's bottom: since 2026-10-27 the working animation sits at
// the end of the reply in progress and the time facts under his message
// (tests/reply-end.spec.ts), and since 2026-10-28 the session's usage is the
// box's pies (tests/usage-bar.spec.ts).

const USAGE = { input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000, cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5" };
const PAGE_WITH_USAGE = { ...PAGE, usage: USAGE };
const MEMORY = { ...GOBLIN, tree: { task_id: GOBLIN.id, generation: GOBLIN.generation, harness: GOBLIN.harness, memory: 208 * 2 ** 20, own_memory: 208 * 2 ** 20, conversation_at: "", children: [], unread: [], source_updated_at: "", fetched_at: "" } };

test("a settled session shows no working animation and no time fact under his message", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { ...MEMORY, phase: "settled" }), "rebase onto main", PAGE_WITH_USAGE);

  // Act
  await openGoblin(page);

  // Assert
  await expect(page.locator('[data-message="m0"] .message-actions')).toBeHidden();
  await expect(page.locator(".reply-working")).toHaveCount(0);
});

// The Overlord, 2026-10-30: "make color of the helix match teh harnes icon
// coloring". Each harness's helix takes the ink of its own mark: Claude
// Code's orange, Codex's light ink and pi's moss.
for (const [harness, name, ink] of [["claude", "Claude Code", "rgb(217, 119, 87)"], ["codex", "Codex", "rgb(232, 238, 242)"], ["pi", "pi", "rgb(134, 179, 107)"]]) {
  test(`the reply in progress ends with ${name}'s working animation in its ink while the session works`, async ({ page }, testInfo) => {
    // Arrange
    await open(page, snapshot({}, { ...MEMORY, harness, tree: { ...MEMORY.tree, harness }, runtime: { state: "working" } }), "rebase onto main", PAGE_WITH_USAGE);
    await openGoblin(page);

    // Assert
    const working = page.locator(".reply-working .working-indicator");
    await expect(working).toBeVisible();
    await expect(working).toHaveCSS("color", ink);
    await expect(working.locator(".working-helix-row > i").first()).toHaveCSS("background-color", ink);
    await expect(page.locator(".context-pane .working-indicator:visible")).toHaveCount(1);
    await testInfo.attach(`footer-working-${harness}.png`, { body: await page.screenshot(), contentType: "image/png" });
  });
}

// The Overlord, 2026-10-28, on a panel at rest that ran 32 animations and was
// drawn again four times a second: "32 animations??? remvoe like all but the
// streaming one", and 2026-10-30, of the still halftone that replaced it:
// "this one is boring i prefered the helix". A working session's conversation
// runs the helix, and every animation in it moves by transform and opacity
// alone, which the compositor draws with no work on the main thread, the
// words beside it rotating among them; React draws nothing again while
// nothing happens, as the working spot's clock once drew it every second.
// React's own devtools hook counts each commit.
test("a working session's conversation runs the helix by transform and opacity alone and redraws nothing at rest", async ({ page }) => {
  // Arrange: the clock stands five seconds into a minute.
  await page.clock.install({ time: new Date("2026-10-27T12:00:05Z") });
  await page.addInitScript(() => {
    const seen = { commits: 0 };
    Object.assign(window, {
      seen,
      __REACT_DEVTOOLS_GLOBAL_HOOK__: {
        supportsFiber: true, renderers: new Map(), inject() { return 1; }, checkDCE() {}, onCommitFiberUnmount() {}, onPostCommitFiberRoot() {}, onScheduleFiberRoot() {},
        onCommitFiberRoot() { seen.commits++; },
      },
    });
  });
  await open(page, snapshot({}, { ...MEMORY, runtime: { state: "working" } }), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);
  await expect(page.locator(".reply-working .working-indicator")).toBeVisible();
  await page.clock.runFor(2_000);
  await page.evaluate(() => { (window as unknown as { seen: { commits: number } }).seen.commits = 0; });

  // Act
  await page.clock.runFor(20_000);

  // Assert
  const commits = await page.evaluate(() => (window as unknown as { seen: { commits: number } }).seen.commits);
  const animations = await page.evaluate(() => document.getAnimations()
    .filter((animation) => animation.playState === "running" && animation.effect instanceof KeyframeEffect && !!animation.effect.target?.closest(".context-pane"))
    .map((animation) => ({
      name: animation.id,
      properties: [...new Set((animation.effect as KeyframeEffect).getKeyframes().flatMap((frame) => Object.keys(frame)))].filter((key) => !["offset", "computedOffset", "easing", "composite"].includes(key)),
    })));
  expect.soft(commits, "React commits while nothing happened").toBe(0);
  expect(new Set(animations.map((animation) => animation.name))).toEqual(new Set(["working-helix-morph", "working-helix-a", "working-helix-b", "working-dither-morph", "working-dither", "working-word"]));
  expect(animations.filter((animation) => animation.properties.some((property) => property !== "transform" && property !== "opacity"))).toEqual([]);
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
