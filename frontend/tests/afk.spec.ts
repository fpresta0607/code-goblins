import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, ORIGIN, test, type Page, type Route } from "./site";

// AFK mode on the board: the toggle in the CFO panel's header, what the CFO's
// bar says while it is on, the offer when he is back and the link to his page
// of the last stretch once it turns off. The supervisor is played here: the board's stream
// is held open and each snapshot is pushed to it, and what the board asks is
// collected.
const HOURS = 60 * 60 * 1000;
const BOARD = "his own board (goblins-window.exe pid 4242)";
const task = (id: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "northwind-api", phase: "working", verified: false, generation: id + "-1", ...fields });
const TASKS = [task("nw-invoice-export"), task("nw-checkout-tax"), task("nw-search-index")];
const question = (id: string, text: string, asker = "") => ({ id, text, options: ["Hold it", "Do it"], recommended: "Hold it", status: "pending", task: asker, generation: asker ? asker + "-1" : "", created_at: new Date(Date.now() - HOURS).toISOString() });
// The second is a goblin's question the CFO passed up to him: a goblin's own
// question is the CFO's to answer, never waits on him and is never held.
const QUESTIONS = [question("q-drop-table", "Migration 0042 drops the legacy_invoices table. Apply it?"), question("q-old-branch", "nw-checkout-tax asks: Delete the old branch fix/tax-rounding-v1?")];
const HELD = [
  { item: "question:q-drop-table", task: "", what: "Migration 0042 drops the legacy_invoices table. Apply it?", at: QUESTIONS[0].created_at, waiting: true, now: "still waiting on you", meanwhile: "", recommendation: "Hold it" },
  { item: "question:q-old-branch", task: "nw-checkout-tax", what: "Delete the old branch fix/tax-rounding-v1?", at: QUESTIONS[1].created_at, waiting: true, now: "still waiting on you", meanwhile: "working: moved on to the refund path", recommendation: "Hold it" },
];
let revision = 0;
const snapshot = (fields: Record<string, unknown> = {}) => ({ healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: ++revision, attention: [], tasks: TASKS, sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }], ...fields });
// AFK mode on since a moment ago unless ago says how long, so a test that is
// not about the offer never meets it.
const on = (fields: Record<string, unknown> = {}, ago = 0) => ({ state: "on", since: new Date(Date.now() - ago).toISOString(), from: BOARD, decided: 0, held: [], ...fields });
// AFK mode off with the saved report served by the board.
const PAGE = "/api/afk/report/afk-fixture/index.html";
const KEPT = { state: "off", page: PAGE };
// What the supervisor tells him on a board it refuses: one short sentence that
// says what to do. What it found goes to the CFO, never to the board.
const REFUSAL = "Switch AFK in the Code Goblins window, or run cfo afk on in PowerShell.";

interface Asked { on: unknown; token: string }
interface Supervisor { asked: Asked[]; refuses: boolean; announces: boolean }

