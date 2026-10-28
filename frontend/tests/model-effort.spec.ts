import { expect, test, type Page } from "./site";
import { box, boxMenu, GOBLIN, NAME, open, openGoblin, PAGE, snapshot } from "./session-board";

// The Overlord, 2026-10-28, on the build he runs: "claude-opus-5-5 is not
// proper case, effort type that's running is not shown", "Max effort is a
// rainbow", "the effort selector is a drawer that extends off the model
// selector's pane", and the close button that did nothing. The box names the
// model by its proper name everywhere and shows the effort the session runs,
// read from how it was started until its own record says otherwise. The
// supervisor is played by tests/session-board.ts.

const CATALOG = { harnesses: [
  { id: "claude", name: "Claude Code", models: [{ id: "claude-opus-5-5", name: "Opus 5.5", efforts: ["low", "medium", "high", "xhigh", "max"], default_effort: "xhigh" }] },
  { id: "codex", name: "Codex", models: [{ id: "gpt-6-astra", name: "GPT-6-Astra", efforts: ["low", "medium", "high", "xhigh"], default_effort: "medium" }] },
  { id: "pi", name: "Pi", models: [{ id: "openrouter/z-ai/glm-5.3-flash", name: "GLM 5.3 Flash", efforts: ["off", "low", "medium", "high"], default_effort: "medium" }] },
] };
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
    await expect(box(page).getByRole("button", { name: "Change model", exact: true })).toHaveText(session.name);
    await expect(box(page).getByRole("button", { name: "Change effort", exact: true })).toHaveText(session.effort);
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
  await expect(box(page, "CFO").getByRole("button", { name: "Change effort", exact: true })).toHaveText("high");
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

    // Assert: the session's record has arrived, and the effort control shows
    // its icon, not the saved engine's effort.
    await expect(box(page, "CFO").getByRole("button", { name: "Change model", exact: true })).toHaveText(unapplied.shown);
    await expect(box(page, "CFO").getByRole("button", { name: "Change effort", exact: true })).toHaveText("");
  });
}

test("max effort is drawn as a rainbow", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { effort: "max", phase: "settled" }), "", { ...PAGE, usage: { ...RECORD, model: GOBLIN.model } });

  // Act
  await openGoblin(page);

  // Assert: a gradient of six colours or more across the word.
  const level = box(page).getByRole("button", { name: "Change effort", exact: true }).locator(".effort-level");
  await expect(level).toHaveText("max");
  const paint = await level.evaluate((element) => getComputedStyle(element).backgroundImage);
  expect(paint).toContain("linear-gradient");
  expect(paint.match(/rgb/g)?.length ?? 0).toBeGreaterThanOrEqual(6);
});

test("the effort is a drawer off the model's pane, out when the effort control opens it", async ({ page }) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", { ...PAGE, usage: { ...RECORD, model: GOBLIN.model, effort: "xhigh" } });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  const model = box(page).getByRole("menu", { name: "Model", exact: true });
  const drawer = model.getByRole("menu", { name: "Effort", exact: true });

  // Act: the effort control.
  await box(page).getByRole("button", { name: "Change effort", exact: true }).click();

  // Assert: one pane, the model's, with its effort drawer out and the
  // effort it runs taking the keyboard.
  await expect(model).toBeVisible();
  await expect(box(page).getByRole("menu")).toHaveCount(2);
  await expect(drawer.getByRole("menuitemradio", { name: "xhigh", exact: true })).toBeFocused();

  // Act: the model control, then the drawer's own toggle.
  await box(page).getByRole("button", { name: "Change effort", exact: true }).click();
  await box(page).getByRole("button", { name: "Change model", exact: true }).click();

  // Assert
  const toggle = model.getByRole("button", { name: /^Effort/ });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(drawer).toHaveCount(0);
  await toggle.click();
  await expect(drawer.getByRole("menuitemradio")).toHaveCount(5);
});

test("the effort control pressed by keyboard with the model's pane open pulls the drawer out", async ({ page }) => {
  // Arrange: the model's pane open, its running model taking the keyboard.
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", { ...PAGE, usage: { ...RECORD, model: GOBLIN.model, effort: "xhigh" } });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  const model = box(page).getByRole("menu", { name: "Model", exact: true });
  await box(page).getByRole("button", { name: "Change model", exact: true }).click();
  await expect(model.getByRole("menuitemradio", { name: "Opus 5.5", exact: true })).toBeFocused();

  // Act: back to the effort control, and Enter.
  await page.keyboard.press("Shift+Tab");
  await expect(box(page).getByRole("button", { name: "Change effort", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");

  // Assert: the same pane, its drawer out, the effort it runs focused.
  await expect(box(page).getByRole("button", { name: "Change effort", exact: true })).toHaveAttribute("aria-expanded", "true");
  await expect(model.getByRole("button", { name: /^Effort/ })).toHaveAttribute("aria-expanded", "true");
  await expect(model.getByRole("menu", { name: "Effort", exact: true }).getByRole("menuitemradio", { name: "xhigh", exact: true })).toBeFocused();
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
