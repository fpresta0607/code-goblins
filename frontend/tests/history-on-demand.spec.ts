import { expect, test, type Page } from "./site";

// #1611: every board's snapshot carried each item the Command Center ever
// closed, 362 KB of a 501 KB first snapshot on the live fleet, though History
// lists only its newest 20, and only once it opens. The snapshot now carries
// the items closed in the last hour and how many History lists, and History
// reads the rest when it opens, and again when an item closes while it is
// open.
test.use({ timezoneId: "UTC", viewport: { width: 1440, height: 900 } });

const answered = (id: string, text: string, at: string) => ({ id, identity: "i-" + id, task: "", text, options: ["Yes", "No"], status: "succeeded", answer: "Yes", answered_option: "Yes", answer_kind: "option", answered_by: "overlord", created_at: at, answered_at: at });
const RECENT = answered("recent", "Merge the docs fix?", new Date(Date.now() - 10 * 60_000).toISOString());
const NEWER = answered("newer", "Ship the release notes?", new Date(Date.now() - 60_000).toISOString());
const OLDER = [answered("older-1", "Rotate the staging key?", "2026-10-28T09:00:00Z"), answered("older-2", "Drop the old index?", "2026-10-27T09:00:00Z")];
const ASKED = { id: "asked", identity: "i-asked", task: "", text: "Pause the deploys?", options: ["Yes", "No"], status: "pending", created_at: new Date().toISOString() };
const board = (revision: number, questions: object[], historyCount: number) => ({
  healthy: true, instance: "board-0m", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision, tasks: [],
  questions, reviews: [], runs: [], credentials: [], actions: [], history_count: historyCount,
});

// The board gets its snapshots from the test alone, and reads History from
// what the supervisor holds at each read.
async function open(page: Page, held: { questions: object[] }) {
  const reads: string[] = [];
  await page.addInitScript(() => {
    class FixtureSource extends EventTarget {
      private publish = (event: Event) => {
        if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent(event.detail.type, { data: JSON.stringify(event.detail.data) }));
      };
      constructor() {
        super();
        window.addEventListener("fixture-stream", this.publish);
      }
      close() { window.removeEventListener("fixture-stream", this.publish); }
    }
    Object.defineProperty(window, "EventSource", { value: FixtureSource });
  });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route((url) => url.pathname === "/api/items/closed", (route) => {
    reads.push(new URL(route.request().url()).search);
    return route.fulfill({ json: { questions: held.questions } });
  });
  await page.goto("/");
  return reads;
}
const send = (page: Page, data: object) => page.evaluate((detail) => { window.dispatchEvent(new CustomEvent("fixture-stream", { detail })); }, { type: "snapshot", data });

test("History reads what closed before the last hour when it opens, and again only when an item closes", async ({ page }) => {
  // Arrange: an answer of the last hour in the snapshot, two older ones held.
  const held = { questions: [RECENT, ...OLDER] };
  const reads = await open(page, held);
  await send(page, board(1, [RECENT], 3));
  await page.locator(".command-center-menu > summary").click();
  const history = page.locator(".command-center-updates details.inbox-history");
  const rows = history.locator(".inbox-list li");
  await expect(history.locator("summary")).toHaveText(/History\s*3/);
  expect(reads).toEqual([]);

  // Act
  await history.locator("summary").click();

  // Assert: the newest first.
  await expect(rows).toHaveCount(3);
  await expect(rows.locator(".inbox-summary")).toHaveText([RECENT.text, OLDER[0].text, OLDER[1].text]);
  expect(reads).toEqual([""]);

  // Act: a snapshot in which a question came and nothing closed.
  await send(page, board(2, [ASKED, RECENT], 3));

  // Assert
  await expect(page.locator(".command-center-updates .inbox-list.waiting li")).toHaveCount(1);
  expect(reads).toEqual([""]);

  // Act: one in which an item closed.
  held.questions = [NEWER, RECENT, ...OLDER];
  await send(page, board(3, [ASKED, NEWER, RECENT], 4));

  // Assert
  await expect(rows).toHaveCount(4);
  await expect(rows.first().locator(".inbox-summary")).toHaveText(NEWER.text);
  await expect(history.locator("summary")).toHaveText(/History\s*4/);
  await expect.poll(() => reads).toEqual(["", ""]);
});
