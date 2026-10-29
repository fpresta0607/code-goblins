import { expect, test, type Page } from "./site";
import { box, card, holdMessages, NAME, open, openGoblin, PAGE, releaseMessages, snapshot } from "./session-board";

// The Overlord, 2026-10-28: "Claude uses a bunch of different colors to show
// smaller little circle pie graphs, pie slices, percentages, relevant to the
// context of the actual conversation". A goblin's box shows its session's
// usage as small pies, one per reading the session backs, each with its
// percentage, at the end of the box's controls row, and nothing at all when
// there is no reading: never "unavailable". While the conversation still
// connects it shows no usage. The supervisor is played by
// tests/session-board.ts; every reading is made up.

const at = "2026-10-28T09:00:00Z";
const usage = (fields: Record<string, unknown> = {}) => ({ input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000, cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5", ...fields });
const subscription = (provider: string, percent: number) => ({ provider, status: "available", percent_remaining: percent, read_at: at, resets_at: "2026-11-02T09:00:00Z", source: "oauth", floor_percent: 5 });
const KEY = { provider: "openrouter", status: "available", spent_usd: 4, limit_usd: 20, remaining_usd: 16, period: "weekly", resets_at: "", read_at: at, byok_usd: null, includes_byok: false };

// The dock under the conversation, which holds the box and the usage bar.
const dock = (page: Page) => page.locator(".composer-dock:visible");
const bar = (page: Page) => dock(page).getByRole("group", { name: "Usage", exact: true });
const meter = (page: Page, name: string) => bar(page).getByRole("meter", { name, exact: true });

const SESSIONS = [
  { harness: "claude", model: "claude-opus-5-5", context: usage(), fields: { subscriptions: [subscription("claude", 88)] }, other: "Week", otherPercent: "12%" },
  { harness: "codex", model: "gpt-6-astra", context: usage({ context_tokens: 129_000, context_window: 258_400, model: "gpt-6-astra" }), fields: { subscriptions: [subscription("codex", 40)] }, other: "Week", otherPercent: "60%" },
  { harness: "pi", model: "z-ai/glm-5.3-flash", context: usage({ context_tokens: 64_000, context_window: 200_000, model: "z-ai/glm-5.3-flash" }), fields: { key_spend: [KEY] }, other: "Key spend", otherPercent: "20%" },
] as const;

for (const session of SESSIONS) {
  test(`a ${session.harness} session's box shows its context and its allowance as pies with their percentages`, async ({ page }) => {
    // Arrange
    await open(page, snapshot(session.fields, { harness: session.harness, model: session.model }), "rebase onto main", { ...PAGE, usage: session.context });

    // Act
    await openGoblin(page);

    // Assert: the context pie reads the session's own share of its window.
    const context = meter(page, "Context");
    const percent = Math.round(session.context.context_tokens * 100 / session.context.context_window * 10) / 10;
    await expect(context).toHaveAttribute("aria-valuenow", String(percent));
    await expect(context.locator(".usage-percent")).toHaveText(Math.round(percent) + "%");
    await expect(context.locator(".usage-pie-slice")).toHaveAttribute("stroke-dasharray", `${session.context.context_tokens * 100 / session.context.context_window} 100`);
    await expect(meter(page, session.other).locator(".usage-percent")).toHaveText(session.otherPercent);
    // Each reading has a colour of its own.
    const inks = await bar(page).locator(".usage-pie-slice").evaluateAll((slices) => slices.map((slice) => getComputedStyle(slice).stroke));
    expect(new Set(inks).size).toBe(2);
    await expect(dock(page)).not.toContainText(/unavailable|could not be read/i);
  });
}

// The Overlord, 2026-10-28: "The usage circle goes in place of the arrow in
// the box", the end of the controls row, where Send sat before it moved onto
// the line he writes on.
for (const [label, width, isNamed] of [["a desktop panel", 1440, true], ["a phone", 390, false]] as const) {
  test(`the usage pies end the box's controls row after the microphone, inside its border, on ${label}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 844 });
    await open(page, snapshot(), "rebase onto main", { ...PAGE, usage: usage() });

    // Act
    await openGoblin(page);

    // Assert
    await expect(bar(page)).toBeVisible();
    expect(await bar(page).evaluate((element) => ({
      row: element.parentElement?.className,
      before: element.previousElementSibling?.getAttribute("aria-label"),
      isLast: element.nextElementSibling === null,
      border: getComputedStyle(element).borderTopStyle,
    }))).toEqual({ row: "composer-row-end", before: "Hold to dictate", isLast: true, border: "none" });
    const [input, pies] = await Promise.all([box(page).locator(".composer-input").boundingBox(), bar(page).boundingBox()]);
    expect(input && pies && pies.x >= input.x && pies.x + pies.width <= input.x + input.width + 0.5, "inside the box's border").toBe(true);
    await expect(meter(page, "Context").locator(".usage-percent")).toBeVisible();
    await expect(meter(page, "Context").locator(".usage-label")).toBeVisible({ visible: isNamed });
  });
}

for (const [label, fields] of [["no window", { context_window: 0 }], ["no tokens yet", { context_tokens: 0 }]] as const) {
  test(`a session with ${label} and no allowance shows no usage, and no words for why`, async ({ page }) => {
    // Arrange
    await open(page, snapshot(), "rebase onto main", { ...PAGE, usage: usage(fields) });

    // Act
    await openGoblin(page);

    // Assert
    await expect(page.getByRole("log", { name: "Messages of Zeke - Touch Wrangler" })).toContainText("On it.");
    await expect(bar(page)).toHaveCount(0);
    await expect(dock(page)).not.toContainText(/usage|unavailable|could not be read/i);
  });
}

test("a session with no recorded usage and no allowance read shows nothing", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  await openGoblin(page);

  // Assert
  await expect(bar(page)).toHaveCount(0);
  await expect(dock(page)).not.toContainText(/usage|unavailable|could not be read/i);
});

test("while the conversation connects its box shows no usage, and once connected shows the reading", async ({ page }) => {
  // Arrange: the conversation's first page is held back.
  await holdMessages(page);
  await open(page, snapshot({ subscriptions: [subscription("claude", 88)] }), "rebase onto main", { ...PAGE, usage: usage() });
  await card(page, NAME).click();
  await expect(page.locator("#panel-title")).toHaveText(NAME);

  // Assert
  await expect(page.locator(".messages-view:not([hidden])")).toBeVisible();
  await expect(bar(page)).toHaveCount(0);
  await expect(dock(page)).not.toContainText(/usage|unavailable|%/i);

  // Act
  await releaseMessages(page);

  // Assert
  await expect(meter(page, "Context")).toHaveAttribute("aria-valuenow", "59.3");
  await expect(meter(page, "Week")).toBeVisible();
});
