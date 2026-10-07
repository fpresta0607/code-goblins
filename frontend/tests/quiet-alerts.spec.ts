import { readFileSync } from "node:fs";
import { expect, test, type BrowserContext, type Page } from "./site";

// The Overlord, 2026-10-07, with a toast about a goblin the CFO was retiring
// ("... failed: Waiting on the CFO: Requested by the operator; worktree
// C:\dev\PrecisionDocs-AI\.worktrees...") whose Open opened its task panel:
// "these command center alerts where it takes to your task panel is
// pointless", then "alerts should only be open command center questions".
// So the board, and the desktop window around it, alert only for a new
// Command Center item that asks him something, in its asker's words on one
// plain line, and the alert opens that item. A goblin blocked, failed or done,
// a pause the CFO asked for that did not finish, and the CFO falling behind
// on its questions are said on the cards and the CFO's bar, never as an alert.
test.use({ timezoneId: "UTC" });

const RETIRED = "Unused admin route removed; macro-security test can fail (issues 1432, 1273)";
const LIFECYCLE_SAID = "Requested by the operator; worktree C:\\dev\\PrecisionDocs-AI\\.worktrees\\gb-pd-small-cleanups; task session and branch";
const ASKS = "May I merge the release train now?";
const question = (id: string, text: string, created_at: string) => ({ id, identity: "c".repeat(64), task: "", status: "pending", created_at, text: text + "\n\n- Its checks are **green**.", options: ["Merge it", "Hold it"], recommended: "Merge it" });
const RELEASE = question("release-train-20261007", ASKS, "2026-10-07T15:10:00Z");
const task = (id: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, project: "code-goblins", phase: "working", generation: id + "-1", verified: false, ...fields });
const working = [task("pd-small-cleanups", RETIRED, { project: "PrecisionDocs-AI", pr: "https://github.com/fpresta0607/PrecisionDocs-AI/pull/1507" }), task("cg-probe", "Probe the DNS"), task("cg-theme", "Board theme")];
let revision = 0;
const board = (fields: Record<string, unknown> = {}) => ({ healthy: true, instance: "board-1", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision: ++revision, attention: [], tasks: working, questions: [], reviews: [], runs: [], credentials: [], actions: [], ...fields });
// What reached the Overlord on 2026-10-07, and its kin: one goblin done, one
// failed by its own report, one blocked at its gate, and the retired goblin's
// pause that did not finish, which an older supervisor served as the goblin
// failed and waiting on the CFO; then that unanswered report counted as a
// question left on the CFO for ten minutes.
const news = () => board({ tasks: [
  task("pd-small-cleanups", RETIRED, { project: "PrecisionDocs-AI", phase: "failed", report: "", pr: "https://github.com/fpresta0607/PrecisionDocs-AI/pull/1507", reason: "Waiting on the CFO: " + LIFECYCLE_SAID, activity: LIFECYCLE_SAID,
    lifecycle: { phase: "failed", action: "pause", at: "2026-10-07T14:49:11Z", kept: ["worktree C:\\dev\\PrecisionDocs-AI\\.worktrees\\gb-pd-small-cleanups"], stopped: [], problems: ["Stopping-point deadline reached or request failed; no new handoff was saved", "context deadline exceeded"], handoff_saved: false, validation_restarts: false } }),
  task("cg-probe", "Probe the DNS", { phase: "failed", report: "failed", activity: "failed: go test timed out at 9f3c2a1e" }),
  task("cg-theme", "Board theme", { phase: "blocked", reason: "Pipeline decision required at review; use cfo pipeline respond" }),
], cfo_quiet: { since: "2026-10-07T14:59:11Z", count: 1, oldest_age: 600 } });

// Every board the test opens gets its snapshots from the test, through a
// stand-in event stream: nothing arrives that the test did not send.
async function standInStream(context: BrowserContext) {
  await context.addInitScript(() => {
    class FixtureSource extends EventTarget {
      private publish = (event: Event) => {
        if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) }));
      };
      constructor() {
        super();
        window.addEventListener("fixture-stream", this.publish);
      }
      close() { window.removeEventListener("fixture-stream", this.publish); }
    }
    Object.defineProperty(window, "EventSource", { value: FixtureSource });
  });
  await context.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
}
const send = (page: Page, data: object) => page.evaluate((detail) => { window.dispatchEvent(new CustomEvent("fixture-stream", { detail })); }, data);