// open loads the board with its stream held open and first as its snapshot.
// What the board asks of the switch is collected in asked and refused while
// refuses is set; announces says whether the supervisor can be asked what was
// already announced, and it has announced everything when it can.
async function open(page: Page, first: object, supervisor: Supervisor = { asked: [], refuses: false, announces: true }): Promise<Supervisor> {
  await page.addInitScript(() => {
    const streams: EventTarget[] = [];
    // What held the page's main thread, kept for slowPush below.
    const longFrames: unknown[] = [];
    const pushedAt: number[] = [];
    new PerformanceObserver((list) => { for (const entry of list.getEntries()) longFrames.push(entry.toJSON()); }).observe({ type: "long-animation-frame", buffered: true });
    class HeldStream extends EventTarget {
      onerror: unknown = null;
      constructor() { super(); streams.push(this); }
      close() { streams.splice(streams.indexOf(this), 1); }
    }
    Object.assign(window, { EventSource: HeldStream, longFrames, pushedAt, pushSnapshot: (value: unknown) => { pushedAt.push(performance.now()); for (const stream of streams) stream.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(value) })); return streams.length; } });
  });
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (pathname === "/api/afk" && request.method() === "POST") {
      supervisor.asked.push({ on: (request.postDataJSON() as { on: unknown }).on, token: request.headers()["x-cfo-token"] });
      await route.fulfill(supervisor.refuses ? { status: 403, json: { error: REFUSAL } } : { json: { state: "on" } });
    } else if (pathname === "/api/announce" && supervisor.announces) await route.fulfill({ json: { claimed: [] } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  const started = Date.now();
  try {
    await expect.poll(() => push(page, first)).toBeGreaterThan(0);
  } finally {
    if (Date.now() - started > SLOW_PUSH_MS) await slowPush(page, Date.now() - started);
  }
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  return supervisor;
}
// On CI runners the first push has twice waited over five seconds on the
// page's main thread, which drew nothing meanwhile (runs 37360134485 and
// 37517175728), and that does not happen on a developer machine. A push that
// slow attaches the page's long animation frames, each with the scripts,
// style and layout and rendering it spent its time on, and when the pushes
// were made, so the next one names what held the thread.
const SLOW_PUSH_MS = 2000;
async function slowPush(page: Page, waited: number) {
  const held = await page.evaluate(() => {
    const { longFrames, pushedAt } = window as unknown as { longFrames: unknown[]; pushedAt: number[] };
    return { now: performance.now(), pushedAt, longFrames };
  });
  await test.info().attach("long animation frames", { contentType: "application/json", body: JSON.stringify({ waited, ...held }, null, 1) });
}
const push = (page: Page, value: object) => page.evaluate((next) => (window as unknown as { pushSnapshot: (value: unknown) => number }).pushSnapshot(next), value);

const bar = (page: Page) => page.locator(".cfo-pin");
const header = (page: Page) => page.locator(".panel-header");
const toggle = (page: Page) => page.getByRole("switch", { name: "AFK mode" });
const offer = (page: Page) => page.locator("dialog.afk-dialog").filter({ hasText: "Welcome back" });
// The toggle is in the header of the CFO's panel, which the CFO's bar opens.
async function openCfoPanel(page: Page) {
  await bar(page).getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(header(page).locator("#panel-title")).toHaveText("CFO");
}

test("the CFO panel's header carries the AFK toggle beside its status: off at rest, on asks first, and nothing turns on until he says so", async ({ page }) => {
  const supervisor = await open(page, snapshot());
  // The switch is not on the CFO's bar, which is as it was.
  await expect(bar(page).getByRole("switch")).toHaveCount(0);
  await expect(bar(page).locator(".cfo-rest").getByRole("button")).toHaveCount(2);
  await openCfoPanel(page);
  await expect(header(page).getByRole("switch")).toHaveCount(1);
  await expect(toggle(page)).toHaveText("AFK");
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await expect(bar(page).locator(".cfo-rest")).toContainText("All quiet. 3 goblins at work.");

  // On asks, with the focus on Cancel: Enter alone turns nothing on.
  await toggle(page).click();
  const asks = page.locator("dialog.afk-dialog");
  await expect(asks).toContainText("Go AFK?");
  await expect(asks.getByRole("button", { name: "Cancel" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(asks).toHaveCount(0);
  expect(supervisor.asked).toEqual([]);

  await toggle(page).click();
  await asks.getByRole("button", { name: "Turn AFK on" }).click();
  await expect.poll(() => supervisor.asked).toEqual([{ on: true, token: "fixture" }]);
  await push(page, snapshot({ afk: on() }));
  await expect(asks).toHaveCount(0);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "true");
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since .+, from your board\. 0 decided\.$/);
  // Off asks nothing first: it only gives him his decisions back.
  await toggle(page).click();
  await expect.poll(() => supervisor.asked.at(-1)).toEqual({ on: false, token: "fixture" });
});

test("the toggle is in the header in the panel's Task view and its Terminal view alike", async ({ page }) => {
  await open(page, snapshot());
  await openCfoPanel(page);
  for (const view of ["Task", "Terminal"]) {
    await page.locator(".panel-pill").getByRole("button", { name: view, exact: true }).click();
    await expect(toggle(page)).toBeVisible();
  }
});

// The Overlord, 2026-10-08: "this yellow text i hate. as an error". A refusal
// of his own press is an error he can read: what did not happen, then the one
// short sentence the supervisor has for him, in the question and under the
// header, and it stays until he closes it. "No dialog ever shows a raw refusal
// paragraph: one short sentence at most" (2026-10-09).
test("a refusal is one short sentence shown as an error, in the question and under the header, until he closes it, and the toggle stays as it was", async ({ page }) => {
  await page.clock.install();
  const supervisor = await open(page, snapshot(), { asked: [], refuses: true, announces: true });
  await openCfoPanel(page);
  await toggle(page).click();
  const asks = page.locator("dialog.afk-dialog");
  await asks.getByRole("button", { name: "Turn AFK on" }).click();
  await expect(asks.getByRole("alert")).toHaveText("AFK did not turn on" + REFUSAL);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await asks.getByRole("button", { name: "Cancel" }).click();
  await expect(header(page).getByRole("alert")).toHaveCount(0);

  await push(page, snapshot({ afk: on() }));
  await toggle(page).click();
  const refusal = header(page).getByRole("alert");
  await expect(refusal).toHaveText("AFK did not turn off" + REFUSAL);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "true");
  await page.clock.fastForward(60_000);
  await expect(refusal).toBeVisible();
  await refusal.getByRole("button", { name: "Close the message" }).click();
  await expect(header(page).getByRole("alert")).toHaveCount(0);
  expect(supervisor.asked.map((ask) => ask.on)).toEqual([true, false]);
});

// A turn-on refused after he closed the question still says which way he
// pressed: AFK did not turn on, under the header.
test("a refusal that arrives after he cancelled Go AFK says AFK did not turn on", async ({ page }) => {
  await open(page, snapshot());
  let answer = () => {};
  const answered = new Promise<void>((resolve) => { answer = resolve; });
  await page.route("**/api/afk", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await answered;
    await route.fulfill({ status: 403, json: { error: REFUSAL } });
  });
  await openCfoPanel(page);
  await toggle(page).click();
  const asks = page.locator("dialog.afk-dialog");
  await asks.getByRole("button", { name: "Turn AFK on" }).click();
  await expect(asks.getByRole("button", { name: "Turning AFK on…" })).toBeDisabled();
  await asks.getByRole("button", { name: "Cancel" }).click();
  await expect(asks).toHaveCount(0);
  answer();
  await expect(header(page).getByRole("alert")).toHaveText("AFK did not turn on" + REFUSAL);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
});

