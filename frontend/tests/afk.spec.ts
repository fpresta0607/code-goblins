import { ORIGIN, expect, servePages, test, type Page, type Route } from "./site";

// AFK mode on the board: the toggle in the CFO panel's header, what the CFO's
// bar holds for the Overlord while it is on, the offer when he is back and the
// report when it turns off. The supervisor is played here: the board's stream
// is held open and each snapshot is pushed to it, and what the board asks is
// collected.
const HOURS = 60 * 60 * 1000;
const BOARD = "his own board (goblins-window.exe pid 4242)";
const task = (id: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "northwind-api", phase: "working", verified: false, generation: id + "-1", ...fields });
const TASKS = [task("nw-invoice-export"), task("nw-checkout-tax"), task("nw-search-index")];
const question = (id: string, text: string, asker = "") => ({ id, text, options: ["Hold it", "Do it"], recommended: "Hold it", status: "pending", task: asker, generation: asker ? asker + "-1" : "", created_at: new Date(Date.now() - HOURS).toISOString() });
const QUESTIONS = [question("q-drop-table", "Migration 0042 drops the legacy_invoices table. Apply it?"), question("q-old-branch", "Delete the old branch fix/tax-rounding-v1?", "nw-checkout-tax")];
const HELD = [
  { item: "question:q-drop-table", task: "", what: "Migration 0042 drops the legacy_invoices table. Apply it?", at: QUESTIONS[0].created_at, waiting: true, now: "still waiting on you", meanwhile: "" },
  { item: "question:q-old-branch", task: "nw-checkout-tax", what: "Delete the old branch fix/tax-rounding-v1?", at: QUESTIONS[1].created_at, waiting: true, now: "still waiting on you", meanwhile: "working: moved on to the refund path" },
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
    { title: "Merged", entries: [{ at: "2026-10-02T04:31:00Z", kind: "merge", what: "https://github.com/northwind/northwind-api/pull/412", link: "https://github.com/northwind/northwind-api/pull/412", evidence: "head 3f9c2ab; 5 checks completed green", outcome: "merged" }] },
    { title: "Merge words with no merge recorded", entries: [{ at: "2026-10-02T10:58:00Z", kind: "merge", what: "https://github.com/northwind/northwind-api/pull/417", link: "https://github.com/northwind/northwind-api/pull/417", evidence: "head 77aa01c; 5 checks completed green", outcome: "" }] },
    { title: "Deployed", entries: [] },
    { title: "Answered for goblins", entries: [{ at: "2026-10-02T04:05:00Z", kind: "answer", what: "notify-nw-invoice-export-12", task: "nw-invoice-export", evidence: "asked: Which CSV dialect? answered: RFC 4180" }] },
    { title: "Other decisions", entries: [{ at: "2026-10-02T05:00:00Z", kind: "other", what: "Restarted the dev server", link: "javascript:alert(1)", evidence: "it had stopped answering" }] },
  ],
  finished: [{ task: "nw-login-rate", pr: "https://github.com/northwind/northwind-api/pull/412", at: "2026-10-02T04:12:00Z" }],
  held: [HELD[0], { ...HELD[1], waiting: false, now: "you answered it: Keep it for now" }],
  spent: ["claude week: 41% used when it turned on, 49% when it turned off (8 points)"],
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
    class HeldStream extends EventTarget {
      onerror: unknown = null;
      constructor() { super(); streams.push(this); }
      close() { streams.splice(streams.indexOf(this), 1); }
    }
    Object.assign(window, { EventSource: HeldStream, pushSnapshot: (value: unknown) => { for (const stream of streams) stream.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(value) })); return streams.length; } });
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
  await expect.poll(() => push(page, first)).toBeGreaterThan(0);
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  return supervisor;
}
const push = (page: Page, value: object) => page.evaluate((next) => (window as unknown as { pushSnapshot: (value: unknown) => number }).pushSnapshot(next), value);

