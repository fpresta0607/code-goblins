import { CEO_DOOR, expect, test, type Page } from "./site";
import { box, open, openGoblin, snapshot } from "./session-board";
import { openTaskList } from "./site";

// Every place Modern Chat names a session's harness shows that harness's own
// mark, the one the board draws on its card, from one shared component (the
// Overlord, 2026-10-27: "make sure that whatever harness is being used inside
// the modern chat, that it is using the right icon"), and the row under the
// message shows the session's own model. The panel's header draws no mark:
// its row keeps the session's name, and its Task view names the harness, the
// model and the effort in words (#1987). The supervisor is played by
// tests/session-board.ts.

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

    // Assert: the row under the turn's last reply and the model's button
    // draw the mark the board's card draws for this harness, the row's named
    // for it, and the button names the session's own model; the panel's
    // header draws none.
    const card = await drawing(page, ".card-harness .mark");
    expect(card, "the card has a mark").not.toBe("");
    await expect(page.locator(".goblin-panel > .panel-header .mark"), "the panel's header").toHaveCount(0);
    const replyMark = page.locator(".messages-view:not([hidden]) .message-reply-actions .mark");
    expect(await drawing(page, ".messages-view:not([hidden]) .message-reply-actions .mark"), "the row under the last reply").toBe(card);
    await expect(replyMark).toHaveAttribute("aria-label", session.name);
    const model = box(page).getByRole("button", { name: "Change model", exact: true });
    await expect(model.locator(".engine-control-model")).toHaveText(session.shown);
    expect(await model.evaluate((button) => button.querySelector(".mark path")?.getAttribute("d") ?? ""), "the model's button").toBe(card);
  });
}

// The CEO's panel has no task, so its Task view names the model of the CEO's
// live session, the one its conversation shows, and the effort its saved
// engine runs once that applied, as the box's model button shows it
// (model-effort.spec.ts): a pending engine or one that could not be applied
// runs no effort of its own.
const CEO_ENGINES = [
  { why: "no saved engine", engine: undefined, line: "Claude Code · Opus 5.5" },
  { why: "an applied engine", engine: { pending: false, problem: "" }, line: "Claude Code · Opus 5.5 · high" },
  { why: "a pending engine", engine: { pending: true, problem: "" }, line: "Claude Code · Opus 5.5" },
  { why: "an engine that could not be applied", engine: { pending: false, problem: "The selection could not be applied: the CEO is busy" }, line: "Claude Code · Opus 5.5" },
] as const;
for (const ceo of CEO_ENGINES) {
  test(`the CEO's Task view with ${ceo.why} names its harness, its live session's model and the effort it runs`, async ({ page }) => {
    // Arrange
    const [claude] = SESSIONS;
    await open(page, snapshot({ sessions: [{ id: "ceo-1", harness: claude.harness, role: "ceo", model: claude.model }], ...(ceo.engine && { ceo_engine: { harness: claude.harness, model: claude.model, effort: "high", waiting: "", ...ceo.engine } }) }));

    // Act
    await page.locator(".ceo-pin").getByRole("button", { name: CEO_DOOR }).last().click();
    await expect(page.locator("#panel-title")).toHaveText("CEO");

    // Assert
    await expect(page.locator(".goblin-panel > .panel-header .mark")).toHaveCount(0);
    await expect((await openTaskList(page)).getByText(ceo.line, { exact: true })).toBeVisible();
  });
}
