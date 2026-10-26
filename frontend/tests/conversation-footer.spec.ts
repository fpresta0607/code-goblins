import { expect, test, type Page } from "./site";
import { GOBLIN, open, openGoblin, PAGE, snapshot } from "./session-board";

// The Overlord, 2026-10-26: "just have a streaming ands stats line in bottom
// cornern liek this ... where teh time stamp is plus a banner that you can
// open for usage stats on teh session". The conversation's bottom corner
// carries the working animation and the stats, and a banner above the
// composer opens the session's usage in place.

const USAGE = { input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000, cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5" };
const PAGE_WITH_USAGE = { ...PAGE, usage: USAGE };
const MEMORY = { ...GOBLIN, tree: { task_id: GOBLIN.id, generation: GOBLIN.generation, harness: GOBLIN.harness, memory: 208 * 2 ** 20, own_memory: 208 * 2 ** 20, conversation_at: "", children: [], unread: [], source_updated_at: "", fetched_at: "" } };
const footer = (page: Page) => page.locator(".session-footer");

test("the conversation's bottom line carries the time facts, not a wall of numbers", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { ...MEMORY, phase: "settled" }), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);

  // Assert
  const line = footer(page);
  await expect(line).toBeVisible();
  await expect(line.getByText(/^Sent /)).toBeVisible();
  await expect(line.getByText(/^Replied /)).toBeVisible();
  await expect(line.locator(".session-working")).toHaveCount(0);
  // The fuller numbers live on in the banner, not on this line.
  await expect(line).not.toContainText("$3.209");
  await expect(line).not.toContainText("59.3%");
  const banner = page.getByRole("region", { name: "Session usage", exact: true });
  await banner.getByRole("button", { name: "Usage", exact: true }).click();
  await expect(banner).toContainText("1.4M");
});

test("the line shows the harness's working animation while the session works", async ({ page }, testInfo) => {
  // Arrange
  await open(page, snapshot({}, { ...MEMORY, runtime: { state: "working" } }), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);

  // Assert
  const working = footer(page).locator(".session-working");
  await expect(working).toBeVisible();
  await expect(working).toContainText("Working");
  // The indicator takes Claude Code's own ink from the board's mark.
  await expect(working).toHaveCSS("color", "rgb(217, 119, 87)");
  await expect(page.locator(".context-pane .working-indicator:visible")).toHaveCount(1);
  await testInfo.attach("footer-working.png", { body: await page.screenshot(), contentType: "image/png" });
});

test("a session whose usage cannot be read draws no banner and says why in the line", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);

  // Assert
  await expect(footer(page)).toContainText("usage unreadable");
  await expect(footer(page)).toContainText("its Claude usage could not be read");
  await expect(page.getByRole("region", { name: "Session usage", exact: true })).toHaveCount(0);
});

test("the usage banner opens the session's full stats in the panel, and leaves the composer in reach", async ({ page }) => {
  // Arrange
  const subscriptions = [{ provider: "claude", status: "available", percent_remaining: 12, read_at: "", resets_at: "2026-10-28T09:00:00Z", source: "oauth", floor_percent: 5 }];
  await open(page, snapshot({ subscriptions }, MEMORY), "rebase onto main", PAGE_WITH_USAGE);
  await openGoblin(page);
  const banner = page.getByRole("region", { name: "Session usage", exact: true });

  // Assert the one line above the composer.
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("Approaching weekly usage limit");
  await expect(banner).toContainText("Resets ");
  await expect(banner.getByRole("button", { name: "Usage", exact: true })).toBeVisible();

  // Act
  await banner.getByRole("button", { name: "Usage", exact: true }).click();

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
  test(`Codex keeps the board's working indicator in the footer when runtime is ${runtime ? "idle" : "unavailable"}`, async ({ page }) => {
    await open(page, snapshot({}, { harness: "codex", phase: "working", runtime }), "", PAGE_WITH_USAGE);
    await openGoblin(page);
    await expect(footer(page).locator(".session-working")).toBeVisible();
    await expect(page.locator(".panel-header .working-indicator")).toHaveCount(0);
    await expect(page.locator(".context-pane .working-indicator:visible")).toHaveCount(1);
  });
}

test("thinking keeps its paragraphs, lists and code readable without a drawer", async ({ page }) => {
  const thought = "First **read** the change.\n\n1. Check `input`.\n2. Keep the draft.\n\n```sh\ngit status --short\n```\n\n" + "https://example.test/" + "long".repeat(80);
  await open(page, snapshot(), "", { messages: [{ id: "thought", seq: 0, at: "2026-10-26T10:00:00Z", kind: "reply", blocks: [{ kind: "thinking", text: thought }] }], more: false });
  await openGoblin(page);
  const thinking = page.locator(".message-thinking-read");
  await expect(thinking).toBeVisible();
  await expect(thinking.locator("strong")).toHaveText("read");
  await expect(thinking.locator("li")).toHaveCount(2);
  await expect(thinking.locator("pre code")).toHaveText("git status --short");
  await expect(thinking.locator("details, summary")).toHaveCount(0);
  await expect.poll(() => thinking.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
});
