import { expect, test, type Page } from "./site";
import { box, boxMenu, GOBLIN, NAME, open, openGoblin, PAGE, snapshot } from "./session-board";

// The Overlord, 2026-10-28, on the build he runs: "claude-opus-5-5 is not
// proper case, effort type that's running is not shown", "Max effort is a
// rainbow", and the close button that did nothing. The box names the model
// by its proper name everywhere and shows on the model's button the effort
// the session runs, read from how it was started until its own record says
// otherwise. The supervisor is played by tests/session-board.ts.

const SESSIONS = [
  { harness: "claude", model: "claude-opus-5-5", effort: "xhigh", name: "Opus 5.5", raw: ["claude-opus-5-5"] },
  { harness: "codex", model: "gpt-6-astra", effort: "high", name: "GPT-6 Astra", raw: ["gpt-6-astra"] },
  { harness: "pi", model: "openrouter/z-ai/glm-5.3-flash", effort: "medium", name: "GLM 5.3 Flash", raw: ["openrouter/z-ai/glm-5.3-flash", "glm-5.3-flash"] },
] as const;
// A session's record that names its model but no effort, as one started at
// its effort with no /effort typed since.
const RECORD = { input_tokens: 1200, output_tokens: 80, context_tokens: 2400, context_window: 100000 };

// shown is every word the page draws, and every tip and name it gives a
// control, where a raw model id must not appear.
const shown = (page: Page) => page.evaluate(() => [document.body.innerText, ...[...document.querySelectorAll("[data-tip], [aria-label], [title]")].flatMap((element) => ["data-tip", "aria-label", "title"].map((name) => element.getAttribute(name) ?? ""))].join("\n"));

for (const session of SESSIONS) {
  test(`a ${session.harness} session shows its model by name and the effort it runs, and no raw model id anywhere`, async ({ page }) => {
    // Arrange
    await open(page, snapshot({ cfo_harness: session.harness }, { harness: session.harness, model: session.model, effort: session.effort, phase: "settled" }), "", { ...PAGE, usage: { ...RECORD, model: session.model } });

    // Act
    await openGoblin(page);

    // Assert
    const model = box(page).getByRole("button", { name: "Change model", exact: true });
    await expect(model.locator(".engine-control-model")).toHaveText(session.name);
    await expect(model.locator(".effort-level")).toHaveText(session.effort);
    const words = await shown(page);
    for (const raw of session.raw) expect(words, "no raw " + raw).not.toContain(raw);
  });
}

test("the CFO's box shows the effort its engine was started at", async ({ page }) => {
  // Arrange
  await open(page, snapshot({ cfo_engine: { harness: "claude", model: "claude-opus-5-5", effort: "high", pending: false, problem: "", waiting: "" } }), "", { ...PAGE, usage: { ...RECORD, model: "claude-opus-5-5" } });

  // Act
  await page.keyboard.press("Control+Alt+1");

  // Assert
  await expect(box(page, "CFO").getByRole("button", { name: "Change model", exact: true }).locator(".effort-level")).toHaveText("high");
});

// The server keeps a saved engine it did not apply, with pending false: one
// for another harness than the CFO started as, or one whose apply failed
// (internal/supervisor/cfo_engine.go). The CFO does not run its effort.
const UNAPPLIED = [
  { why: "names another harness", cfo_harness: "codex", model: "gpt-6-astra", shown: "GPT-6 Astra", engine: { harness: "claude", pending: false, problem: "" } },
  { why: "could not be applied", cfo_harness: "claude", model: "claude-opus-5-5", shown: "Opus 5.5", engine: { harness: "claude", pending: false, problem: "The selection could not be applied: the CFO is busy" } },
  { why: "is still pending", cfo_harness: "claude", model: "claude-opus-5-5", shown: "Opus 5.5", engine: { harness: "claude", pending: true, problem: "" } },
] as const;
for (const unapplied of UNAPPLIED) {
  test(`the CFO's box takes no effort from a saved engine that ${unapplied.why}`, async ({ page }) => {
    // Arrange
    await open(page, snapshot({ cfo_harness: unapplied.cfo_harness, cfo_engine: { ...unapplied.engine, model: "claude-opus-5-5", effort: "high", waiting: "" } }), "", { ...PAGE, usage: { ...RECORD, model: unapplied.model } });

    // Act
    await page.keyboard.press("Control+Alt+1");

    // Assert: the session's record has arrived, and the model's button shows
    // no effort, not the saved engine's.
    const model = box(page, "CFO").getByRole("button", { name: "Change model", exact: true });
    await expect(model.locator(".engine-control-model")).toHaveText(unapplied.shown);
    await expect(model.locator(".effort-level")).toHaveCount(0);
  });
}

test("max effort is drawn as a rainbow", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { effort: "max", phase: "settled" }), "", { ...PAGE, usage: { ...RECORD, model: GOBLIN.model } });

  // Act
  await openGoblin(page);

  // Assert: a gradient of six colours or more across the word.
  const level = box(page).getByRole("button", { name: "Change model", exact: true }).locator(".effort-level");
  await expect(level).toHaveText("max");
  const paint = await level.evaluate((element) => getComputedStyle(element).backgroundImage);
  expect(paint).toContain("linear-gradient");
  expect(paint.match(/rgb/g)?.length ?? 0).toBeGreaterThanOrEqual(6);
});

test("Close on speech history closes it", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { phase: "settled" }));
  await openGoblin(page);
  await (await boxMenu(page)).getByRole("button", { name: "Speech history", exact: true }).click();
  const history = page.getByRole("dialog", { name: "Speech history", exact: true });
  await expect(history).toBeVisible();

  // Act
  await history.getByRole("button", { name: "Close speech history", exact: true }).click();

  // Assert
  await expect(history).toHaveCount(0);
  await expect(box(page, NAME)).toBeVisible();
});