// The supervisor's record of what the board announced: each key goes to the
// first that asks for it, and taken holds what another viewer, such as the
// desktop window's own look at the board, claimed first.
async function announcer(context: BrowserContext, taken: string[] = []) {
  const announced = new Set(taken);
  const asked: string[] = [];
  await context.route("**/api/announce", async (route) => {
    const keys: string[] = (route.request().postDataJSON() as { keys: string[] }).keys;
    asked.push(...keys);
    const claimed = keys.filter((key) => !announced.has(key));
    for (const key of claimed) announced.add(key);
    await route.fulfill({ json: { claimed } });
  });
  return asked;
}

// A board out of sight: its tab hidden or its window minimized.
const OUT_OF_SIGHT = () => Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
// A board out of sight waits a second and a half before it asks the
// supervisor what to announce, so whatever it would announce has been asked
// for and shown by this long after its snapshot.
const ANNOUNCED_BY_MS = 2500;

const bar = (page: Page) => page.locator(".cfo-pin");
const card = (page: Page) => page.locator("dialog.question-modal");
const goblin = (page: Page, title: string) => page.locator(".task-card").filter({ hasText: title }).first();

async function open(page: Page) {
  await page.goto("/");
  await send(page, board());
  await expect(bar(page)).toContainText("All quiet");
  await page.evaluate(() => document.fonts.ready);
}

test.describe("on the board, in a browser tab", () => {
  test.use({ viewport: { width: 1440, height: 1000 } });

  // A browser that allows notifications records each one the board asks it
  // to show.
  const RECORD_NOTIFICATIONS = () => {
    class Note {
      static permission = "granted";
      static requestPermission = async () => "granted";
      closed = false;
      onclick: (() => void) | null = null;
      onclose: (() => void) | null = null;
      constructor(public title: string, public options: { body: string; tag: string }) { (window as unknown as { notes: Note[] }).notes.push(this); }
      close() { this.closed = true; this.onclose?.(); }
    }
    Object.assign(window, { notes: [], Notification: Note });
  };
  const notes = (page: Page) => page.evaluate(() => (window as unknown as { notes: { title: string; options: { body: string; tag: string }; closed: boolean }[] }).notes.map((note) => ({ title: note.title, body: note.options.body, tag: note.options.tag, closed: note.closed })));

  test("a goblin done, failed or blocked, a retired goblin's pause that did not finish, and the CFO falling behind raise no alert; their cards and the CFO's bar say them", async ({ page, context }, testInfo) => {
    // Arrange
    await context.addInitScript(RECORD_NOTIFICATIONS);
    await context.addInitScript(OUT_OF_SIGHT);
    await standInStream(context);
    const asked = await announcer(context);
    await open(page);

    // Act
    await send(page, news());
    await expect(goblin(page, "Board theme").locator(".plain-status")).toHaveClass(/phase-blocked/);
    await page.waitForTimeout(ANNOUNCED_BY_MS);
    await testInfo.attach("goblins' news on the board", { body: await page.screenshot(), contentType: "image/png" });

    // Assert
    await expect(bar(page)).toContainText("The CFO has not answered 1 question; the oldest has waited 10 minutes.");
    expect(await notes(page)).toEqual([]);
    expect(asked).toEqual([]);
    await expect(page.locator(".toasts")).toHaveCount(0);
    await expect(card(page)).toBeHidden();
    await expect(goblin(page, RETIRED).locator(".card-status-text")).toHaveText("Pause did not finish");
    await expect(goblin(page, RETIRED).locator(".plain-status")).not.toHaveClass(/phase-failed/);
    await expect(goblin(page, "Probe the DNS").locator(".card-status-text")).toHaveText("Failed");
  });

  test("a new Command Center question raises one Windows notification in its asker's words, and its click opens it there", async ({ page, context }, testInfo) => {
    // Arrange
    await context.addInitScript(RECORD_NOTIFICATIONS);
    await context.addInitScript(OUT_OF_SIGHT);
    await standInStream(context);
    const asked = await announcer(context);
    await open(page);
    await send(page, news());

    // Act
    await send(page, { ...news(), questions: [RELEASE] });

    // Assert
    const alert = "question:" + RELEASE.id + "@" + RELEASE.created_at;
    await expect.poll(() => notes(page)).toEqual([{ title: "CFO", body: "The CFO asks: " + ASKS, tag: alert, closed: false }]);
    expect(asked).toEqual(["alert:" + alert]);
    await expect(card(page)).toBeHidden();
    await page.evaluate(() => (window as unknown as { notes: { onclick: () => void }[] }).notes[0].onclick());
    await expect(card(page)).toContainText(ASKS);
    await testInfo.attach("the notification opened its question", { body: await page.screenshot(), contentType: "image/png" });
    expect(await notes(page)).toEqual([{ title: "CFO", body: "The CFO asks: " + ASKS, tag: alert, closed: true }]);
  });
});