// The Overlord, 2026-10-08: "hate that loading message doesnt happen on button
// when i click turn off". The button he pressed says it is working until the
// supervisor answers.
test("Turn AFK off says it is working until the supervisor answers", async ({ page }) => {
  await open(page, snapshot({ afk: on({}, 8 * HOURS) }));
  let answer = () => {};
  const answered = new Promise<void>((resolve) => { answer = resolve; });
  await page.route("**/api/afk", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await answered;
    await route.fulfill({ json: { state: "off" } });
  });
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  // The CFO decided nothing in this stretch, so the offer counts nothing.
  await expect(offer(page)).toContainText("AFK has been on since");
  await expect(offer(page)).not.toContainText("The CFO decided");
  await offer(page).getByRole("button", { name: "Turn AFK off" }).click();
  const working = offer(page).getByRole("button", { name: "Turning AFK off…" });
  await expect(working).toBeDisabled();
  await expect(working).toHaveAttribute("aria-busy", "true");
  await expect(working.locator(".card-start-spinner")).toBeVisible();
  answer();
  await expect(offer(page)).toHaveCount(0);
  await expect(page.locator("dialog[open]")).toHaveCount(0);
});

// The Overlord, 2026-10-08: "held for you in afk mode not needed it should
// just show the command center button to open whats to be answered". AFK mode
// is complete autopilot, so the bar lists nothing held for him: whatever waits
// is a click away on Open Command Center, which does not glow while he is away.
test("while AFK is on the bar stays at rest and lists nothing held, and an outline Open Command Center is its one way to what waits", async ({ page }) => {
  await open(page, snapshot({ questions: QUESTIONS }));
  // With AFK off the same items bring the CFO's lantern box with its lantern
  // Open Command Center.
  const command = bar(page).getByRole("button", { name: /^Open Command Center/ });
  await expect(command).toHaveAccessibleName("Open Command Center: 2 waiting on you");
  await expect(bar(page).locator(".dialogue")).toHaveCount(1);
  await expect(command).not.toHaveClass(/outline/);

  await push(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since .+\. 4 decided\.$/);
  await expect(bar(page).locator(".dialogue")).toHaveCount(0);
  await expect(bar(page).getByText(/held/i)).toHaveCount(0);
  await expect(bar(page).locator(".afk-held, details")).toHaveCount(0);
  await expect(bar(page).locator(".cfo-rest").getByRole("button", { name: "Open Command Center: 2 waiting on you" })).toHaveClass(/outline/);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();

  await command.click();
  await expect(page.locator("dialog.question-modal")).toBeVisible();
});

test("with AFK on and nothing waiting the bar has no Command Center button, and an item that comes while he is away adds it and opens nothing", async ({ page }) => {
  await open(page, snapshot({ afk: on() }));
  const command = bar(page).getByRole("button", { name: /^Open Command Center/ });
  await expect(command).toHaveCount(0);
  await push(page, snapshot({ questions: [QUESTIONS[0]], afk: on({ held: [HELD[0]] }) }));
  await expect(command).toHaveAccessibleName("Open Command Center: 1 waiting on you");
  await page.waitForTimeout(300);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
});

test("a board that loads, or reconnects, with AFK already on and items already waiting opens nothing by itself", async ({ page }) => {
  await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
  const command = bar(page).getByRole("button", { name: /^Open Command Center/ });
  await expect(command).toHaveAccessibleName("Open Command Center: 2 waiting on you");
  await page.reload();
  await expect.poll(() => push(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }))).toBeGreaterThan(0);
  await expect(command).toHaveAccessibleName("Open Command Center: 2 waiting on you");
  await page.waitForTimeout(300);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
});

for (const response of ["successful", "failed"] as const) test(`a late ${response} announcement reply opens nothing and sends no notification after AFK turns on`, async ({ page }) => {
  await page.addInitScript(() => {
    class Note {
      static permission = "granted";
      onclick: (() => void) | null = null;
      onclose: (() => void) | null = null;
      constructor(title: string) { (window as unknown as { notes: string[] }).notes.push(title); }
      close() { this.onclose?.(); }
    }
    Object.assign(window, { notes: [], Notification: Note });
    document.hasFocus = () => false;
  });
  await open(page, snapshot());
  const inFlight: Route[] = [];
  await page.route("**/api/announce", (route) => { inFlight.push(route); });
  await push(page, snapshot({ questions: [QUESTIONS[0]] }));
  await expect.poll(() => inFlight.some((route) => (route.request().postDataJSON() as { keys: string[] }).keys.includes("alert:question:q-drop-table@" + QUESTIONS[0].created_at))).toBe(true);
  await push(page, snapshot({ questions: [QUESTIONS[0]], afk: on({ held: [HELD[0]] }) }));
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  for (const route of inFlight.splice(0)) await route.fulfill(response === "successful"
    ? { json: { claimed: (route.request().postDataJSON() as { keys: string[] }).keys } }
    : { status: 503, json: { error: "The supervisor is restarting" } });
  await page.waitForTimeout(500);
  expect(await page.evaluate(() => (window as unknown as { notes: string[] }).notes)).toEqual([]);
  await expect(page.locator(".toasts .dialogue")).toHaveCount(0);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
});