const bar = (page: Page) => page.locator(".cfo-pin");
const header = (page: Page) => page.locator(".panel-header");
const toggle = (page: Page) => page.getByRole("switch", { name: "AFK mode" });
const offer = (page: Page) => page.locator("dialog.afk-dialog").filter({ hasText: "Welcome back" });
const report = (page: Page) => page.locator("dialog.afk-report");
const heldPanel = (page: Page) => bar(page).locator(".afk-held-panel");
// The list of what is held opens only on his click.
async function openHeld(page: Page) {
  await expect(heldPanel(page)).not.toHaveAttribute("open", "");
  await heldPanel(page).locator("summary").click();
  await expect(heldPanel(page)).toHaveAttribute("open", "");
}
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
  await expect(bar(page).locator(".cfo-rest")).toContainText("All quiet. The CFO supervises 3 goblins.");

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
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since .+, from your board\. 0 decided, 0 held for you\.$/);
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

test("a refusal is said in the supervisor's words, in the question and under the header, and the toggle stays as it was", async ({ page }) => {
  const supervisor = await open(page, snapshot(), { asked: [], refuses: true, announces: true });
  await openCfoPanel(page);
  await toggle(page).click();
  const asks = page.locator("dialog.afk-dialog");
  await asks.getByRole("button", { name: "Turn AFK on" }).click();
  await expect(asks.getByRole("alert")).toHaveText(REFUSAL);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await asks.getByRole("button", { name: "Cancel" }).click();
  await expect(header(page).locator(".afk-problem")).toHaveCount(0);

  await push(page, snapshot({ afk: on() }));
  await toggle(page).click();
  await expect(header(page).getByRole("alert")).toHaveText(REFUSAL);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "true");
  expect(supervisor.asked.map((ask) => ask.on)).toEqual([true, false]);
});

test("while AFK is on nothing glows on the bar though things wait on him, it lists what is held once he opens the list, and Answer opens that item in the Command Center", async ({ page }) => {
  await open(page, snapshot({ questions: QUESTIONS }));
  // With AFK off the same items put a glowing Open Command Center on the bar.
  const glowing = bar(page).getByRole("button", { name: /^Open Command Center/ });
  await expect(glowing).toHaveAccessibleName("Open Command Center: 2 waiting on you");

  await push(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
  await expect(glowing).toHaveCount(0);
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since .+\. 4 decided, 2 held for you\.$/);
  await openHeld(page);
  const rows = bar(page).locator(".afk-held li");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("CFO");
  await expect(rows.nth(0)).toContainText("Still waiting on you.");
  await expect(rows.nth(1)).toContainText("nw-checkout-tax");
  await expect(rows.nth(1)).toContainText("Still waiting on you. Meanwhile: working: moved on to the refund path.");
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();

  await rows.nth(1).getByRole("button", { name: /^Answer nw-checkout-tax/ }).click();
  const center = page.locator("dialog.question-modal");
  await expect(center).toBeVisible();
  await expect(center.locator(".question-card")).toContainText("Delete the old branch fix/tax-rounding-v1?");
});

test("the list is there and empty while nothing is held, and stays shut when an item comes while he is away", async ({ page }) => {
  await open(page, snapshot({ afk: on() }));
  const panel = bar(page).locator(".afk-held-panel");
  await expect(panel.locator("summary")).toHaveText("Held for you 0");
  await expect(panel).not.toHaveAttribute("open", "");
  await push(page, snapshot({ questions: [QUESTIONS[0]], afk: on({ held: [HELD[0]] }) }));
  await expect(panel.locator("summary")).toHaveText("Held for you 1");
  await expect(panel).not.toHaveAttribute("open", "");
  await panel.locator("summary").click();
  await expect(panel.locator(".afk-held li")).toHaveCount(1);
});

