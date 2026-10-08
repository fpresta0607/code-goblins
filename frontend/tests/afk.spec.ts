import { expect, test, type Page, type Route } from "./site";

// AFK mode on the board: the toggle in the CFO panel's header, what the CFO's
// bar says while it is on, the offer when he is back and the report when it
// turns off. The supervisor is played here: the board's stream
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
const KEPT = { state: "off", report: "afk-20261002T034000.000Z" };
const REPORT = {
  found: true, session: KEPT.report, since: "2026-10-02T03:40:00Z", ended: "2026-10-02T12:05:00Z", lasted: "8h25m", from: BOARD, ended_from: "his own terminal (powershell.exe pid 5151)",
  sections: [
    { title: "Left for you", entries: [{ at: "2026-10-02T06:12:00Z", kind: "left", what: "Sign in to Vercel for nw-search-index", evidence: "his own sign-in, backlog row nw-search-vercel-sign-in, nw-search-index moved on to the ranking work", outcome: "" }] },
    { title: "Merged", entries: [{ at: "2026-10-02T04:31:00Z", kind: "merge", what: "https://github.com/northwind/northwind-api/pull/412", link: "https://github.com/northwind/northwind-api/pull/412", evidence: "head 3f9c2ab; 5 checks completed green", outcome: "merged" }] },
    { title: "Merge words with no merge recorded", entries: [{ at: "2026-10-02T10:58:00Z", kind: "merge", what: "https://github.com/northwind/northwind-api/pull/417", link: "https://github.com/northwind/northwind-api/pull/417", evidence: "head 77aa01c; 5 checks completed green", outcome: "" }] },
    { title: "Deployed", entries: [] },
    { title: "Answered for goblins", entries: [{ at: "2026-10-02T04:05:00Z", kind: "answer", what: "notify-nw-invoice-export-12", task: "nw-invoice-export", evidence: "asked: Which CSV dialect? answered: RFC 4180" }] },
    { title: "Other decisions", entries: [{ at: "2026-10-02T05:00:00Z", kind: "other", what: "Restarted the dev server", link: "javascript:alert(1)", evidence: "it had stopped answering" }] },
    { title: "Paused at a floor", entries: [{ at: "2026-10-02T06:40:00Z", kind: "pause", what: "at the memory floor", task: "nw-search-index", evidence: "3.1 GB of memory and 8.0 GB of commit free on two readings in a row, under the 4 GB floor; nw-search-index was the newest goblin not pushing or merging", outcome: "paused" }] },
  ],
  finished: [{ task: "nw-login-rate", pr: "https://github.com/northwind/northwind-api/pull/412", at: "2026-10-02T04:12:00Z" }],
  held: [HELD[0], { ...HELD[1], waiting: false, now: "you answered it: Keep it for now" }],
  spent: [
    { provider: "claude", window: "week", on: 41, off: 49 },
    { provider: "claude", window: "session", on: 42, off: 3, reset: true },
    { provider: "codex", window: "week", off: 4 },
    { provider: "codex", window: "credits", credits: true, spent: 12.5, unit: "credits" },
  ],
  notes: [],
};
// The supervisor's own words for a board that an agent's program shows.
const REFUSAL = "AFK mode is the Supreme Overlord's switch, and the program that shows this board runs under an agent harness (node.exe pid 5120): he turns it on or off from a terminal or a board of his own, and the registered CFO only at his ask, with his words";

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
    } else if (pathname === "/api/afk/report") await route.fulfill({ json: REPORT });
    else if (pathname === "/api/announce" && supervisor.announces) await route.fulfill({ json: { claimed: [] } });
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
const report = (page: Page) => page.locator("dialog.afk-report");
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
// of his own press is an error he can read: what did not happen, then the
// supervisor's words in full, in the question and under the header, and it
// stays until he closes it.
test("a refusal is shown in full as an error, in the question and under the header, until he closes it, and the toggle stays as it was", async ({ page }) => {
  await page.clock.install();
  const supervisor = await open(page, snapshot(), { asked: [], refuses: true, announces: true });
  await openCfoPanel(page);
  await toggle(page).click();
  const asks = page.locator("dialog.afk-dialog");
  await asks.getByRole("button", { name: "Turn AFK on" }).click();
  await expect(asks.getByRole("alert")).toHaveText("AFK did not turn on" + REFUSAL + ".");
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await asks.getByRole("button", { name: "Cancel" }).click();
  await expect(header(page).getByRole("alert")).toHaveCount(0);

  await push(page, snapshot({ afk: on() }));
  await toggle(page).click();
  const refusal = header(page).getByRole("alert");
  await expect(refusal).toHaveText("AFK did not turn off" + REFUSAL + ".");
  await expect(toggle(page)).toHaveAttribute("aria-checked", "true");
  await page.clock.fastForward(60_000);
  await expect(refusal).toBeVisible();
  await refusal.getByRole("button", { name: "Close the message" }).click();
  await expect(header(page).getByRole("alert")).toHaveCount(0);
  expect(supervisor.asked.map((ask) => ask.on)).toEqual([true, false]);
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
  await offer(page).getByRole("button", { name: "Turn AFK off" }).click();
  const working = offer(page).getByRole("button", { name: "Turning AFK off…" });
  await expect(working).toBeDisabled();
  await expect(working).toHaveAttribute("aria-busy", "true");
  await expect(working.locator(".card-start-spinner")).toBeVisible();
  answer();
  await expect(offer(page)).toHaveCount(0);
  await expect(report(page)).toContainText("AFK report");
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

test("his first click after he has been gone still does what he meant and offers to turn AFK off: keys typed blind press nothing, Stay AFK keeps it on, and Turn AFK off shows the report", async ({ page }) => {
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
  // off asks the supervisor, whose snapshot then shows the report.
  await page.clock.fastForward(6 * 60 * 1000);
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await offer(page).getByRole("button", { name: "Turn AFK off" }).click();
  await expect.poll(() => supervisor.asked).toEqual([{ on: false, token: "fixture" }]);
  // The supervisor keeps the report before it answers, so the report shows
  // as soon as it answers, whenever the board's next snapshot comes.
  await expect(offer(page)).toHaveCount(0);
  await expect(report(page)).toContainText("AFK report");
  await expect(report(page)).toContainText("8h25m");
  await push(page, snapshot({ questions: QUESTIONS, afk: KEPT }));
  await expect(report(page)).toHaveCount(1);
});

test("turning AFK off from the toggle shows the report as soon as the supervisor answers, and the snapshot after it brings no second one", async ({ page }) => {
  const supervisor = await open(page, snapshot({ afk: on() }));
  await openCfoPanel(page);
  await toggle(page).click();
  await expect.poll(() => supervisor.asked).toEqual([{ on: false, token: "fixture" }]);
  await expect(report(page)).toContainText("8h25m");
  await page.keyboard.press("Escape");
  await expect(report(page)).toHaveCount(0);
  await push(page, snapshot({ afk: KEPT }));
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await page.waitForTimeout(300);
  await expect(report(page)).toHaveCount(0);
  // A stretch that ends elsewhere, from his terminal, still shows its report
  // when the snapshot says so.
  await push(page, snapshot({ afk: on() }));
  await push(page, snapshot({ afk: { ...KEPT, report: "afk-20261002T130000.000Z" } }));
  await expect(report(page)).toContainText("AFK report");
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

test("a stretch the CFO turned on at his ask says so on the bar, and the offer and the report quote his words", async ({ page }) => {
  await page.clock.install();
  const byTheCFO = { from: "the CFO at his ask (claude pid 4242)", asked: "I'm stepping away, turn AFK on" };
  await open(page, snapshot({ afk: on({ ...byTheCFO, decided: 1 }, 8 * HOURS) }));
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since .+, turned on by the CFO at your ask\. 1 decided\.$/);
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await expect(offer(page)).toContainText("turned on by the CFO at your ask: “I'm stepping away, turn AFK on”. The CFO decided 1.");
  await offer(page).getByRole("button", { name: "Stay AFK" }).click();

  // He turns it off himself, from the board, and the report says who made
  // each switch.
  await page.route("**/api/afk/report", (route) => route.fulfill({ json: { ...REPORT, ...byTheCFO, ended_from: BOARD, ended_asked: "" } }));
  await push(page, snapshot({ afk: KEPT }));
  await expect(report(page).locator(".afk-report-title")).toContainText("Turned on by the CFO at your ask: “I'm stepping away, turn AFK on”, off from your board.");
});

test("his first click or key after the CFO turned AFK on at his ask offers the switch back at once with his words, and the usual wait follows his answer", async ({ page }) => {
  await page.clock.install();
  const byTheCFO = { from: "the CFO at his ask (claude pid 4242)", asked: "I'm heading to bed, turn AFK on" };
  const supervisor = await open(page, snapshot({ afk: on(byTheCFO) }));
  const switched = page.locator("dialog.afk-dialog").filter({ hasText: "The CFO turned AFK on" });
  // Nothing opens by itself after the switch.
  await expect(switched).toHaveCount(0);

  await page.locator(".task-card").filter({ hasText: "nw-search-index" }).first().click();
  await expect(switched).toContainText("turned on by the CFO at your ask: “I'm heading to bed, turn AFK on”.");
  await expect(offer(page)).toHaveCount(0);
  await expect(switched).toBeFocused();
  await switched.getByRole("button", { name: "Stay AFK" }).click();
  await expect(page.locator("dialog.afk-dialog")).toHaveCount(0);
  // The click that brought the offer opened the task he clicked.
  await expect(page.locator("#panel-title")).toHaveText("nw-search-index");

  // He has answered it, so his next click and key offer nothing.
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await page.keyboard.press("Shift");
  await expect(page.locator("dialog.afk-dialog")).toHaveCount(0);
  expect(supervisor.asked).toEqual([]);
});

test("the report lists what still waits on him first, then how much of each thing the CFO did, each decision with its link and what it stood on in a drawer of its own, and what was spent, and opens again from the header", async ({ page }) => {
  await open(page, snapshot({ questions: [QUESTIONS[0]], afk: KEPT }));
  // A page that opens after AFK mode turned off shows no report by itself.
  await expect(report(page)).toHaveCount(0);
  await openCfoPanel(page);
  await header(page).getByRole("button", { name: "Open the last AFK report" }).click();
  await expect(report(page).locator(".afk-report-title")).toContainText("Turned on from your board, off from your terminal.");
  // Held for you counts only what still waits on him in the Command Center.
  await expect(report(page).locator(".afk-tally li")).toHaveText(["Held for you 1", "Left for you 1", "Merged 1", "Merge words with no merge recorded 1", "Deployed 0", "Answered for goblins 1", "Other decisions 1", "Paused at a floor 1", "Goblins finished 1"]);
  // Coming back he reads what is held before what was decided.
  expect(await report(page).locator("section").evaluateAll((sections) => sections.map((section) => section.getAttribute("aria-label")))).toEqual(["Held for you", "Left for you", "Merged", "Merge words with no merge recorded", "Answered for goblins", "Other decisions", "Paused at a floor", "Goblins finished", "Spent"]);
  // A section with nothing in it is counted above and not listed.
  await expect(report(page).getByRole("region", { name: "Deployed" })).toHaveCount(0);

  // The Overlord, 2026-10-08: "really long merged and goblins completed rows
  // should be collapsed in panel like drawer". What still waits on him is
  // open, and every other section is a drawer, closed until he opens it.
  for (const name of ["Merged", "Answered for goblins", "Other decisions", "Paused at a floor", "Goblins finished"]) {
    const section = report(page).getByRole("region", { name, exact: true });
    await expect(section.locator("details.afk-drawer")).not.toHaveAttribute("open");
    await expect(section.locator("li")).toHaveCount(0);
    await section.locator("summary").click();
    await expect(section.locator("li")).toHaveCount(1);
  }
  for (const name of ["Left for you", "Merge words with no merge recorded"]) {
    await expect(report(page).getByRole("region", { name, exact: true }).locator("details")).toHaveCount(0);
  }
  await expect(report(page).getByRole("region", { name: "Goblins finished" })).toContainText("nw-login-rate");

  // What only he can do was left for him in the backlog and worked around, and
  // it still waits on him.
  const left = report(page).getByRole("region", { name: "Left for you" }).locator("li");
  await expect(left.locator("strong")).toHaveText("Sign in to Vercel for nw-search-index");
  await expect(left).toContainText("Evidence: his own sign-in, backlog row nw-search-vercel-sign-in");
  await expect(left.locator(".delivery")).toHaveClass(/uncertain/);
  const merged = report(page).getByRole("region", { name: "Merged", exact: true }).locator("li");
  await expect(merged).toContainText("northwind-api #412: merged");
  await expect(merged).toContainText("Evidence: head 3f9c2ab; 5 checks completed green");
  await expect(merged.getByRole("link", { name: "northwind-api #412" })).toHaveAttribute("href", "https://github.com/northwind/northwind-api/pull/412");
  await expect(merged.getByRole("link")).toHaveAttribute("target", "_blank");
  await expect(report(page).getByRole("region", { name: "Merge words with no merge recorded" })).toContainText("northwind-api #417: no outcome was recorded");
  const answered = report(page).getByRole("region", { name: "Answered for goblins" }).locator("li");
  await expect(answered.locator("strong")).toHaveText("nw-invoice-export");
  await expect(answered).toContainText("Asked: Which CSV dialect? Answered: RFC 4180");
  // A link that is no web link is words, never something to open.
  const other = report(page).getByRole("region", { name: "Other decisions" });
  await expect(other).toContainText("Restarted the dev server");
  await expect(other.getByRole("link")).toHaveCount(0);

  // A goblin the supervisor paused at the memory floor wears the pause mark,
  // with the readings the pause stood on.
  const paused = report(page).getByRole("region", { name: "Paused at a floor" }).locator("li");
  await expect(paused.locator("strong")).toHaveText("nw-search-index: at the memory floor: paused");
  await expect(paused).toContainText("Evidence: 3.1 GB of memory and 8.0 GB of commit free on two readings in a row");
  await expect(paused.locator(".delivery path")).toHaveAttribute("d", "M8 5v14M16 5v14");

  // The Overlord, 2026-10-08: "held for you is truly only when command center
  // needs me". What he answered since is not held for him, and nothing there
  // wears a check.
  const held = report(page).getByRole("region", { name: "Held for you" }).locator("li");
  await expect(held).toHaveCount(1);
  await expect(held.locator(".afk-recommends")).toHaveText("The CFO recommends: Hold it.");
  await expect(report(page).getByRole("region", { name: "Held for you" })).not.toContainText("You answered it");
  await expect(held.locator(".delivery")).toHaveClass(/uncertain/);
  // What was spent is a row for each allowance used: its mark, its name, a bar
  // with what was used before AFK in gray and what AFK used in green under a
  // green arrow from where it stood to where it ended, the percents, and how
  // much AFK used. A window that reset started from nothing, and a reading not
  // taken shows only what was read, with no line saying it was not.
  const spentSection = report(page).getByRole("region", { name: "Spent" });
  await expect(spentSection.locator("h3")).toContainText("Before AFK");
  await expect(spentSection.locator("h3")).toContainText("Used while AFK was on");
  const spent = spentSection.locator("li");
  await expect(spent).toHaveText(["Claude week41% → 49%+8%", "Claude session42% → 3%reset", "Codex week4%", "Codex credits12.5 spent"]);
  await expect(spent.nth(0).getByRole("img", { name: "Claude week 41% used at AFK on and 49% at AFK off" })).toBeVisible();
  await expect(spent.nth(0).locator(".afk-spent-before")).toHaveAttribute("style", "width: 41%;");
  await expect(spent.nth(0).locator(".afk-spent-used")).toHaveAttribute("style", "left: 41%; width: 8%;");
  await expect(spent.nth(0).locator(".afk-spent-arrow")).toHaveAttribute("style", "left: 41%; width: 8%;");
  await expect(spent.nth(0).locator(".afk-spent-change.used")).toHaveText("+8%");
  await expect(spent.nth(1).getByRole("img")).toHaveAccessibleName("Claude session 42% used at AFK on and 3% at AFK off after it reset");
  await expect(spent.nth(1).locator(".afk-spent-arrow")).toHaveAttribute("style", "left: 0%; width: 3%;");
  await expect(spent.nth(1).locator(".afk-spent-change")).not.toHaveClass(/used/);
  await expect(spent.nth(2).locator(".afk-spent-before")).toHaveAttribute("style", "width: 4%;");
  await expect(spent.nth(2).locator(".afk-spent-arrow, .afk-spent-used, .afk-spent-change")).toHaveCount(0);
  await expect(spent.nth(3).getByRole("img")).toHaveCount(0);
  expect(await spentSection.evaluate((section) => section.textContent)).not.toMatch(/not read/);

  // Something it held still waits on him, so its one button at the bottom is
  // the Command Center, which it opens.
  const actions = report(page).locator(".afk-report-actions button");
  await expect(actions).toHaveText(["Open Command Center"]);
  await expect(actions).toHaveClass(/primary/);
  await actions.click();
  await expect(report(page)).toHaveCount(0);
  await expect(page.locator("dialog.question-modal")).toBeVisible();
});

test("with nothing it held still waiting on him, the report's one button at the bottom is Back to the board, and with nothing used it has no Spent", async ({ page }) => {
  await open(page, snapshot({ afk: KEPT }));
  await page.route("**/api/afk/report", (route) => route.fulfill({ json: { ...REPORT, held: [REPORT.held[1]], spent: [] } }));
  await openCfoPanel(page);
  await header(page).getByRole("button", { name: "Open the last AFK report" }).click();
  // What he answered since needs nothing more of him, so nothing is held.
  await expect(report(page).locator(".afk-tally li").first()).toHaveText("Held for you 0");
  await expect(report(page).getByRole("region", { name: "Held for you" })).toHaveCount(0);
  await expect(report(page).getByRole("region", { name: "Spent" })).toHaveCount(0);
  const actions = report(page).locator(".afk-report-actions button");
  await expect(actions).toHaveText(["Back to the board"]);
  await expect(actions).toHaveClass(/primary/);
  await actions.click();
  await expect(report(page)).toHaveCount(0);
  await header(page).getByRole("button", { name: "Open the last AFK report" }).click();
  await page.keyboard.press("Escape");
  await expect(report(page)).toHaveCount(0);

  // With nothing that waited on him there is no Held for you to read, as for
  // any other heading with nothing under it.
  await page.route("**/api/afk/report", (route) => route.fulfill({ json: { ...REPORT, held: [], spent: [] } }));
  await header(page).getByRole("button", { name: "Open the last AFK report" }).click();
  await expect(report(page).locator(".afk-tally li").first()).toHaveText("Held for you 0");
  await expect(report(page).getByRole("region", { name: "Left for you" })).toBeVisible();
  await expect(report(page).getByRole("region", { name: "Held for you" })).toHaveCount(0);
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