test("with AFK on, a new item raises no alert and opens nothing, even when the supervisor cannot be asked what was announced", async ({ page }) => {
  await page.addInitScript(() => {
    class Note {
      static permission = "granted";
      static requestPermission = async () => "granted";
      constructor(public title: string, public options: { body: string }) { (window as unknown as { notes: string[] }).notes.push(options.body); }
      close() {}
    }
    Object.assign(window, { notes: [], Notification: Note });
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
  });
  const notes = () => page.evaluate(() => (window as unknown as { notes: string[] }).notes);
  await open(page, snapshot({ afk: on() }), { asked: [], refuses: false, announces: false });
  const asked = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/announce");
  await push(page, snapshot({ questions: [QUESTIONS[1]], afk: on({ held: [HELD[1]] }) }));
  await asked;
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  await page.waitForTimeout(500);
  expect(await notes()).toEqual([]);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
  // The same failed ask with AFK off announces from what this browser
  // remembers, as it always has: the item that is new since is notified, and
  // the one that arrived while he was away never is.
  await push(page, snapshot({ questions: QUESTIONS }));
  await expect.poll(notes).toEqual(["The CFO asks: Migration 0042 drops the legacy_invoices table. Apply it?"]);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
});

test("his first click after he has been gone still does what he meant and offers to turn AFK off: keys typed blind press nothing, Stay AFK keeps it on, and Turn AFK off opens the report", async ({ page }) => {
  await page.clock.install();
  const supervisor = await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 7, held: HELD }, 8 * HOURS) }));
  // Nothing opens by itself while he is away.
  await expect(offer(page)).toHaveCount(0);
  await page.locator(".task-card").filter({ hasText: "nw-search-index" }).first().click();
  await expect(offer(page)).toContainText("The CFO decided 7.");
  await expect(offer(page)).toContainText("turned on from your board.");
  // What he was typing when it opened presses neither button.
  await expect(offer(page)).toBeFocused();
  await page.keyboard.press("Space");
  await page.keyboard.press("Enter");
  await page.keyboard.type("git status");
  await expect(offer(page)).toBeVisible();
  expect(supervisor.asked).toEqual([]);

  await offer(page).getByRole("button", { name: "Stay AFK" }).click();
  await expect(offer(page)).toHaveCount(0);
  // The click that brought the offer opened the task he clicked.
  await expect(page.locator("#panel-title")).toHaveText("nw-search-index");
  // He is here now, so his next clicks offer nothing, and AFK is still on.
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since /);
  await expect(offer(page)).toHaveCount(0);

  // Gone again for longer than the wait: the next click offers, and Turn AFK
  // off asks the supervisor and opens the report over the board.
  await page.clock.fastForward(6 * 60 * 1000);
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await offer(page).getByRole("button", { name: "Turn AFK off" }).click();
  await expect.poll(() => supervisor.asked).toEqual([{ on: false, token: "fixture" }]);
  await expect(offer(page)).toHaveCount(0);
  await push(page, snapshot({ questions: QUESTIONS, afk: KEPT }));
  await expect(page.getByRole("dialog", { name: "Your AFK report" })).toBeVisible();
});

test("turning AFK off opens the report modal and its button reopens it without a new page", async ({ page, context }) => {
  const supervisor = await open(page, snapshot({ afk: on() }));
  await page.route("**" + PAGE, (route) => route.fulfill({ contentType: "text/html", body: '<article class="back"><h1>Welcome back</h1></article>' }));
  await openCfoPanel(page);
  await toggle(page).click();
  await expect.poll(() => supervisor.asked).toEqual([{ on: false, token: "fixture" }]);
  await push(page, snapshot({ afk: { state: "off" } }));
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await expect(page.getByRole("dialog", { name: "Your AFK report" })).toContainText("Loading your report");
  await expect(header(page).getByRole("link", { name: "Open your page of the last AFK stretch" })).toHaveCount(0);
  await push(page, snapshot({ afk: KEPT }));
  const report = page.getByRole("dialog", { name: "Your AFK report" });
  await expect(report).toBeVisible();
  expect(context.pages()).toHaveLength(1);
  await expect(page.locator('a[target="_blank"][href*="afk"]')).toHaveCount(0);
  await report.getByRole("button", { name: "Close AFK report" }).click();
  await expect(report).toHaveCount(0);
  await push(page, snapshot({ afk: KEPT }));
  await expect(report).toHaveCount(0);
  await header(page).getByRole("button", { name: "Open your last AFK report" }).click();
  await expect(report).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(report).toHaveCount(0);
});

