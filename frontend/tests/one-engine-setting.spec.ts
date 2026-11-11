import { expect, openTaskList, test, type Locator, type Page } from "./site";
import { box, GOBLIN, NAME, open, openGoblin, PAGE, pasted, snapshot, typed } from "./session-board";

// A session has one record of its model and effort, a goblin's task record
// and the CEO's engine record, and every view of the session shows that
// record and nothing else: the tip of its card's harness mark, the model's
// button in Modern Chat's box and the line under its name in the panel's Task
// view (the Overlord, 2026-11-10: "The board tooltips are not in sync with
// the model selected. Make sure model selected is in sync across terminal,
// modern chat and in board. It all should be one standard setting,
// centralized control, just different views", #2326). The supervisor writes a
// change made anywhere into that record (internal/supervisor/session_engine.go)
// and sends the snapshot that holds it, so every view changes at once, with
// no reload. It is played by tests/session-board.ts.

// What a session's conversation says it spent, which names no model and no
// effort.
const USAGE = { input_tokens: 1200, output_tokens: 80, context_tokens: 2400, context_window: 100000 };
const EFFORTS = ["low", "medium", "high", "xhigh", "max"];
const CATALOG = { harnesses: [{ id: "claude", name: "Claude Code", models: [
  { id: "claude-opus-5-5", name: "Opus 5.5", efforts: EFFORTS, default_effort: "xhigh" },
  { id: "claude-fable-5-2", name: "Fable 5.2", efforts: EFFORTS, default_effort: "high" },
] }] };
// The session the fleet's tree reports for Zeke's task, which named Opus 5.5
// as it started and names it still: no view of the task's own session reads
// its model from there.
const STARTED = { sessions: [{ id: "zeke-session", role: "goblin", task_id: GOBLIN.id, generation: GOBLIN.generation, harness: "claude", model: "claude-opus-5-5" }] };
const engine = (harness: string, model: string, effort: string) => ({ harness, model, effort, pending: false, problem: "", waiting: "" });

// push sends the board a later snapshot, as the supervisor does once a record
// changed.
const push = (page: Page, next: object) => page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: data })), next);

interface Shown { tip: string; model: string; effort: string }

// shows asserts what the card's tooltip and Modern Chat's box, of the session
// named name whose card's mark is mark, show of its engine, leaving the
// pointer on the mark with its tooltip up.
async function shows(page: Page, mark: Locator, name: string, shown: Shown) {
  await mark.hover();
  await expect(page.getByRole("tooltip"), "the card's tooltip").toHaveText(shown.tip);
  const control = box(page, name).getByRole("button", { name: "Change model", exact: true });
  await expect(control.locator(".engine-control-model"), "the model in Modern Chat's box").toHaveText(shown.model);
  await expect(control.locator(".effort-level"), "the effort in Modern Chat's box").toHaveText(shown.effort);
}
// The harness mark on Zeke's card on the board, and on the CEO's card, which
// the Orchestration view draws.
const zeke = (page: Page) => page.locator(".task-board .task-card-shell").filter({ hasText: NAME }).locator(".card-harness [role=img]");
const ceo = (page: Page) => page.locator(".flow-node").filter({ has: page.locator("strong", { hasText: "CEO" }) }).locator(".card-harness [role=img]");

test("a model set in a goblin's terminal shows on its card's tooltip, in Modern Chat's box and in its Task view, with no reload", async ({ page }, testInfo) => {
  // Arrange: a session started on Opus 5.5 at xhigh, with its chat open.
  await open(page, snapshot(STARTED, { session: "zeke-session", phase: "settled" }), "", { ...PAGE, usage: USAGE });
  await openGoblin(page);
  await shows(page, zeke(page), NAME, { tip: "Claude Code · Opus 5.5 · xhigh", model: "Opus 5.5", effort: "xhigh" });
  await page.screenshot({ path: testInfo.outputPath("terminal-model-before.png") });
  await page.evaluate(() => Object.assign(window, { isSamePage: true }));

  // Act: /model claude-fable-5-2 and /effort high typed in the terminal,
  // which the supervisor read back from the harness's own session record
  // into the task's record.
  await push(page, snapshot({ ...STARTED, revision: 2 }, { session: "zeke-session", phase: "settled", model: "claude-fable-5-2", effort: "high" }));

  // Assert: every view shows the record, on the page as it stood.
  await shows(page, zeke(page), NAME, { tip: "Claude Code · Fable 5.2 · high", model: "Fable 5.2", effort: "high" });
  await page.screenshot({ path: testInfo.outputPath("terminal-model-after.png") });
  expect(await page.evaluate(() => (window as { isSamePage?: boolean }).isSamePage), "the page was not loaded again").toBe(true);
  await expect((await openTaskList(page)).getByText("Claude Code · Fable 5.2 · high", { exact: true })).toBeVisible();
});

