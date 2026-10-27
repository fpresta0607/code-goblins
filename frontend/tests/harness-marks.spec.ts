import { expect, test, type Page } from "./site";
import { box, open, openGoblin, snapshot } from "./session-board";

// Every place Modern Chat names a session's harness shows that harness's own
// mark, the one the board draws on its card, from one shared component (the
// Overlord, 2026-10-27: "make sure that whatever harness is being used inside
// the modern chat, that it is using the right icon"), and the row under the
// message shows the session's own model. The supervisor is played by
// tests/session-board.ts.

const SESSIONS = [
  { harness: "claude", name: "Claude Code", model: "claude-opus-5-5", shown: "Opus 5.5" },
  { harness: "codex", name: "Codex", model: "gpt-6-astra", shown: "GPT-6 Astra" },
  { harness: "pi", name: "Pi", model: "z-ai/glm-5.3-flash", shown: "glm-5.3-flash" },
] as const;

// The drawing of the first mark under selector: its glyph's path.
const drawing = (page: Page, selector: string) => page.locator(selector).first().evaluate((mark) => mark.querySelector("path")?.getAttribute("d") ?? "");

for (const session of SESSIONS) {
  test(`a ${session.name} session shows its own harness's mark and its own model`, async ({ page }) => {
    // Arrange
    await open(page, snapshot({}, { harness: session.harness, model: session.model }));

    // Act
    await openGoblin(page);

    // Assert: the panel's status line, the session line and the model's
    // button draw the mark the board's card draws for this harness, named
    // for it, and the button names the session's own model.
    const card = await drawing(page, ".card-harness .mark");
    expect(card, "the card has a mark").not.toBe("");
    expect(await drawing(page, ".panel-status .mark"), "the panel's status line").toBe(card);
    expect(await drawing(page, ".messages-view:not([hidden]) .session-footer .mark"), "the session line").toBe(card);
    await expect(page.locator(".messages-view:not([hidden]) .session-footer .mark")).toHaveAttribute("aria-label", session.name);
    const model = box(page).getByRole("button", { name: "Change model", exact: true });
    await expect(model).toHaveText(session.shown);
    expect(await model.evaluate((button) => button.querySelector(".mark path")?.getAttribute("d") ?? ""), "the model's button").toBe(card);
  });
}