test("the report's green primary button walks through every pending decision and closes when done", async ({ page }) => {
  await open(page, snapshot({ afk: KEPT, questions: QUESTIONS }));
  await page.route("**" + PAGE, (route) => route.fulfill({ contentType: "text/html", body: '<article class="back"><h1>Welcome back</h1></article>' }));
  const answered: string[] = [];
  await page.route("**/api/actions", (route) => {
    const action = route.request().postDataJSON() as { question_id: string };
    answered.push(action.question_id);
    return route.fulfill({ status: 202, json: { ...action, status: "queued" } });
  });
  await openCfoPanel(page);
  await header(page).getByRole("button", { name: "Open your last AFK report" }).click();
  const report = page.getByRole("dialog", { name: "Your AFK report" });
  const start = report.getByRole("button", { name: "Go through what needs you" });
  await expect(start).toHaveClass(/primary/);
  expect(await start.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe("rgb(0, 229, 155)");
  await start.click();
  await expect(report).toHaveCount(0);
  const decisions = page.locator("dialog.question-modal");
  for (const question of QUESTIONS) {
    await expect(decisions.locator(".question-body")).toHaveText(question.text);
    await decisions.getByRole("radio", { name: "Do it", exact: true }).check();
    await decisions.getByRole("button", { name: "Send decision" }).click();
  }
  await expect(decisions).toContainText("You're all done");
  await expect(decisions).not.toBeVisible();
  expect(answered).toEqual(QUESTIONS.map((question) => question.id));
  await push(page, snapshot({ afk: KEPT }));
  await header(page).getByRole("button", { name: "Open your last AFK report" }).click();
  await expect(start).toBeDisabled();
  await expect(report).toContainText("Nothing is waiting in Command Center.");
});

test.describe("AFK report layout", () => {
  let directory = "";
  let html = "";
  test.beforeAll(async () => {
    directory = await mkdtemp(join(tmpdir(), "afk-report-"));
    execFileSync("go", ["test", "./internal/afk", "-run", "^TestReturnModalFixture$", "-count=1"], { cwd: "..", env: { ...process.env, AFK_PROOF_DIR: directory }, windowsHide: true, timeout: 120_000 });
    html = await readFile(join(directory, "report.html"), "utf8");
  });
  test.afterAll(async () => { if (directory) await rm(directory, { recursive: true, force: true }); });
  for (const width of [1097, 390]) test(`the AFK report wraps every long row at ${width} px and links to its Command Center card`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page, snapshot({ afk: on(), questions: QUESTIONS }));
    await page.route("**" + PAGE, (route) => route.fulfill({ contentType: "text/html", body: html }));
    await page.route("**/api/afk/report/afk-fixture/*.png", (route) => route.fulfill({ path: join("public/assets", new URL(route.request().url()).pathname.split("/").at(-1)!) }));
    await page.route("**/pictures/*.png", (route) => route.fulfill({ path: "public/assets/goblin-workshop.png" }));
    await openCfoPanel(page);
    await toggle(page).click();
    await push(page, snapshot({ afk: KEPT, questions: QUESTIONS }));
    const report = page.getByRole("dialog", { name: "Your AFK report" });
    await expect(report).toBeVisible();
    await expect(report.getByRole("button", { name: "Go through what needs you" })).toBeInViewport();
    const frame = report.locator("iframe").contentFrame();
    const titles = frame.locator(".inbox-text strong").filter({ hasText: "Review the complete migration" });
    await expect(titles).toHaveCount(3);
    for (const title of await titles.all()) {
      const dimensions = await title.evaluate((element) => ({ height: element.clientHeight, scrollHeight: element.scrollHeight, width: element.clientWidth, scrollWidth: element.scrollWidth, lineHeight: parseFloat(getComputedStyle(element).lineHeight) }));
      expect(dimensions.height).toBeGreaterThan(dimensions.lineHeight);
      expect(dimensions.scrollHeight).toBeLessThanOrEqual(dimensions.height + 1);
      expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.width + 1);
    }
    expect(await frame.locator("html").evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
    await frame.locator("body").evaluate(() => document.fonts.ready);
    await page.screenshot({ path: testInfo.outputPath(`after-${width}.png`) });
    await frame.getByRole("heading", { name: "Record", exact: true }).scrollIntoViewIfNeeded();
    await expect(frame.getByRole("heading", { name: "Record", exact: true })).toBeInViewport();
    await expect(report.getByRole("button", { name: "Go through what needs you" })).toBeInViewport();
    await page.screenshot({ path: testInfo.outputPath(`after-${width}-scrolled.png`) });
    await frame.getByRole("button", { name: "Open in Command Center" }).first().click();
    await expect(report).toHaveCount(0);
    await expect(page.locator("dialog.question-modal")).toContainText(QUESTIONS[0].text);
  });

  test("the report opens the current decision in History's change card", async ({ page }) => {
    const decided = { ...question("afk-left-20261015T050000.000000000Z", "Delete the idle cache database?"), status: "succeeded", options: ["Delete in a week", "Delete now", "Keep it longer"], answer: "Keep it longer", answered_option: "Keep it longer", answer_kind: "option", answered_by: "overlord" };
    await open(page, snapshot({ afk: KEPT, questions: [decided] }));
    await page.route("**" + PAGE, (route) => route.fulfill({ contentType: "text/html", body: html }));
    const report = page.getByRole("dialog", { name: "Your AFK report" });
    await expect(report).toHaveCount(0);
    await openCfoPanel(page);
    await header(page).getByRole("button", { name: "Open your last AFK report" }).click();
    await report.locator("iframe").contentFrame().getByRole("button", { name: "Change answer" }).first().click();
    await expect(report).toHaveCount(0);
    const change = page.locator("dialog.question-modal .change-card");
    await expect(change).toContainText(decided.text);
    await expect(change.getByRole("radio", { name: "Keep it longer" })).toBeChecked();
    await expect(change.getByRole("button", { name: "Change answer" })).toBeDisabled();
    await change.getByRole("radio", { name: "Delete now", exact: true }).check();
    await expect(change.getByRole("button", { name: "Change answer" })).toBeEnabled();
  });

  test("a report that cannot load can be retried and Escape inside it returns focus", async ({ page }) => {
    await open(page, snapshot({ afk: KEPT }));
    let isAvailable = false;
    await page.route("**" + PAGE, (route) => route.fulfill(isAvailable ? { contentType: "text/html", body: html } : { status: 404, body: "" }));
    await openCfoPanel(page);
    const reopen = header(page).getByRole("button", { name: "Open your last AFK report" });
    await reopen.click();
    const report = page.getByRole("dialog", { name: "Your AFK report" });
    await expect(report.getByRole("status")).toHaveText("Report unavailable");
    isAvailable = true;
    await report.getByRole("button", { name: "Try again" }).click();
    const frame = report.locator("iframe").contentFrame();
    await expect(frame.getByRole("heading", { name: /Welcome back/ })).toBeVisible();
    await frame.locator("li[data-title]").first().focus();
    await page.keyboard.press("Enter");
    await expect(frame.getByRole("dialog")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(report).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(report).toHaveCount(0);
    await expect(reopen).toBeFocused();
  });
});