test("a model picked in the board's own control reaches the harness, and every view shows it once the session's record names it", async ({ page }, testInfo) => {
  // Arrange
  const relay = await open(page, snapshot({}, { phase: "settled" }), "", { ...PAGE, usage: USAGE });
  await page.route("**/api/engines", (route) => route.fulfill({ json: CATALOG }));
  await openGoblin(page);
  relay.say({ type: "screen", asking: false, open: false, working: false });
  const control = box(page).getByRole("button", { name: "Change model", exact: true });
  await expect(control).toBeEnabled();

  // Act: the pick, which goes in as the harness's own commands, the model
  // and, once the terminal took it, the effort the catalog gives that model;
  // then the record the supervisor read back from the harness.
  await control.click();
  await box(page).getByRole("menu", { name: "Model", exact: true }).getByRole("menuitemradio", { name: "Fable 5.2" }).click();
  await expect.poll(() => typed(relay), "the harness received the model").toBe(pasted("/model claude-fable-5-2") + "\r");
  relay.say({ type: "screen", asking: false, open: false, working: false });
  await expect.poll(() => typed(relay), "the harness received the effort").toBe(pasted("/model claude-fable-5-2") + "\r" + pasted("/effort high") + "\r");
  await push(page, snapshot({ revision: 2 }, { phase: "settled", model: "claude-fable-5-2", effort: "high" }));

  // Assert
  await shows(page, zeke(page), NAME, { tip: "Claude Code · Fable 5.2 · high", model: "Fable 5.2", effort: "high" });
  await page.screenshot({ path: testInfo.outputPath("board-control-model.png") });
  await expect((await openTaskList(page)).getByText("Claude Code · Fable 5.2 · high", { exact: true })).toBeVisible();
});

test("a goblin switched to another harness and model shows them in every view, whatever its earlier conversation ran on", async ({ page }, testInfo) => {
  // Arrange
  await open(page, snapshot({}, { phase: "settled" }), "", { ...PAGE, usage: USAGE });
  await openGoblin(page);
  await shows(page, zeke(page), NAME, { tip: "Claude Code · Opus 5.5 · xhigh", model: "Opus 5.5", effort: "xhigh" });

  // Act: ceo switch wrote the record and started a new generation, and the
  // conversation the box still holds is the one Opus 5.5 answered in.
  await push(page, snapshot({ revision: 2 }, { phase: "settled", generation: "cg-touch-2", harness: "codex", model: "gpt-6-astra", effort: "medium" }));

  // Assert
  await shows(page, zeke(page), NAME, { tip: "Codex · GPT-6 Astra · medium", model: "GPT-6 Astra", effort: "medium" });
  await page.screenshot({ path: testInfo.outputPath("switch-model.png") });
  await expect((await openTaskList(page)).getByText("Codex · GPT-6 Astra · medium", { exact: true })).toBeVisible();
});

test("the CEO's card, box and Task view show its engine record, and a model set in its terminal with no reload", async ({ page }, testInfo) => {
  // Arrange
  await open(page, snapshot({ ceo_engine: engine("claude", "claude-opus-5-5", "high") }), "", { ...PAGE, usage: USAGE });
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CEO");
  await shows(page, ceo(page), "CEO", { tip: "Claude Code · Opus 5.5 · high", model: "Opus 5.5", effort: "high" });

  // Act: /model typed in the CEO's own terminal, read back into its record.
  await push(page, snapshot({ revision: 2, ceo_engine: engine("claude", "claude-fable-5-2", "xhigh") }));

  // Assert
  await shows(page, ceo(page), "CEO", { tip: "Claude Code · Fable 5.2 · xhigh", model: "Fable 5.2", effort: "xhigh" });
  await page.screenshot({ path: testInfo.outputPath("ceo-model.png") });
  await expect((await openTaskList(page)).getByText("Claude Code · Fable 5.2 · xhigh", { exact: true })).toBeVisible();
});
