import { expect, test } from "./site";
import { box, field, open, openGoblin, pasted, snapshot, typed } from "./session-board";

// The Overlord, 2026-10-28, of a Claude Code session at its usage limit:
// the limit banner was "far too large and garbled", the dither stayed on
// "Working", a message he sent did nothing, and the box drew the terminal's
// prompt line above itself ("What is that window above the chat input ...
// remove it"). Then, of Claude's own app: one card in the conversation and
// one compact strip on the box. The supervisor is played by
// tests/session-board.ts.

const at = (minutes: number) => new Date(Date.parse("2026-10-27T21:00:00Z") + minutes * 60_000).toISOString();
const AT_LIMIT = { messages: [
  { id: "p0", seq: 0, at: at(0), kind: "prompt", source: "overlord", text: "Check the train" },
  { id: "r1", seq: 1, at: at(1), kind: "reply", model: "claude-opus-5-5", blocks: [{ kind: "text", text: "The train is green." }] },
  { id: "p2", seq: 2, at: at(2), kind: "prompt", source: "overlord", text: "Then merge it" },
  { id: "r3", seq: 3, at: at(3), kind: "reply", model: "<synthetic>", blocks: [{ kind: "text", text: "You've hit your 5-hour limit · resets 10pm (America/Chicago)" }] },
], more: false, usage: { input_tokens: 1200, output_tokens: 80, context_tokens: 2400, context_window: 100000, model: "claude-opus-5-5" } };
// Claude Code's own screen at the limit, as the relay reads it: words and a
// way out, nothing to choose.
const LIMIT_SCREEN = { type: "screen", asking: false, open: true, working: true, submission: "", panel: { rows: ["⚠ Usage limit reached · limit resets 10pm", "· clau.de/wrap-up · /usage-credits to"], choices: [], hints: ["Continuing automatically at 10pm · Esc"] } };

test("at its usage limit a Claude Code session shows Modern's own card and strip, no dither, and none of the harness's words", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", AT_LIMIT);
  await openGoblin(page);

  // Act
  relay.say(LIMIT_SCREEN);

  // Assert: the card in the conversation.
  const card = page.getByRole("log").getByRole("region", { name: "Session limit reached", exact: true });
  await expect(card).toContainText("Auto-resuming at 10pm");
  await expect(card.getByRole("checkbox", { name: "Auto-continue when limits reset" })).toBeChecked();
  await expect(card.getByRole("button", { name: "Try again", exact: true })).toBeVisible();
  await expect(card.getByRole("link", { name: "Open usage settings" })).toHaveAttribute("href", "https://claude.ai/settings/usage");
  await expect(page.locator('[data-message="r3"] .message-reply-actions time')).toHaveCount(1);
  // One strip on the box, and nothing of the harness's own screen.
  const strip = page.locator(".composer-dock .limit-strip");
  await expect(strip).toHaveText(/Session limit reached\s*Resets at 10pm\s*Try again/);
  await expect(strip.getByRole("button")).toHaveCount(1);
  await expect(box(page).getByRole("group", { name: /asks$/ })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText(/clau\.de|usage-credits/);
  await expect(page.locator(".reply-working")).toHaveCount(0);
  await expect(box(page).getByRole("button", { name: "Change model", exact: true })).toHaveText("Opus 5.5");

  // Act: he cancels the session's own wait, which its screen then says.
  const continues = card.getByRole("checkbox", { name: "Auto-continue when limits reset" });
  await continues.click();
  await expect.poll(() => typed(relay)).toBe("\x1b");
  relay.say({ ...LIMIT_SCREEN, working: false, panel: { ...LIMIT_SCREEN.panel, hints: [] } });

  // Assert
  await expect(continues).not.toBeChecked();
  await expect(card).toContainText("Resets at 10pm");

  // Act: he tries again from the strip, while the harness's own screen for
  // the limit is still open.
  await strip.getByRole("button", { name: "Try again", exact: true }).click();

  // Assert: Escape closes that screen first, and "continue" goes in once it
  // has closed.
  await expect.poll(() => typed(relay)).toBe("\x1b\x1b");
  relay.say({ type: "screen", asking: false, open: false, working: false, submission: "" });
  await expect.poll(() => typed(relay)).toBe("\x1b\x1b" + pasted("continue") + "\r");
});

test("a screen of words with nothing to choose is never drawn over the box, and a message is held, not typed onto it", async ({ page }) => {
  // Arrange: the terminal's prompt line, his last message on it.
  const relay = await open(page, snapshot({}, { phase: "settled" }));
  const sent: string[] = [];
  await page.route("**/api/actions", (route) => {
    sent.push((route.request().postDataJSON() as { text: string }).text);
    return route.fulfill({ status: 202, json: { status: "queued" } });
  });
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: true, working: false, submission: "", panel: { rows: ["> Claude runs a crew now I feel like and it"], choices: [], hints: [] } });

  // Act
  await field(page).fill("merge the train");
  await field(page).press("Enter");

  // Assert: nothing of the screen is drawn, and the terminal is typed
  // nothing: the board holds the message until the harness's input is empty.
  await expect(box(page).getByRole("group", { name: /asks$/ })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("Claude runs a crew now");
  await expect.poll(() => sent).toEqual(["merge the train"]);
  expect(typed(relay)).toBe("");
});