test("a press on the toggle itself is his answer, so it offers nothing", async ({ page }) => {
  // The CFO's panel is open before AFK turns on, so the toggle is his first
  // touch after he has been gone.
  const supervisor = await open(page, snapshot());
  await openCfoPanel(page);
  await push(page, snapshot({ afk: on({}, 8 * HOURS) }));
  await expect(toggle(page)).toHaveAttribute("aria-checked", "true");
  await toggle(page).click();
  await expect.poll(() => supervisor.asked).toEqual([{ on: false, token: "fixture" }]);
  await expect(offer(page)).toHaveCount(0);
});

test("a stretch the CFO turned on at his ask says so on the bar, and the offer quotes his words", async ({ page }) => {
  await page.clock.install();
  const byTheCFO = { from: "the CFO at his ask (claude pid 4242)", asked: "I'm stepping away, turn AFK on" };
  await open(page, snapshot({ afk: on({ ...byTheCFO, decided: 1 }, 8 * HOURS) }));
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since .+, turned on by the CFO at your ask\. 1 decided\.$/);
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await expect(offer(page)).toContainText("turned on by the CFO at your ask: “I'm stepping away, turn AFK on”. The CFO decided 1.");
  await offer(page).getByRole("button", { name: "Stay AFK" }).click();
  await expect(offer(page)).toHaveCount(0);
});

// The Overlord, 2026-10-09, of the dialog that met him after the CFO turned
// AFK on at his ask: "not right button for this kind of turn on". He had just
// asked for AFK, so the main button keeps it on, turning it off is the lesser
// one, one short line says the CFO did it at his ask, and "The CFO decided 0."
// is gone while nothing was decided.
test("his first click or key after the CFO turned AFK on at his ask says so in one short line, its main button keeps AFK on, and the usual wait follows his answer", async ({ page }) => {
  await page.clock.install();
  const byTheCFO = { from: "the CFO at his ask (claude pid 4242)", asked: "AFK did not turn on. AFK mode is the Supreme Overlord's switch, and the supervisor could not follow its parents to the desktop ... fix this issue i should alwyas be abel to turn on afk no issues" };
  const supervisor = await open(page, snapshot({ afk: on(byTheCFO) }));
  const switched = page.locator("dialog.afk-dialog").filter({ hasText: "The CFO turned AFK on" });
  // Nothing opens by itself after the switch.
  await expect(switched).toHaveCount(0);

  await page.locator(".task-card").filter({ hasText: "nw-search-index" }).first().click();
  await expect(switched.locator("p").first()).toHaveText(/^It did so at your ask, at \d{1,2}:\d{2}\s?[AP]M\.$/);
  // His words stay with his page of the stretch, and nothing was decided yet.
  await expect(switched).not.toContainText("fix this issue");
  await expect(switched).not.toContainText("The CFO decided");
  await expect(switched).not.toContainText("Turning it off puts your page of the stretch");
  await expect(offer(page)).toHaveCount(0);
  await expect(switched).toBeFocused();
  // The main button keeps it on, and turning it off is the lesser one.
  await expect(switched.getByRole("button")).toHaveText(["Got it", "Turn AFK off"]);
  await expect(switched.getByRole("button", { name: "Got it" })).toHaveClass(/primary/);
  await expect(switched.getByRole("button", { name: "Turn AFK off" })).not.toHaveClass(/primary/);
  await switched.getByRole("button", { name: "Got it" }).click();
  await expect(page.locator("dialog.afk-dialog")).toHaveCount(0);
  // The click that brought the offer opened the task he clicked.
  await expect(page.locator("#panel-title")).toHaveText("nw-search-index");

  // He has answered it, so his next click and key offer nothing.
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await page.keyboard.press("Shift");
  await expect(page.locator("dialog.afk-dialog")).toHaveCount(0);
  expect(supervisor.asked).toEqual([]);
});