test("a board that loads, or reconnects, with AFK already on and items already held keeps the list shut until he opens it", async ({ page }) => {
  await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
  await expect(heldPanel(page).locator("summary")).toHaveText("Held for you 2");
  await expect(heldPanel(page)).not.toHaveAttribute("open", "");
  await expect(heldPanel(page).locator(".afk-held li")).toHaveCount(0);
  await page.reload();
  await expect.poll(() => push(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }))).toBeGreaterThan(0);
  await expect(heldPanel(page).locator("summary")).toHaveText("Held for you 2");
  await page.waitForTimeout(300);
  await expect(heldPanel(page)).not.toHaveAttribute("open", "");
  await openHeld(page);
  await expect(heldPanel(page).locator(".afk-held li")).toHaveCount(2);
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
  await expect.poll(() => inFlight.some((route) => (route.request().postDataJSON() as { keys: string[] }).keys.includes("open:question:q-drop-table"))).toBe(true);
  await expect.poll(() => inFlight.some((route) => (route.request().postDataJSON() as { keys: string[] }).keys.includes("alert:question:q-drop-table"))).toBe(true);
  await push(page, snapshot({ questions: [QUESTIONS[0]], afk: on({ held: [HELD[0]] }) }));
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/1 held for you\.$/);
  for (const route of inFlight.splice(0)) await route.fulfill(response === "successful"
    ? { json: { claimed: (route.request().postDataJSON() as { keys: string[] }).keys } }
    : { status: 503, json: { error: "The supervisor is restarting" } });
  await page.waitForTimeout(500);
  expect(await page.evaluate(() => (window as unknown as { notes: string[] }).notes)).toEqual([]);
  await expect(page.locator(".toasts .dialogue")).toHaveCount(0);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
});

test("with AFK on, a new item raises no alert and opens nothing, even when the supervisor cannot be asked what was announced", async ({ page }) => {
  await open(page, snapshot({ afk: on() }), { asked: [], refuses: false, announces: false });
  const asked = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/announce");
  await push(page, snapshot({ questions: [QUESTIONS[1]], afk: on({ held: [HELD[1]] }) }));
  await asked;
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/1 held for you\.$/);
  await page.waitForTimeout(500);
  await expect(page.locator(".toasts .dialogue")).toHaveCount(0);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
  // The same failed ask with AFK off announces from what this browser
  // remembers, as it always has.
  await push(page, snapshot({ questions: QUESTIONS }));
  await expect(page.locator(".toasts .dialogue")).toContainText("Migration 0042 drops the legacy_invoices table. Apply it?");
});

test("his first click after he has been gone still does what he meant and offers to turn AFK off: keys typed blind press nothing, Stay AFK keeps it on, and Turn AFK off shows the report", async ({ page }) => {
  await page.clock.install();
  const supervisor = await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 7, held: HELD }, 8 * HOURS) }));
  // Nothing opens by itself while he is away.
  await expect(offer(page)).toHaveCount(0);
  await page.locator(".task-card").filter({ hasText: "nw-search-index" }).first().click();
  await expect(offer(page)).toContainText("The CFO decided 7 and holds 2 for you.");
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
  await expect(report(page)).toHaveCount(0);
  await push(page, snapshot({ questions: QUESTIONS, afk: KEPT }));
  await expect(offer(page)).toHaveCount(0);
  await expect(report(page)).toContainText("AFK report");
  await expect(report(page)).toContainText("8h25m");
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
  await expect(bar(page).locator(".cfo-rest > p")).toHaveText(/^AFK since .+, turned on by the CFO at your ask\. 1 decided, 0 held for you\.$/);
  await page.locator(".board-column").first().click({ position: { x: 8, y: 8 } });
  await expect(offer(page)).toContainText("turned on by the CFO at your ask: “I'm stepping away, turn AFK on”. The CFO decided 1 and holds 0 for you.");
  await offer(page).getByRole("button", { name: "Stay AFK" }).click();

  // He turns it off himself, from the board, and the report says who made
  // each switch.
  await page.route("**/api/afk/report", (route) => route.fulfill({ json: { ...REPORT, ...byTheCFO, ended_from: BOARD, ended_asked: "" } }));
  await push(page, snapshot({ afk: KEPT }));
  await expect(report(page).locator(".afk-report-title")).toContainText("Turned on by the CFO at your ask: “I'm stepping away, turn AFK on”, off from your board.");
});

