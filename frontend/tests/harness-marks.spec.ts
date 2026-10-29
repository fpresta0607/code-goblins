import { expect, test, type Page } from "./site";
import { box, open, openGoblin, snapshot } from "./session-board";

// Every place Modern Chat names a session's harness shows that harness's own
// mark, the one the board draws on its card, from one shared component (the
// Overlord, 2026-10-27: "make sure that whatever harness is being used inside
// the modern chat, that it is using the right icon"), and the row under the
// message shows the session's own model. The panel's header draws it beside
// the session's name (the Overlord, 2026-10-29, of the one-row header: "I like
// the avatar and the icon"). The supervisor is played by tests/session-board.ts.

const SESSIONS = [
  { harness: "claude", name: "Claude Code", model: "claude-opus-5-5", shown: "Opus 5.5" },
  { harness: "codex", name: "Codex", model: "gpt-6-astra", shown: "GPT-6 Astra" },
  { harness: "pi", name: "Pi", model: "z-ai/glm-5.3-flash", shown: "GLM 5.3 Flash" },
] as const;

// The drawing of the first mark under selector: its glyph's path.
const drawing = (page: Page, selector: string) => page.locator(selector).first().evaluate((mark) => mark.querySelector("path")?.getAttribute("d") ?? "");

for (const session of SESSIONS) {
  test(`a ${session.name} session shows its own harness's mark and its own model`, async ({ page }) => {
    // Arrange: a session at rest, so its last reply ends its turn.
    await open(page, snapshot({}, { harness: session.harness, model: session.model, phase: "settled" }));

    // Act
    await openGoblin(page);

    // Assert: the panel's header, the row under the turn's last reply and
    // the model's button draw the mark the board's card draws for this
    // harness, the row's named for it, and the button names the session's
    // own model.
    const card = await drawing(page, ".card-harness .mark");
    expect(card, "the card has a mark").not.toBe("");
    expect(await drawing(page, ".goblin-panel > .panel-header .mark"), "the panel's header").toBe(card);
    const replyMark = page.locator(".messages-view:not([hidden]) .message-reply-actions .mark");
    expect(await drawing(page, ".messages-view:not([hidden]) .message-reply-actions .mark"), "the row under the last reply").toBe(card);
    await expect(replyMark).toHaveAttribute("aria-label", session.name);
    const model = box(page).getByRole("button", { name: "Change model", exact: true });
    await expect(model.locator(".engine-control-model")).toHaveText(session.shown);
    expect(await model.evaluate((button) => button.querySelector(".mark path")?.getAttribute("d") ?? ""), "the model's button").toBe(card);
  });
}

// The CFO's panel has no task, so the mark in its header names the model of
// the CFO's live session, the one its conversation under the header shows.
test("the CFO's header names its live session's model in its harness mark's tip", async ({ page }) => {
  // Arrange
  const [claude] = SESSIONS;
  await open(page, snapshot({ sessions: [{ id: "cfo-1", harness: claude.harness, role: "cfo", model: claude.model }] }));

  // Act
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CFO");

  // Assert
  const mark = page.locator(".goblin-panel > .panel-header .panel-identity > .mark");
  await expect(mark).toHaveAttribute("data-tip", claude.name + " · " + claude.shown);
  await expect(mark).toHaveAccessibleName(claude.name + " · " + claude.shown);
});