// The lesser button still turns it off, and a refusal
// there is the one short sentence and never the supervisor's own words.
test("after the CFO turned AFK on at his ask, the lesser Turn AFK off turns it off, and a refusal is one short sentence", async ({ page }) => {
  const byTheCFO = { from: "the CFO at his ask (claude pid 4242)", asked: "turn AFK on" };
  const supervisor = await open(page, snapshot({ afk: on({ ...byTheCFO, decided: 2 }) }), { asked: [], refuses: true, announces: true });
  const switched = page.locator("dialog.afk-dialog").filter({ hasText: "The CFO turned AFK on" });
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  // Once the CFO decided something the line counts it.
  await expect(switched.locator("p").first()).toHaveText(/^It did so at your ask, at .+\. The CFO decided 2\.$/);
  await switched.getByRole("button", { name: "Turn AFK off" }).click();
  await expect(switched.getByRole("alert")).toHaveText("AFK did not turn off" + REFUSAL);
  await expect(switched).toBeVisible();

  supervisor.refuses = false;
  await switched.getByRole("button", { name: "Turn AFK off" }).click();
  await expect(switched).toHaveCount(0);
  await expect(page.locator("dialog[open]")).toHaveCount(0);
  expect(supervisor.asked.map((ask) => ask.on)).toEqual([false, false]);
});

test("a switch that cannot be read is shown off, its tip says how to reset it, and what waits on him still leads", async ({ page }) => {
  const supervisor = await open(page, snapshot({ questions: [QUESTIONS[0]], afk: { state: "unreadable" } }));
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  await openCfoPanel(page);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await expect(toggle(page)).toHaveAttribute("data-tip", "Reset AFK to off");
  await expect(header(page).getByText(/cannot be read/)).toHaveCount(0);
  // His press resets it: it asks for off, and asks nothing first.
  await toggle(page).click();
  await expect.poll(() => supervisor.asked).toEqual([{ on: false, token: "fixture" }]);
  await expect(page.locator("dialog.afk-dialog")).toHaveCount(0);
});

// within says whether every child of the element at selector lies inside it.
const within = (page: Page, selector: string) => page.locator(selector).evaluate((element) => {
  const box = element.getBoundingClientRect();
  return [...element.children].every((child) => { const part = child.getBoundingClientRect(); return part.width === 0 || part.left >= box.left - 0.5 && part.right <= box.right + 0.5 && part.top >= box.top - 0.5 && part.bottom <= box.bottom + 0.5; });
});
const smallest = (page: Page) => bar(page).evaluate((pin) => Math.min(...[...pin.querySelectorAll("p, span, strong, small, time, button, summary")].filter((element) => (element.textContent || "").trim() !== "").map((element) => parseFloat(getComputedStyle(element).fontSize))));
const fits = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth);