// The desktop window hands the board's notifications to Windows: its script
// (notesScript in cmd/goblins-window/notes.go) runs in the board's page after
// each load and posts each notification the page raises to the window over
// WebView2's bridge, and the window tells the page when one is clicked. The
// window also raises a notification from its own look at the board, which the
// page holds nothing for. The window's script runs here as the window runs it,
// read from its source, over a stand-in for the bridge.
test.describe("in the desktop window, at its size", () => {
  test.use({ viewport: { width: 1707, height: 1000 }, deviceScaleFactor: 1.5 });

  const source = readFileSync(new URL("../../cmd/goblins-window/notes.go", import.meta.url), "utf8");
  const message = /const noteMessage = "([^"]+)"/.exec(source)![1];
  const start = source.indexOf("const notesScript = `") + "const notesScript = `".length;
  const notesScript = source.slice(start, source.indexOf("})();`", start) + "})();".length).replaceAll("` + noteMessage + `", message);
  // The bridge records what the page posts, and the window runs its script
  // once each load completes, past the page's own rules as WebView2 runs it.
  const WINDOW = `const posted = []; Object.assign(window, { posted, chrome: { webview: { postMessage: (text) => posted.push(text) } } });
window.addEventListener("DOMContentLoaded", () => { ${notesScript} });`;
  const posted = (page: Page) => page.evaluate(() => (window as unknown as { posted: string[] }).posted.map((text) => text.startsWith("notify:") ? JSON.parse(text.slice("notify:".length)) as unknown : text));
  // What the window does when the Overlord clicks a notification of id.
  const clicked = (page: Page, id: string) => page.evaluate((note) => (window as unknown as { codeGoblinsNoteClicked: (id: string) => void }).codeGoblinsNoteClicked(note), id);

  test("only a new Command Center item reaches Windows, and a click on it opens that item, whichever of the page and the window raised it", async ({ page, context }, testInfo) => {
    // Arrange: the window raised the second question from its own look and
    // claimed it first, so the page raises nothing for it.
    const second = question("deploy-site-20261007", "May I deploy the website now?", "2026-10-07T15:12:00Z");
    const fromWindow = "question:" + second.id + "@" + second.created_at;
    await context.addInitScript({ content: WINDOW });
    await context.addInitScript(OUT_OF_SIGHT);
    await standInStream(context);
    await announcer(context, ["alert:" + fromWindow]);
    await open(page);

    // Act: the goblins' news, then the CFO's two questions.
    await send(page, news());
    await expect(goblin(page, "Board theme").locator(".plain-status")).toHaveClass(/phase-blocked/);
    await page.waitForTimeout(ANNOUNCED_BY_MS);
    const quiet = await posted(page);
    await testInfo.attach("goblins' news in the window's size", { body: await page.screenshot(), contentType: "image/png" });
    await send(page, { ...news(), questions: [RELEASE, second] });

    // Assert: the goblins' news posted nothing to Windows, and the question
    // the page was handed reached it in its asker's words.
    expect(quiet).toEqual([]);
    const fromPage = "question:" + RELEASE.id + "@" + RELEASE.created_at;
    await expect.poll(() => posted(page)).toEqual([{ id: fromPage, title: "CFO", body: "The CFO asks: " + ASKS }]);
    await testInfo.attach("one item posted to Windows, in the window's size", { body: await page.screenshot(), contentType: "image/png" });
    await clicked(page, fromPage);
    await expect(card(page)).toContainText(ASKS);
    await page.keyboard.press("Escape");
    await expect(card(page)).toBeHidden();
    await clicked(page, fromWindow);
    await expect(card(page)).toContainText("May I deploy the website now?");
  });
});