test("the report lists what is held first, then how much of each thing the CFO did, each decision with its link and what it stood on, and what was spent, and opens again from the header", async ({ page }) => {
  await open(page, snapshot({ afk: KEPT }));
  // A page that opens after AFK mode turned off shows no report by itself.
  await expect(report(page)).toHaveCount(0);
  await openCfoPanel(page);
  await header(page).getByRole("button", { name: "Open the last AFK report" }).click();
  await expect(report(page).locator(".afk-report-title")).toContainText("Turned on from your board, off from your terminal.");
  await expect(report(page).locator(".afk-tally li")).toHaveText(["Held for you 2", "Merged 1", "Merge words with no merge recorded 1", "Deployed 0", "Answered for goblins 1", "Other decisions 1", "Goblins finished 1"]);
  // Coming back he reads what is held before what was decided.
  expect(await report(page).locator("section").evaluateAll((sections) => sections.map((section) => section.getAttribute("aria-label")))).toEqual(["Held for you", "Merged", "Merge words with no merge recorded", "Answered for goblins", "Other decisions", "Goblins finished", "Spent"]);
  // A section with nothing in it is counted above and not listed.
  await expect(report(page).getByRole("region", { name: "Deployed" })).toHaveCount(0);

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

  const held = report(page).getByRole("region", { name: "Held for you" }).locator("li");
  await expect(held).toHaveCount(2);
  await expect(held.nth(1)).toContainText("You answered it: Keep it for now.");
  await expect(report(page).getByRole("region", { name: "Spent" })).toContainText("claude week: 41% used when it turned on, 49% when it turned off (8 points)");

  await report(page).getByRole("button", { name: "Back to the board" }).click();
  await expect(report(page)).toHaveCount(0);
  await header(page).getByRole("button", { name: "Open the last AFK report" }).click();
  await page.keyboard.press("Escape");
  await expect(report(page)).toHaveCount(0);
});

test("a switch that cannot be read is shown off, says how to reset it, and still lets what waits on him lead", async ({ page }) => {
  const supervisor = await open(page, snapshot({ questions: [QUESTIONS[0]], afk: { state: "unreadable" } }));
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  await openCfoPanel(page);
  await expect(toggle(page)).toHaveAttribute("aria-checked", "false");
  await expect(header(page).getByRole("status")).toHaveText("AFK's switch cannot be read, so nothing is decided for you. Press the toggle to reset it.");
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

test("on a phone the bar keeps its line, its terminal and what is held inside it, and the toggle stays in the CFO panel's header, with nothing wider than the page", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
  await openHeld(page);
  await expect(bar(page).locator(".afk-held li")).toHaveCount(2);
  expect(await within(page, ".cfo-rest")).toBe(true);
  expect(await smallest(page)).toBeGreaterThanOrEqual(15);
  await openCfoPanel(page);
  await expect(toggle(page)).toBeVisible();
  expect(await within(page, ".panel-header")).toBe(true);
  expect(await toggle(page).evaluate((element) => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(15);
  expect(await fits(page)).toBe(true);
});

test("in the desktop window the toggle sits on the header's first row, and on a panel at its narrowest it is still there and inside the header", async ({ browser }) => {
  // His window: maximized on a 2560 by 1600 screen at 150 percent.
  const context = await browser.newContext({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5, baseURL: ORIGIN });
  await servePages(context);
  const page = await context.newPage();
  await open(page, snapshot({ questions: QUESTIONS, afk: on({ decided: 4, held: HELD }) }));
  await openHeld(page);
  await expect(bar(page).locator(".afk-held li")).toHaveCount(2);
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
  await context.close();

  // The panel dragged to its narrowest, 360 pixels: the header wraps rather
  // than hide or cover the toggle.
  const narrow = await browser.newContext({ viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5, baseURL: ORIGIN });
  await servePages(narrow);
  const small = await narrow.newPage();
  await small.addInitScript(() => localStorage.setItem("cfo-pane-width", "360"));
  await open(small, snapshot({ afk: on() }));
  await openCfoPanel(small);
  for (const view of ["Task", "Terminal"]) {
    await small.locator(".panel-pill").getByRole("button", { name: view, exact: true }).click();
    if (view === "Terminal") await small.getByRole("button", { name: "Restore the panel", exact: true }).click();
    expect(Math.round((await small.locator(".context-pane").boundingBox())!.width), view + " view").toBe(360);
    await expect(toggle(small)).toBeVisible();
    expect(await within(small, ".panel-header"), view + " view").toBe(true);
    const overlap = await header(small).evaluate((element) => {
      const status = element.querySelector(".panel-status")!.getBoundingClientRect(), switched = element.querySelector(".afk-toggle")!.getBoundingClientRect();
      return status.right > switched.left && status.left < switched.right && status.bottom > switched.top && status.top < switched.bottom;
    });
    expect(overlap, view + " view: the status and the toggle overlap").toBe(false);
  }
  await narrow.close();
});