test("on a phone the bar keeps its line, its terminal and Open Command Center inside it, and the toggle stays in the CFO panel's header, with nothing wider than the page", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 2 waiting on you" })).toBeVisible();
  expect(await within(page, ".cfo-rest")).toBe(true);
  expect(await smallest(page)).toBeGreaterThanOrEqual(15);
  await openCfoPanel(page);
  await expect(toggle(page)).toBeVisible();
  expect(await within(page, ".panel-header")).toBe(true);
  expect(await toggle(page).evaluate((element) => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(15);
  expect(await fits(page)).toBe(true);
});

// His window: maximized on a 2560 by 1600 screen at 150 percent.
test.describe("in the desktop window", () => {
  test.use({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

  test("the toggle sits on the header's first row", async ({ page }) => {
    await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
    await expect(bar(page).getByRole("button", { name: "Open Command Center: 2 waiting on you" })).toBeVisible();
    await openCfoPanel(page);
    for (const view of ["Task", "Terminal"]) {
      await page.locator(".panel-pill").getByRole("button", { name: view, exact: true }).click();
      const rows = await header(page).evaluate((element) => [".goblin-avatar", ".afk-toggle"].map((selector) => { const box = element.querySelector(selector)!.getBoundingClientRect(); return [Math.round(box.top), Math.round(box.bottom)]; }));
      // The toggle is beside the CFO's portrait, name and status, not under them.
      expect(rows[1][0], view + " view").toBeLessThan(rows[0][1]);
      expect(rows[1][1], view + " view").toBeGreaterThan(rows[0][0]);
      expect(await within(page, ".panel-header"), view + " view").toBe(true);
    }
    expect(await within(page, ".cfo-rest")).toBe(true);
    expect(await fits(page)).toBe(true);
  });

  // The panel dragged to its narrowest, 360 pixels: the header wraps rather
  // than hide or cover the toggle.
  test("on a panel at its narrowest the toggle is still there and inside the header", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("cfo-pane-width", "360"));
    await open(page, snapshot({ afk: on() }));
    await openCfoPanel(page);
    for (const view of ["Task", "Terminal"]) {
      await page.locator(".panel-pill").getByRole("button", { name: view, exact: true }).click();
      expect(Math.round((await page.locator(".context-pane").boundingBox())!.width), view + " view").toBe(360);
      await expect(toggle(page)).toBeVisible();
      expect(await within(page, ".panel-header"), view + " view").toBe(true);
      const overlap = await header(page).evaluate((element) => {
        const status = element.querySelector(".panel-status")!.getBoundingClientRect(), switched = element.querySelector(".afk-toggle")!.getBoundingClientRect();
        return status.right > switched.left && status.left < switched.right && status.bottom > switched.top && status.top < switched.bottom;
      });
      expect(overlap, view + " view: the status and the toggle overlap").toBe(false);
    }
  });
});

// The Overlord, 2026-10-13: turning AFK off made the whole board black, and
// only killing the app brought it back. His board was loaded before an update
// installed a new build, and the page AFK off opened arrived the first time it
// was shown: the new supervisor no longer had the old build's page. The
// terminals arrive the same way, the first time a terminal opens.
test.describe("a board left open across an update", () => {
  // serveBuilds answers each load of the board with the build named next in
  // builds, the last one from then on, as the supervisor names it in the page,
  // and the terminals' page only while isServed says the loaded build has it.
  // count is the loads asked for, and loaded the pages that took their place:
  // a reload is asked for before the page it replaces is gone, so a test that
  // reaches into the page after a reload waits for loaded.
  async function serveBuilds(page: Page, builds: string[], isServed: (loads: number) => boolean) {
    const site = process.env.BOARD_TEST_SITE!;
    const index = await readFile(join(site, "index.html"), "utf8");
    const loads = { count: 0, loaded: 0 };
    page.on("domcontentloaded", () => { loads.loaded++; });
    // A board's first open shows the CFO's terminal at once: this one has
    // been opened before, so no terminal opens until he opens one.
    await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
    await page.route(ORIGIN + "/", (route) => {
      const build = builds[Math.min(loads.count, builds.length - 1)];
      loads.count++;
      return route.fulfill({ contentType: "text/html", body: index.replace("<head>", `<head><meta name="cfo-build" content="${build}">`) });
    });
    await page.route("**/assets/TerminalDeck-*", (route) => isServed(loads.count) ? route.fallback() : route.fulfill({ status: 404, body: "" }));
    return loads;
  }
  const loadedBuild = (page: Page) => page.locator('meta[name="cfo-build"]').getAttribute("content");
  const openTerminal = (page: Page) => bar(page).getByRole("button", { name: "Open the CFO's terminal" }).last().click();

  test("opening a terminal reloads it once onto the new build, where the terminals open", async ({ page }) => {
    // Arrange
    const loads = await serveBuilds(page, ["old", "new"], (count) => count > 1);
    await open(page, snapshot({ build: "new" }));

    // Act
    await openTerminal(page);

    // Assert
    await expect.poll(() => loads.loaded).toBe(2);
    await expect.poll(() => push(page, snapshot({ build: "new" }))).toBeGreaterThan(0);
    await expect(page.locator(".board-column").first()).toBeVisible();
    expect(await loadedBuild(page)).toBe("new");
    const served = page.waitForResponse("**/assets/TerminalDeck-*");
    await openTerminal(page);
    expect((await served).status()).toBe(200);
    await expect(page.locator(".update-banner")).toHaveCount(0);
    expect(loads.count).toBe(2);
  });

  test("with text he typed it keeps the board and the text, and Reload is his to press", async ({ page }) => {
    // Arrange
    const loads = await serveBuilds(page, ["old", "new"], (count) => count > 1);
    await open(page, snapshot({ build: "new", questions: [QUESTIONS[0]] }));
    await bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" }).click();
    const dialog = page.locator("dialog.question-modal");
    await dialog.getByRole("radio", { name: /^Other/ }).check();
    const draft = dialog.getByRole("textbox", { name: "Your written answer" });
    await draft.fill("Hold the release until I look");
    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();

    // Act
    const missing = page.waitForResponse("**/assets/TerminalDeck-*");
    await openTerminal(page);
    await missing;

    // Assert
    const banner = page.locator(".update-banner");
    await expect(banner.getByRole("button", { name: "Reload" })).toBeVisible();
    await expect(page.locator(".board-column").first()).toBeVisible();
    await bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" }).click();
    await expect(draft).toHaveValue("Hold the release until I look");
    await page.waitForTimeout(300);
    expect(loads.count).toBe(1);
  });

  test("a page still missing after its one reload leaves the board drawn with Reload, and reloads no more", async ({ page }) => {
    // Arrange
    const loads = await serveBuilds(page, ["new"], () => false);
    await open(page, snapshot({ build: "new" }));

    // Act
    await openTerminal(page);
    await expect.poll(() => loads.loaded).toBe(2);
    await expect.poll(() => push(page, snapshot({ build: "new" }))).toBeGreaterThan(0);
    const missing = page.waitForResponse("**/assets/TerminalDeck-*");
    await openTerminal(page);
    await missing;

    // Assert
    await expect(page.locator(".update-banner").getByRole("button", { name: "Reload" })).toBeVisible();
    await expect(page.locator(".board-column").first()).toBeVisible();
    await page.waitForTimeout(300);
    expect(loads.count).toBe(2);
  });
});
